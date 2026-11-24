// SPDX-License-Identifier: AGPL-3.0-only
// Live monitoring: the last minute of traffic as the route-engine sees it,
// read once a second from GET /v1/live. Rates are derived here from the
// counters; a rate with nothing to divide by is shown as "—", never as zero.
import { useQuery } from '@tanstack/react-query'
import { geoArea, geoCentroid, geoNaturalEarth1, geoPath } from 'd3-geo'
import type { FeatureCollection, Geometry, Polygon } from 'geojson'
import { useEffect, useMemo, useRef, useState, type MouseEvent, type PointerEvent } from 'react'
import { feature } from 'topojson-client'
import type { GeometryCollection, Topology } from 'topojson-specification'
import worldUrl from 'world-atlas/countries-110m.json?url'
import { api, type ApiError } from '../lib/api'
import { dec, num, sipReason, sipTone } from '../lib/format'
import { isoNumeric } from '../lib/iso3166'
import { Split } from '../ui/kit'

// ---- what the API returns ----

interface Tally {
  attempts: number; finished: number; answered: number; failed_network: number
  code_503: number; duration_sec: number; pdd_ms: number; pdd_samples: number
}
interface Share { key: string; count: number }
interface LiveData {
  live: {
    window_sec: number
    calls: number
    total: Tally
    seconds: (Tally & { t: number; calls: number })[] | null
    clients: (Tally & { client_id: number; calls: number; max_calls: number; max_cps: number })[] | null
    connections: (Tally & { connection_id: number; calls: number; max_calls: number })[] | null
    countries: (Tally & { country: string; calls: number; prefixes: Share[] | null; connections: Share[] | null })[] | null
    sip_codes: Share[] | null
  }
  clients: Record<string, string>
  connections: Record<string, { name: string; vendor_id: number; vendor: string; host: string; transport: string }>
}

// ---- rates and thresholds (the thresholds are the design's) ----

interface Rates { cps: number; asr: number | null; ner: number | null; acd: number | null; pdd: number | null; e5: number | null }

function rates(t: Tally, windowSec: number): Rates {
  return {
    cps: t.attempts / windowSec,
    asr: t.finished ? t.answered / t.finished : null,
    ner: t.finished ? 1 - t.failed_network / t.finished : null,
    acd: t.answered ? t.duration_sec / t.answered : null,
    pdd: t.pdd_samples ? t.pdd_ms / t.pdd_samples / 1000 : null,
    e5: t.finished ? t.code_503 / t.finished : null,
  }
}

type Metric = 'asr' | 'pdd' | 'ner'
type Tone = 'ok' | 'warn' | 'bad'
const TH: Record<Metric, (v: number) => Tone> = {
  asr: (v) => (v >= 0.45 ? 'ok' : v >= 0.35 ? 'warn' : 'bad'),
  pdd: (v) => (v <= 3 ? 'ok' : v <= 5 ? 'warn' : 'bad'),
  ner: (v) => (v >= 0.95 ? 'ok' : v >= 0.9 ? 'warn' : 'bad'),
}
const LEG: Record<Metric, string[]> = {
  asr: ['ASR ≥ %45', '%35 – %45', '< %35'],
  pdd: ['PDD ≤ 3 sn', '3 – 5 sn', '> 5 sn'],
  ner: ['NER ≥ %95', '%90 – %95', '< %90'],
}
const metrics = [{ value: 'asr', label: 'ASR' }, { value: 'pdd', label: 'PDD' }, { value: 'ner', label: 'NER' }]
const tone = (m: Metric, v: number | null) => (v == null ? null : TH[m](v))
/** CSS colour of a tone; a value that could not be computed has none. */
const col = (t: string | null, none = 'var(--tx3)') => (t ? `var(--${t})` : none)
const useTone = (u: number) => (u >= 0.9 ? 'bad' : u >= 0.8 ? 'warn' : 'acc')
const share = (v: number | null) => (v == null ? null : v * 100)
const unit = (text: string, before: string, after = '') => (text === '—' ? text : before + text + after)

const regionNames = new Intl.DisplayNames(['tr'], { type: 'region' })
function countryName(cc: string) {
  if (!cc) return 'Bilinmeyen'
  try { return regionNames.of(cc) ?? cc } catch { return cc }
}

