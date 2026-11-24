// SPDX-License-Identifier: AGPL-3.0-only
import { useState, type CSSProperties } from 'react'
import { api, useLookup, type Row } from '../lib/api'
import { useBaseCurrency } from './ExchangeRates'
import { money, num } from '../lib/format'
import { Badge, DataTable, Field, Stat, errorText, type Col } from '../ui/kit'

const reasons: Record<string, string> = {
  ok: 'OK', 'unknown source': 'Kaynak IP hiçbir trunk ile eşleşmedi', 'account inactive': 'Hesap aktif değil',
  'destination not allowed': 'Hedef için satış fiyatı yok ya da engelli', 'insufficient balance': 'Bakiye yetersiz',
  'source blocked': 'Kaynak adres güvenlik duvarında engelli', 'caller not allowed': 'Arayan numara kurallara takıldı', 'anonymity disallowed': 'Numarasız arayan kabul edilmiyor', 'no routing plan': 'Yönlendirme planı atanmamış', 'no route': 'Kullanılabilir rota yok',
}

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
    if (!ip.trim() || !dst.trim()) return setError('Kaynak IP ve aranan numara gerekli.')
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
    if (r.step === 1 && r.share_pct > 0) return <Badge tone="ok">{shared ? `İlk deneme %${r.share_pct}` : 'Kullanılacak'}</Badge>
    return <Badge>{shared ? `Yedek · %${r.share_pct}` : 'Yedek'}</Badge>
  }
  const cols: Col<Row>[] = [
    { label: '#', width: '24px', render: (r) => <span className="mono tx3">{routes.indexOf(r) + 1}</span> },
    { label: 'Sağlayıcı', width: '130px', render: (r) => vendors.name(r.vendor_id) },
    { label: 'Bağlantı', width: '110px', mono: true, render: (r) => conns.name(r.connection_id) },
    { label: 'Gönderilecek SIP adresi', width: 'minmax(280px,1fr)', render: (r) => <span className="mono" style={{ fontSize: 12 }}>{r.uri}</span> },
    { label: `Alış ${base}/dk`, width: '100px', align: 'right', mono: true, render: (r) => money(r.buy_price_base, 6) },
    { label: `Marj ${base}/dk`, width: '100px', align: 'right', mono: true, render: (r) => { const m = d!.sell_price_base - r.buy_price_base; return <span className={m < 0 ? 'bad' : undefined}>{money(m, 6)}</span> } },
    { label: 'Durum', width: '170px', render: status },
  ]
  const steps: [string, string, boolean][] = d ? [
    ['Trunk eşleşmesi', client ? `${ip.trim()} → ${client.name}${trunk ? ` / ${trunk.name}` : ''}` : 'eşleşme yok', !!d.client_id],
    ...(d.client_id ? [
      ['Numara', d.number ? `${dst.trim()} → ${d.number}` : '—', !!d.number] as [string, string, boolean],
      ['Satış fiyatı', d.sell_prefix != null && d.sell_price_per_min != null && d.allowed ? `önek ${d.sell_prefix} · ${money(d.sell_price_per_min, 6)} USD/dk` : d.sell_prefix ? `önek ${d.sell_prefix}` : '—', d.allowed || !!d.sell_prefix] as [string, string, boolean],
      ['Rota', routes.length ? `${routes.length} bağlantı sıralandı` : 'rota hesaplanmadı', routes.length > 0] as [string, string, boolean],
    ] : []),
    ...(d.caller ? [['Arayan numara', `${src.trim()} → ${d.caller} (sağlayıcıya gösterilecek)`, true] as [string, string, boolean]] : []),
    ['Karar', `SIP ${d.sip_code} · ${d.reason}`, d.allowed],
  ] : []

  return (
    <div className="page">
      <div><h1>Rota simülatörü</h1><div className="tx2 pretty" style={{ marginTop: 4 }}>Gerçek çağrı başlatmadan, bir INVITE'ın nasıl karşılanacağını ve hangi sırayla hangi sağlayıcılara gönderileceğini gösterir.</div></div>
      <div className="row wrap" style={{ gap: 16, alignItems: 'flex-start' }}>
        <div className="card col" style={{ flex: '1 1 280px', maxWidth: 340, padding: 16, gap: 12 }}>
          <Field label="Kaynak IP"><input className="input mono" value={ip} onChange={(e) => setIp(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} placeholder="203.0.113.10" /></Field>
          <Field label="Aranan numara" hint="Trunk'ın teknik önekiyle birlikte, geldiği biçimde.">
            <input className="input mono" value={dst} onChange={(e) => setDst(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} placeholder="905321112233" /></Field>
          <Field label="Arayan numara" hint="İsteğe bağlı; From'daki numara. Bu ve kimlik numarası boşsa arayan numara kuralları hesaba katılmaz.">
            <input className="input mono" value={src} onChange={(e) => setSrc(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} placeholder="902125550000" /></Field>
          <Field label="Kimlik numarası (PAI)" hint="İsteğe bağlı; P-Asserted-Identity'deki numara. Boş = başlık yok.">
            <input className="input mono" value={asserted} onChange={(e) => setAsserted(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && run()} placeholder="902125550000" /></Field>
          <Field label="Çağrı zamanı" hint="İsteğe bağlı; güne ve saate göre fiyat ve rotalar için. Boş = şimdi (tarayıcının saatiyle girilir).">
            <input type="datetime-local" className="input mono" value={at} onChange={(e) => setAt(e.target.value)} /></Field>
          {error && <div className="banner bad">{error}</div>}
          <button className="btn primary" disabled={busy} onClick={run}>Simüle et</button>
        </div>
        <div className="col" style={{ flex: '3 1 560px', minWidth: 0, gap: 16 }}>
          {!d && <div className="card state"><div className="t">Henüz simülasyon yok</div><div className="d">Kaynak IP ve aranan numarayı girip “Simüle et”e basın. Sonuç gerçek yapılandırmadan hesaplanır; çağrı başlatılmaz.</div></div>}
          {d && <>
            <div className="verdict" style={{ '--c': d.allowed ? 'var(--ok)' : 'var(--bad)' } as CSSProperties}>
              <div className="t">{d.allowed ? 'İzin verildi' : 'Reddedildi'}</div>
              <div className="mono">SIP {d.sip_code} · {reasons[d.reason] ?? d.reason}</div>
            </div>
            <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(200px,1fr))' }}>
              <Stat text label="Eşleşen müşteri" value={client?.name ?? '—'} sub={trunk ? `trunk ${trunk.name}` : ''} />
              <Stat label="Normalize numara" value={<span style={{ fontSize: 14 }}>{d.number ?? '—'}</span>} sub={stripped ? `teknik önek ${stripped} silindi` : ''} />
              <Stat text label="Tarife · önek" value={client?.tariff_id ? `${tariffs.name(client.tariff_id)} · ${d.sell_prefix ?? '—'}` : '—'} sub="" />
              <Stat label="Satış fiyatı" value={<span style={{ fontSize: 14 }}>{d.allowed ? money(d.sell_price_per_min, 6) : '—'}</span>} unit={`${client?.currency ?? base}/dk`} sub={client && client.currency !== base && d.allowed ? `${money(d.sell_price_base, 6)} ${base}/dk` : ''} />
              <Stat label="Azami çağrı süresi" value={<span style={{ fontSize: 14 }}>{d.max_duration_sec ? num(d.max_duration_sec) : '—'}</span>} unit="sn" sub={d.max_duration_sec ? 'bakiye ve fiyata göre' : ''} />
              <Stat text label="Yönlendirme planı" value={client?.routing_plan_id ? plans.name(client.routing_plan_id) : '—'} sub="" />
            </div>
            <div className="card clip">
              <div className="sect" style={{ padding: '12px 16px', borderBottom: '1px solid var(--line)' }}>Sıralı rota listesi</div>
              {routes.length
                ? <DataTable cols={cols} rows={routes} minWidth={940} />
                : <div className="tx2" style={{ padding: '32px 16px', textAlign: 'center' }}>Çağrı yönlendirme aşamasından önce reddedildiği için rota hesaplanmadı.</div>}
            </div>
            <div className="card" style={{ padding: '12px 16px' }}>
              <div className="sect" style={{ marginBottom: 8 }}>Karar adımları</div>
              {steps.map(([t, detail, ok], i) => (
                <div key={t} className="row wrap" style={{ alignItems: 'baseline', gap: 10, padding: '5px 0' }}>
                  <span className="mono tx3" style={{ fontSize: 12, width: 14 }}>{i + 1}</span>
                  <span className="dot" style={{ background: ok ? 'var(--ok)' : 'var(--bad)' }} />
                  <span style={{ fontWeight: 500, minWidth: 150 }}>{t}</span>
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
