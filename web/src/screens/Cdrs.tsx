// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { download, useLookup, usePaged, type ApiError, type Row } from '../lib/api'
import { dt, fromLocalInput, money, num, sipReason } from '../lib/format'
import { Badge, DataTable, Pager, Search, errorText, useUI, type Col } from '../ui/kit'
import { SipCode } from './common'
import { SipFlow } from './SipFlow'
import { useBaseCurrency } from './ExchangeRates'

const todayStart = () => new Date(Math.floor(Date.now() / 86_400_000) * 86_400_000).toISOString().slice(0, 16)

export function Cdrs() {
  const [search] = useSearchParams()
  const [from, setFrom] = useState(search.size ? '' : todayStart())
  const [to, setTo] = useState('')
  const [client, setClient] = useState('')
  const [vendor, setVendor] = useState('')
  const [dst, setDst] = useState(search.get('dst') ?? '')
  const [callId, setCallId] = useState(search.get('call_id') ?? '')
  const [selected, setSelected] = useState<Row | null>(null)
  const clients = useLookup('/v1/clients')
  const vendors = useLookup('/v1/vendors')
  const connections = useLookup('/v1/vendor-connections')
  const trunks = useLookup('/v1/client-trunks')
  const filters = { from: fromLocalInput(from), to: fromLocalInput(to), client_id: client, vendor_id: vendor, dst, call_id: callId }
  const list = usePaged('/v1/cdrs', filters, 50)
  const ui = useUI()
  const [exporting, setExporting] = useState(false)
  const clear = () => { setFrom(''); setTo(''); setClient(''); setVendor(''); setDst(''); setCallId('') }

  const sum = (f: (r: Row) => number) => list.rows.reduce((a, r) => a + f(r), 0)
  // In the account's currency, so that rows in different currencies add up;
  // records from before currencies carry their own amounts.
  const base = useBaseCurrency()
  const sell = (r: Row) => r.sell_base ?? r.sell_amount
  const buy = (r: Row) => r.buy_base ?? r.buy_amount
  const profit = (r: Row) => sell(r) - buy(r)
  const cols: Col<Row>[] = [
    { label: 'Başlangıç', width: '150px', mono: true, render: (r) => dt(r.start_at, true), total: <span style={{ fontFamily: 'var(--sans)', fontSize: 13 }}>Sayfa toplamı</span> },
    { label: 'Arayan', width: '110px', mono: true, render: (r) => r.src },
    { label: 'Aranan', width: '115px', mono: true, render: (r) => r.dst },
    { label: 'Süre sn', width: '50px', align: 'right', mono: true, render: (r) => r.duration_sec, total: num(sum((r) => r.duration_sec)) },
    { label: 'SIP kodu', width: '150px', render: (r) => <SipCode code={r.sip_code} /> },
    { label: 'Müşteri', width: 'minmax(130px,1fr)', render: (r) => clients.name(r.client_id) },
    // A call delivered to a client's trunk went to no vendor: the trunk
    // stands in the vendor's column.
    { label: 'Sağlayıcı', width: 'minmax(110px,1fr)', render: (r) => r.dst_trunk_id && !r.vendor_id
      ? <span className="row" style={{ gap: 6 }}><Badge tone="acc">Trunk</Badge>{trunks.name(r.dst_trunk_id)}</span>
      : vendors.name(r.vendor_id) },
    { label: `Satış ${base}`, width: '76px', align: 'right', mono: true, render: (r) => money(sell(r), 4), total: money(sum(sell), 4) },
    { label: `Alış ${base}`, width: '76px', align: 'right', mono: true, render: (r) => money(buy(r), 4), total: money(sum(buy), 4) },
    { label: `Kâr ${base}`, width: '76px', align: 'right', mono: true, render: (r) => <span className={profit(r) < 0 ? 'bad' : undefined}>{money(profit(r), 4)}</span>, total: money(sum(profit), 4) },
  ]

  // Every record the filters match, not only this page; the server builds the file.
  const exportCSV = async () => {
    setExporting(true)
    try {
      await download('/v1/cdrs/export', filters, 'cagri-kayitlari.csv')
    } catch (e) {
      const err = e as ApiError
      ui.toast(err.status === 422 ? '200.000\'den fazla kayıt eşleşiyor. Tarih aralığını ya da filtreleri daraltın.' : errorText(err))
    } finally { setExporting(false) }
  }

  const s = selected
  return (
    <div className="page">
      <div className="row wrap" style={{ gap: 12 }}>
        <h1 className="grow">Çağrı kayıtları</h1>
        <button className="btn" disabled={!list.rows.length || exporting} onClick={exportCSV} title="Filtrelerle eşleşen tüm kayıtları indirir">{exporting ? 'Hazırlanıyor…' : 'CSV indir'}</button>
      </div>
      <div className="card clip">
        <div className="filters">
          <label className="field"><span className="label">Başlangıç (UTC)</span><input type="datetime-local" className="input mono" value={from} onChange={(e) => setFrom(e.target.value)} /></label>
          <label className="field"><span className="label">Bitiş (UTC)</span><input type="datetime-local" className="input mono" value={to} onChange={(e) => setTo(e.target.value)} /></label>
          <label className="field"><span className="label">Müşteri</span>
            <select className="select" value={client} onChange={(e) => setClient(e.target.value)}><option value="">Tümü</option>{clients.rows.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</select></label>
          <label className="field"><span className="label">Sağlayıcı</span>
            <select className="select" value={vendor} onChange={(e) => setVendor(e.target.value)}><option value="">Tümü</option>{vendors.rows.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}</select></label>
          <label className="field"><span className="label">Aranan numara</span><Search mono width="100%" value={dst} onChange={setDst} placeholder="ör. 90532" /></label>
          <label className="field"><span className="label">Call-ID</span><Search mono width="100%" value={callId} onChange={setCallId} placeholder="Tam eşleşme" /></label>
        </div>
        <DataTable cols={cols} rows={list.rows} minWidth={1180} gap={14} totals loading={list.loading} error={list.error} onRetry={list.refetch}
          onRow={setSelected} selectedId={s?.id} errorTitle="Çağrı kayıtları yüklenemedi"
          empty={{ title: 'Bu filtrelerle çağrı yok', desc: 'Tarih aralığını genişletin ya da numara ve müşteri filtrelerini kaldırın.', action: <button className="btn" onClick={clear}>Filtreleri temizle</button> }} />
        <Pager p={list} noun="çağrı" />
      </div>
      {s && (
        <div className="drawer">
          <div className="row" style={{ padding: '12px 16px', borderBottom: '1px solid var(--line)' }}>
            <div className="sect grow">Çağrı detayı</div>
            <Badge tone={s.sip_code < 300 ? 'ok' : s.sip_code === 403 || s.sip_code >= 500 ? 'bad' : 'warn'}>{s.sip_code} {sipReason(s.sip_code)}</Badge>
            <button className="btn inline" onClick={() => setSelected(null)}>Kapat</button>
          </div>
          <div style={{ padding: '12px 16px', borderBottom: '1px solid var(--line2)' }}>
            <div className="tx3" style={{ fontSize: 11.5 }}>Call-ID</div>
            <div className="mono" style={{ fontSize: 12, marginTop: 2, wordBreak: 'break-all' }}>{s.call_id}</div>
          </div>
          <div style={{ padding: '8px 16px' }}>
            {([
              ['Başlangıç', dt(s.start_at, true), true], ['Arayan', s.src || '—', true], ['Aranan', s.dst, true], ['Kaynak IP', s.src_ip || '—', true],
              ['Müşteri', clients.name(s.client_id), false], ['Trunk', s.trunk_id ? trunks.name(s.trunk_id) : '—', true],
              ['Sağlayıcı', s.vendor_id ? vendors.name(s.vendor_id) : '—', false],
              ...(s.dst_trunk_id ? [['Hedef trunk', `${trunks.name(s.dst_trunk_id)} · ${clients.name(trunks.byId.get(s.dst_trunk_id)?.client_id)}`, false]] : []),
              ['Süre', `${s.duration_sec} sn`, true], ['Faturalanan süre', `${s.sell_billed_sec} sn`, true],
              ['Satış öneki', s.sell_prefix || '—', true], ['Satış tutarı', `${money(sell(s), 6)} ${base}${s.sell_base != null && s.sell_base !== s.sell_amount ? ` (müşteriye ${money(s.sell_amount, 6)})` : ''}`, true],
              ['Alış öneki', s.buy_prefix || '—', true], ['Alış tutarı', `${money(buy(s), 6)} ${base}${s.buy_base != null && s.buy_base !== s.buy_amount ? ` (sağlayıcıya ${money(s.buy_amount, 6)})` : ''}`, true],
              ['Kâr', `${money(profit(s), 6)} ${base}`, true],
            ] as [string, string, boolean][]).map(([l, v, mono]) => <div className="kv" key={l}><span>{l}</span><span className={mono ? 'mono' : undefined}>{v}</span></div>)}
          </div>
          <div style={{ padding: '12px 16px', borderTop: '1px solid var(--line2)' }}>
            <div className="tx3" style={{ fontSize: 11.5, marginBottom: 6 }}>Rota denemeleri</div>
            {s.connection_id
              ? <div className="row" style={{ alignItems: 'baseline', padding: '3px 0' }}>
                  <span className="mono tx3" style={{ fontSize: 12 }}>1</span>
                  <span className="grow">{vendors.name(s.vendor_id)} · <span className="mono">{connections.name(s.connection_id)}</span></span>
                  <SipCode code={s.sip_code} />
                </div>
              : s.dst_trunk_id
                ? <div className="row" style={{ alignItems: 'baseline', padding: '3px 0' }}>
                    <span className="mono tx3" style={{ fontSize: 12 }}>1</span>
                    <span className="grow"><Badge tone="acc">Trunk</Badge> {trunks.name(s.dst_trunk_id)}</span>
                    <SipCode code={s.sip_code} />
                  </div>
                : <div className="tx2">Yönlendirme öncesinde reddedildi; sağlayıcıya gönderilmedi.</div>}
          </div>
          <div style={{ padding: '12px 16px', borderTop: '1px solid var(--line2)' }}>
            <div className="tx3" style={{ fontSize: 11.5, marginBottom: 6 }}>SIP akışı</div>
            {s.connection_id || s.dst_trunk_id
              ? <SipFlow key={s.call_id} callId={s.call_id} compact />
              : <div className="tx2">Sağlayıcıya gönderilmeyen çağrıların iletileri saklanmaz.</div>}
          </div>
        </div>
      )}
    </div>
  )
}
