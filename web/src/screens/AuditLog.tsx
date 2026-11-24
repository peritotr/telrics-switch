// SPDX-License-Identifier: AGPL-3.0-only
// The audit log: who changed what, and when. Read-only; the server writes
// it with every change and masks secrets before they get here.
import { useState } from 'react'
import { download, usePaged, type ApiError, type Row } from '../lib/api'
import { dt, fromLocalInput, money } from '../lib/format'
import { Badge, DataTable, PageHead, Pager, Search, errorText, useUI, type Col, type Tone } from '../ui/kit'

// Names for what the API calls resources and actions. An edition that adds
// its own passes their names to addAuditLabels; an unknown one is shown as
// the API names it.
const resources: Record<string, string> = {
  clients: 'Müşteri', 'client-trunks': 'Müşteri trunk\'ı', bundles: 'Dakika paketi', vendors: 'Sağlayıcı', 'vendor-connections': 'Sağlayıcı bağlantısı',
  tariffs: 'Tarife', rates: 'Fiyat', 'routing-plans': 'Yönlendirme planı', 'routing-entries': 'Rota', 'cli-rules': 'Arayan numara kuralı',
  'exchange-rates': 'Döviz kuru', 'firewall-rules': 'Güvenlik duvarı kuralı', firewall: 'Güvenlik duvarı', 'report-schedules': 'Zamanlanmış rapor',
  users: 'Kullanıcı', 'api-keys': 'API anahtarı', account: 'Hesap', 'live-calls': 'Canlı çağrı', cdrs: 'Çağrı kaydı',
}
const actions: Record<string, [string, Tone]> = {
  create: ['Oluşturma', 'ok'], update: ['Değişiklik', 'acc'], delete: ['Silme', 'bad'],
  payment: ['Ödeme', 'neutral'], adjustment: ['Bakiye düzeltme', 'neutral'], refund: ['İade', 'neutral'],
  rates_import: ['Fiyat yükleme', 'neutral'], submit: ['Kayıt gönderme', 'neutral'], hangup: ['Çağrıyı kapatma', 'warn'],
  unban: ['Engeli kaldırma', 'neutral'], send: ['Rapor gönderme', 'neutral'], password_issued: ['Parola üretme', 'neutral'],
  login: ['Giriş', 'neutral'], login_failed: ['Hatalı giriş', 'warn'], password_changed: ['Parola değişikliği', 'neutral'],
  password_reset_requested: ['Parola sıfırlama isteği', 'neutral'], password_reset: ['Parola sıfırlama', 'neutral'],
  two_step_on: ['İki adımlı doğrulama açıldı', 'ok'], two_step_off: ['İki adımlı doğrulama kapatıldı', 'warn'],
  two_step_reset: ['İki adımlı doğrulama sıfırlandı', 'warn'], recovery_codes_renewed: ['Yedek kodlar yenilendi', 'neutral'],
  require_two_step_on: ['İki adım zorunlu kılındı', 'ok'], require_two_step_off: ['İki adım zorunluluğu kaldırıldı', 'warn'],
}
export function addAuditLabels(more: { resources?: Record<string, string>; actions?: Record<string, [string, Tone]> }) {
  Object.assign(resources, more.resources)
  Object.assign(actions, more.actions)
}

// What an operation's details are called; one the panel does not know is
// shown under the API's name for it. Amounts are in micro-units.
const details: Record<string, string> = {
  amount: 'Tutar', balance_after: 'İşlem sonrası bakiye', reference: 'Referans', note: 'Not', reason: 'Neden', locked: 'Hesap kilitlendi',
  unlocked: 'Kilit açıldı', two_step_setup_required: 'İki adım kurulumu bekleniyor', username: 'Kullanıcı adı', role: 'Rol', two_step: 'İki adımlı doğrulama', method: 'Kod türü', was_on: 'Açıktı',
  effective_from: 'Geçerlilik başlangıcı', mode: 'Yükleme türü', rates: 'Yüklenen fiyat', closed: 'Kapatılan fiyat', invalid: 'Geçersiz satır',
  inserted: 'Eklenen kayıt', duplicates: 'Yinelenen kayıt', period_start: 'Dönem başlangıcı', period_end: 'Dönem bitişi', recipients: 'Alıcı sayısı',
  subtotal: 'Ara toplam', tax: 'Vergi', total: 'Toplam',
}
const amounts = new Set(['amount', 'balance_after', 'subtotal', 'tax', 'total'])
const reasons: Record<string, string> = { password: 'Parola hatalı', code: 'Kod hatalı', disabled: 'Hesap devre dışı', app: 'Uygulama kodu', recovery: 'Yedek kod' }
export function addAuditDetails(more: Record<string, string>) { Object.assign(details, more) }

