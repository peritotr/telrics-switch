// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useInvalidate, usePaged, type Row } from '../lib/api'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, PageHead, Pager, errorText, useUI, type Col } from '../ui/kit'

// The account's public holidays (T-19, ADR-0044): dates that count as
// another day of the week for the windows of rates and routes.
const dayNames = ['Pazartesi', 'Salı', 'Çarşamba', 'Perşembe', 'Cuma', 'Cumartesi', 'Pazar']
const dayOptions = ['Pzt', 'Sal', 'Çar', 'Per', 'Cum', 'Cmt', 'Paz'].map((label, i) => ({ value: String(i + 1), label }))
const weekday = (day: string) => dayNames[(new Date(`${day}T00:00:00Z`).getUTCDay() + 6) % 7]
const dateLabel = (day: string) => day.split('-').reverse().join('.')

export function Holidays() {
  const { canEdit } = useSession()
  const thisYear = new Date().getFullYear()
  const [year, setYear] = useState(thisYear)
  const list = usePaged('/v1/holidays', { from: `${year}-01-01T00:00:00Z`, to: `${year + 1}-01-01T00:00:00Z` })
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()
  const row = editing === 'new' ? null : editing

  const remove = async (r: Row) => {
    if (!(await ui.confirm({ title: 'Tatil silinsin mi?', body: `${dateLabel(r.day)} ${r.name || ''} yeniden kendi günü (${weekday(r.day)}) sayılır. Bu işlem geri alınamaz.`, action: 'Sil', danger: true }))) return
    try {
      await api.del(`/v1/holidays/${r.id}`)
      invalidate('/v1/holidays')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const cols: Col<Row>[] = [
    { label: 'Tarih', width: '110px', mono: true, render: (r) => dateLabel(r.day) },
    { label: 'Kendi günü', width: '120px', render: (r) => <span className="tx2">{weekday(r.day)}</span> },
    { label: 'Sayıldığı gün', width: '130px', render: (r) => <Badge tone={r.counts_as === 7 ? 'acc' : 'neutral'}>{dayNames[r.counts_as - 1]}</Badge> },
    { label: 'Ad', width: 'minmax(200px,1fr)', render: (r) => r.name || <span className="tx3">—</span> },
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>Düzenle</button> },
    { label: '', width: '50px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>Sil</button> },
  ]

  return (
    <div className="page">
      <PageHead title="Resmî tatiller" desc="Tatil günü, güne göre sınırlı fiyatlarda ve rotalarda seçilen gün (varsayılan pazar) sayılır. Tarih, tarifenin ya da yönlendirme planının saat dilimiyle okunur; gün sınırı olmayan fiyat ve rotalar etkilenmez.">
        {canEdit && <button className="btn primary" onClick={() => setEditing('new')}>Tatil ekle</button>}
      </PageHead>
      <div className="card clip">
        <div className="toolbar">
          <select className="select" style={{ width: 'auto' }} value={year} onChange={(e) => setYear(Number(e.target.value))}>
            {[thisYear - 1, thisYear, thisYear + 1, thisYear + 2].map((y) => <option key={y} value={y}>{y}</option>)}
          </select>
          <div className="grow tx2 pretty">Gece yarısını aşan bir pencere başladığı güne sayılır: tatilden önceki gecenin sabahı tatil değildir, tatil gecesinin sabahı tatildir.</div>
        </div>
        <DataTable cols={cols} rows={list.rows} minWidth={680} loading={list.loading} error={list.error} onRetry={list.refetch}
          empty={{ title: `${year} için tatil yok`, desc: 'Tatil yokken her tarih kendi günü sayılır.' }} />
        <Pager p={list} noun="tatil" />
      </div>
      {editing && <FormModal title={row ? 'Tatili düzenle' : 'Tatil ekle'} action={row ? 'Kaydet' : 'Ekle'} width={460} onClose={() => setEditing(null)}
        fields={[
          { key: 'day', label: 'Tarih', type: 'date', required: true, span: true },
          { key: 'counts_as', label: 'Sayıldığı gün', type: 'seg', options: dayOptions, span: true, hint: 'Güne göre sınırlı fiyat ve rotalar bu tarihi bu gün sayar' },
          { key: 'name', label: 'Ad', span: true, placeholder: 'ör. Cumhuriyet Bayramı' },
        ]}
        initial={row ? { day: row.day, counts_as: String(row.counts_as), name: row.name } : { counts_as: '7' }}
        validate={(v) => (/^\d{4}-\d{2}-\d{2}$/.test(String(v.day ?? '')) ? {} : { day: 'Bir tarih seçin.' })}
        onSubmit={async (v) => {
          const body = { day: v.day, counts_as: Number(v.counts_as), name: String(v.name ?? '').trim() }
          if (row) await api.patch(`/v1/holidays/${row.id}`, body)
          else await api.post('/v1/holidays', body)
          invalidate('/v1/holidays')
          setEditing(null)
          ui.toast(row ? 'Tatil kaydedildi' : 'Tatil eklendi')
        }} />}
    </div>
  )
}
