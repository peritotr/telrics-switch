// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useInvalidate, usePaged, type Row } from '../lib/api'
import { dt } from '../lib/format'
import { t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { DataTable, FormModal, PageHead, Pager, Toggle, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { int, text } from './common'

const periods = () => [{ value: 'daily', label: t('schedules.daily') }, { value: 'weekly', label: t('schedules.weekly') }, { value: 'monthly', label: t('schedules.monthly') }]
const groups = () => [{ value: 'client', label: t('schedules.byClient') }, { value: 'vendor', label: t('schedules.byVendor') }, { value: 'prefix', label: t('schedules.byPrefix') }, { value: 'day', label: t('schedules.byDay') }]
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
    if (!(await ui.confirm({ title: t('schedules.deleteTitle', { name: r.name }), body: t('schedules.deleteBody'), action: t('action.delete'), danger: true }))) return
    try {
      await api.del(`/v1/report-schedules/${r.id}`)
      invalidate('/v1/report-schedules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const sendNow = async (r: Row) => {
    if (!(await ui.confirm({ title: t('schedules.sendTitle', { name: r.name }), body: t('schedules.sendBody', { recipients: r.recipients }), action: t('schedules.send') }))) return
    try {
      const out = await api.post(`/v1/report-schedules/${r.id}/send`, {})
      ui.toast(t('schedules.queued', { n: out.recipients }))
    } catch (e) { ui.toast(errorText(e as any)) }
  }

  const cols: Col<Row>[] = [
    { label: t('schedules.name'), width: 'minmax(160px,1fr)', render: (r) => r.name },
    { label: t('schedules.period'), width: '90px', render: (r) => label(periods(), r.period) },
    { label: t('schedules.groupBy'), width: '100px', render: (r) => label(groups(), r.group_by) },
    { label: t('schedules.recipients'), width: 'minmax(200px,1.4fr)', render: (r) => <span className="mono" style={{ fontSize: 12 }}>{r.recipients}</span> },
    { label: t('schedules.sending'), width: '150px', render: (r) => <span className="mono">{String(r.send_hour).padStart(2, '0')}:00 {r.timezone}</span> },
    { label: t('schedules.sentThrough'), width: '170px', mono: true, render: (r) => (r.sent_through ? dt(r.sent_through) : <span className="tx3">—</span>) },
    { label: '', width: '110px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => sendNow(r)}>{t('schedules.sendNow')}</button> },
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>{t('action.edit')}</button> },
    { label: '', width: '50px', align: 'right', render: (r) => <Toggle on={r.enabled} onChange={canEdit ? (v) => patch(r, { enabled: v }) : undefined} /> },
    { label: '', width: '64px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>{t('action.delete')}</button> },
  ]
  const fields: FieldDef[] = [
    { key: 'name', label: t('schedules.name'), required: true, span: true },
    { key: 'period', label: t('schedules.period'), type: 'seg', options: periods(), span: true },
    { key: 'group_by', label: t('schedules.groupBy'), type: 'seg', options: groups(), span: true },
    { key: 'recipients', label: t('schedules.recipients'), type: 'mono', required: true, span: true, placeholder: t('schedules.recipientsPlaceholder'), hint: t('schedules.recipientsHint') },
    { key: 'send_hour', label: t('schedules.sendHour'), type: 'int', hint: t('schedules.sendHourHint') },
    { key: 'timezone', label: t('schedules.timezone'), type: 'mono', placeholder: 'Europe/Istanbul', hint: t('schedules.timezoneHint') },
  ]
  return (
    <div className="page">
      <PageHead title={t('schedules.title')} desc={t('schedules.desc')}>
        {canEdit && <button className="btn primary" onClick={() => setEditing('new')}>{t('schedules.add')}</button>}
      </PageHead>
      <div className="card clip">
        <DataTable cols={cols} rows={list.rows} minWidth={1180} loading={list.loading} error={list.error} onRetry={list.refetch}
          empty={{ title: t('schedules.empty'), desc: t('schedules.emptyDesc') }} />
        <Pager p={list} noun={t('schedules.noun')} />
      </div>
      {editing && <FormModal title={row ? t('schedules.edit') : t('schedules.add')} action={row ? t('action.save') : t('action.add')} onClose={() => setEditing(null)} fields={fields}
        initial={row ? { ...row, send_hour: String(row.send_hour) } : { period: 'daily', group_by: 'client', send_hour: '8', timezone: 'UTC' }}
        validate={(v) => {
          const e: Record<string, string> = {}
          if (!emails.test(String(v.recipients ?? '').trim())) e.recipients = t('schedules.errRecipients')
          const hour = int(v.send_hour, -1)
          if (hour < 0 || hour > 23) e.send_hour = t('schedules.errHour')
          return e
        }}
        onSubmit={async (v) => {
          const body = { name: text(v.name), period: v.period, group_by: v.group_by, recipients: String(v.recipients).trim(), send_hour: int(v.send_hour, 8), timezone: text(v.timezone) ?? 'UTC' }
          if (row) await api.patch(`/v1/report-schedules/${row.id}`, body)
          else await api.post('/v1/report-schedules', body)
          invalidate('/v1/report-schedules')
          setEditing(null)
          ui.toast(row ? t('schedules.updated') : t('schedules.added'))
        }} />}
    </div>
  )
}
