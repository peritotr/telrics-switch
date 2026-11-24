// SPDX-License-Identifier: AGPL-3.0-only
// The audit log: who changed what, and when. Read-only; the server writes
// it with every change and masks secrets before they get here.
import { useState } from 'react'
import { download, usePaged, type ApiError, type Row } from '../lib/api'
import { dt, fromLocalInput, money } from '../lib/format'
import { getLang, t, tx } from '../lib/i18n'
import { Badge, DataTable, PageHead, Pager, Search, errorText, useUI, type Col, type Tone } from '../ui/kit'

// Names for what the API calls resources and actions, as message keys. An
// edition that adds its own passes their names (message keys of its own
// catalogue, or plain text) to addAuditLabels; an unknown one is shown as
// the API names it.
const resources: Record<string, string> = {
  'clients': 'audit.res.clients', 'client-trunks': 'audit.res.client-trunks', 'bundles': 'audit.res.bundles',
  'vendors': 'audit.res.vendors', 'vendor-connections': 'audit.res.vendor-connections', 'tariffs': 'audit.res.tariffs',
  'rates': 'audit.res.rates', 'routing-plans': 'audit.res.routing-plans', 'routing-entries': 'audit.res.routing-entries',
  'cli-rules': 'audit.res.cli-rules', 'exchange-rates': 'audit.res.exchange-rates', 'firewall-rules': 'audit.res.firewall-rules',
  'firewall': 'audit.res.firewall', 'report-schedules': 'audit.res.report-schedules', 'users': 'audit.res.users',
  'api-keys': 'audit.res.api-keys', 'account': 'audit.res.account', 'live-calls': 'audit.res.live-calls',
  'cdrs': 'audit.res.cdrs',
}
const actions: Record<string, [string, Tone]> = {
  create: ['audit.act.create', 'ok'], update: ['audit.act.update', 'acc'], delete: ['audit.act.delete', 'bad'],
  payment: ['audit.act.payment', 'neutral'], adjustment: ['audit.act.adjustment', 'neutral'], refund: ['audit.act.refund', 'neutral'],
  rates_import: ['audit.act.rates_import', 'neutral'], submit: ['audit.act.submit', 'neutral'], hangup: ['audit.act.hangup', 'warn'],
  unban: ['audit.act.unban', 'neutral'], send: ['audit.act.send', 'neutral'], password_issued: ['audit.act.password_issued', 'neutral'],
  login: ['audit.act.login', 'neutral'], login_failed: ['audit.act.login_failed', 'warn'], password_changed: ['audit.act.password_changed', 'neutral'],
  password_reset_requested: ['audit.act.password_reset_requested', 'neutral'], password_reset: ['audit.act.password_reset', 'neutral'],
  two_step_on: ['audit.act.two_step_on', 'ok'], two_step_off: ['audit.act.two_step_off', 'warn'],
  two_step_reset: ['audit.act.two_step_reset', 'warn'], recovery_codes_renewed: ['audit.act.recovery_codes_renewed', 'neutral'],
  require_two_step_on: ['audit.act.require_two_step_on', 'ok'], require_two_step_off: ['audit.act.require_two_step_off', 'warn'],
  sign_in_set: ['audit.act.sign_in_set', 'neutral'], sign_in_cleared: ['audit.act.sign_in_cleared', 'neutral'], unregister: ['audit.act.unregister', 'warn'],
}
export function addAuditLabels(more: { resources?: Record<string, string>; actions?: Record<string, [string, Tone]> }) {
  Object.assign(resources, more.resources)
  Object.assign(actions, more.actions)
}

// What an operation's details are called; one the panel does not know is
// shown under the API's name for it. Amounts are in micro-units.
const details: Record<string, string> = {
  'amount': 'audit.detail.amount', 'balance_after': 'audit.detail.balance_after', 'reference': 'audit.detail.reference',
  'note': 'audit.detail.note', 'reason': 'audit.detail.reason', 'locked': 'audit.detail.locked',
  'unlocked': 'audit.detail.unlocked', 'two_step_setup_required': 'audit.detail.two_step_setup_required', 'username': 'audit.detail.username',
  'role': 'audit.detail.role', 'two_step': 'audit.detail.two_step', 'method': 'audit.detail.method',
  'was_on': 'audit.detail.was_on', 'effective_from': 'audit.detail.effective_from', 'mode': 'audit.detail.mode',
  'rates': 'audit.detail.rates', 'closed': 'audit.detail.closed', 'invalid': 'audit.detail.invalid',
  'inserted': 'audit.detail.inserted', 'duplicates': 'audit.detail.duplicates', 'period_start': 'audit.detail.period_start',
  'period_end': 'audit.detail.period_end', 'recipients': 'audit.detail.recipients', 'subtotal': 'audit.detail.subtotal',
  'tax': 'audit.detail.tax', 'total': 'audit.detail.total',
}
const amounts = new Set(['amount', 'balance_after', 'subtotal', 'tax', 'total'])
const reasons: Record<string, string> = {
  'password': 'audit.reason.password', 'code': 'audit.reason.code', 'disabled': 'audit.reason.disabled',
  'app': 'audit.reason.app', 'recovery': 'audit.reason.recovery',
}
export function addAuditDetails(more: Record<string, string>) { Object.assign(details, more) }

