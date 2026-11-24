// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useInvalidate, usePaged, type Row } from '../lib/api'
import { dt } from '../lib/format'
import { useSession } from '../lib/session'
import { DataTable, FormModal, PageHead, Pager, Toggle, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { int, text } from './common'

const periods = [{ value: 'daily', label: 'Günlük' }, { value: 'weekly', label: 'Haftalık' }, { value: 'monthly', label: 'Aylık' }]
const groups = [{ value: 'client', label: 'Müşteri' }, { value: 'vendor', label: 'Sağlayıcı' }, { value: 'prefix', label: 'Önek' }, { value: 'day', label: 'Gün' }]
const label = (list: { value: string; label: string }[], v: string) => list.find((o) => o.value === v)?.label ?? v
const emails = /^[^@\s,]+@[^@\s,]+(\s*,\s*[^@\s,]+@[^@\s,]+)*$/

export function ReportSchedules() {
  const { canEdit } = useSession()
  const list = usePaged('/v1/report-schedules', {})
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()
  const row = editing === 'new' ? null : editing

  const patch = async (r: Row, body: Row) => {
    try {
      await api.patch(`/v1/report-schedules/${r.id}`, body)
      invalidate('/v1/report-schedules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const remove = async (r: Row) => {
    if (!(await ui.confirm({ title: `“${r.name}” silinsin mi?`, body: 'Bu rapor artık gönderilmez. Bu işlem geri alınamaz.', action: 'Sil', danger: true }))) return
    try {
      await api.del(`/v1/report-schedules/${r.id}`)
      invalidate('/v1/report-schedules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const sendNow = async (r: Row) => {
    if (!(await ui.confirm({ title: `“${r.name}” şimdi gönderilsin mi?`, body: `Son tamamlanan dönemin raporu şu adreslere gönderilir: ${r.recipients}`, action: 'Gönder' }))) return
    try {
      const out = await api.post(`/v1/report-schedules/${r.id}/send`, {})
      ui.toast(`Rapor ${out.recipients} alıcıya gönderilmek üzere sıraya alındı`)
    } catch (e) { ui.toast(errorText(e as any)) }
  }

  const cols: Col<Row>[] = [
    { label: 'Ad', width: 'minmax(160px,1fr)', render: (r) => r.name },
    { label: 'Dönem', width: '90px', render: (r) => label(periods, r.period) },
    { label: 'Kırılım', width: '100px', render: (r) => label(groups, r.group_by) },
    { label: 'Alıcılar', width: 'minmax(200px,1.4fr)', render: (r) => <span className="mono" style={{ fontSize: 12 }}>{r.recipients}</span> },
    { label: 'Gönderim', width: '150px', render: (r) => <span className="mono">{String(r.send_hour).padStart(2, '0')}:00 {r.timezone}</span> },
    { label: 'Son gönderilen dönem sonu', width: '170px', mono: true, render: (r) => (r.sent_through ? dt(r.sent_through) : <span className="tx3">—</span>) },
    { label: '', width: '110px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => sendNow(r)}>Şimdi gönder</button> },
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>Düzenle</button> },
    { label: '', width: '50px', align: 'right', render: (r) => <Toggle on={r.enabled} onChange={canEdit ? (v) => patch(r, { enabled: v }) : undefined} /> },
    { label: '', width: '50px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>Sil</button> },
  ]
  const fields: FieldDef[] = [
    { key: 'name', label: 'Ad', required: true, span: true },
    { key: 'period', label: 'Dönem', type: 'seg', options: periods, span: true },
    { key: 'group_by', label: 'Kırılım', type: 'seg', options: groups, span: true },
    { key: 'recipients', label: 'Alıcılar', type: 'mono', required: true, span: true, placeholder: 'ops@ornek.com, finans@ornek.com', hint: 'E-posta adresleri, virgülle ayrılmış' },
    { key: 'send_hour', label: 'Gönderim saati', type: 'int', hint: '0–23; dönem bittikten sonra bu saatte gönderilir' },
    { key: 'timezone', label: 'Saat dilimi', type: 'mono', placeholder: 'Europe/Istanbul', hint: 'Dönemler ve saat bu dilimle hesaplanır' },
  ]
  return (
    <div className="page">
      <PageHead title="Zamanlanmış raporlar" desc="Biten günün, haftanın (pazartesi–pazar) ya da ayın özet raporu belirlediğiniz adreslere e-postayla gönderilir. Her dönem bir kez gönderilir.">
        {canEdit && <button className="btn primary" onClick={() => setEditing('new')}>Rapor ekle</button>}
      </PageHead>
      <div className="card clip">
        <DataTable cols={cols} rows={list.rows} minWidth={1180} loading={list.loading} error={list.error} onRetry={list.refetch}
          empty={{ title: 'Zamanlanmış rapor yok', desc: 'Raporların gönderilebilmesi için sunucuda e-posta ayarlı olmalıdır.' }} />
        <Pager p={list} noun="rapor" />
      </div>
      {editing && <FormModal title={row ? 'Raporu düzenle' : 'Rapor ekle'} action={row ? 'Kaydet' : 'Ekle'} onClose={() => setEditing(null)} fields={fields}
        initial={row ? { ...row, send_hour: String(row.send_hour) } : { period: 'daily', group_by: 'client', send_hour: '8', timezone: 'UTC' }}
        validate={(v) => {
          const e: Record<string, string> = {}
          if (!emails.test(String(v.recipients ?? '').trim())) e.recipients = 'Virgülle ayrılmış geçerli e-posta adresleri girin.'
          const hour = int(v.send_hour, -1)
          if (hour < 0 || hour > 23) e.send_hour = '0 ile 23 arasında bir saat girin.'
          return e
        }}
        onSubmit={async (v) => {
          const body = { name: text(v.name), period: v.period, group_by: v.group_by, recipients: String(v.recipients).trim(), send_hour: int(v.send_hour, 8), timezone: text(v.timezone) ?? 'UTC' }
          if (row) await api.patch(`/v1/report-schedules/${row.id}`, body)
          else await api.post('/v1/report-schedules', body)
          invalidate('/v1/report-schedules')
          setEditing(null)
          ui.toast(row ? 'Rapor güncellendi' : 'Rapor eklendi')
        }} />}
    </div>
  )
}