interface CountryRow { cc: string; name: string; calls: number; r: Rates; prefixes: Share[]; connections: Share[] }

// ---- small pieces ----

function Spark({ values, color }: { values: (number | null)[]; color: string }) {
  const pts = values.flatMap((v, i) => (v == null ? [] : [[i, v]]))
  const lo = Math.min(...pts.map((p) => p[1])), hi = Math.max(...pts.map((p) => p[1])), span = hi - lo || 1
  return (
    <svg className="lv-spark" viewBox="0 0 100 28" preserveAspectRatio="none">
      {pts.length > 1 && <polyline fill="none" stroke={color} strokeWidth="1.5" vectorEffect="non-scaling-stroke"
        points={pts.map(([i, v]) => `${((i / (values.length - 1)) * 100).toFixed(1)},${(25 - ((v - lo) / span) * 22).toFixed(1)}`).join(' ')} />}
    </svg>
  )
}

function Kpi({ label, value, unit, sub, tone, series }: { label: string; value: string; unit?: string; sub?: string; tone?: string | null; series: (number | null)[] }) {
  return (
    <div className="stat">
      <div className="l">{label}</div>
      <div className="v" style={{ color: col(tone ?? null, 'var(--tx)') }}>{value} <span className="u">{unit}</span></div>
      <div className="s">{sub}</div>
      <Spark values={series} color={col(tone ?? null, 'var(--acc)')} />
    </div>
  )
}

function Gauge({ part, color }: { part: number; color?: string }) {
  return <div className="lv-g"><i style={{ width: `${(Math.min(1, Math.max(0, part)) * 100).toFixed(1)}%`, background: color }} /></div>
}

function Bars({ title, items, mono }: { title: string; items: Share[]; mono?: boolean }) {
  const sum = items.reduce((a, s) => a + s.count, 0)
  if (!sum) return null
  return (
    <div className="lv-bars">
      <div className="t">{title}</div>
      {items.map((s) => (
        <div className="lv-bar" key={s.key}>
          <div className={'ell' + (mono ? ' mono' : '')}>{s.key}</div>
          <Gauge part={s.count / sum} />
          <div className="p">%{num((100 * s.count) / sum)}</div>
        </div>
      ))}
    </div>
  )
}

function Empty({ title, desc }: { title: string; desc: string }) {
  return <div className="state"><div className="t">{title}</div><div className="d">{desc}</div></div>
}

// ---- map ----

const W = 960, H = 480
// The whole inhabited world (Antarctica is not drawn); the transit point is the design's.
const FRAME = [[-168, 74], [-168, -56], [178, 74], [178, -56], [0, 78], [0, -56]]
const POP: [number, number] = [28.97, 41.01]
const LABELS = 8
const MAX_ZOOM = 8
/** Keeps the map covering the frame: no empty margin can be dragged in. */
const clampView = (v: { k: number; x: number; y: number }) =>
  ({ k: v.k, x: Math.min(0, Math.max(W - W * v.k, v.x)), y: Math.min(0, Math.max(H - H * v.k, v.y)) })
const radius = (calls: number) => 3 + Math.sqrt(calls) * 0.42
const numericToAlpha2 = new Map(Object.entries(isoNumeric).map(([a2, n]) => [n, a2]))

/** The largest piece of a country, so that overseas territories do not pull the circle into the sea. */
function mainland(g: Geometry): Geometry {
  if (g.type !== 'MultiPolygon') return g
  return g.coordinates.map((c): Polygon => ({ type: 'Polygon', coordinates: c })).reduce((a, b) => (geoArea(a) >= geoArea(b) ? a : b))
}

function geometry(topo: Topology) {
  const land = feature(topo, topo.objects.countries as GeometryCollection) as FeatureCollection
  const proj = geoNaturalEarth1().fitExtent([[6, 6], [W - 6, H - 6]], { type: 'MultiPoint', coordinates: FRAME })
  const path = geoPath(proj)
  const shapes: { cc: string | undefined; d: string }[] = []
  const centre = new Map<string, [number, number]>()
  for (const f of land.features) {
    if (f.id === '010') continue // Antarctica
    const cc = numericToAlpha2.get(String(f.id ?? ''))
    shapes.push({ cc, d: path(f) ?? '' })
    if (cc) centre.set(cc, geoCentroid(mainland(f.geometry)))
  }
  return { proj, path, shapes, centre }
}

