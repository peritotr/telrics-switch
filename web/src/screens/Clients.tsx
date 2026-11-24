// SPDX-License-Identifier: AGPL-3.0-only
import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, useGet, useInvalidate, useLookup, type Row } from '../lib/api'
import { dt, money, moneyInput, parseMoney } from '../lib/format'
import { rich, t, tx } from '../lib/i18n'
import { useSession } from '../lib/session'
import { Badge, DataTable, Field, FormModal, Modal, PageHead, Stat, StatusBadge, Tabs, Toggle, errorText, useUI, type Col } from '../ui/kit'
import { CallsTab, LedgerTab, ListPage, PaymentModal, codecOptions, currencies, idOrNull, int, secondsError, text } from './common'

const billing = [{ value: 'prepaid', label: 'Prepaid' }, { value: 'postpaid', label: 'Postpaid' }]
const billingLabel = (v: string) => (v === 'postpaid' ? 'Postpaid' : 'Prepaid')

export function Clients() {
  const navigate = useNavigate()
  const tariffs = useLookup('/v1/tariffs', { kind: 'sell' })
  const plans = useLookup('/v1/routing-plans')
  const cols: Col<Row>[] = [
    { label: t('clients.name'), width: 'minmax(200px,1.6fr)', render: (r) => r.name },
    { label: t('clients.billing'), width: '100px', render: (r) => <span className="tx2">{billingLabel(r.billing_mode)}</span> },
    { label: t('clients.balance'), width: '150px', align: 'right', mono: true, render: (r) => <span className={r.balance < 0 ? 'bad' : undefined}>{money(r.balance)} {r.currency}</span> },
    { label: t('clients.creditLimit'), width: '130px', align: 'right', mono: true, render: (r) => money(r.credit_limit) },
    { label: t('clients.tariff'), width: 'minmax(140px,1fr)', render: (r) => tariffs.name(r.tariff_id) },
    { label: t('clients.plan'), width: 'minmax(130px,1fr)', render: (r) => plans.name(r.routing_plan_id) },
    { label: t('clients.status'), width: '90px', render: (r) => <StatusBadge value={r.status} /> },
  ]
  return <ListPage title={t('clients.title')} desc={t('clients.desc')} path="/v1/clients" noun={t('clients.noun')}
    cols={cols} minWidth={1040} onRow={(r) => navigate(`/clients/${r.id}`)}
    add={{
      label: t('clients.add'), title: t('clients.add'), sub: t('clients.addSub'),
      initial: { billing_mode: 'prepaid', currency: 'USD' },
      fields: [
        { key: 'name', label: t('clients.name'), required: true, span: true },
        { key: 'billing_mode', label: t('clients.billingMode'), type: 'seg', options: billing },
        { key: 'currency', label: t('clients.currency'), type: 'seg', options: currencies },
        { key: 'tariff_id', label: t('clients.tariff'), type: 'select', options: tariffs.rows.map((r) => ({ value: String(r.id), label: r.name })) },
        { key: 'routing_plan_id', label: t('clients.plan'), type: 'select', options: plans.rows.map((r) => ({ value: String(r.id), label: r.name })) },
      ],
      build: (v) => ({ name: text(v.name), billing_mode: v.billing_mode, currency: v.currency, tariff_id: idOrNull(v.tariff_id), routing_plan_id: idOrNull(v.routing_plan_id) }),
      done: (row) => navigate(`/clients/${row.id}`),
    }} />
}