const actors = [{ value: 'user', label: 'Kullanıcı' }, { value: 'key', label: 'API anahtarı' }, { value: 'system', label: 'Sistem' }]
const resourceName = (r: string) => resources[r] ?? r
const sorted = (m: Record<string, string>) => Object.entries(m).sort((a, b) => a[1].localeCompare(b[1], 'tr'))

function Action({ value }: { value: string }) {
  const known = actions[value]
  return known ? <Badge tone={known[1]}>{known[0]}</Badge> : <Badge><span className="mono">{value}</span></Badge>
}

function Actor({ row }: { row: Row }) {
  if (row.actor_type === 'system') return <span className="tx2">Sistem</span>
  return <>
    <span className={row.actor_type === 'key' ? 'mono' : undefined}>{row.actor_name || `#${row.actor_id}`}</span>
    {row.actor_type === 'key' && <span className="tx3"> · API anahtarı</span>}
  </>
}

/** A value as it was stored; what the server masked is said to be hidden. */
function Value({ v }: { v: unknown }) {
  if (v === undefined) return <span className="tx3">—</span>
  if (v === '***') return <span className="tx3">gizli</span>
  if (v === null || v === '') return <span className="tx3">boş</span>
  if (v === true || v === false) return <>{v ? 'evet' : 'hayır'}</>
  return <span className="mono" style={{ fontSize: 12, wordBreak: 'break-all' }}>{typeof v === 'object' ? JSON.stringify(v) : String(v)}</span>
}

