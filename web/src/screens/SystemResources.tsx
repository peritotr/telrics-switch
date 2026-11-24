// SPDX-License-Identifier: AGPL-3.0-only
import { useQuery } from '@tanstack/react-query'
import { api, type ApiError, type Row } from '../lib/api'
import { dec, num } from '../lib/format'
import { t } from '../lib/i18n'
import { Badge, DataTable, PageHead, errorText, type Col } from '../ui/kit'

interface Sample { at: string; cpu_permille: number; load_milli: number; mem_total: number; mem_available: number; rx_bps: number; tx_bps: number }
interface Disk { path: string; total: number; free: number }
interface SystemData {
  machine: { supported: boolean; cores: number; uptime_sec: number; latest: Sample | null; history: Sample[]; disks: Disk[] | null }
  services: { name: string; up: boolean; ms: number; note?: string }[]
}

const comma = (v: number, digits = 1) => dec(v, digits)
const pct = (v: number) => t('system.pct', { v: comma(v) })
/** A number of bytes in the largest unit that keeps it readable. */
function bytes(v: number, perSecond = false): [string, string] {
  const units = perSecond ? [t('system.bitps'), t('system.kbitps'), t('system.mbitps'), t('system.gbitps')] : ['B', 'KB', 'MB', 'GB', 'TB']
  let x = perSecond ? v * 8 : v, i = 0
  const step = perSecond ? 1000 : 1024
  while (x >= step && i < units.length - 1) { x /= step; i++ }
  return [comma(x, x >= 100 || i === 0 ? 0 : 1), units[i]]
}
function uptime(sec: number) {
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60)
  return d ? t('system.upDays', { d, h }) : h ? t('system.upHours', { h, m }) : t('system.upMinutes', { m })
}
const tone = (pct: number) => (pct >= 90 ? 'var(--bad)' : pct >= 75 ? 'var(--warn)' : 'var(--tx)')

/** A line over the last samples; top is the value drawn at full height. */
function Spark({ values, top, color = 'var(--acc)' }: { values: number[]; top: number; color?: string }) {
  const span = top || 1
  return (
    <svg className="lv-spark" viewBox="0 0 100 28" preserveAspectRatio="none">
      {values.length > 1 && <polyline fill="none" stroke={color} strokeWidth="1.5" vectorEffect="non-scaling-stroke"
        points={values.map((v, i) => `${((i / (values.length - 1)) * 100).toFixed(1)},${(26 - (Math.min(v, span) / span) * 24).toFixed(1)}`).join(' ')} />}
    </svg>
  )
}

function Tile({ label, value, unit, sub, color, children }: { label: string; value: string; unit?: string; sub?: string; color?: string; children?: React.ReactNode }) {
  return (
    <div className="stat">
      <div className="l">{label}</div>
      <div className="v" style={{ color }}>{value} <span className="u">{unit}</span></div>
      <div className="s">{sub}</div>
      {children}
    </div>
  )
}

export function SystemResources() {
  const q = useQuery({ queryKey: ['/v1/system'], queryFn: () => api.get<SystemData>('/v1/system'), refetchInterval: 5000, staleTime: 0 })
  const d = q.data
  const m = d?.machine
  const now = m?.latest
  const hist = m?.history ?? []
  const memPct = now && now.mem_total ? (100 * (now.mem_total - now.mem_available)) / now.mem_total : 0
  const netTop = Math.max(1, ...hist.map((s) => Math.max(s.rx_bps, s.tx_bps)))
  const down = d?.services.filter((s) => !s.up).length ?? 0

  const serviceCols: Col<Row>[] = [
    { label: t('system.service'), width: 'minmax(200px,1fr)', render: (s) => s.name },
    { label: t('system.status'), width: '160px', render: (s) => (s.up ? <Badge tone="ok">{t('system.up')}</Badge> : <Badge tone="bad">{s.note ?? t('system.down')}</Badge>) },
    { label: t('system.responseTime'), width: '120px', align: 'right', mono: true, render: (s) => (s.up ? `${num(s.ms)} ms` : '—') },
  ]
  const diskCols: Col<Row>[] = [
    { label: t('system.path'), width: 'minmax(200px,1fr)', mono: true, render: (x) => x.path },
    { label: t('system.used'), width: '160px', align: 'right', mono: true, render: (x) => { const p = x.total ? (100 * (x.total - x.free)) / x.total : 0; return <span style={{ color: tone(p) }}>{pct(p)}</span> } },
    { label: t('system.free'), width: '140px', align: 'right', mono: true, render: (x) => bytes(x.free).join(' ') },
    { label: t('system.total'), width: '140px', align: 'right', mono: true, render: (x) => bytes(x.total).join(' ') },
  ]

  return (
    <div className="page">
      <PageHead title={t('system.title')} desc={t('system.desc')} />
      {q.error && <div className="banner bad">{t('system.loadFailed', { error: errorText(q.error as ApiError) })}</div>}
      {d && down > 0 && <div className="banner bad">{t('system.servicesDown', { n: down })}</div>}
      {m && !m.supported && <div className="banner">{t('system.unsupported')}</div>}
      {m?.supported && (
        <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(210px,1fr))' }}>
          <Tile label={t('system.cpu')} value={now ? pct(now.cpu_permille / 10) : '—'} color={now ? tone(now.cpu_permille / 10) : undefined} sub={t('system.cores', { n: m.cores })}>
            <Spark values={hist.map((s) => s.cpu_permille)} top={1000} /></Tile>
          <Tile label={t('system.load')} value={now ? comma(now.load_milli / 1000, 2) : '—'} color={now && m.cores ? tone((100 * now.load_milli) / 1000 / m.cores) : undefined} sub={t('system.uptime', { t: uptime(m.uptime_sec) })}>
            <Spark values={hist.map((s) => s.load_milli)} top={Math.max(m.cores * 1000, ...hist.map((s) => s.load_milli))} /></Tile>
          <Tile label={t('system.memory')} value={now ? pct(memPct) : '—'} color={now ? tone(memPct) : undefined} sub={now ? t('system.memFree', { free: bytes(now.mem_available).join(' '), total: bytes(now.mem_total).join(' ') }) : ''}>
            <Spark values={hist.map((s) => s.mem_total - s.mem_available)} top={now?.mem_total ?? 1} /></Tile>
          <Tile label={t('system.netIn')} value={now ? bytes(now.rx_bps, true)[0] : '—'} unit={now ? bytes(now.rx_bps, true)[1] : ''} sub={t('system.netInSub')}>
            <Spark values={hist.map((s) => s.rx_bps)} top={netTop} /></Tile>
          <Tile label={t('system.netOut')} value={now ? bytes(now.tx_bps, true)[0] : '—'} unit={now ? bytes(now.tx_bps, true)[1] : ''} sub={t('system.netOutSub')}>
            <Spark values={hist.map((s) => s.tx_bps)} top={netTop} /></Tile>
        </div>
      )}
      <div className="card clip">
        <div className="sect" style={{ padding: '12px 16px', borderBottom: '1px solid var(--line)' }}>{t('system.services')}</div>
        <DataTable cols={serviceCols} rows={(d?.services ?? []) as Row[]} minWidth={520} loading={q.isPending} />
      </div>
      <div className="card clip">
        <div className="sect" style={{ padding: '12px 16px', borderBottom: '1px solid var(--line)' }}>{t('system.disks')}</div>
        <DataTable cols={diskCols} rows={(m?.disks ?? []) as Row[]} minWidth={640} loading={q.isPending}
          empty={{ title: t('system.noDisks'), desc: t('system.noDisksDesc') }} />
      </div>
    </div>
  )
}
