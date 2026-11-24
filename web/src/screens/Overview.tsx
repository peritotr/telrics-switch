// SPDX-License-Identifier: AGPL-3.0-only
import { useMemo, useState } from 'react'
import { useGet, useLookup, type Row } from '../lib/api'
import { day, money, num, pct } from '../lib/format'
import { useBaseCurrency } from './ExchangeRates'
import { DataTable, Split, Stat, Tabs, type Col } from '../ui/kit'

const DAY = 86_400_000
const ranges = [{ value: '1', label: 'Bugün' }, { value: '7', label: '7 gün' }, { value: '14', label: '14 gün' }]
const metrics = [{ value: 'money', label: 'Gelir / maliyet' }, { value: 'calls', label: 'Çağrı' }, { value: 'asr', label: 'ASR' }]
const dims = [{ value: 'client', label: 'Müşteri' }, { value: 'vendor', label: 'Sağlayıcı' }, { value: 'prefix', label: 'Önek' }, { value: 'day', label: 'Gün' }]

interface Sum { calls: number; answered: number; duration_sec: number; revenue: number; cost: number; failed_network: number; pdd_samples: number; pdd_sum_ms: number }
const zero = (): Sum => ({ calls: 0, answered: 0, duration_sec: 0, revenue: 0, cost: 0, failed_network: 0, pdd_samples: 0, pdd_sum_ms: 0 })
const add = (a: Sum, r: Row): Sum => ({
  calls: a.calls + r.calls, answered: a.answered + r.answered, duration_sec: a.duration_sec + Number(r.duration_sec),
  revenue: a.revenue + r.revenue, cost: a.cost + r.cost,
  failed_network: a.failed_network + Number(r.failed_network ?? 0), pdd_samples: a.pdd_samples + Number(r.pdd_samples ?? 0), pdd_sum_ms: a.pdd_sum_ms + Number(r.pdd_sum_ms ?? 0),
})
const asr = (s: Sum) => (s.calls ? (100 * s.answered) / s.calls : NaN)
// Network effectiveness: the share of attempts the network did not fail.
const ner = (s: Sum) => (s.calls ? (100 * (s.calls - s.failed_network)) / s.calls : NaN)
// Mean post-dial delay in seconds, over the attempts that reported progress.
const pdd = (s: Sum) => (s.pdd_samples ? `${(s.pdd_sum_ms / s.pdd_samples / 1000).toFixed(1).replace('.', ',')}` : '—')
const acd = (s: Sum) => (s.answered ? Math.round(s.duration_sec / s.answered) : 0)
const margin = (s: Sum) => (s.revenue ? (100 * (s.revenue - s.cost)) / s.revenue : NaN)

/** Rounds a chart maximum up to 1, 2, 2.5 or 5 times a power of ten. */
function niceMax(v: number) {
  if (v <= 0) return 1
  const p = 10 ** Math.floor(Math.log10(v))
  return [1, 2, 2.5, 5, 10].map((m) => m * p).find((m) => m >= v)!
}

