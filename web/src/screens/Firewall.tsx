// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, useInvalidate, usePaged, type ApiError, type Row } from '../lib/api'
import { dt, fromLocalInput, toLocalInput } from '../lib/format'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, PageHead, Pager, Search, Toggle, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { text } from './common'

const actions = [{ value: 'deny', label: 'Engelle' }, { value: 'allow', label: 'İzin ver' }]
// One address or a network, IPv4 or IPv6; the server has the last word.
const address = /^[0-9a-fA-F:.]+(\/\d{1,3})?$/

interface Ban { ip: string; reason: string; since: string; until: string; count: number }

function remaining(until: string) {
  const sec = Math.max(0, Math.round((Date.parse(until) - Date.now()) / 1000))
  return sec >= 60 ? `${Math.floor(sec / 60)} dk ${sec % 60} sn` : `${sec} sn`
}

export function Firewall() {
  const { canEdit } = useSession()
  const [q, setQ] = useState('')
  const rules = usePaged('/v1/firewall-rules', { q })
  const bans = useQuery({ queryKey: ['/v1/firewall/bans'], queryFn: () => api.get<{ bans: Ban[] }>('/v1/firewall/bans'), refetchInterval: 5000, staleTime: 0 })
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const [preset, setPreset] = useState<Row>({})
  const ui = useUI()
  const invalidate = useInvalidate()
  const row = editing === 'new' ? null : editing

  const patch = async (r: Row, body: Row) => {
    try {
      await api.patch(`/v1/firewall-rules/${r.id}`, body)
      invalidate('/v1/firewall-rules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const remove = async (r: Row) => {
    if (!(await ui.confirm({ title: 'Kural silinsin mi?', body: `${r.cidr} için “${r.action === 'deny' ? 'Engelle' : 'İzin ver'}” kuralı kaldırılır. Engel kalktıktan sonra adresin yeniden cevap alması bir dakikayı bulabilir.`, action: 'Sil', danger: true }))) return
    try {
      await api.del(`/v1/firewall-rules/${r.id}`)
      invalidate('/v1/firewall-rules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const unban = async (b: Ban) => {
    try {
      await api.post('/v1/firewall/unban', { ip: b.ip })
      invalidate('/v1/firewall/bans')
      ui.toast(`${b.ip} için engel kaldırıldı`)
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const rule = (b: Ban, action: string) => { setPreset({ cidr: b.ip, action, note: action === 'deny' ? 'Taşkın nedeniyle engellendi' : '' }); setEditing('new') }

  const expired = (r: Row) => r.expires_at && Date.parse(r.expires_at) <= Date.now()
  const ruleCols: Col<Row>[] = [
    { label: 'Adres ya da ağ', width: 'minmax(180px,1fr)', mono: true, render: (r) => r.cidr },
    { label: 'İşlem', width: '110px', render: (r) => (r.action === 'deny' ? <Badge tone="bad">Engelle</Badge> : <Badge tone="ok">İzin ver</Badge>) },
    { label: 'Not', width: 'minmax(160px,1.4fr)', render: (r) => <span className="tx2">{r.note}</span> },
    { label: 'Bitiş', width: '170px', mono: true, render: (r) => (r.expires_at ? <span className={expired(r) ? 'tx3' : undefined}>{dt(r.expires_at)}{expired(r) ? ' · bitti' : ''}</span> : <span className="tx3">süresiz</span>) },
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => { setPreset({}); setEditing(r) }}>Düzenle</button> },
    { label: '', width: '50px', align: 'right', render: (r) => <Toggle on={r.enabled} onChange={canEdit ? (v) => patch(r, { enabled: v }) : undefined} /> },
    { label: '', width: '50px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>Sil</button> },
  ]
  const banCols: Col<Row>[] = [
    { label: 'Adres', width: 'minmax(160px,1fr)', mono: true, render: (b) => b.ip },
    { label: 'Neden', width: '140px', render: (b) => (b.reason === 'flood' ? 'İstek taşkını' : b.reason) },
    { label: 'İlk engel', width: '170px', mono: true, render: (b) => dt(b.since) },
    { label: 'Kalan süre', width: '110px', align: 'right', mono: true, render: (b) => remaining(b.until) },
    { label: 'Tekrar', width: '70px', align: 'right', mono: true, render: (b) => b.count },
    { label: '', width: '120px', align: 'right', render: (b) => canEdit && <button className="btn inline" onClick={() => unban(b as Ban)}>Engeli kaldır</button> },
    { label: '', width: '120px', align: 'right', render: (b) => canEdit && <button className="btn inline" onClick={() => rule(b as Ban, 'deny')}>Kalıcı engelle</button> },
    { label: '', width: '90px', align: 'right', render: (b) => canEdit && <button className="btn inline" onClick={() => rule(b as Ban, 'allow')}>İzin ver</button> },
  ]
  const fields: FieldDef[] = [
    { key: 'cidr', label: 'Adres ya da ağ', type: 'mono', required: true, span: true, placeholder: '203.0.113.7 ya da 198.51.100.0/24' },
    { key: 'action', label: 'İşlem', type: 'seg', options: actions, span: true },
    { key: 'expires_at', label: 'Bitiş (UTC)', type: 'datetime', hint: 'Boş = süresiz' },
    { key: 'note', label: 'Not', span: true },
  ]
  return (
    <div className="page">
      <PageHead title="Güvenlik duvarı" desc="SIP sunucusunun hangi adreslere hiç cevap vermeyeceğini ve hangilerini taşkın korumasından muaf tutacağını belirler. Bu kurallar uygulama düzeyindedir; sunucunun işletim sistemi güvenlik duvarının yerini tutmaz.">
        {canEdit && <button className="btn primary" onClick={() => { setPreset({}); setEditing('new') }}>Kural ekle</button>}
      </PageHead>
      <div className="card clip">
        <div className="toolbar">
          <Search value={q} onChange={setQ} />
          <div className="grow tx2 pretty">“Engelle” her şeyin önündedir: engellenen adres müşteriye ait olsa da cevap alamaz; süren çağrıları kesilmez. “İzin ver” çağrı yetkisi vermez, yalnızca istek sınırını kaldırır.</div>
        </div>
        <DataTable cols={ruleCols} rows={rules.rows} minWidth={860} loading={rules.loading} error={rules.error} onRetry={rules.refetch}
          empty={q ? { title: 'Kayıt bulunamadı', desc: 'Aramayla eşleşen kural yok.', action: <button className="btn" onClick={() => setQ('')}>Aramayı temizle</button> }
            : { title: 'Kural yok', desc: 'Kural yokken yalnızca otomatik taşkın koruması çalışır.' }} />
        <Pager p={rules} noun="kural" />
      </div>
      <div className="card clip">
        <div className="toolbar">
          <div className="grow"><span className="sect">Otomatik engellenenler</span> <span className="tx2 pretty">· Tanınmayan bir adres iki saniyede 30'dan fazla istek gönderirse iki dakika yok sayılır; taşkın sürdükçe engel yenilenir.</span></div>
        </div>
        {bans.error
          ? <div className="banner bad" style={{ margin: 16 }}>Liste alınamadı: {errorText(bans.error as ApiError)}</div>
          : <DataTable cols={banCols} rows={(bans.data?.bans ?? []) as unknown as Row[]} minWidth={980} loading={bans.isPending}
              empty={{ title: 'Şu an engellenen adres yok', desc: 'Taşkın koruması bir adresi engellediğinde burada görünür.' }} />}
      </div>
      {editing && <FormModal title={row ? 'Kuralı düzenle' : 'Kural ekle'} action={row ? 'Kaydet' : 'Ekle'} onClose={() => setEditing(null)} fields={fields}
        initial={row ? { cidr: row.cidr, action: row.action, note: row.note, expires_at: toLocalInput(row.expires_at) } : { action: 'deny', ...preset }}
        validate={(v) => (address.test(String(v.cidr ?? '').trim()) ? {} : { cidr: 'Bir IP adresi ya da ağ girin, ör. 203.0.113.7 ya da 198.51.100.0/24.' })}
        onSubmit={async (v) => {
          const body = { cidr: String(v.cidr).trim(), action: v.action, note: text(v.note) ?? '', expires_at: fromLocalInput(v.expires_at ?? '') }
          if (row) await api.patch(`/v1/firewall-rules/${row.id}`, body)
          else await api.post('/v1/firewall-rules', body)
          invalidate('/v1/firewall-rules')
          setEditing(null)
          ui.toast(row ? 'Kural güncellendi' : 'Kural eklendi; bir dakika içinde uygulanır')
        }} />}
    </div>
  )
}
