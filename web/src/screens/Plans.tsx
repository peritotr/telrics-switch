// SPDX-License-Identifier: AGPL-3.0-only
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, useGet, useInvalidate, useLookup, type Row } from '../lib/api'
import { t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { Badge, DataTable, Field, FormModal, PageHead, Seg, Stat, Tabs, Toggle, errorText, useUI, type Col } from '../ui/kit'
import { windowBody, windowErrors, windowFields, windowInitial, windowLabel } from '../lib/window'
import { ListPage, int, text } from './common'

const policies = () => [{ value: 'lcr', label: 'LCR' }, { value: 'priority', label: t('plans.priority') }]
const policyLabel = (p: string) => (p === 'lcr' ? 'LCR' : t('plans.priority'))

export function Plans() {
  const navigate = useNavigate()
  const cols: Col<Row>[] = [
    { label: t('plans.name'), width: 'minmax(240px,1.6fr)', render: (r) => r.name },
    { label: t('plans.policy'), width: '140px', render: (r) => policyLabel(r.policy) },
    { label: t('plans.allowLoss'), width: '140px', render: (r) => (r.allow_loss ? <Badge tone="warn">{t('plans.allowed')}</Badge> : <span className="tx2">{t('plans.forbidden')}</span>) },
  ]
  return <ListPage title={t('plans.title')} desc={t('plans.desc')}
    path="/v1/routing-plans" noun={t('plans.noun')} cols={cols} minWidth={620} onRow={(r) => navigate(`/plans/${r.id}`)}
    add={{
      label: t('plans.add'), title: t('plans.addTitle'), initial: { policy: 'lcr' },
      fields: [
        { key: 'name', label: t('plans.name'), required: true, span: true },
        { key: 'policy', label: t('plans.policy'), type: 'seg', options: policies() },
        { key: 'allow_loss', label: t('plans.allowLoss'), type: 'check', hint: t('plans.allowLossHint') },
      ],
      build: (v) => ({ name: text(v.name), policy: v.policy, allow_loss: !!v.allow_loss }),
      done: (row) => navigate(`/plans/${row.id}`),
    }} />
}

export function PlanDetail() {
  const id = Number(useParams().id)
  const { canEdit } = useSession()
  const q = useGet<Row>(`/v1/routing-plans/${id}`)
  const entries = useGet<Row[]>('/v1/routing-entries', { routing_plan_id: id, limit: 1000 })
  const conns = useLookup('/v1/vendor-connections')
  const vendors = useLookup('/v1/vendors')
  const [tab, setTab] = useState('routes')
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<Row | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()
  const p = q.data
  if (q.error) return <div className="page"><Link to="/plans" style={{ fontSize: 12 }}>{t('plans.back')}</Link><div className="banner bad">{t('plans.loadFailed', { error: errorText(q.error as any) })}</div></div>
  if (!p) return <div className="tx3">{t('kit.loading')}</div>

  const rows = entries.data ?? []
  const prefixes = new Set(rows.map((r) => r.prefix))
  const patch = async (r: Row, body: Row) => {
    try {
      await api.patch(`/v1/routing-entries/${r.id}`, body)
      invalidate('/v1/routing-entries')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const remove = async (r: Row) => {
    if (!(await ui.confirm({ title: t('plans.deleteTitle'), body: t('plans.deleteBody', { prefix: r.prefix || t('plans.all'), connection: conns.name(r.connection_id) }), action: t('action.delete'), danger: true }))) return
    try {
      await api.del(`/v1/routing-entries/${r.id}`)
      invalidate('/v1/routing-entries')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  // The share of calls a route is tried first for, among the enabled routes
  // of its prefix and priority. Only known here when the order does not
  // depend on prices.
  const share = (r: Row) => {
    if (p.policy !== 'priority' || !r.enabled) return '—'
    const total = rows.filter((x) => x.enabled && x.prefix === r.prefix && x.priority === r.priority).reduce((n, x) => n + x.weight, 0)
    return total > 0 ? t('plans.pct', { n: Math.round((r.weight * 100) / total) }) : '—'
  }
  const cols: Col<Row>[] = [
    { label: t('plans.prefix'), width: '120px', mono: true, render: (r) => (rows.findIndex((x) => x.prefix === r.prefix) === rows.indexOf(r) ? <span style={{ fontWeight: 500 }}>{r.prefix || t('plans.all')}</span> : '') },
    { label: t('plans.vendor'), width: 'minmax(160px,1fr)', render: (r) => vendors.name(conns.byId.get(r.connection_id)?.vendor_id) },
    { label: t('plans.connection'), width: 'minmax(140px,1fr)', mono: true, render: (r) => conns.name(r.connection_id) },
    { label: t('plans.priority'), width: '80px', align: 'right', mono: true, render: (r) => r.priority },
    { label: t('plans.weight'), width: '80px', align: 'right', mono: true, render: (r) => r.weight },
    { label: t('plans.window'), width: '140px', render: (r) => windowLabel(r) || <span className="tx3">{t('plans.always')}</span> },
    ...(p.policy === 'priority' ? [{ label: t('plans.share'), width: '70px', align: 'right', mono: true, render: share } as Col<Row>] : []),
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>{t('action.edit')}</button> },
    { label: '', width: '50px', align: 'right', render: (r) => <Toggle on={r.enabled} onChange={canEdit ? (v) => patch(r, { enabled: v }) : undefined} /> },
    { label: '', width: '64px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>{t('action.delete')}</button> },
  ]
  return (
    <div className="page">
      <PageHead back={<Link to="/plans" style={{ fontSize: 12 }}>{t('plans.back')}</Link>}
        title={<><h1>{p.name}</h1><span className="mono tx3" style={{ fontSize: 12 }}>ID {p.id}</span></>} />
      <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(200px,1fr))' }}>
        <Stat text label={t('plans.policy')} value={policyLabel(p.policy)} sub={p.policy === 'lcr' ? t('plans.lcrSub') : t('plans.prioritySub')} />
        <Stat text label={t('plans.allowLoss')} value={p.allow_loss ? t('plans.allowed') : t('plans.forbidden')} tone={p.allow_loss ? 'warn' : undefined} sub={p.allow_loss ? t('plans.lossOnSub') : t('plans.lossOffSub')} />
        <Stat label={t('plans.prefixes')} value={prefixes.size} sub={t('plans.routeRows', { n: rows.length })} />
      </div>
      <div className="card clip">
        <Tabs value={tab} tabs={[{ value: 'routes', label: t('plans.tabRoutes') }, { value: 'genel', label: t('plans.tabGeneral') }]} onChange={setTab} />
        {tab === 'routes' && <>
          <div className="toolbar">
            <div className="grow tx2 pretty">{t('plans.help')}</div>
            {canEdit && <button className="btn primary" onClick={() => setAdding(true)}>{t('plans.addRoute')}</button>}
          </div>
          <DataTable cols={cols} rows={rows} minWidth={1040} loading={entries.isPending} error={entries.error as any} onRetry={entries.refetch}
            empty={{ title: t('plans.noRoutes'), desc: t('plans.noRoutesDesc') }} />
        </>}
        {tab === 'genel' && <GeneralForm plan={p} />}
      </div>
      {adding && <FormModal title={t('plans.addRoute')} sub={p.name} action={t('action.add')} onClose={() => setAdding(false)} initial={{ priority: '10', weight: '1', ...windowInitial() }}
        fields={[
          { key: 'prefix', label: t('plans.prefix'), type: 'mono', placeholder: t('plans.prefixPlaceholder'), hint: t('plans.prefixHint') },
          { key: 'connection_id', label: t('plans.vendorConnection'), type: 'select', required: true,
            options: conns.rows.map((c) => ({ value: String(c.id), label: `${vendors.name(c.vendor_id)} · ${c.name}` })) },
          { key: 'priority', label: t('plans.priority'), type: 'int', hint: t('plans.priorityHint') },
          { key: 'weight', label: t('plans.weight'), type: 'int', hint: t('plans.weightHint') },
          ...windowFields(p.timezone),
        ]}
        validate={(v) => ({ ...(/^\d*$/.test(v.prefix ?? '') ? {} : { prefix: t('plans.errPrefix') }), ...windowErrors(v) })}
        onSubmit={async (v) => {
          await api.post('/v1/routing-entries', { routing_plan_id: id, prefix: v.prefix ?? '', connection_id: Number(v.connection_id), priority: int(v.priority, 10), weight: int(v.weight, 1), ...windowBody(v) })
          invalidate('/v1/routing-entries')
          setAdding(false)
          ui.toast(t('plans.routeAdded'))
        }} />}
      {editing && <FormModal title={t('plans.editRoute')} sub={`${editing.prefix || t('plans.all')} · ${conns.name(editing.connection_id)}`} action={t('action.save')} onClose={() => setEditing(null)}
        initial={{ priority: String(editing.priority), weight: String(editing.weight), ...windowInitial(editing) }}
        validate={windowErrors}
        fields={[
          { key: 'priority', label: t('plans.priority'), type: 'int', hint: t('plans.priorityHint') },
          { key: 'weight', label: t('plans.weight'), type: 'int', hint: t('plans.weightHint') },
          ...windowFields(p.timezone, editing.days),
        ]}
        onSubmit={async (v) => {
          await api.patch(`/v1/routing-entries/${editing.id}`, { priority: int(v.priority, editing.priority), weight: int(v.weight, editing.weight), ...windowBody(v) })
          invalidate('/v1/routing-entries')
          setEditing(null)
          ui.toast(t('plans.routeUpdated'))
        }} />}
    </div>
  )
}

function GeneralForm({ plan }: { plan: Row }) {
  const { canEdit } = useSession()
  const [v, setV] = useState<Row>(plan)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  useEffect(() => setV(plan), [plan])
  const save = async () => {
    if (!text(v.name)) return setError(t('plans.errName'))
    setBusy(true)
    setError('')
    try {
      await api.patch(`/v1/routing-plans/${plan.id}`, { name: text(v.name), policy: v.policy, allow_loss: !!v.allow_loss, timezone: text(v.timezone) ?? 'UTC' })
      invalidate('/v1/routing-plans')
      ui.toast(t('plans.saved'))
    } catch (e) { setError(errorText(e as any)) }
    setBusy(false)
  }
  return (
    <>
      <fieldset disabled={!canEdit}><div className="form-col" style={{ padding: 20, maxWidth: 420 }}>
        <Field label={t('plans.name')}><input className="input" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} /></Field>
        <Field label={t('plans.policy')}><Seg value={v.policy} options={policies()} onChange={(x) => setV({ ...v, policy: x })} /></Field>
        <Field label={t('plans.timezone')} hint={t('plans.timezoneHint')}><input className="input mono" value={v.timezone ?? ''} onChange={(e) => setV({ ...v, timezone: e.target.value })} /></Field>
        <label className="radio"><input type="checkbox" checked={!!v.allow_loss} onChange={(e) => setV({ ...v, allow_loss: e.target.checked })} />{t('plans.allowLossCheck')}</label>
      </div></fieldset>
      {error && <div className="banner bad" style={{ margin: '0 20px 12px' }}>{error}</div>}
      {canEdit && <div className="form-foot">
        <button className="btn" onClick={() => { setV(plan); setError('') }}>{t('action.cancel')}</button>
        <button className="btn primary" disabled={busy} onClick={save}>{t('action.save')}</button>
      </div>}
    </>
  )
}
