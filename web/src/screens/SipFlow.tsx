// SPDX-License-Identifier: AGPL-3.0-only
// A call's signalling as a ladder: one column per address, one arrow per
// SIP message in the order they crossed the switch. Used in the call
// record's drawer and on the tools' call trace page.
import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { download, useGet, type ApiError } from '../lib/api'
import { day, dec } from '../lib/format'
import { getLang, t } from '../lib/i18n'
import { PageHead, errorText, useUI } from '../ui/kit'

interface Message { at: string; direction: string; transport: string; src_ip: string; src_port: number; dst_ip: string; dst_port: number; line: string; raw: string }
interface Trace { call_id: string; messages: Message[]; truncated: boolean }

/** Microseconds since the epoch; Date keeps only milliseconds. */
const micros = (at: string) => Date.parse(at.slice(0, 19) + 'Z') * 1000 + Number(at.slice(20, 26).padEnd(6, '0'))
const p2 = (n: number) => String(n).padStart(2, '0')
function stamp(at: string) {
  const d = new Date(at.slice(0, 19) + 'Z')
  const point = getLang() === 'en' ? '.' : ','
  return `${day(d)} ${p2(d.getUTCHours())}:${p2(d.getUTCMinutes())}:${p2(d.getUTCSeconds())}${point}${at.slice(20, 26)} UTC`
}
const endpoint = (ip: string, port: number) => (ip.includes(':') ? `[${ip}]:${port}` : `${ip}:${port}`)

/** What an arrow says: the method, or the answer with the request it answers when that is not the INVITE. */
function describe(m: Message) {
  const status = /^SIP\/2\.0 (\d{3}) ?(.*)$/.exec(m.line)
  if (!status) return { code: 0, short: m.line.split(' ')[0], long: m.line.split(' ')[0] }
  const method = /^CSeq:\s*\d+\s+(\w+)/im.exec(m.raw)?.[1] ?? ''
  const of = method && method !== 'INVITE' ? ` (${method})` : ''
  return { code: Number(status[1]), short: status[1] + of, long: `${status[1]} ${status[2]}${of}` }
}
const tone = (code: number) => (code === 0 ? 'var(--tx)' : code < 200 ? 'var(--tx3)' : code < 300 ? 'var(--ok)' : code < 400 ? 'var(--warn)' : 'var(--bad)')

function Ladder({ messages, compact, selected, onSelect }: { messages: Message[]; compact?: boolean; selected: number | null; onSelect: (i: number | null) => void }) {
  const { cols, roles } = useMemo(() => {
    const cols: string[] = []
    const own = new Set<string>()
    for (const m of messages) {
      for (const ip of [m.src_ip, m.dst_ip]) if (!cols.includes(ip)) cols.push(ip)
      if (m.direction === 'in') own.add(m.dst_ip)
      if (m.direction === 'out') own.add(m.src_ip)
    }
    const caller = messages[0]?.src_ip
    const roles = cols.map((ip) => (own.has(ip) ? t('trace.switch') : ip === caller ? t('trace.caller') : t('trace.vendor')))
    return { cols, roles }
  }, [messages])
  const gutter = 58, colW = compact ? 88 : 190, rowH = 34, head = 44
  const width = gutter + cols.length * colW, height = head + messages.length * rowH + 10
  const x = (ip: string) => gutter + cols.indexOf(ip) * colW + colW / 2
  const t0 = messages.length ? micros(messages[0].at) : 0
  return (
    <div className="flow-scroll">
      <svg className="flow" width={width} height={height} role="img" aria-label={t('trace.flow')}>
        {cols.map((ip, i) => (
          <g key={ip}>
            <text x={x(ip)} y={16} textAnchor="middle" className="flow-role">{roles[i]}</text>
            <text x={x(ip)} y={32} textAnchor="middle" className="flow-ip"><title>{ip}</title>{ip.length > (compact ? 13 : 28) ? ip.slice(0, compact ? 12 : 27) + '…' : ip}</text>
            <line x1={x(ip)} x2={x(ip)} y1={head - 4} y2={height - 4} className="flow-life" />
          </g>
        ))}
        {messages.map((m, i) => {
          const y = head + i * rowH, line = y + 23
          const a = x(m.src_ip), b = x(m.dst_ip)
          // A message between two ports of one address: a short stub to the right.
          const self = a === b, end = self ? a + 28 : b, dir = end > a ? 1 : -1
          const d = describe(m)
          const room = Math.floor((Math.abs(end - a) - 14) / 6.6)
          const label = !compact || d.long.length <= room ? d.long : d.short
          const secure = m.transport === 'tls' || m.transport === 'wss'
          const rel = dec((micros(m.at) - t0) / 1e6, 3)
          return (
            <g key={i} className={'flow-row' + (selected === i ? ' sel' : '')} onClick={() => onSelect(selected === i ? null : i)}>
              <title>{`${m.line}\n${endpoint(m.src_ip, m.src_port)} → ${endpoint(m.dst_ip, m.dst_port)} · ${m.transport.toUpperCase()}`}</title>
              <rect x={0} y={y} width={width} height={rowH} className="flow-bg" />
              <text x={gutter - 8} y={line + 4} textAnchor="end" className="flow-time">+{rel}</text>
              <line x1={a} x2={end - dir * 6} y1={line} y2={line} stroke={tone(d.code)} strokeWidth={1.4} strokeDasharray={d.code > 0 && d.code < 200 ? '4 3' : undefined} />
              <polygon points={`${end},${line} ${end - dir * 7},${line - 3.5} ${end - dir * 7},${line + 3.5}`} fill={tone(d.code)} />
              <text x={self ? a + 6 : (a + b) / 2} y={line - 6} textAnchor={self ? 'start' : 'middle'} className="flow-label" fill={tone(d.code)}>
                {label}{secure && !compact && <tspan className="flow-tag" dx={6}>TLS</tspan>}
              </text>
            </g>
          )
        })}
      </svg>
    </div>
  )
}