export function AuditLog() {
  const [from, setFrom] = useState('')
  const [to, setTo] = useState('')
  const [actor, setActor] = useState('')
  const [resource, setResource] = useState('')
  const [action, setAction] = useState('')
  const [q, setQ] = useState('')
  const [selected, setSelected] = useState<Row | null>(null)
  const filters = { from: fromLocalInput(from), to: fromLocalInput(to), actor_type: actor, resource, action, q }
  const list = usePaged('/v1/audit-log', filters, 50)
  const ui = useUI()
  const [exporting, setExporting] = useState(false)
  // The server streams every record the filters match, not just this page.
  const exportCSV = async () => {
    setExporting(true)
    try { await download('/v1/audit-log/export', filters, 'denetim-kaydi.csv') } catch (e) { ui.toast(errorText(e as ApiError)) } finally { setExporting(false) }
  }
  const clear = () => { setFrom(''); setTo(''); setActor(''); setResource(''); setAction(''); setQ('') }

  const cols: Col<Row>[] = [
    { label: 'Zaman (UTC)', width: '150px', mono: true, render: (r) => dt(r.at, true) },
    { label: 'Kim', width: 'minmax(170px,1.1fr)', render: (r) => <span className="ell"><Actor row={r} /></span> },
    { label: 'İşlem', width: '210px', render: (r) => <Action value={r.action} /> },
    { label: 'Kaynak', width: '170px', render: (r) => resourceName(r.resource) },
    { label: 'Kayıt', width: 'minmax(150px,1fr)', render: (r) => <span className="ell">{r.label || (r.row_id ? <span className="mono tx2">#{r.row_id}</span> : <span className="tx3">—</span>)}</span> },
    { label: 'Adres', width: '130px', mono: true, render: (r) => r.ip || '—' },
  ]
  const s = selected
  const changes = Object.entries((s?.changes ?? {}) as Record<string, { old?: unknown; new?: unknown }>).sort((a, b) => a[0].localeCompare(b[0]))
  const detail = Object.entries((s?.detail ?? {}) as Record<string, unknown>)
  return (
    <div className="page">
      <PageHead title="Denetim kaydı" desc="Yönetim API'si ve panel üzerinden yapılan her değişiklik: kim, ne zaman, hangi adresten, neyi değiştirdi. Parolalar, anahtarlar ve doğrulama kodları kayda girmez. Kayıt değiştirilemez ve silinemez; saklama süresi dolan satırlar kendiliğinden kaldırılır.">
        <button className="btn" disabled={!list.rows.length || exporting} onClick={exportCSV} title="Filtrelerle eşleşen tüm kayıtları indirir">{exporting ? 'Hazırlanıyor…' : 'CSV indir'}</button>
      </PageHead>
      <div className="card clip">
        <div className="filters">
          <label className="field"><span className="label">Başlangıç (UTC)</span><input type="datetime-local" className="input mono" value={from} onChange={(e) => setFrom(e.target.value)} /></label>
          <label className="field"><span className="label">Bitiş (UTC)</span><input type="datetime-local" className="input mono" value={to} onChange={(e) => setTo(e.target.value)} /></label>
          <label className="field"><span className="label">Kim</span>
            <select className="select" value={actor} onChange={(e) => setActor(e.target.value)}><option value="">Tümü</option>{actors.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}</select></label>
          <label className="field"><span className="label">Kaynak</span>
            <select className="select" value={resource} onChange={(e) => setResource(e.target.value)}><option value="">Tümü</option>{sorted(resources).map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></label>
          <label className="field"><span className="label">İşlem</span>
            <select className="select" value={action} onChange={(e) => setAction(e.target.value)}><option value="">Tümü</option>
              {sorted(Object.fromEntries(Object.entries(actions).map(([v, [l]]) => [v, l]))).map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></label>
          <label className="field"><span className="label">Ara</span><Search width="100%" value={q} onChange={setQ} placeholder="Ad, e-posta, adres, değer" /></label>
        </div>
        <DataTable cols={cols} rows={list.rows} minWidth={1040} gap={14} loading={list.loading} error={list.error} onRetry={list.refetch}
          onRow={setSelected} selectedId={s?.id} errorTitle="Denetim kaydı yüklenemedi"
          empty={from || to || actor || resource || action || q
            ? { title: 'Bu filtrelerle kayıt yok', desc: 'Tarih aralığını genişletin ya da filtreleri kaldırın.', action: <button className="btn" onClick={clear}>Filtreleri temizle</button> }
            : { title: 'Henüz kayıt yok', desc: 'Panelden ya da API üzerinden yapılan ilk değişiklik burada görünür.' }} />
        <Pager p={list} noun="kayıt" />
      </div>
      {s && (
        <div className="drawer" style={{ width: 440 }}>
          <div className="row" style={{ padding: '12px 16px', borderBottom: '1px solid var(--line)' }}>
            <div className="sect grow">Kayıt detayı</div>
            <Action value={s.action} />
            <button className="btn inline" onClick={() => setSelected(null)}>Kapat</button>
          </div>
          <div style={{ padding: '8px 16px' }}>
            <div className="kv"><span>Zaman (UTC)</span><span className="mono">{dt(s.at, true)}</span></div>
            <div className="kv"><span>Kim</span><span><Actor row={s} /></span></div>
            <div className="kv"><span>Adres</span><span className="mono">{s.ip || '—'}</span></div>
            <div className="kv"><span>Kaynak</span><span>{resourceName(s.resource)}</span></div>
            <div className="kv"><span>Kayıt</span><span>{s.label || '—'}{s.row_id ? <span className="mono tx3"> #{s.row_id}</span> : null}</span></div>
          </div>
          {changes.length > 0 && (
            <div style={{ padding: '12px 16px', borderTop: '1px solid var(--line2)' }}>
              <div className="tx3" style={{ fontSize: 11.5, marginBottom: 6 }}>{s.action === 'create' ? 'Oluşturulan değerler' : s.action === 'delete' ? 'Silinen değerler' : 'Değişen alanlar'}</div>
              <div style={{ display: 'grid', gridTemplateColumns: s.action === 'update' ? 'minmax(90px,auto) 1fr 1fr' : 'minmax(90px,auto) 1fr', gap: '6px 12px', alignItems: 'baseline' }}>
                <span className="tx3" style={{ fontSize: 11.5 }}>Alan</span>
                {s.action !== 'create' && <span className="tx3" style={{ fontSize: 11.5 }}>Önceki</span>}
                {s.action !== 'delete' && <span className="tx3" style={{ fontSize: 11.5 }}>Yeni</span>}
                {changes.map(([field, ch]) => [
                  <span key={field} className="mono" style={{ fontSize: 12 }}>{field}</span>,
                  s.action !== 'create' && <span key={field + '-old'}><Value v={ch.old} /></span>,
                  s.action !== 'delete' && <span key={field + '-new'}><Value v={ch.new} /></span>,
                ])}
              </div>
            </div>
          )}
          {detail.length > 0 && (
            <div style={{ padding: '12px 16px', borderTop: '1px solid var(--line2)' }}>
              <div className="tx3" style={{ fontSize: 11.5, marginBottom: 6 }}>Ayrıntı</div>
              {detail.map(([k, v]) => <div className="kv" key={k}>
                {details[k] ? <span>{details[k]}</span> : <span className="mono" style={{ fontSize: 12 }}>{k}</span>}
                <span>{amounts.has(k) && typeof v === 'number' ? <span className="mono">{money(v)}</span>
                  : (k === 'reason' || k === 'method') && typeof v === 'string' && reasons[v] ? reasons[v] : <Value v={v} />}</span>
              </div>)}
            </div>
          )}
          {changes.length === 0 && detail.length === 0 && <div className="tx2" style={{ padding: '12px 16px', borderTop: '1px solid var(--line2)' }}>Bu işlem için başka ayrıntı tutulmadı.</div>}
        </div>
      )}
    </div>
  )
}