const actors = () => [{ value: 'user', label: t('audit.actor.user') }, { value: 'key', label: t('audit.actor.key') }, { value: 'system', label: t('audit.actor.system') }]
const resourceName = (r: string) => (resources[r] ? tx(resources[r]) : r)
/** Names in alphabetical order of the language in force. */
const sorted = (m: Record<string, string>) => Object.entries(m).map(([v, l]) => [v, tx(l)]).sort((a, b) => a[1].localeCompare(b[1], getLang()))

function Action({ value }: { value: string }) {
  const known = actions[value]
  return known ? <Badge tone={known[1]}>{tx(known[0])}</Badge> : <Badge><span className="mono">{value}</span></Badge>
}

function Actor({ row }: { row: Row }) {
  if (row.actor_type === 'system') return <span className="tx2">{t('audit.actor.system')}</span>
  return <>
    <span className={row.actor_type === 'key' ? 'mono' : undefined}>{row.actor_name || `#${row.actor_id}`}</span>
    {row.actor_type === 'key' && <span className="tx3">{t('audit.keySuffix')}</span>}
  </>
}

/** A value as it was stored; what the server masked is said to be hidden. */
function Value({ v }: { v: unknown }) {
  if (v === undefined) return <span className="tx3">—</span>
  if (v === '***') return <span className="tx3">{t('audit.hidden')}</span>
  if (v === null || v === '') return <span className="tx3">{t('audit.empty')}</span>
  if (v === true || v === false) return <>{v ? t('audit.yes') : t('audit.no')}</>
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
    try { await download('/v1/audit-log/export', filters, t('audit.file')) } catch (e) { ui.toast(errorText(e as ApiError)) } finally { setExporting(false) }
  }
  const clear = () => { setFrom(''); setTo(''); setActor(''); setResource(''); setAction(''); setQ('') }

  const cols: Col<Row>[] = [
    { label: t('audit.time'), width: '150px', mono: true, render: (r) => dt(r.at, true) },
    { label: t('audit.who'), width: 'minmax(170px,1.1fr)', render: (r) => <span className="ell"><Actor row={r} /></span> },
    { label: t('audit.action'), width: '210px', render: (r) => <Action value={r.action} /> },
    { label: t('audit.resource'), width: '170px', render: (r) => resourceName(r.resource) },
    { label: t('audit.record'), width: 'minmax(150px,1fr)', render: (r) => <span className="ell">{r.label || (r.row_id ? <span className="mono tx2">#{r.row_id}</span> : <span className="tx3">—</span>)}</span> },
    { label: t('audit.address'), width: '130px', mono: true, render: (r) => r.ip || '—' },
  ]
  const s = selected
  const changes = Object.entries((s?.changes ?? {}) as Record<string, { old?: unknown; new?: unknown }>).sort((a, b) => a[0].localeCompare(b[0]))
  const detail = Object.entries((s?.detail ?? {}) as Record<string, unknown>)
  return (
    <div className="page">
      <PageHead title={t('audit.title')} desc={t('audit.desc')}>
        <button className="btn" disabled={!list.rows.length || exporting} onClick={exportCSV} title={t('audit.exportTitle')}>{exporting ? t('action.preparing') : t('action.downloadCsv')}</button>
      </PageHead>
      <div className="card clip">
        <div className="filters">
          <label className="field"><span className="label">{t('audit.from')}</span><input type="datetime-local" className="input mono" value={from} onChange={(e) => setFrom(e.target.value)} /></label>
          <label className="field"><span className="label">{t('audit.to')}</span><input type="datetime-local" className="input mono" value={to} onChange={(e) => setTo(e.target.value)} /></label>
          <label className="field"><span className="label">{t('audit.who')}</span>
            <select className="select" value={actor} onChange={(e) => setActor(e.target.value)}><option value="">{t('audit.all')}</option>{actors().map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}</select></label>
          <label className="field"><span className="label">{t('audit.resource')}</span>
            <select className="select" value={resource} onChange={(e) => setResource(e.target.value)}><option value="">{t('audit.all')}</option>{sorted(resources).map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></label>
          <label className="field"><span className="label">{t('audit.action')}</span>
            <select className="select" value={action} onChange={(e) => setAction(e.target.value)}><option value="">{t('audit.all')}</option>
              {sorted(Object.fromEntries(Object.entries(actions).map(([v, [l]]) => [v, l]))).map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select></label>
          <label className="field"><span className="label">{t('audit.search')}</span><Search width="100%" value={q} onChange={setQ} placeholder={t('audit.searchPlaceholder')} /></label>
        </div>
        <DataTable cols={cols} rows={list.rows} minWidth={1040} gap={14} loading={list.loading} error={list.error} onRetry={list.refetch}
          onRow={setSelected} selectedId={s?.id} errorTitle={t('audit.loadError')}
          empty={from || to || actor || resource || action || q
            ? { title: t('audit.noMatch'), desc: t('audit.noMatchDesc'), action: <button className="btn" onClick={clear}>{t('audit.clearFilters')}</button> }
            : { title: t('audit.none'), desc: t('audit.noneDesc') }} />
        <Pager p={list} noun={t('audit.noun')} />
      </div>
      {s && (
        <div className="drawer" style={{ width: 440 }}>
          <div className="row" style={{ padding: '12px 16px', borderBottom: '1px solid var(--line)' }}>
            <div className="sect grow">{t('audit.detailTitle')}</div>
            <Action value={s.action} />
            <button className="btn inline" onClick={() => setSelected(null)}>{t('action.close')}</button>
          </div>
          <div style={{ padding: '8px 16px' }}>
            <div className="kv"><span>{t('audit.time')}</span><span className="mono">{dt(s.at, true)}</span></div>
            <div className="kv"><span>{t('audit.who')}</span><span><Actor row={s} /></span></div>
            <div className="kv"><span>{t('audit.address')}</span><span className="mono">{s.ip || '—'}</span></div>
            <div className="kv"><span>{t('audit.resource')}</span><span>{resourceName(s.resource)}</span></div>
            <div className="kv"><span>{t('audit.record')}</span><span>{s.label || '—'}{s.row_id ? <span className="mono tx3"> #{s.row_id}</span> : null}</span></div>
          </div>
          {changes.length > 0 && (
            <div style={{ padding: '12px 16px', borderTop: '1px solid var(--line2)' }}>
              <div className="tx3" style={{ fontSize: 11.5, marginBottom: 6 }}>{s.action === 'create' ? t('audit.created') : s.action === 'delete' ? t('audit.deleted') : t('audit.changed')}</div>
              <div style={{ display: 'grid', gridTemplateColumns: s.action === 'update' ? 'minmax(90px,auto) 1fr 1fr' : 'minmax(90px,auto) 1fr', gap: '6px 12px', alignItems: 'baseline' }}>
                <span className="tx3" style={{ fontSize: 11.5 }}>{t('audit.field')}</span>
                {s.action !== 'create' && <span className="tx3" style={{ fontSize: 11.5 }}>{t('audit.old')}</span>}
                {s.action !== 'delete' && <span className="tx3" style={{ fontSize: 11.5 }}>{t('audit.new')}</span>}
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
              <div className="tx3" style={{ fontSize: 11.5, marginBottom: 6 }}>{t('audit.details')}</div>
              {detail.map(([k, v]) => <div className="kv" key={k}>
                {details[k] ? <span>{tx(details[k])}</span> : <span className="mono" style={{ fontSize: 12 }}>{k}</span>}
                <span>{amounts.has(k) && typeof v === 'number' ? <span className="mono">{money(v)}</span>
                  : (k === 'reason' || k === 'method') && typeof v === 'string' && reasons[v] ? tx(reasons[v]) : <Value v={v} />}</span>
              </div>)}
            </div>
          )}
          {changes.length === 0 && detail.length === 0 && <div className="tx2" style={{ padding: '12px 16px', borderTop: '1px solid var(--line2)' }}>{t('audit.noDetail')}</div>}
        </div>
      )}
    </div>
  )
}
