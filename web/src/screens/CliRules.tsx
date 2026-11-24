// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useInvalidate, useLookup, usePaged, type Row } from '../lib/api'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, PageHead, Pager, Search, Toggle, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { int, text } from './common'

const actions = [
  { value: 'block', label: 'Engelle' }, { value: 'allow', label: 'İzin ver' }, { value: 'rewrite', label: 'Dönüştür' },
  { value: 'anonymous', label: 'Numarasızı reddet' },
]
const sources = [{ value: 'from', label: 'From' }, { value: 'pai', label: 'Kimlik (PAI)' }]

function actionBadge(r: Row) {
  switch (r.action) {
    case 'block': return <Badge tone="bad">Engelle</Badge>
    case 'allow': return <Badge tone="ok">İzin ver</Badge>
    case 'anonymous': return <Badge tone="bad">Numarasızı reddet</Badge>
    default: return <Badge>Dönüştür</Badge>
  }
}

/** What a rewrite rule makes of a number, in words. */
function change(r: Row) {
  const parts = [r.strip_digits > 0 ? `baştan ${r.strip_digits} hane sil` : '', r.add_prefix ? `başa ${r.add_prefix} ekle` : ''].filter(Boolean)
  return parts.length ? parts.join(', ') : 'değişiklik yok'
}