function WorldMap({ rows, metric, selected, onSelect }: { rows: CountryRow[]; metric: Metric; selected: string | undefined; onSelect: (cc: string) => void }) {
  const world = useQuery({
    queryKey: ['world-atlas'], staleTime: Infinity, gcTime: Infinity,
    queryFn: async () => {
      const resp = await fetch(worldUrl)
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`)
      return (await resp.json()) as Topology
    },
  })
  const geo = useMemo(() => (world.data ? geometry(world.data) : null), [world.data])
  const [tip, setTip] = useState<{ cc: string; x: number; y: number } | null>(null)
  // Zoom and pan: k is the scale, (x, y) the offset in viewBox units.
  const [view, setView] = useState({ k: 1, x: 0, y: 0 })
  const svg = useRef<SVGSVGElement>(null)
  const drag = useRef<{ px: number; py: number; moved: boolean } | null>(null)
  const zoomAt = (factor: number, cx = W / 2, cy = H / 2) => setView((v) => {
    const k = Math.min(MAX_ZOOM, Math.max(1, v.k * factor))
    return clampView({ k, x: cx - ((cx - v.x) / v.k) * k, y: cy - ((cy - v.y) / v.k) * k })
  })
  useEffect(() => {
    const el = svg.current
    if (!el) return
    const wheel = (e: WheelEvent) => {
      e.preventDefault()
      const box = el.getBoundingClientRect()
      zoomAt(e.deltaY < 0 ? 1.25 : 0.8, ((e.clientX - box.left) / box.width) * W, ((e.clientY - box.top) / box.height) * H)
    }
    el.addEventListener('wheel', wheel, { passive: false })
    return () => el.removeEventListener('wheel', wheel)
  }, [geo])
  const down = (e: PointerEvent) => { drag.current = { px: e.clientX, py: e.clientY, moved: false } }
  const move = (e: PointerEvent) => {
    const d = drag.current, el = svg.current
    if (!d || !el || !(e.buttons & 1)) { drag.current = null; return }
    const dx = e.clientX - d.px, dy = e.clientY - d.py
    if (!d.moved && Math.abs(dx) + Math.abs(dy) < 4) return
    d.moved = true; d.px = e.clientX; d.py = e.clientY
    const scale = W / el.getBoundingClientRect().width
    setTip(null)
    setView((v) => clampView({ k: v.k, x: v.x + dx * scale, y: v.y + dy * scale }))
  }
  // A click that ends a drag must not select the country under it.
  const dragged = () => drag.current?.moved === true
  if (world.error) return <div className="lv-mapmsg">Harita verisi yüklenemedi. Ülke tablosu ve metrikler çalışmaya devam ediyor.</div>
  if (!geo) return <svg className="lv-map" viewBox={`0 0 ${W} ${H}`} />

  const byCc = new Map(rows.map((c) => [c.cc, c]))
  const placed = rows.flatMap((c) => {
    const at = geo.centre.get(c.cc), xy = at && geo.proj(at)
    return at && xy ? [{ c, at, x: xy[0], y: xy[1], r: radius(c.calls) }] : []
  }) // rows come sorted by calls, so smaller circles are drawn on top
  const fill = (c: CountryRow) => col(tone(metric, c.r[metric]))
  const show = (e: MouseEvent, cc: string) =>
    setTip({ cc, x: Math.min(e.clientX + 14, window.innerWidth - 190), y: Math.min(e.clientY + 14, window.innerHeight - 150) })
  const hide = () => setTip(null)
  const pop = geo.proj(POP)!
  const hover = tip && byCc.get(tip.cc)
  return (
    <>
      <div className="lv-zoom">
        <button className="btn sm" onClick={() => zoomAt(1.5)} disabled={view.k >= MAX_ZOOM} aria-label="Yakınlaştır" title="Yakınlaştır">+</button>
        <button className="btn sm" onClick={() => zoomAt(1 / 1.5)} disabled={view.k <= 1} aria-label="Uzaklaştır" title="Uzaklaştır">−</button>
        <button className="btn sm" onClick={() => setView({ k: 1, x: 0, y: 0 })} disabled={view.k === 1}>Tümü</button>
      </div>
      <svg ref={svg} className={'lv-map' + (view.k > 1 ? ' pan' : '')} viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Hedef ülkelere göre trafik haritası"
        onPointerDown={down} onPointerMove={move}>
        <g transform={`translate(${view.x} ${view.y}) scale(${view.k})`}>
        <g>
          {geo.shapes.map((s, i) => {
            const c = s.cc ? byCc.get(s.cc) : undefined
            const t = c && tone(metric, c.r[metric])
            return <path key={i} d={s.d} className={'lv-ctry' + (c ? ' has' : '') + (c && c.cc === selected ? ' sel' : '')}
              style={t ? { fill: `color-mix(in oklab, var(--${t}) 30%, var(--land))` } : undefined}
              onClick={c && (() => { if (!dragged()) onSelect(c.cc) })} onMouseMove={(e) => (c ? show(e, c.cc) : hide())} onMouseLeave={hide} />
          })}
        </g>
        <g>{placed.map((p) => <path key={p.c.cc} className="lv-arc" strokeWidth={0.5 + p.c.calls / 350}
          d={geo.path({ type: 'LineString', coordinates: [POP, p.at] }) ?? ''} />)}</g>
        {/* Marks keep their size on screen while the map under them grows. */}
        <g>{placed.map((p) => <circle key={p.c.cc} className={'lv-bub' + (p.c.cc === selected ? ' sel' : '')} cx={p.x} cy={p.y} r={p.r / view.k}
          style={{ fill: fill(p.c) }} onClick={() => { if (!dragged()) onSelect(p.c.cc) }} onMouseMove={(e) => show(e, p.c.cc)} onMouseLeave={hide} />)}</g>
        <g>{placed.slice(0, LABELS).map((p) => <text key={p.c.cc} className="lv-lab" style={{ fontSize: 10 / view.k }} x={p.x} y={p.y - (p.r + 4) / view.k}>{p.c.cc}</text>)}</g>
        <rect className="lv-pop" x={pop[0] - 4 / view.k} y={pop[1] - 4 / view.k} width={8 / view.k} height={8 / view.k} />
        </g>
      </svg>
      {hover && (
        <div className="lv-tip" style={{ left: tip.x, top: tip.y }}>
          <b>{hover.name}</b>
          <div><span>Eşzamanlı çağrı</span><span>{num(hover.calls)}</span></div>
          <div><span>CPS</span><span>{dec(hover.r.cps, 1)}</span></div>
          <div><span>ASR</span><span>{unit(dec(share(hover.r.asr), 1), '%')}</span></div>
          <div><span>ACD</span><span>{unit(num(hover.r.acd), '', ' sn')}</span></div>
          <div><span>PDD</span><span>{unit(dec(hover.r.pdd, 1), '', ' sn')}</span></div>
          <div><span>NER</span><span>{unit(dec(share(hover.r.ner), 1), '%')}</span></div>
        </div>
      )}
    </>
  )
}

// ---- screen ----

function errorMessage(e: ApiError) {
  if (e.status === 503) return 'Yönlendirme motoruna ulaşılamıyor ya da motorun adresi tanımlı değil.'
  if (e.status === 403) return 'Canlı trafik yalnızca tüm müşterileri gören hesaplara açıktır.'
  return e.status ? e.message : 'Sunucuya ulaşılamadı.'
}

export function LiveMonitor() {
  const [paused, setPaused] = useState(false)
  const [metric, setMetric] = useState<Metric>('asr')
  const [picked, setPicked] = useState<string>()
  const q = useQuery({ queryKey: ['/v1/live'], queryFn: () => api.get<LiveData>('/v1/live'), enabled: !paused, refetchInterval: 1000, staleTime: 0 })
  const error = q.error as ApiError | null

  const head = (
    <div className="row wrap" style={{ gap: 12 }}>
      <h1>Canlı izleme</h1>
      <span className={'lv-pill' + (paused ? ' off' : '')}><i />{paused ? 'Duraklatıldı' : 'Canlı · 1 sn'}</span>
      <span className="lv-note">Grafikler son 60 saniyeyi gösterir</span>
      <div className="grow" />
      <button className="btn" onClick={() => setPaused(!paused)}>{paused ? 'Sürdür' : 'Duraklat'}</button>
    </div>
  )
  if (error || !q.data) {
    return (
      <div className="page">
        {head}
        <div className="card">
          {error
            ? <div className="state">
              <div className="t bad">Canlı veri alınamadı</div>
              <div className="d">{errorMessage(error)}{error.status > 0 && <> <span className="mono">(HTTP {error.status})</span></>}</div>
              <button className="btn" onClick={() => q.refetch()}>Yeniden dene</button>
            </div>
            : <div className="tx3" style={{ padding: '10px 16px', fontSize: 12 }}>Yükleniyor…</div>}
        </div>
      </div>
    )
  }

  const { live, clients: clientNames, connections: connInfo } = q.data
  const win = live.window_sec || 60
  const seconds = live.seconds ?? []
  const T = rates(live.total, win)
  const perSec = seconds.map((s) => rates(s, 1))
  const cpsNow = seconds.length ? seconds[seconds.length - 1].attempts : 0
  const cpsPeak = Math.max(0, ...seconds.map((s) => s.attempts))
  const callSeries = seconds.map((s) => s.calls)

  const countries: CountryRow[] = (live.countries ?? [])
    .map((c) => ({ cc: c.country, name: countryName(c.country), calls: c.calls, r: rates(c, win), prefixes: c.prefixes ?? [], connections: c.connections ?? [] }))
    .sort((a, b) => b.calls - a.calls || b.r.cps - a.r.cps)
  const known = countries.filter((c) => c.cc)
  const selected = countries.find((c) => c.cc === picked) ?? countries[0]
  const critical = known.filter((c) => tone('asr', c.r.asr) === 'bad').length

  // The engine reports a country's vendor split per connection; the card shows it per vendor.
  const vendorShares = (c: CountryRow) => {
    const by = new Map<string, number>()
    for (const s of c.connections) {
      const name = connInfo[s.key]?.vendor ?? `#${s.key}`
      by.set(name, (by.get(name) ?? 0) + s.count)
    }
    return [...by].map(([key, count]) => ({ key, count })).sort((a, b) => b.count - a.count)
  }

  const codes = live.sip_codes ?? []
  const codeSum = codes.reduce((a, s) => a + s.count, 0)
  const conns = (live.connections ?? []).map((c) => ({ ...c, info: connInfo[c.connection_id], r: rates(c, win) })).sort((a, b) => b.calls - a.calls)
  const clients = (live.clients ?? []).map((c) => ({ ...c, r: rates(c, win) })).sort((a, b) => b.calls - a.calls)
  const cell = (m: 'asr' | 'pdd', v: number | null) => ({ color: col(tone(m, v)) })

  return (
    <div className="page">
      {head}
      <div className="stats lv-kpis">
        <Kpi label="Eşzamanlı çağrı" value={num(live.calls)} series={callSeries} />
        <Kpi label="CPS" value={dec(cpsNow, 1)} sub={`Tepe ${dec(cpsPeak, 1)}`} series={seconds.map((s) => s.attempts)} />
        <Kpi label="ASR" value={dec(share(T.asr), 1)} unit="%" sub="Cevaplanan / toplam" tone={tone('asr', T.asr)} series={perSec.map((r) => r.asr)} />
        <Kpi label="ACD" value={num(T.acd)} unit="sn" sub="Ortalama çağrı süresi" series={perSec.map((r) => r.acd)} />
        <Kpi label="PDD" value={dec(T.pdd, 2)} unit="sn" sub="Çalma öncesi gecikme" tone={tone('pdd', T.pdd)} series={perSec.map((r) => r.pdd)} />
        <Kpi label="NER" value={dec(share(T.ner), 1)} unit="%" sub="Ağın çağrıyı ulaştırma oranı" tone={tone('ner', T.ner)} series={perSec.map((r) => r.ner)} />
        <Kpi label="SIP 503 oranı" value={dec(share(T.e5), 2)} unit="%" sub="Sağlayıcı kaynaklı ret" tone={T.e5 != null && T.e5 > 0.05 ? 'bad' : null} series={perSec.map((r) => r.e5)} />
        <Kpi label="Aktif hedef ülke" value={num(known.length)} sub={`${num(critical)} ülkede ASR kritik`} series={callSeries} />
      </div>

      <div className="lv-row2">
        <section className="card clip lv-mapp">
          <div className="lv-ph">
            <span className="sect">Hedef ülkelere göre trafik</span>
            <span className="lv-note">Daire boyutu: eşzamanlı çağrı · ayrıntı için ülkenin üzerine gelin</span>
            <span className="grow" />
            <Split small value={metric} options={metrics} onChange={(v) => setMetric(v as Metric)} />
          </div>
          <WorldMap rows={known} metric={metric} selected={selected?.cc} onSelect={setPicked} />
          <div className="lv-legend">
            {(['ok', 'warn', 'bad'] as const).map((t, i) => <span key={t}><i className="lv-dot" style={{ background: col(t) }} />{LEG[metric][i]}</span>)}
            <span><i className="lv-sq" />Geçiş noktası · İstanbul</span>
          </div>
        </section>
        <div className="lv-side">
          {selected && (
            <section className="card clip">
              <div className="lv-det">
                <h3>{selected.name}{selected.cc && <small>{selected.cc}</small>}</h3>
                <div className="lv-mg">
                  <div><span>Eşzamanlı</span><b>{num(selected.calls)}</b></div>
                  <div><span>CPS</span><b>{dec(selected.r.cps, 1)}</b></div>
                  <div><span>ASR</span><b style={{ color: col(tone('asr', selected.r.asr), 'var(--tx)') }}>{unit(dec(share(selected.r.asr), 1), '%')}</b></div>
                  <div><span>ACD</span><b>{unit(num(selected.r.acd), '', ' sn')}</b></div>
                  <div><span>PDD</span><b style={{ color: col(tone('pdd', selected.r.pdd), 'var(--tx)') }}>{unit(dec(selected.r.pdd, 1), '', ' sn')}</b></div>
                  <div><span>NER</span><b style={{ color: col(tone('ner', selected.r.ner), 'var(--tx)') }}>{unit(dec(share(selected.r.ner), 1), '%')}</b></div>
                </div>
                <Bars title="Önek dağılımı" items={selected.prefixes} mono />
                <Bars title="Sağlayıcı dağılımı" items={vendorShares(selected)} />
              </div>
            </section>
          )}
          <section className="card clip lv-t">
            <div className="thead" style={{ gridTemplateColumns: 'minmax(110px,1fr) 52px 44px 48px 48px' }}>
              <div>Ülke</div><div className="right">Çağrı</div><div className="right">CPS</div><div className="right">ASR %</div><div className="right">PDD sn</div>
            </div>
            <div className="lv-scroll">
              {countries.map((c) => (
                <div key={c.cc || '-'} className={'trow click' + (c === selected ? ' sel' : '')} style={{ gridTemplateColumns: 'minmax(110px,1fr) 52px 44px 48px 48px' }} onClick={() => setPicked(c.cc)}>
                  <div className="n"><i className="lv-dot" style={{ background: col(tone(metric, c.r[metric])) }} /><span className="ell">{c.name}</span></div>
                  <div className="right">{num(c.calls)}</div>
                  <div className="right">{dec(c.r.cps, 1)}</div>
                  <div className="right" style={cell('asr', c.r.asr)}>{dec(share(c.r.asr), 1)}</div>
                  <div className="right" style={cell('pdd', c.r.pdd)}>{dec(c.r.pdd, 1)}</div>
                </div>
              ))}
              {!countries.length && <Empty title="Trafik yok" desc="Son 60 saniyede hedef ülkesi belirlenen çağrı olmadı." />}
            </div>
          </section>
        </div>
      </div>

      <div className="lv-row3">
        <section className="card clip lv-t">
          <div className="lv-ph"><span className="sect">SIP yanıt dağılımı</span></div>
          <div className="thead" style={{ gridTemplateColumns: '170px minmax(60px,1fr) 52px 60px' }}>
            <div>Kod</div><div /><div className="right">Pay %</div><div className="right">Yanıt/sn</div>
          </div>
          {codes.map((s) => (
            <div key={s.key} className="trow" style={{ gridTemplateColumns: '170px minmax(60px,1fr) 52px 60px' }}>
              <div className="n" style={{ gap: 6 }}>
                <span className="mono" style={{ fontWeight: 500, color: sipTone(Number(s.key)) }}>{s.key}</span>
                <span className="tx2 ell">{sipReason(Number(s.key))}</span>
              </div>
              <Gauge part={s.count / codeSum} color={sipTone(Number(s.key))} />
              <div className="right">{dec((100 * s.count) / codeSum, 1)}</div>
              <div className="right">{dec(s.count / win, 1)}</div>
            </div>
          ))}
          {!codes.length && <Empty title="Yanıt yok" desc="Son 60 saniyede sonuçlanan arama denemesi olmadı." />}
        </section>

        <section className="card clip lv-t">
          <div className="lv-ph"><span className="sect">Sağlayıcı bağlantıları</span></div>
          <div className="thead" style={{ gridTemplateColumns: 'minmax(120px,1fr) 44px minmax(70px,.8fr) 86px 44px 48px' }}>
            <div>Bağlantı</div><div>Taşıma</div><div>Kullanım</div><div className="right">Çağrı / limit</div><div className="right">ASR %</div><div className="right">PDD sn</div>
          </div>
          {conns.map((c) => {
            const used = c.max_calls ? c.calls / c.max_calls : 0
            return (
              <div key={c.connection_id} className="trow" title={c.info?.vendor} style={{ gridTemplateColumns: 'minmax(120px,1fr) 44px minmax(70px,.8fr) 86px 44px 48px' }}>
                <div className="n m"><span className="ell">{c.info?.name ?? `#${c.connection_id}`}</span></div>
                <div className="tx2">{(c.info?.transport ?? '').toUpperCase()}</div>
                <Gauge part={used} color={col(useTone(used))} />
                <div className="right">{num(c.calls)} / {c.max_calls ? num(c.max_calls) : '—'}</div>
                <div className="right" style={cell('asr', c.r.asr)}>{dec(share(c.r.asr), 1)}</div>
                <div className="right" style={cell('pdd', c.r.pdd)}>{dec(c.r.pdd, 1)}</div>
              </div>
            )
          })}
          {!conns.length && <Empty title="Bağlantı yok" desc="Limiti olan ya da son 60 saniyede trafik taşıyan sağlayıcı bağlantısı yok." />}
        </section>

        <section className="card clip lv-t">
          <div className="lv-ph"><span className="sect">Müşteri yükü</span></div>
          <div className="thead" style={{ gridTemplateColumns: 'minmax(130px,1fr) minmax(70px,.8fr) 90px 80px' }}>
            <div>Müşteri</div><div>Kullanım</div><div className="right">Çağrı / limit</div><div className="right">CPS / limit</div>
          </div>
          {clients.map((c) => {
            const used = c.max_calls ? c.calls / c.max_calls : 0
            return (
              <div key={c.client_id} className="trow" style={{ gridTemplateColumns: 'minmax(130px,1fr) minmax(70px,.8fr) 90px 80px' }}>
                <div className="n"><span className="ell">{clientNames[c.client_id] ?? `#${c.client_id}`}</span></div>
                <Gauge part={used} color={col(useTone(used))} />
                <div className="right" style={{ color: used >= 0.8 ? col(useTone(used)) : 'var(--tx)' }}>{num(c.calls)} / {c.max_calls ? num(c.max_calls) : '—'}</div>
                <div className="right">{dec(c.r.cps, 1)} / {c.max_cps ? num(c.max_cps) : '—'}</div>
              </div>
            )
          })}
          {!clients.length && <Empty title="Müşteri yok" desc="Limiti olan ya da son 60 saniyede çağrı gönderen müşteri yok." />}
        </section>
      </div>
    </div>
  )
}
