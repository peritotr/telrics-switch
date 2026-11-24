// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useInvalidate, useLookup, usePaged, type Row } from '../lib/api'
import { t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, PageHead, Pager, Search, Toggle, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { int, text } from './common'

const actions = () => [
  { value: 'block', label: t('clirules.block') }, { value: 'allow', label: t('clirules.allow') }, { value: 'rewrite', label: t('clirules.rewrite') },
  { value: 'anonymous', label: t('clirules.anonymous') },
]
const sources = () => [{ value: 'from', label: 'From' }, { value: 'pai', label: t('clirules.pai') }]

function actionBadge(r: Row) {
  switch (r.action) {
    case 'block': return <Badge tone="bad">{t('clirules.block')}</Badge>
    case 'allow': return <Badge tone="ok">{t('clirules.allow')}</Badge>
    case 'anonymous': return <Badge tone="bad">{t('clirules.anonymous')}</Badge>
    default: return <Badge>{t('clirules.rewrite')}</Badge>
  }
}

/** What a rewrite rule makes of a number, in words. */
function change(r: Row) {
  const parts = [r.strip_digits > 0 ? t('clirules.strip', { n: r.strip_digits }) : '', r.add_prefix ? t('clirules.addPrefix', { prefix: r.add_prefix }) : ''].filter(Boolean)
  return parts.length ? parts.join(', ') : t('clirules.noChange')
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
    if (!(await ui.confirm({ title: t('clirules.deleteTitle'), body: r.action === 'anonymous'
      ? t('clirules.deleteAnon', { client: r.client_id ? clients.name(r.client_id) : t('clirules.allClients') })
      : t('clirules.deletePrefix', { client: r.client_id ? clients.name(r.client_id) : t('clirules.allClients'), prefix: r.prefix || t('clirules.all') }), action: t('action.delete'), danger: true }))) return
    try {
      await api.del(`/v1/cli-rules/${r.id}`)
      invalidate('/v1/cli-rules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }

  const cols: Col<Row>[] = [
    { label: t('clirules.client'), width: 'minmax(160px,1fr)', render: (r) => (r.client_id ? clients.name(r.client_id) : <span className="tx2">{t('clirules.allClients')}</span>) },
    { label: t('clirules.number'), width: '110px', render: (r) => (r.number_source === 'pai' ? t('clirules.pai') : 'From') },
    { label: t('clirules.callerPrefix'), width: '140px', mono: true, render: (r) => (r.action === 'anonymous' ? <span className="tx3">—</span> : r.prefix || t('clirules.all')) },
    { label: t('clirules.action'), width: '160px', render: actionBadge },
    { label: t('clirules.change'), width: 'minmax(180px,1fr)', render: (r) => (r.action === 'rewrite' ? change(r) : <span className="tx3">—</span>) },
    { label: t('clirules.note'), width: 'minmax(140px,1fr)', render: (r) => <span className="tx2">{r.note}</span> },
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>{t('action.edit')}</button> },
    { label: '', width: '50px', align: 'right', render: (r) => <Toggle on={r.enabled} onChange={canEdit ? (v) => patch(r, { enabled: v }) : undefined} /> },
    { label: '', width: '64px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>{t('action.delete')}</button> },
  ]
  const fields: FieldDef[] = [
    { key: 'client_id', label: t('clirules.client'), type: 'select', span: true, blank: t('clirules.allClients'),
      options: clients.rows.map((c) => ({ value: String(c.id), label: c.name })) },
    { key: 'number_source', label: t('clirules.source'), type: 'seg', options: sources(), span: true,
      hint: t('clirules.sourceHint') },
    { key: 'action', label: t('clirules.action'), type: 'seg', options: actions(), span: true },
    { key: 'prefix', label: t('clirules.callerPrefix'), type: 'mono', placeholder: t('clirules.prefixPlaceholder'), hint: t('clirules.prefixHint'), show: (v) => v.action !== 'anonymous' },
    { key: 'strip_digits', label: t('clirules.stripDigits'), type: 'int', show: (v) => v.action === 'rewrite' },
    { key: 'add_prefix', label: t('clirules.addPrefixLabel'), type: 'mono', placeholder: t('clirules.addPlaceholder'), show: (v) => v.action === 'rewrite' },
    { key: 'note', label: t('clirules.note'), span: true },
  ]
  const row = editing === 'new' ? null : editing

  return (
    <div className="page">
      <PageHead title={t('clirules.title')} desc={t('clirules.desc')}>
        {canEdit && <button className="btn primary" onClick={() => setEditing('new')}>{t('clirules.add')}</button>}
      </PageHead>
      <div className="card clip">
        <div className="toolbar">
          <Search value={q} onChange={setQ} />
          <div className="grow tx2 pretty">{t('clirules.help')}</div>
        </div>
        <DataTable cols={cols} rows={list.rows} minWidth={1090} loading={list.loading} error={list.error} onRetry={list.refetch}
          empty={q ? { title: t('clirules.notFound'), desc: t('clirules.notFoundDesc'), action: <button className="btn" onClick={() => setQ('')}>{t('list.clearSearch')}</button> }
            : { title: t('clirules.none'), desc: t('clirules.noneDesc') }} />
        <Pager p={list} noun={t('clirules.noun')} />
      </div>
      {editing && <FormModal title={row ? t('clirules.edit') : t('clirules.add')} action={row ? t('action.save') : t('action.add')} onClose={() => setEditing(null)} fields={fields}
        initial={row ? { client_id: row.client_id ? String(row.client_id) : '', number_source: row.number_source ?? 'from', prefix: row.prefix, action: row.action, strip_digits: String(row.strip_digits), add_prefix: row.add_prefix, note: row.note }
          : { client_id: '', number_source: 'from', action: 'block', strip_digits: '0' }}
        validate={(v) => ({
          ...(v.action === 'anonymous' || /^\d*$/.test(v.prefix ?? '') ? {} : { prefix: t('clirules.errPrefix') }),
          ...(v.action === 'rewrite' && v.number_source === 'pai' ? { action: t('clirules.errRewritePai') } : {}),
          ...(v.action !== 'rewrite' || /^\d*$/.test(v.add_prefix ?? '') ? {} : { add_prefix: t('clirules.errDigits') }),
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
          ui.toast(row ? t('clirules.updated') : t('clirules.added'))
        }} />}
    </div>
  )
}