/** The ladder of one call with its states, the selected message's text and the capture download. */
export function SipFlow({ callId, compact }: { callId: string; compact?: boolean }) {
  const q = useGet<Trace>('/v1/trace', { call_id: callId })
  const [picked, setPicked] = useState<{ id: string; i: number } | null>(null)
  const [busy, setBusy] = useState(false)
  const ui = useUI()
  const err = q.error as ApiError | null
  if (q.isPending) return <div className="tx3" style={{ padding: compact ? '4px 0' : '24px 16px', fontSize: 12 }}>{t('kit.loading')}</div>
  if (err) {
    const [title, desc] = err.status === 404 ? [t('trace.notFound'), t('trace.notFoundDesc')]
      : err.status === 503 ? [t('trace.unavailable'), t('trace.unavailableDesc')]
      : [t('trace.error'), errorText(err)]
    return compact ? <div className="tx2">{title}. {desc}</div>
      : <div className="state"><div className="t bad">{title}</div><div className="d">{desc}</div><button className="btn" onClick={() => q.refetch()}>{t('action.retry')}</button></div>
  }
  const trace = q.data!
  const messages = trace.messages
  // A call in progress is still being written (the collector stores about
  // once a second), so the wide view can be read again without leaving it.
  const refresh = <button className="btn" disabled={q.isFetching} onClick={() => q.refetch()}>{q.isFetching ? t('trace.refreshing') : t('trace.refresh')}</button>
  if (!messages.length) {
    const desc = t('trace.emptyWhy')
    return compact ? <div className="tx2">{t('trace.empty')}. {desc}</div>
      : <div className="state"><div className="t">{t('trace.empty')}</div><div className="d">{desc} {t('trace.emptyNew')}</div>{refresh}</div>
  }
  const selected = picked?.id === callId && picked.i < messages.length ? picked.i : null
  const m = selected == null ? null : messages[selected]
  const span = dec((micros(messages[messages.length - 1].at) - micros(messages[0].at)) / 1e6, 1)
  const pcap = async () => {
    setBusy(true)
    try {
      await download('/v1/trace/pcap', { call_id: callId }, callId.replace(/[^0-9A-Za-z._-]/g, '_').slice(0, 80) + '.pcap')
    } catch (e) { ui.toast(errorText(e as ApiError)) } finally { setBusy(false) }
  }
  return (
    <div className={compact ? undefined : 'flow-wide'}>
      <div className="row" style={{ gap: 8, padding: compact ? '0 0 6px' : '12px 16px', borderBottom: compact ? undefined : '1px solid var(--line)' }}>
        <span className="tx2 grow" style={{ fontSize: 12 }}>{t('trace.summary', { n: messages.length, s: span })}{trace.truncated ? t('trace.truncated') : ''}</span>
        {!compact && refresh}
        {compact && <Link className="btn inline" style={{ display: 'inline-flex', alignItems: 'center' }} to={`/tools/trace?call_id=${encodeURIComponent(callId)}`}>{t('trace.wide')}</Link>}
        <button className={compact ? 'btn inline' : 'btn'} disabled={busy} onClick={pcap} title={t('trace.pcapHint')}>{busy ? t('action.preparing') : t('trace.pcap')}</button>
      </div>
      <Ladder messages={messages} compact={compact} selected={selected} onSelect={(i) => setPicked(i == null ? null : { id: callId, i })} />
      {m ? (
        <div className="flow-raw" style={compact ? undefined : { margin: '0 16px 16px' }}>
          <div className="row" style={{ gap: 8, alignItems: 'baseline' }}>
            <span className="mono tx2 grow" style={{ fontSize: 11.5, overflowWrap: 'anywhere' }}>{stamp(m.at)} · {endpoint(m.src_ip, m.src_port)} → {endpoint(m.dst_ip, m.dst_port)} · {m.transport.toUpperCase()}</span>
            <button className="btn inline" onClick={() => setPicked(null)}>{t('action.close')}</button>
          </div>
          <pre style={{ maxHeight: compact ? 280 : 420 }}>{m.raw}</pre>
        </div>
      ) : <div className="tx3" style={{ fontSize: 11.5, padding: compact ? '4px 0 0' : '0 16px 14px' }}>{t('trace.clickHint')}</div>}
    </div>
  )
}

/** Tools: the trace of a call, found by its Call-ID. */
export function CallTrace() {
  const [search, setSearch] = useSearchParams()
  const id = search.get('call_id') ?? ''
  const [text, setText] = useState(id)
  const [shown, setShown] = useState(id)
  if (shown !== id) { setShown(id); setText(id) }
  const go = () => setSearch(text.trim() ? { call_id: text.trim() } : {})
  return (
    <div className="page">
      <PageHead title={t('trace.title')} desc={t('trace.desc')} />
      <div className="card clip">
        <div className="toolbar">
          <input className="input mono" style={{ flex: 1, minWidth: 240 }} placeholder={t('trace.placeholder')} value={text} autoFocus
            onChange={(e) => setText(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && go()} />
          <button className="btn primary" disabled={!text.trim()} onClick={go}>{t('trace.fetch')}</button>
          {id && <Link className="btn" style={{ display: 'inline-flex', alignItems: 'center' }} to={`/cdrs?call_id=${encodeURIComponent(id)}`}>{t('trace.cdr')}</Link>}
        </div>
        {id ? <SipFlow key={id} callId={id} />
          : <div className="state"><div className="t">{t('trace.searchTitle')}</div><div className="d">{t('trace.searchDesc')}</div></div>}
      </div>
    </div>
  )
}