export function ClientDetail() {
  const id = Number(useParams().id)
  const { canEdit, ext } = useSession()
  const q = useGet<Row>(`/v1/clients/${id}`)
  const tariffs = useLookup('/v1/tariffs', { kind: 'sell' })
  const plans = useLookup('/v1/routing-plans')
  const [tab, setTab] = useState('genel')
  const [paying, setPaying] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  const c = q.data
  if (q.error) return <div className="page"><Link to="/clients" style={{ fontSize: 12 }}>{t('clients.back')}</Link><div className="banner bad">{t('clients.loadFailed', { error: errorText(q.error as any) })}</div></div>
  if (!c) return <div className="tx3">{t('kit.loading')}</div>

  const suspended = c.status !== 'active'
  const toggleStatus = async () => {
    const ok = await ui.confirm(suspended
      ? { title: t('clients.activateTitle', { name: c.name }), body: t('clients.activateBody'), action: t('clients.activate') }
      : { title: t('clients.suspendTitle', { name: c.name }), body: t('clients.suspendBody'), action: t('clients.suspend'), danger: true })
    if (!ok) return
    try {
      await api.patch(`/v1/clients/${id}`, { status: suspended ? 'active' : 'suspended' })
      invalidate('/v1/clients')
      ui.toast(suspended ? t('clients.activated') : t('clients.suspended'))
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const plan = plans.byId.get(c.routing_plan_id)
  const tariff = tariffs.byId.get(c.tariff_id)
  const extra = ext.flatMap((e) => e.clientTabs ?? [])
  const tabs = [
    { value: 'genel', label: t('clients.tabGeneral') }, { value: 'trunk', label: t('clients.tabTrunks') }, { value: 'bundles', label: t('clients.tabBundles') }, { value: 'ledger', label: t('clients.tabLedger') }, { value: 'calls', label: t('clients.tabCalls') },
    ...extra.map((e) => ({ value: e.key, label: tx(e.label) })),
  ]
  const payButton = canEdit && <button className="btn primary" onClick={() => setPaying(true)}>{t('clients.addPayment')}</button>
  return (
    <div className="page">
      <PageHead back={<Link to="/clients" style={{ fontSize: 12 }}>{t('clients.back')}</Link>}
        title={<><h1>{c.name}</h1><StatusBadge value={c.status} /><span className="mono tx3" style={{ fontSize: 12 }}>ID {c.id}</span></>}>
        {canEdit && <div className="row">
          <button className="btn" onClick={toggleStatus}>{suspended ? t('clients.activate') : t('clients.suspend')}</button>
          {payButton}
        </div>}
      </PageHead>
      <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(170px,1fr))' }}>
        <Stat label={t('clients.balance')} value={money(c.balance)} unit={c.currency} sub={t('clients.balanceSub')} tone={c.balance < 0 ? 'bad' : undefined} />
        <Stat label={t('clients.creditLimit')} value={money(c.credit_limit)} unit={c.currency}
          sub={c.billing_mode === 'prepaid' ? t('clients.prepaidSub') : t('clients.postpaidSub')} />
        <Stat text label={t('clients.tariff')} value={tariff ? <Link to={`/tariffs/${tariff.id}`}>{tariff.name}</Link> : '—'} sub={tariff ? t('clients.tariffSub', { currency: tariff.currency }) : t('clients.noTariff')} />
        <Stat text label={t('clients.plan')} value={plan ? <Link to={`/plans/${plan.id}`}>{plan.name}</Link> : '—'}
          sub={plan ? t('clients.planSub', { policy: plan.policy === 'lcr' ? 'LCR' : t('clients.priority'), loss: plan.allow_loss ? t('clients.lossAllowed') : t('clients.lossForbidden') }) : t('clients.noPlan')} />
      </div>
      <div className="card clip">
        <Tabs value={tab} tabs={tabs} onChange={setTab} />
        {tab === 'genel' && <GeneralForm key={c.id} client={c} tariffs={tariffs.rows} plans={plans.rows} />}
        {tab === 'trunk' && <Trunks client={c} />}
        {tab === 'bundles' && <Bundles client={c} />}
        {tab === 'ledger' && <LedgerTab party="client" row={c} action={payButton} />}
        {tab === 'calls' && <CallsTab filter={{ client_id: c.id }} />}
        {extra.find((t) => t.key === tab)?.render(c)}
      </div>
      {paying && <PaymentModal party="client" row={c} onClose={() => setPaying(false)} />}
    </div>
  )
}

function GeneralForm({ client, tariffs, plans }: { client: Row; tariffs: Row[]; plans: Row[] }) {
  const { canEdit, ext } = useSession()
  const initial = () => ({
    ...client, credit_limit: moneyInput(client.credit_limit), low_balance: client.low_balance == null ? '' : moneyInput(client.low_balance), tariff_id: client.tariff_id ?? '', routing_plan_id: client.routing_plan_id ?? '',
    email: client.email ?? '', tax_id: client.tax_id ?? '', address: client.address ?? '',
  })
  const [v, setV] = useState<Row>(initial)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  useEffect(() => setV(initial()), [client]) // eslint-disable-line react-hooks/exhaustive-deps
  const set = (k: string, val: any) => setV((old) => ({ ...old, [k]: val }))
  const bind = (k: string) => ({ value: v[k] ?? '', onChange: (e: { target: { value: string } }) => set(k, e.target.value) })
  const save = async () => {
    const limit = parseMoney(String(v.credit_limit))
    if (!text(v.name)) return setError(t('clients.errName'))
    if (limit == null || limit < 0) return setError(t('clients.errLimit'))
    const lowText = String(v.low_balance ?? '').trim()
    const low = lowText === '' ? null : parseMoney(lowText)
    if (lowText !== '' && low == null) return setError(t('clients.errLow'))
    const body: Row = {
      name: text(v.name), email: text(v.email), tax_id: text(v.tax_id), address: text(v.address), status: v.status,
      currency: v.currency, billing_mode: v.billing_mode, credit_limit: limit, tariff_id: idOrNull(v.tariff_id), routing_plan_id: idOrNull(v.routing_plan_id),
      max_calls: int(v.max_calls), max_cps: int(v.max_cps), low_balance: low,
    }
    // Fields an extension section added or changed travel in the same request.
    const fixed = ['id', 'balance', 'created_at', 'reseller_id']
    for (const k of Object.keys(v)) if (!(k in body) && !fixed.includes(k) && v[k] !== client[k]) body[k] = v[k]
    setBusy(true)
    setError('')
    try {
      await api.patch(`/v1/clients/${client.id}`, body)
      invalidate('/v1/clients')
      ui.toast(t('clients.saved'))
    } catch (e) { setError(errorText(e as any)) }
    setBusy(false)
  }
  return (
    <>
      <fieldset disabled={!canEdit}><div className="form-grid">
        <div className="form-col">
          <div className="sect">{t('clients.identity')}</div>
          <Field label={t('clients.name')}><input className="input" {...bind('name')} /></Field>
          <Field label={t('clients.email')}><input className="input" {...bind('email')} /></Field>
          <Field label={t('clients.taxId')}><input className="input mono" {...bind('tax_id')} /></Field>
          <Field label={t('clients.address')}><textarea className="textarea" rows={2} {...bind('address')} /></Field>
          <div className="field"><span className="label">{t('clients.status')}</span>
            <div className="row wrap" style={{ gap: 16 }}>
              {[['active', t('status.active')], ['suspended', t('status.suspended')], ['closed', t('status.closed')]].map(([val, l]) =>
                <label className="radio" key={val}><input type="radio" name="cst" checked={v.status === val} onChange={() => set('status', val)} />{l}</label>)}
            </div>
          </div>
        </div>
        <div className="form-col">
          <div className="sect">{t('clients.billingRouting')}</div>
          <div className="grid2">
            <Field label={t('clients.currency')}><select className="select" {...bind('currency')}>{currencies.map((o) => <option key={o.value}>{o.value}</option>)}</select></Field>
            <Field label={t('clients.billingMode')}><select className="select" {...bind('billing_mode')}>{billing.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}</select></Field>
          </div>
          <div className="field"><span className="label">{t('clients.balance')}</span>
            <div className="readonly-box">{money(client.balance)} {client.currency}</div>
            <span className="hint">{t('clients.balanceHint')}</span>
          </div>
          <Field label={t('clients.creditLimitIn', { currency: v.currency })}><input className="input num" inputMode="decimal" {...bind('credit_limit')} /></Field>
          <Field label={t('clients.lowBalance', { currency: v.currency })} hint={t('clients.lowBalanceHint')}><input className="input num" inputMode="decimal" {...bind('low_balance')} /></Field>
          <Field label={t('clients.tariff')}><select className="select" {...bind('tariff_id')}><option value="">—</option>{tariffs.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}</select></Field>
          <Field label={t('clients.plan')}><select className="select" {...bind('routing_plan_id')}><option value="">—</option>{plans.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}</select></Field>
        </div>
        <div className="form-col">
          <div className="sect">{t('clients.limits')}</div>
          <div className="grid2">
            <Field label={t('clients.maxCalls')} hint={t('clients.unlimited')}><input className="input num" inputMode="numeric" {...bind('max_calls')} /></Field>
            <Field label={t('clients.maxCps')} hint={t('clients.unlimited')}><input className="input num" inputMode="numeric" {...bind('max_cps')} /></Field>
          </div>
          {ext.map((e, i) => e.clientForm && <div key={i} className="form-col">{e.clientForm({ values: v, set, disabled: !canEdit })}</div>)}
        </div>
      </div></fieldset>
      {error && <div className="banner bad" style={{ margin: '0 20px 12px' }}>{error}</div>}
      {canEdit && <div className="form-foot">
        <button className="btn" onClick={() => { setV(initial()); setError('') }}>{t('action.cancel')}</button>
        <button className="btn primary" disabled={busy} onClick={save}>{t('action.save')}</button>
      </div>}
    </>
  )
}

// A trunk's addresses as typed: one per line or separated by commas.
const addressList = (v: any) => String(v ?? '').split(/[\s,;]+/).filter((a) => a !== '')
// One address or a network, IPv4 or IPv6; the server has the last word.
const sourceAddress = /^(?=.*[.:])[0-9a-fA-F:.]+(\/\d{1,3})?$/

// What a new trunk gets for the settings in the form's advanced part; the
// form counts a setting as changed when it is not this.
const trunkDefaults = {
  security: 'any', max_contacts: 1, tech_prefix: '', number_forms: [] as string[], number_other: '', max_duration_sec: '0', max_calls: '0', max_cps: '0', codecs: [] as string[], transcode: false,
}
// How a client writes numbers is a list of rules (what to take off the
// front, what to put there; the longest match wins). The form offers the
// usual ones by name and takes any other as a line "silinecek > eklenecek".
type NumberRule = { strip: string; add: string }
const ruleLine = /^(\+?\d{0,8})\s*>\s*(\d{0,8})$/
const numberForms = (t: Row) => {
  const forms: string[] = [], other: string[] = []
  let country = ''
  for (const r of (t.number_rules ?? []) as NumberRule[]) {
    if (r.strip === '+' && r.add === '') forms.push('plus')
    else if (r.strip === '00' && r.add === '') forms.push('zeros')
    else if (r.strip === '0' && r.add !== '') { forms.push('national'); country = r.add }
    else other.push(`${r.strip} > ${r.add}`.trim())
  }
  return { number_forms: forms, number_add: country, number_other: other.join('\n') }
}
const otherLines = (v: Row) => String(v.number_other ?? '').split('\n').map((l) => l.trim()).filter((l) => l !== '')
const numberRules = (v: Row): NumberRule[] => {
  const forms: string[] = v.number_forms ?? []
  const named: NumberRule[] = [['plus', '+', ''], ['zeros', '00', ''], ['national', '0', v.number_add ?? '']]
    .filter(([f]) => forms.includes(f)).map(([, strip, add]) => ({ strip, add }))
  return [...named, ...otherLines(v).map((l) => { const m = ruleLine.exec(l); return { strip: m?.[1] ?? '', add: m?.[2] ?? '' } })]
}
// What is wrong with the rules typed by hand, if anything.
const numberRulesError = (v: Row) => {
  const bad = otherLines(v).find((l) => { const m = ruleLine.exec(l); return !m || (m[1] === '' && m[2] === '') })
  if (bad) return t('clients.errRuleLine', { line: bad })
  const rules = numberRules(v)
  const twice = rules.find((r, i) => rules.findIndex((o) => o.strip === r.strip) !== i)
  if (twice) return twice.strip === '' ? t('clients.errRuleEmptyTwice') : t('clients.errRuleTwice', { prefix: twice.strip })
  return rules.length > 20 ? t('clients.errRuleMax') : undefined
}
/** How many of a trunk's advanced settings are not as a new one comes. */
const trunkSettings = (t: Row) => [t.security !== 'any', (t.max_contacts ?? 1) !== 1, t.tech_prefix !== '', numberForms(t).number_forms.length > 0, numberForms(t).number_other !== '',
  !!t.max_duration_sec, !!t.max_calls, !!t.max_cps, (t.codecs ?? []).length > 0, !!t.transcode].filter(Boolean).length

function Trunks({ client }: { client: Row }) {
  const { canEdit } = useSession()
  const q = useGet<Row[]>('/v1/client-trunks', { client_id: client.id, limit: 1000 })
  // Where the trunks that sign in are registered right now; the engine
  // keeps it, and registrations come and go by themselves.
  const regs = useQuery({ queryKey: ['/v1/registrations', { client_id: client.id }],
    queryFn: () => api.get<{ registrations: Row[] }>('/v1/registrations', { client_id: client.id }), refetchInterval: 15000, retry: false })
  const regsOf = (tr: Row) => (regs.data?.registrations ?? []).filter((r) => r.trunk_id === tr.id)
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const [issued, setIssued] = useState<Row | null>(null)
  const [shown, setShown] = useState<Row | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()
  // A trunk that signs in has no password until one is issued; issuing
  // again replaces it, and only the newest is ever shown.
  const issue = async (tr: Row, renew: boolean) => {
    if (renew && !(await ui.confirm({ title: t('clients.renewTitle', { name: tr.name }), body: t('clients.renewBody'), action: t('clients.renew'), danger: true }))) return
    try {
      setIssued(await api.post(`/v1/client-trunks/${tr.id}/password`))
      invalidate('/v1/client-trunks')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const toggle = async (tr: Row) => {
    if (tr.enabled && !(await ui.confirm({ title: t('clients.trunkOffTitle', { name: tr.name }), body: t('clients.trunkOffBody'), action: t('action.close'), danger: true }))) return
    try {
      await api.patch(`/v1/client-trunks/${tr.id}`, { enabled: !tr.enabled })
      invalidate('/v1/client-trunks')
    } catch (e) {
      const err = e as any
      ui.toast(err?.status === 409 ? t('clients.trunkOnConflict') : errorText(err))
    }
  }
  // As many addresses as fit, and how many there are when there are several.
  const addresses = (tr: Row) => {
    const list = (tr.addresses as string[] | undefined) ?? []
    if (list.length === 0) return '—'
    if (list.length === 1) return list[0]
    return <span className="row" style={{ gap: 6, minWidth: 0 }} title={list.join('\n')}>
      <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{list.join(', ')}</span>
      <span className="tx2" style={{ flex: 'none' }}>({list.length})</span></span>
  }
  // Whether a trunk that signs in is registered right now; the details
  // open from the mark.
  const registered = (tr: Row) => {
    if (regs.isPending) return null
    if (regs.error) return <span className="tx2" style={{ flex: 'none', fontFamily: 'var(--sans)' }} title={errorText(regs.error as any)}>{t('clients.regUnknown')}</span>
    const list = regsOf(tr)
    if (list.length === 0) return <span className="tx2" style={{ flex: 'none', fontFamily: 'var(--sans)' }}>{t('clients.regNone')}</span>
    return <button className="btn inline" style={{ flex: 'none', fontFamily: 'var(--sans)' }} title={list.map((r) => `${r.src_ip}:${r.src_port}`).join('\n')} onClick={() => setShown(tr)}>
      <span className="ok">{t('clients.regYes')}</span>{list.length > 1 ? ` (${list.length})` : ''}</button>
  }
  const cols: Col<Row>[] = [
    { label: t('clients.name'), width: '140px', mono: true, render: (tr) => tr.name },
    { label: t('clients.auth'), width: '140px', render: (tr) => <span className="tx2">{tr.auth_type === 'ip' ? 'IP' : t('clients.authDigest')}</span> },
    { label: t('clients.addressesUser'), width: 'minmax(180px,1fr)', mono: true, render: (tr) => (tr.auth_type === 'ip' ? addresses(tr)
      : <span className="row" style={{ gap: 8, minWidth: 0 }}><span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{tr.username}</span>{registered(tr)}</span>) },
    { label: t('clients.techPrefix'), width: '100px', mono: true, render: (tr) => tr.tech_prefix || '—' },
    { label: t('clients.encryption'), width: '180px', render: (tr) => tr.security === 'cert' ? <span>{t('clients.certificate')} · <span className="mono">{tr.tls_cert_name}</span></span>
      : <span className="tx2">{tr.security === 'tls' ? t('clients.tlsRequired') : t('clients.open')}</span> },
    { label: t('clients.advancedCol'), width: '100px', render: (tr) => (trunkSettings(tr) ? <Badge tone="acc">{t('clients.settingsCount', { n: trunkSettings(tr) })}</Badge> : <span className="tx3">{t('clients.default')}</span>) },
    { label: '', width: '190px', align: 'right', render: (tr) => <span className="row" style={{ gap: 10, justifyContent: 'flex-end' }}>
      {canEdit && tr.auth_type === 'digest' && <button className="btn inline" onClick={() => issue(tr, true)}>{t('clients.issuePassword')}</button>}
      {canEdit && <button className="btn inline" onClick={() => setEditing(tr)}>{t('action.edit')}</button>}
      <Toggle on={tr.enabled} onChange={canEdit ? () => toggle(tr) : undefined} /></span> },
  ]
  const adding = editing === 'new'
  const byAddress = (v: Row) => (adding ? v.auth_type !== 'digest' : (editing as Row).auth_type === 'ip')
  return (
    <>
      <div className="toolbar">
        <div className="grow tx2">{t('clients.trunksHelp')}</div>
        {canEdit && <button className="btn" onClick={() => setEditing('new')}>{t('clients.addTrunk')}</button>}
      </div>
      <DataTable cols={cols} rows={q.data ?? []} minWidth={1120} loading={q.isPending} error={q.error as any} onRetry={q.refetch}
        empty={{ title: t('clients.noTrunks'), desc: t('clients.noTrunksDesc') }} />
      {editing && <FormModal title={adding ? t('clients.addTrunk') : t('clients.editTrunk')} action={adding ? t('action.add') : t('action.save')} onClose={() => setEditing(null)}
        sub={adding ? t('clients.trunkSubNew', { name: client.name })
          : editing.auth_type === 'ip' ? t('clients.trunkSubIp', { name: client.name }) : t('clients.trunkSubUser', { name: client.name, user: editing.username })}
        width={640} defaults={trunkDefaults}
        initial={adding ? { auth_type: 'ip', ...trunkDefaults }
          : { name: editing.name, addresses: ((editing.addresses as string[] | undefined) ?? []).join('\n'), tech_prefix: editing.tech_prefix, security: editing.security, tls_cert_name: editing.tls_cert_name, max_contacts: editing.max_contacts ?? 1,
            ...numberForms(editing),
            max_duration_sec: String(editing.max_duration_sec), max_calls: String(editing.max_calls), max_cps: String(editing.max_cps), codecs: editing.codecs ?? [], transcode: !!editing.transcode }}
        fields={[
          { key: 'name', label: t('clients.name'), type: 'mono', required: true },
          { key: 'auth_type', label: t('clients.authType'), type: 'seg', options: [{ value: 'ip', label: t('clients.sourceIp') }, { value: 'digest', label: t('clients.userPassword') }], show: () => adding,
            hint: t('clients.authTypeHint') },
          { key: 'addresses', label: t('clients.addresses'), type: 'textarea', mono: true, rows: 3, required: true, span: true, placeholder: '203.0.113.10\n198.51.100.0/24\n2001:db8:12::/48', show: byAddress,
            hint: t('clients.addressesHint') },
          { key: 'username', label: t('clients.username'), type: 'mono', required: true, show: (v) => adding && v.auth_type === 'digest', hint: t('clients.usernameHint') },

          { key: 's_security', label: t('clients.security'), type: 'section', advanced: true },
          { key: 'security', label: t('clients.encryption'), type: 'seg', span: true, advanced: true, options: [{ value: 'any', label: t('clients.open') }, { value: 'tls', label: t('clients.tlsRequired') }, { value: 'cert', label: t('clients.certAndTls') }],
            hint: t('clients.securityHint') },
          { key: 'tls_cert_name', label: t('clients.certName'), type: 'mono', required: true, span: true, advanced: true, placeholder: t('clients.certNamePlaceholder'), show: (v) => v.security === 'cert',
            hint: t('clients.certNameHint') },
          { key: 'max_contacts', label: t('clients.maxContacts'), type: 'int', span: true, advanced: true, show: (v) => !byAddress(v),
            hint: t('clients.maxContactsHint') },

          { key: 's_number', label: t('clients.numberFormat'), type: 'section', advanced: true, hint: t('clients.numberFormatHint') },
          { key: 'tech_prefix', label: t('clients.techPrefix'), type: 'mono', span: true, advanced: true, hint: t('clients.techPrefixHint') },
          { key: 'number_forms', label: t('clients.numberForms'), type: 'pick', span: true, advanced: true, options: [
            { value: 'plus', label: t('clients.formPlus') }, { value: 'zeros', label: t('clients.formZeros') },
            { value: 'national', label: t('clients.formNational') }],
            hint: t('clients.numberFormsHint') },
          { key: 'number_add', label: t('clients.countryCode'), type: 'mono', advanced: true, placeholder: '90', show: (v) => (v.number_forms ?? []).includes('national'), hint: t('clients.countryCodeHint') },
          { key: 'number_other', label: t('clients.otherRules'), type: 'textarea', mono: true, rows: 2, span: true, advanced: true, placeholder: '011 > 1\n> 90',
            hint: t('clients.otherRulesHint') },

          { key: 's_limits', label: t('clients.durationLimits'), type: 'section', advanced: true, hint: t('clients.durationLimitsHint') },
          { key: 'max_duration_sec', label: t('clients.maxDuration'), type: 'int', span: true, advanced: true, hint: t('clients.maxDurationHint') },
          { key: 'max_calls', label: t('clients.trunkMaxCalls'), type: 'int', advanced: true, hint: t('clients.trunkMaxCallsHint') },
          { key: 'max_cps', label: t('clients.maxCps'), type: 'int', advanced: true, hint: t('clients.trunkMaxCpsHint') },

          { key: 's_codecs', label: t('clients.audioCodecs'), type: 'section', advanced: true },
          { key: 'codecs', label: t('clients.codecs'), type: 'pick', span: true, advanced: true, options: codecOptions(),
            hint: t('clients.codecsHint') },
          { key: 'transcode', label: t('clients.transcode'), type: 'check', span: true, advanced: true, text: t('clients.transcodeText'),
            hint: t('clients.transcodeHint') },
        ]}
        validate={(v) => {
          const list = addressList(v.addresses)
          const bad = list.find((a) => !sourceAddress.test(a))
          return {
            tech_prefix: /^\d*$/.test(v.tech_prefix ?? '') ? undefined : t('clients.digitsOnly'),
            number_add: !(v.number_forms ?? []).includes('national') ? undefined : !v.number_add ? t('clients.errCountry') : /^\d{1,8}$/.test(v.number_add) ? undefined : t('clients.errMax8'),
            number_other: numberRulesError(v),
            max_duration_sec: secondsError(v.max_duration_sec, 5, 7200),
            max_calls: /^\d*$/.test(String(v.max_calls ?? '')) ? undefined : t('clients.digitsOnly'),
            max_cps: /^\d*$/.test(String(v.max_cps ?? '')) ? undefined : t('clients.digitsOnly'),
            max_contacts: byAddress(v) || (Number(v.max_contacts) >= 1 && Number(v.max_contacts) <= 10) ? undefined : t('clients.errContacts'),
            username: adding && v.auth_type === 'digest' && !/^[A-Za-z0-9._-]{3,64}$/.test(String(v.username ?? '')) ? t('clients.errUsername') : undefined,
            addresses: !byAddress(v) ? undefined : bad ? t('clients.errAddress', { address: bad })
              : list.length > 200 ? t('clients.errAddressMax') : undefined,
          }
        }}
        onSubmit={async (v) => {
          const digest = adding ? v.auth_type === 'digest' : editing.auth_type === 'digest'
          const common = { name: text(v.name), tech_prefix: v.tech_prefix ?? '', security: v.security ?? 'any', tls_cert_name: v.security === 'cert' ? text(v.tls_cert_name) : '',
            number_rules: numberRules(v), max_duration_sec: int(v.max_duration_sec), max_calls: int(v.max_calls), max_cps: int(v.max_cps), codecs: v.codecs ?? [], transcode: !!v.transcode,
            ...(digest ? { max_contacts: int(v.max_contacts, 1) } : { addresses: addressList(v.addresses) }) }
          let row: Row
          try {
            row = adding
              ? await api.post('/v1/client-trunks', { ...common, client_id: client.id, auth_type: digest ? 'digest' : 'ip', ...(digest ? { username: text(v.username) } : {}) })
              : await api.patch(`/v1/client-trunks/${editing.id}`, common)
          } catch (e) {
            const err = e as any
            if (err?.status === 409 && !digest) throw new Error(t('clients.errConflict'))
            if (err?.status === 422 && /number.rule/.test(String(err?.message))) throw new Error(t('clients.errRules'))
            if (err?.status === 422 && !digest) throw new Error(t('clients.errNetwork'))
            throw e
          }
          invalidate('/v1/client-trunks')
          setEditing(null)
          ui.toast(adding ? t('clients.trunkAdded') : t('clients.trunkUpdated'))
          if (adding && digest) issue(row, false)
        }} />}
      {issued && <TrunkPassword row={issued} onClose={() => setIssued(null)} />}
      {shown && <TrunkRegistrations trunk={shown} list={regsOf(shown)} canEdit={canEdit} onClose={() => setShown(null)} />}
    </>
  )
}

const transportLabel = (t: string) => (t === 'tls' ? 'TLS' : t === 'tcp' ? 'TCP' : 'UDP')

/** Where a trunk is registered right now, and the way to remove a registration. */
function TrunkRegistrations({ trunk, list, canEdit, onClose }: { trunk: Row; list: Row[]; canEdit: boolean; onClose: () => void }) {
  const ui = useUI()
  const invalidate = useInvalidate()
  const remove = async (r: Row) => {
    if (!(await ui.confirm({ title: t('clients.regDeleteTitle'), body: t('clients.regDeleteBody', { trunk: trunk.name, ip: r.src_ip }), action: t('clients.regDelete'), danger: true }))) return
    try {
      await api.del(`/v1/client-trunks/${trunk.id}/registrations?contact=${encodeURIComponent(r.contact)}`)
      ui.toast(t('clients.regDeleted'))
    } catch (e) { ui.toast((e as any)?.status === 404 ? t('clients.regGone') : errorText(e as any)) }
    invalidate('/v1/registrations')
    if (list.length <= 1) onClose()
  }
  return (
    <Modal width={640} onClose={onClose}>
      <div><div className="modal-title">{t('clients.regTitle')}</div><div className="tx2" style={{ marginTop: 2 }}>{rich('clients.regSub', { name: <span className="mono">{trunk.name}</span>, user: <span className="mono">{trunk.username}</span>, n: trunk.max_contacts ?? 1 })}</div></div>
      {list.length === 0 && <div className="tx2">{t('clients.regNow')}</div>}
      {list.map((r) => (
        <div key={r.contact} style={{ border: '1px solid var(--line)', borderRadius: 8, padding: '12px 14px', display: 'grid', gap: 8 }}>
          <div className="row" style={{ gap: 8 }}>
            <span className="mono grow" style={{ fontWeight: 600 }}>{r.src_ip}:{r.src_port}</span>
            <Badge>{transportLabel(r.transport)}</Badge>
            {r.nat && <Badge tone="acc">{r.transport === 'udp' ? t('clients.behindNat') : t('clients.overConnection')}</Badge>}
            {canEdit && <button className="btn inline" onClick={() => remove(r)}>{t('clients.regDelete')}</button>}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '150px 1fr', rowGap: 4 }}>
            <span className="tx2">{t('clients.contact')}</span><span className="mono" style={{ wordBreak: 'break-all' }}>{r.contact}</span>
            <span className="tx2">{t('clients.since')}</span><span className="mono">{dt(r.since, true)}</span>
            <span className="tx2">{t('clients.until')}</span><span className="mono">{dt(r.until, true)}</span>
            {r.user_agent && <><span className="tx2">{t('clients.device')}</span><span style={{ wordBreak: 'break-all' }}>{r.user_agent}</span></>}
          </div>
        </div>
      ))}
      <div className="tx2">{t('clients.regHelp')}</div>
      <div className="modal-foot"><button className="btn" onClick={onClose}>{t('action.close')}</button></div>
    </Modal>
  )
}

/** Seconds as the panel shows talk time: whole minutes, and the seconds over. */
const talk = (sec: number) => (sec % 60 === 0 ? t('clients.min', { m: sec / 60 }) : t('clients.minSec', { m: Math.floor(sec / 60), s: sec % 60 }))
const prefixList = (v: any) => String(v ?? '').split(/[\s,;]+/).filter((p) => p !== '')

/**
 * Bundles of minutes: talk time the client gets free each calendar month
 * towards numbers starting with the bundle's prefixes. The list comes with
 * what the current month has used.
 */
function Bundles({ client }: { client: Row }) {
  const { canEdit } = useSession()
  const path = `/v1/clients/${client.id}/bundles`
  const q = useGet<Row[]>(path)
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()
  const toggle = async (b: Row) => {
    if (b.enabled && !(await ui.confirm({ title: t('clients.bundleOffTitle', { name: b.name || t('clients.bundle') }), body: t('clients.bundleOffBody'), action: t('action.close'), danger: true }))) return
    try {
      await api.patch(`/v1/bundles/${b.id}`, { enabled: !b.enabled })
      invalidate(path)
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const remove = async (b: Row) => {
    if (!(await ui.confirm({ title: t('clients.bundleDeleteTitle', { name: b.name || t('clients.bundle') }), body: t('clients.bundleDeleteBody'), action: t('action.delete'), danger: true }))) return
    try {
      await api.del(`/v1/bundles/${b.id}`)
      invalidate(path)
      ui.toast(t('clients.bundleDeleted'))
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const cols: Col<Row>[] = [
    { label: t('clients.name'), width: 'minmax(140px,1fr)', render: (b) => b.name || '—' },
    { label: t('clients.prefixes'), width: 'minmax(160px,1.4fr)', mono: true, render: (b) => (b.prefixes as string[]).map((p) => p || t('clients.allPrefixes')).join(', ') },
    { label: t('clients.monthly'), width: '110px', align: 'right', mono: true, render: (b) => talk(b.seconds) },
    { label: t('clients.usedThisMonth'), width: '140px', align: 'right', mono: true, render: (b) => talk(b.used_sec) },
    { label: t('clients.remaining'), width: '110px', align: 'right', mono: true, render: (b) => <span className={b.used_sec >= b.seconds ? 'bad' : undefined}>{talk(Math.max(b.seconds - b.used_sec, 0))}</span> },
    { label: t('clients.status'), width: '90px', render: (b) => <Badge tone={b.enabled ? 'ok' : 'neutral'}>{b.enabled ? t('common.on') : t('common.off')}</Badge> },
    { label: '', width: '190px', align: 'right', render: (b) => <span className="row" style={{ gap: 10, justifyContent: 'flex-end' }}>
      {canEdit && <button className="btn inline" onClick={() => setEditing(b)}>{t('action.edit')}</button>}
      {canEdit && <button className="btn inline" onClick={() => remove(b)}>{t('action.delete')}</button>}
      <Toggle on={b.enabled} onChange={canEdit ? () => toggle(b) : undefined} /></span> },
  ]
  const period = q.data?.[0]?.period
  return (
    <>
      <div className="toolbar">
        <div className="grow tx2">{t('clients.bundlesHelp')}{period ? t('clients.period', { period }) : ''}</div>
        {canEdit && <button className="btn" onClick={() => setEditing('new')}>{t('clients.addBundle')}</button>}
      </div>
      <DataTable cols={cols} rows={q.data ?? []} minWidth={980} loading={q.isPending} error={q.error as any} onRetry={q.refetch}
        empty={{ title: t('clients.noBundles'), desc: t('clients.noBundlesDesc') }} />
      {editing && <FormModal title={editing === 'new' ? t('clients.addBundle') : t('clients.editBundle')} action={editing === 'new' ? t('action.add') : t('action.save')} onClose={() => setEditing(null)}
        sub={t('clients.bundleSub', { name: client.name })}
        initial={editing === 'new' ? {} : { name: editing.name, prefixes: (editing.prefixes as string[]).join(', '), minutes: String(Math.floor(editing.seconds / 60)), extra: String(editing.seconds % 60 || '') }}
        fields={[
          { key: 'name', label: t('clients.name'), span: true, placeholder: t('clients.bundleNamePlaceholder') },
          { key: 'prefixes', label: t('clients.prefixes'), type: 'mono', required: true, span: true, placeholder: '9053, 9054, 9055', hint: t('clients.prefixesHint') },
          { key: 'minutes', label: t('clients.minutes'), type: 'int', required: true },
          { key: 'extra', label: t('clients.extraSec'), type: 'int', placeholder: '0' },
        ]}
        validate={(v) => {
          const list = prefixList(v.prefixes)
          const sec = int(v.minutes) * 60 + int(v.extra)
          return {
            prefixes: list.some((p) => !/^\d+$/.test(p)) ? t('clients.errPrefixes') : list.length > 200 ? t('clients.errPrefixMax') : undefined,
            minutes: /^\d*$/.test(String(v.minutes ?? '')) && sec > 0 && sec <= 2_000_000_000 ? undefined : t('clients.errMinutes'),
            extra: /^\d*$/.test(String(v.extra ?? '')) && int(v.extra) < 60 ? undefined : t('clients.errExtra'),
          }
        }}
        onSubmit={async (v) => {
          const body = { name: String(v.name ?? '').trim(), prefixes: prefixList(v.prefixes), seconds: int(v.minutes) * 60 + int(v.extra) }
          if (editing === 'new') await api.post('/v1/bundles', { ...body, client_id: client.id })
          else await api.patch(`/v1/bundles/${editing.id}`, body)
          invalidate(path)
          setEditing(null)
          ui.toast(editing === 'new' ? t('clients.bundleAdded') : t('clients.bundleUpdated'))
        }} />}
    </>
  )
}

/** The one time a trunk's password can be read: only its hash is stored. */
function TrunkPassword({ row, onClose }: { row: Row; onClose: () => void }) {
  const [saved, setSaved] = useState(false)
  const ui = useUI()
  const copy = async () => {
    try { await navigator.clipboard.writeText(row.password); ui.toast(t('clients.pwCopied')) } catch { ui.toast(t('clients.pwCopyFailed')) }
  }
  return (
    <Modal width={520}>
      <div><div className="modal-title">{t('clients.pwTitle')}</div><div className="tx2" style={{ marginTop: 2 }}>{rich('clients.pwSub', { user: <span className="mono">{row.username}</span>, realm: <span className="mono">{row.realm}</span> })}</div></div>
      <div className="banner warn" style={{ borderColor: 'color-mix(in oklch, var(--warn) 40%, transparent)' }}>
        <span className="warn" style={{ fontWeight: 600 }}>{t('clients.pwOnce')}</span> {t('clients.pwOnceBody')}
      </div>
      <div className="row">
        <div className="grow mono" style={{ padding: '10px 12px', background: 'var(--panel2)', border: '1px solid var(--line)', borderRadius: 8, wordBreak: 'break-all' }}>{row.password}</div>
        <button className="btn" onClick={copy}>{t('action.copy')}</button>
      </div>
      <label className="radio"><input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} />{t('clients.pwSent')}</label>
      <div className="modal-foot"><button className="btn primary" disabled={!saved} onClick={onClose}>{t('action.close')}</button></div>
    </Modal>
  )
}