export function CliRules() {
  const { canEdit } = useSession()
  const [q, setQ] = useState('')
  const list = usePaged('/v1/cli-rules', { q })
  const clients = useLookup('/v1/clients')
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()

  const patch = async (r: Row, body: Row) => {
    try {
      await api.patch(`/v1/cli-rules/${r.id}`, body)
      invalidate('/v1/cli-rules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const remove = async (r: Row) => {
    if (!(await ui.confirm({ title: 'Kural silinsin mi?', body: `${r.client_id ? clients.name(r.client_id) : 'Tüm müşteriler'} için ${r.action === 'anonymous' ? 'numarasız arayanı reddetme' : `önek ${r.prefix || '(tümü)'}`} kuralı kaldırılır. Bu işlem geri alınamaz.`, action: 'Sil', danger: true }))) return
    try {
      await api.del(`/v1/cli-rules/${r.id}`)
      invalidate('/v1/cli-rules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }

  const cols: Col<Row>[] = [
    { label: 'Müşteri', width: 'minmax(160px,1fr)', render: (r) => (r.client_id ? clients.name(r.client_id) : <span className="tx2">Tüm müşteriler</span>) },
    { label: 'Numara', width: '110px', render: (r) => (r.number_source === 'pai' ? 'Kimlik (PAI)' : 'From') },
    { label: 'Arayan öneki', width: '140px', mono: true, render: (r) => (r.action === 'anonymous' ? <span className="tx3">—</span> : r.prefix || '(tümü)') },
    { label: 'İşlem', width: '160px', render: actionBadge },
    { label: 'Dönüşüm', width: 'minmax(180px,1fr)', render: (r) => (r.action === 'rewrite' ? change(r) : <span className="tx3">—</span>) },
    { label: 'Not', width: 'minmax(140px,1fr)', render: (r) => <span className="tx2">{r.note}</span> },
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>Düzenle</button> },
    { label: '', width: '50px', align: 'right', render: (r) => <Toggle on={r.enabled} onChange={canEdit ? (v) => patch(r, { enabled: v }) : undefined} /> },
    { label: '', width: '50px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>Sil</button> },
  ]
  const fields: FieldDef[] = [
    { key: 'client_id', label: 'Müşteri', type: 'select', span: true, blank: 'Tüm müşteriler',
      options: clients.rows.map((c) => ({ value: String(c.id), label: c.name })) },
    { key: 'number_source', label: 'Bakılan numara', type: 'seg', options: sources, span: true,
      hint: 'From: arayanın gönderdiği numara. Kimlik (PAI): P-Asserted-Identity başlığındaki numara; başlık yoksa arayan numarasız sayılır' },
    { key: 'action', label: 'İşlem', type: 'seg', options: actions, span: true },
    { key: 'prefix', label: 'Arayan öneki', type: 'mono', placeholder: 'ör. 90212', hint: 'Ülke koduyla, + olmadan. Boş = tüm arayanlar', show: (v) => v.action !== 'anonymous' },
    { key: 'strip_digits', label: 'Baştan silinecek hane', type: 'int', show: (v) => v.action === 'rewrite' },
    { key: 'add_prefix', label: 'Başa eklenecek', type: 'mono', placeholder: 'ör. 0', show: (v) => v.action === 'rewrite' },
    { key: 'note', label: 'Not', span: true },
  ]
  const row = editing === 'new' ? null : editing

  return (
    <div className="page">
      <PageHead title="Arayan numara kuralları" desc="Arayan numaraya (From ya da P-Asserted-Identity) göre çağrıyı engeller, kabul eder ya da sağlayıcıya gösterilecek numarayı dönüştürür. Her numaraya kendi kuralları uygulanır; en uzun eşleşen önek geçerlidir, eşit uzunlukta müşterinin kendi kuralı önce gelir. Numarasız arayanı reddetme kuralı önek kurallarından önce uygulanır.">
        {canEdit && <button className="btn primary" onClick={() => setEditing('new')}>Kural ekle</button>}
      </PageHead>
      <div className="card clip">
        <div className="toolbar">
          <Search value={q} onChange={setQ} />
          <div className="grow tx2 pretty">Yalnızca listedeki arayanları kabul etmek için müşteriye boş önekli bir “Engelle” kuralı, izinli önekler için “İzin ver” kuralları ekleyin. Numarası olmayan ya da gizli arayanlar “Numarasızı reddet” ile 433 cevabı alır.</div>
        </div>
        <DataTable cols={cols} rows={list.rows} minWidth={1090} loading={list.loading} error={list.error} onRetry={list.refetch}
          empty={q ? { title: 'Kayıt bulunamadı', desc: 'Aramayla eşleşen kural yok.', action: <button className="btn" onClick={() => setQ('')}>Aramayı temizle</button> }
            : { title: 'Henüz kural yok', desc: 'Kural yokken arayan numaraya bakılmaz; her arayan kabul edilir ve numarası olduğu gibi iletilir.' }} />
        <Pager p={list} noun="kural" />
      </div>
      {editing && <FormModal title={row ? 'Kuralı düzenle' : 'Kural ekle'} action={row ? 'Kaydet' : 'Ekle'} onClose={() => setEditing(null)} fields={fields}
        initial={row ? { client_id: row.client_id ? String(row.client_id) : '', number_source: row.number_source ?? 'from', prefix: row.prefix, action: row.action, strip_digits: String(row.strip_digits), add_prefix: row.add_prefix, note: row.note }
          : { client_id: '', number_source: 'from', action: 'block', strip_digits: '0' }}
        validate={(v) => ({
          ...(v.action === 'anonymous' || /^\d*$/.test(v.prefix ?? '') ? {} : { prefix: 'Önek yalnızca rakam içerebilir.' }),
          ...(v.action === 'rewrite' && v.number_source === 'pai' ? { action: 'Dönüştürme yalnızca From numarasına uygulanır.' } : {}),
          ...(v.action !== 'rewrite' || /^\d*$/.test(v.add_prefix ?? '') ? {} : { add_prefix: 'Yalnızca rakam içerebilir.' }),
        })}
        onSubmit={async (v) => {
          const rewrite = v.action === 'rewrite'
          const body = {
            client_id: v.client_id ? Number(v.client_id) : null, number_source: v.number_source || 'from',
            prefix: v.action === 'anonymous' ? '' : v.prefix ?? '', action: v.action,
            strip_digits: rewrite ? int(v.strip_digits, 0) : 0, add_prefix: rewrite ? v.add_prefix ?? '' : '', note: text(v.note) ?? '',
          }
          if (row) await api.patch(`/v1/cli-rules/${row.id}`, body)
          else await api.post('/v1/cli-rules', body)
          invalidate('/v1/cli-rules')
          setEditing(null)
          ui.toast(row ? 'Kural güncellendi' : 'Kural eklendi')
        }} />}
    </div>
  )
}
