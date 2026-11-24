// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useInvalidate, usePaged, type Row } from '../lib/api'
import { day } from '../lib/format'
import { t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, PageHead, Pager, errorText, useUI, type Col } from '../ui/kit'

// The account's public holidays (T-19, ADR-0044): dates that count as
// another day of the week for the windows of rates and routes.
// Functions, not constants: the names follow the interface language.
const dayNames = () => [t('holidays.monday'), t('holidays.tuesday'), t('holidays.wednesday'), t('holidays.thursday'), t('holidays.friday'), t('holidays.saturday'), t('holidays.sunday')]
const dayOptions = () => [t('window.mon'), t('window.tue'), t('window.wed'), t('window.thu'), t('window.fri'), t('window.sat'), t('window.sun')].map((label, i) => ({ value: String(i + 1), label }))
const weekday = (d: string) => dayNames()[(new Date(`${d}T00:00:00Z`).getUTCDay() + 6) % 7]
const dateLabel = (d: string) => day(`${d}T00:00:00Z`)

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
    if (!(await ui.confirm({ title: t('holidays.deleteTitle'), body: t('holidays.deleteBody', { date: dateLabel(r.day), name: r.name || '', weekday: weekday(r.day) }), action: t('action.delete'), danger: true }))) return
    try {
      await api.del(`/v1/holidays/${r.id}`)
      invalidate('/v1/holidays')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const cols: Col<Row>[] = [
    { label: t('holidays.date'), width: '110px', mono: true, render: (r) => dateLabel(r.day) },
    { label: t('holidays.ownDay'), width: '120px', render: (r) => <span className="tx2">{weekday(r.day)}</span> },
    { label: t('holidays.countsAs'), width: '130px', render: (r) => <Badge tone={r.counts_as === 7 ? 'acc' : 'neutral'}>{dayNames()[r.counts_as - 1]}</Badge> },
    { label: t('holidays.name'), width: 'minmax(200px,1fr)', render: (r) => r.name || <span className="tx3">—</span> },
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>{t('action.edit')}</button> },
    { label: '', width: '64px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>{t('action.delete')}</button> },
  ]

  return (
    <div className="page">
      <PageHead title={t('holidays.title')} desc={t('holidays.desc')}>
        {canEdit && <button className="btn primary" onClick={() => setEditing('new')}>{t('holidays.add')}</button>}
      </PageHead>
      <div className="card clip">
        <div className="toolbar">
          <select className="select" style={{ width: 'auto' }} value={year} onChange={(e) => setYear(Number(e.target.value))}>
            {[thisYear - 1, thisYear, thisYear + 1, thisYear + 2].map((y) => <option key={y} value={y}>{y}</option>)}
          </select>
          <div className="grow tx2 pretty">{t('holidays.help')}</div>
        </div>
        <DataTable cols={cols} rows={list.rows} minWidth={680} loading={list.loading} error={list.error} onRetry={list.refetch}
          empty={{ title: t('holidays.none', { year }), desc: t('holidays.noneDesc') }} />
        <Pager p={list} noun={t('holidays.noun')} />
      </div>
      {editing && <FormModal title={row ? t('holidays.edit') : t('holidays.add')} action={row ? t('action.save') : t('action.add')} width={460} onClose={() => setEditing(null)}
        fields={[
          { key: 'day', label: t('holidays.date'), type: 'date', required: true, span: true },
          { key: 'counts_as', label: t('holidays.countsAs'), type: 'seg', options: dayOptions(), span: true, hint: t('holidays.countsAsHint') },
          { key: 'name', label: t('holidays.name'), span: true, placeholder: t('holidays.namePlaceholder') },
        ]}
        initial={row ? { day: row.day, counts_as: String(row.counts_as), name: row.name } : { counts_as: '7' }}
        validate={(v) => (/^\d{4}-\d{2}-\d{2}$/.test(String(v.day ?? '')) ? {} : { day: t('holidays.errDay') })}
        onSubmit={async (v) => {
          const body = { day: v.day, counts_as: Number(v.counts_as), name: String(v.name ?? '').trim() }
          if (row) await api.patch(`/v1/holidays/${row.id}`, body)
          else await api.post('/v1/holidays', body)
          invalidate('/v1/holidays')
          setEditing(null)
          ui.toast(row ? t('holidays.saved') : t('holidays.added'))
        }} />}
    </div>
  )
}
