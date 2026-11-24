// SPDX-License-Identifier: AGPL-3.0-only
import { useState, type CSSProperties } from 'react'
import { api, useLookup, type Row } from '../lib/api'
import { useBaseCurrency } from './ExchangeRates'
import { money, num } from '../lib/format'
import { t } from '../lib/i18n'
import { Badge, DataTable, Field, Stat, errorText, type Col } from '../ui/kit'

// By the engine's reason word; read when shown, in the language in force.
const reasons = (): Record<string, string> => ({
  ok: 'OK', 'unknown source': t('simulator.reasonUnknownSource'), 'account inactive': t('simulator.reasonInactive'),
  'destination not allowed': t('simulator.reasonDestination'), 'insufficient balance': t('simulator.reasonBalance'),
  'source blocked': t('simulator.reasonSourceBlocked'), 'caller not allowed': t('simulator.reasonCaller'), 'anonymity disallowed': t('simulator.reasonAnonymous'), 'no routing plan': t('simulator.reasonNoPlan'), 'no route': t('simulator.reasonNoRoute'),
})

export function Simulator() {
  const [ip, setIp] = useState('')
  const [dst, setDst] = useState('')
  const [src, setSrc] = useState('')
  const [asserted, setAsserted] = useState('')
  const [at, setAt] = useState('')
  const [d, setD] = useState<Row | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const clients = useLookup('/v1/clients')
  const trunks = useLookup('/v1/client-trunks')
  const vendors = useLookup('/v1/vendors')
  const conns = useLookup('/v1/vendor-connections')
  const tariffs = useLookup('/v1/tariffs')
  const plans = useLookup('/v1/routing-plans')
  const base = useBaseCurrency()

  const run = async () => {
    if (!ip.trim() || !dst.trim()) return setError(t('simulator.errInput'))
    setBusy(true)
    setError('')
    try {
      setD(await api.post('/v1/route/simulate', { src_ip: ip.trim(), dst: dst.trim(), src: src.trim(), asserted: asserted.trim(), ...(at ? { at: new Date(at).toISOString() } : {}) }))
    } catch (e) { setError(errorText(e as any)); setD(null) }
    setBusy(false)
  }

  const client = d ? clients.byId.get(d.client_id) : undefined
  const trunk = d ? trunks.byId.get(d.trunk_id) : undefined
  const routes: Row[] = d?.routes ?? []
  const stripped = d?.number && dst.trim() !== d.number ? dst.trim().slice(0, dst.trim().length - d.number.length) : ''
  // Routes of one step share its calls by weight: the first step's routes
  // are each tried first for their share, the rest only after a failure.
  const status = (r: Row) => {
    const shared = r.share_pct > 0 && r.share_pct < 100
    if (r.step === 1 && r.share_pct > 0) return <Badge tone="ok">{shared ? t('simulator.firstShare', { n: r.share_pct }) : t('simulator.used')}</Badge>
    return <Badge>{shared ? t('simulator.backupShare', { n: r.share_pct }) : t('simulator.backup')}</Badge>
  }
  const cols: Col<Row>[] = [
    { label: '#', width: '24px', render: (r) => <span className="mono tx3">{routes.indexOf(r) + 1}</span> },
    { label: t('simulator.vendor'), width: '130px', render: (r) => vendors.name(r.vendor_id) },
    { label: t('simulator.connection'), width: '110px', mono: true, render: (r) => conns.name(r.connection_id) },
    { label: t('simulator.uri'), width: 'minmax(280px,1fr)', render: (r) => <span className="mono" style={{ fontSize: 12 }}>{r.uri}</span> },
    { label: t('simulator.buyPerMin', { currency: base }), width: '100px', align: 'right', mono: true, render: (r) => money(r.buy_price_base, 6) },
    { label: t('simulator.marginPerMin', { currency: base }), width: '100px', align: 'right', mono: true, render: (r) => { const m = d!.sell_price_base - r.buy_price_base; return <span className={m < 0 ? 'bad' : undefined}>{money(m, 6)}</span> } },
    { label: t('simulator.status'), width: '170px', render: status },
  ]
  const steps: [string, string, boolean][] = d ? [
    [t('simulator.stepTrunk'), client ? `${ip.trim()} → ${client.name}${trunk ? ` / ${trunk.name}` : ''}` : t('simulator.noMatch'), !!d.client_id],
    ...(d.client_id ? [
      [t('simulator.stepNumber'), d.number ? `${dst.trim()} → ${d.number}` : '—', !!d.number] as [string, string, boolean],
      [t('simulator.stepSell'), d.sell_prefix != null && d.sell_price_per_min != null && d.allowed ? t('simulator.sellDetail', { prefix: d.sell_prefix, price: money(d.sell_price_per_min, 6) }) : d.sell_prefix ? t('simulator.prefixDetail', { prefix: d.sell_prefix }) : '—', d.allowed || !!d.sell_prefix] as [string, string, boolean],
      [t('simulator.stepRoute'), routes.length ? t('simulator.routesSorted', { n: routes.length }) : t('simulator.noRoutes'), routes.length > 0] as [string, string, boolean],
    ] : []),
    ...(d.caller ? [[t('simulator.stepCaller'), t('simulator.callerDetail', { from: src.trim(), to: d.caller }), true] as [string, string, boolean]] : []),
    [t('simulator.stepDecision'), `SIP ${d.sip_code} · ${d.reason}`, d.allowed],
  ] : []

  return (
    <div className="page">
      <div><h1>{t('simulator.title')}</h1><div className="tx2 pretty" style={{ marginTop: 4 }}>{t('simulator.desc')}</div></div>
      <div className="row wrap" style={{ gap: 16, alignItems: 'flex-start' }}>
        <div className="card col" style={{ flex: '1 1 280px', maxWidth: 340, padding: 16, gap: 12 }}>
          <Field label={t('simulator.sourceIp')}><input className="input mono" value={ip} onChange={(e) => setIp(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} placeholder="203.0.113.10" /></Field>
          <Field label={t('simulator.dst')} hint={t('simulator.dstHint')}>
            <input className="input mono" value={dst} onChange={(e) => setDst(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} placeholder="905321112233" /></Field>
          <Field label={t('simulator.src')} hint={t('simulator.srcHint')}>
            <input className="input mono" value={src} onChange={(e) => setSrc(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} placeholder="902125550000" /></Field>
          <Field label={t('simulator.pai')} hint={t('simulator.paiHint')}>
            <input className="input mono" value={asserted} onChange={(e) => setAsserted(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} placeholder="902125550000" /></Field>
          <Field label={t('simulator.at')} hint={t('simulator.atHint')}>
            <input type="datetime-local" className="input mono" value={at} onChange={(e) => setAt(e.target.value)} /></Field>
          {error && <div className="banner bad">{error}</div>}
          <button className="btn primary" disabled={busy} onClick={run}>{t('simulator.run')}</button>
        </div>
        <div className="col" style={{ flex: '3 1 560px', minWidth: 0, gap: 16 }}>
          {!d && <div className="card state"><div className="t">{t('simulator.none')}</div><div className="d">{t('simulator.noneDesc')}</div></div>}
          {d && <>
            <div className="verdict" style={{ '--c': d.allowed ? 'var(--ok)' : 'var(--bad)' } as CSSProperties}>
              <div className="t">{d.allowed ? t('simulator.allowed') : t('simulator.refused')}</div>
              <div className="mono">SIP {d.sip_code} · {reasons()[d.reason] ?? d.reason}</div>
            </div>
            <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(200px,1fr))' }}>
              <Stat text label={t('simulator.client')} value={client?.name ?? '—'} sub={trunk ? t('simulator.trunk', { name: trunk.name }) : ''} />
              <Stat label={t('simulator.number')} value={<span style={{ fontSize: 14 }}>{d.number ?? '—'}</span>} sub={stripped ? t('simulator.stripped', { prefix: stripped }) : ''} />
              <Stat text label={t('simulator.tariffPrefix')} value={client?.tariff_id ? `${tariffs.name(client.tariff_id)} · ${d.sell_prefix ?? '—'}` : '—'} sub="" />
              <Stat label={t('simulator.sell')} value={<span style={{ fontSize: 14 }}>{d.allowed ? money(d.sell_price_per_min, 6) : '—'}</span>} unit={t('simulator.perMin', { currency: client?.currency ?? base })} sub={client && client.currency !== base && d.allowed ? `${money(d.sell_price_base, 6)} ${t('simulator.perMin', { currency: base })}` : ''} />
              <Stat label={t('simulator.maxDuration')} value={<span style={{ fontSize: 14 }}>{d.max_duration_sec ? num(d.max_duration_sec) : '—'}</span>} unit={t('simulator.sec')} sub={d.max_duration_sec ? t('simulator.byBalance') : ''} />
              <Stat text label={t('simulator.plan')} value={client?.routing_plan_id ? plans.name(client.routing_plan_id) : '—'} sub="" />
            </div>
            <div className="card clip">
              <div className="sect" style={{ padding: '12px 16px', borderBottom: '1px solid var(--line)' }}>{t('simulator.routeList')}</div>
              {routes.length
                ? <DataTable cols={cols} rows={routes} minWidth={940} />
                : <div className="tx2" style={{ padding: '32px 16px', textAlign: 'center' }}>{t('simulator.noRouteList')}</div>}
            </div>
            <div className="card" style={{ padding: '12px 16px' }}>
              <div className="sect" style={{ marginBottom: 8 }}>{t('simulator.steps')}</div>
              {steps.map(([title, detail, ok], i) => (
                <div key={title} className="row wrap" style={{ alignItems: 'baseline', gap: 10, padding: '5px 0' }}>
                  <span className="mono tx3" style={{ fontSize: 12, width: 14 }}>{i + 1}</span>
                  <span className="dot" style={{ background: ok ? 'var(--ok)' : 'var(--bad)' }} />
                  <span style={{ fontWeight: 500, minWidth: 150 }}>{title}</span>
                  <span className="mono tx2">{detail}</span>
                </div>
              ))}
            </div>
          </>}
        </div>
      </div>
    </div>
  )
}