export function Overview() {
  const [range, setRange] = useState('7')
  const [metric, setMetric] = useState('money')
  const [dim, setDim] = useState('client')
  const { from, to, days } = useMemo(() => {
    const today = Math.floor(Date.now() / DAY) * DAY
    const n = Number(range)
    const start = today - (n - 1) * DAY
    return { from: new Date(start).toISOString(), to: new Date(today + DAY).toISOString(), days: Array.from({ length: n }, (_, i) => new Date(start + i * DAY).toISOString().slice(0, 10)) }
  }, [range])
  const base = useBaseCurrency()
  const byDay = useGet<Row[]>('/v1/reports/summary', { group_by: 'day', from, to })
  const byDim = useGet<Row[]>('/v1/reports/summary', { group_by: dim, from, to })
  const clients = useLookup('/v1/clients')
  const vendors = useLookup('/v1/vendors')

  const perDay = new Map((byDay.data ?? []).map((r) => [r.key as string, add(zero(), r)]))
  const total = [...perDay.values()].reduce((a, s) => add(a, s as Row), zero())
  const series = days.map((d) => {
    const s = perDay.get(d) ?? zero()
    const [v1, v2] = metric === 'money' ? [s.revenue / 1e6, s.cost / 1e6] : metric === 'calls' ? [s.calls, s.answered] : [asr(s) || 0, 0]
    return { d, v1, v2 }
  })
  const max = metric === 'asr' ? 100 : niceMax(Math.max(...series.flatMap((s) => [s.v1, s.v2])))
  const tick = (v: number) => (metric === 'money' ? money(v * 1e6, max < 10 ? 2 : 0) : metric === 'asr' ? `${v}%` : num(v))
  const [leg1, leg2] = metric === 'money' ? [`Gelir ${base}`, `Maliyet ${base}`] : metric === 'calls' ? ['Çağrı', 'Cevaplanan'] : ['ASR %', '']

  const label = (key: string) =>
    dim === 'client' ? clients.name(Number(key)) : dim === 'vendor' ? (key ? vendors.name(Number(key)) : 'Sağlayıcıya gitmedi') : dim === 'day' ? day(key) : key || 'Fiyatı yok'
  const rows = (byDim.data ?? []).map((r) => ({ ...add(zero(), r), key: r.key as string }))
  if (dim !== 'day' && dim !== 'prefix') rows.sort((a, b) => b.revenue - a.revenue)
  const profitClass = (s: Sum) => (s.revenue - s.cost < 0 ? 'bad' : undefined)
  const cols: Col<Sum & { key: string }>[] = [
    { label: dims.find((d) => d.value === dim)!.label, width: 'minmax(190px,1.8fr)', render: (r) => <span className={dim === 'prefix' ? 'mono' : undefined}>{label(r.key)}</span>, total: 'Toplam' },
    { label: 'Çağrı', width: 'minmax(72px,1fr)', align: 'right', mono: true, render: (r) => num(r.calls), total: num(total.calls) },
    { label: 'Cevaplanan', width: 'minmax(72px,1fr)', align: 'right', mono: true, render: (r) => num(r.answered), total: num(total.answered) },
    { label: 'ASR %', width: 'minmax(72px,1fr)', align: 'right', mono: true, render: (r) => pct(asr(r)), total: pct(asr(total)) },
    { label: 'NER %', width: 'minmax(64px,1fr)', align: 'right', mono: true, render: (r) => pct(ner(r)), total: pct(ner(total)) },
    { label: 'PDD sn', width: 'minmax(64px,1fr)', align: 'right', mono: true, render: (r) => pdd(r), total: pdd(total) },
    { label: 'ACD sn', width: 'minmax(72px,1fr)', align: 'right', mono: true, render: (r) => num(acd(r)), total: num(acd(total)) },
    { label: 'Süre dk', width: 'minmax(72px,1fr)', align: 'right', mono: true, render: (r) => num(r.duration_sec / 60), total: num(total.duration_sec / 60) },
    { label: `Gelir ${base}`, width: 'minmax(72px,1fr)', align: 'right', mono: true, render: (r) => money(r.revenue), total: money(total.revenue) },
    { label: `Maliyet ${base}`, width: 'minmax(72px,1fr)', align: 'right', mono: true, render: (r) => money(r.cost), total: money(total.cost) },
    { label: `Kâr ${base}`, width: 'minmax(72px,1fr)', align: 'right', mono: true, render: (r) => <span className={profitClass(r)}>{money(r.revenue - r.cost)}</span>, total: money(total.revenue - total.cost) },
    { label: 'Marj %', width: 'minmax(72px,1fr)', align: 'right', mono: true, render: (r) => <span className={profitClass(r)}>{pct(margin(r))}</span>, total: pct(margin(total)) },
  ]
  const profit = total.revenue - total.cost

  return (
    <div className="page">
      <div className="row wrap" style={{ gap: 12 }}>
        <h1>Genel bakış</h1>
        <div className="grow" />
        <div className="mono tx2" style={{ fontSize: 12 }}>{day(from)} – {day(new Date(Date.parse(to) - 1).toISOString())} UTC</div>
        <Split value={range} options={ranges} onChange={setRange} />
      </div>
      <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(150px,1fr))' }}>
        <Stat label="Çağrı" value={num(total.calls)} />
        <Stat label="Cevaplanan" value={num(total.answered)} />
        <Stat label="ASR" value={pct(asr(total))} unit="%" />
        <Stat label="ACD" value={num(acd(total))} unit="sn" />
        <Stat label="Toplam süre" value={num(total.duration_sec / 60)} unit="dk" />
        <Stat label="Gelir" value={money(total.revenue)} unit={base} />
        <Stat label="Maliyet" value={money(total.cost)} unit={base} />
        <Stat label="Kâr" value={money(profit)} unit={base} tone={profit < 0 ? 'bad' : 'ok'} />
      </div>
      <div className="card" style={{ padding: 16 }}>
        <div className="row wrap" style={{ gap: 14, marginBottom: 16 }}>
          <div className="sect">Gün bazlı</div>
          <div className="legend"><i />{leg1}</div>
          {leg2 && <div className="legend"><i className="b2" />{leg2}</div>}
          <div className="grow" />
          <Split small value={metric} options={metrics} onChange={setMetric} />
        </div>
        <div className="chart">
          {[0, 0.25, 0.5, 0.75, 1].map((f) => <div key={f} className="tick" style={{ bottom: `${f * 100}%` }}><span>{tick(max * f)}</span></div>)}
          <div className="bars">
            {series.map((s) => (
              <div key={s.d} className="bar" title={`${day(s.d)} · ${leg1}: ${tick(s.v1)}${leg2 ? ` · ${leg2}: ${tick(s.v2)}` : ''}`}>
                <div style={{ height: `${(100 * s.v1) / max}%` }} />
                {leg2 && <div className="b2" style={{ height: `${(100 * s.v2) / max}%` }} />}
              </div>
            ))}
          </div>
        </div>
        <div className="chart-x">{series.map((s) => <div key={s.d}>{s.d.slice(8)}.{s.d.slice(5, 7)}</div>)}</div>
      </div>
      <div className="card clip">
        <div className="row" style={{ borderBottom: '1px solid var(--line)', paddingLeft: 16 }}>
          <div className="sect">Kırılım</div>
          <div style={{ flex: 1, marginBottom: -1 }}><Tabs h40 value={dim} tabs={dims} onChange={setDim} /></div>
        </div>
        <DataTable cols={cols} rows={rows} minWidth={1180} totals loading={byDim.isPending} error={byDim.error as any} onRetry={byDim.refetch}
          empty={{ title: 'Bu aralıkta çağrı yok', desc: 'Seçilen tarih aralığında ücretlendirilmiş çağrı kaydı bulunmuyor.' }} />
      </div>
    </div>
  )
}
