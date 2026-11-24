// SPDX-License-Identifier: AGPL-3.0-only
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, useGet, useInvalidate, useLookup, type Row } from '../lib/api'
import { money } from '../lib/format'
import { t, tx } from '../lib/i18n'
import { useSession } from '../lib/session'
import { useQuery } from '@tanstack/react-query'
import { Badge, DataTable, Field, FormModal, Modal, PageHead, Seg, Stat, StatusBadge, Tabs, Toggle, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { CallsTab, LedgerTab, ListPage, PaymentModal, codecOptions, currencies, idOrNull, int, secondsError, text } from './common'

const transports = ['udp', 'tcp', 'tls'].map((x) => ({ value: x, label: x.toUpperCase() }))

export function Vendors() {
  const navigate = useNavigate()
  const tariffs = useLookup('/v1/tariffs', { kind: 'buy' })
  const cols: Col<Row>[] = [
    { label: t('vendors.name'), width: 'minmax(220px,1.6fr)', render: (r) => r.name },
    { label: t('vendors.owed'), width: '170px', align: 'right', mono: true, render: (r) => `${money(r.balance)} ${r.currency}` },
    { label: t('vendors.buyTariff'), width: 'minmax(160px,1fr)', render: (r) => tariffs.name(r.tariff_id) },
    { label: t('vendors.status'), width: '90px', render: (r) => <StatusBadge value={r.status} /> },
  ]
  return <ListPage title={t('vendors.title')} desc={t('vendors.desc')} path="/v1/vendors" noun={t('vendors.noun')}
    cols={cols} minWidth={760} onRow={(r) => navigate(`/vendors/${r.id}`)}
    add={{
      label: t('vendors.add'), title: t('vendors.add'), sub: t('vendors.addSub'), initial: { currency: 'USD' },
      fields: [
        { key: 'name', label: t('vendors.name'), required: true, span: true },
        { key: 'currency', label: t('vendors.currency'), type: 'seg', options: currencies },
        { key: 'tariff_id', label: t('vendors.buyTariff'), type: 'select', options: tariffs.rows.map((r) => ({ value: String(r.id), label: r.name })) },
      ],
      build: (v) => ({ name: text(v.name), currency: v.currency, tariff_id: idOrNull(v.tariff_id) }),
      done: (row) => navigate(`/vendors/${row.id}`),
    }} />
}

export function VendorDetail() {
  const id = Number(useParams().id)
  const { canEdit, ext } = useSession()
  const q = useGet<Row>(`/v1/vendors/${id}`)
  const conns = useGet<Row[]>('/v1/vendor-connections', { vendor_id: id, limit: 1000 })
  const tariffs = useLookup('/v1/tariffs', { kind: 'buy' })
  const [tab, setTab] = useState('conn')
  const [paying, setPaying] = useState(false)
  const v = q.data
  if (q.error) return <div className="page"><Link to="/vendors" style={{ fontSize: 12 }}>{t('vendors.back')}</Link><div className="banner bad">{t('vendors.loadFailed', { error: errorText(q.error as any) })}</div></div>
  if (!v) return <div className="tx3">{t('kit.loading')}</div>
  const tariff = tariffs.byId.get(v.tariff_id)
  const extra = ext.flatMap((e) => e.vendorTabs ?? [])
  const tabs = [
    { value: 'conn', label: t('vendors.tabConnections') }, { value: 'genel', label: t('vendors.tabGeneral') }, { value: 'ledger', label: t('vendors.tabLedger') }, { value: 'calls', label: t('vendors.tabCalls') },
    ...extra.map((e) => ({ value: e.key, label: tx(e.label) })),
  ]
  const payButton = canEdit && <button className="btn primary" onClick={() => setPaying(true)}>{t('vendors.addPayment')}</button>
  return (
    <div className="page">
      <PageHead back={<Link to="/vendors" style={{ fontSize: 12 }}>{t('vendors.back')}</Link>}
        title={<><h1>{v.name}</h1><StatusBadge value={v.status} /><span className="mono tx3" style={{ fontSize: 12 }}>ID {v.id}</span></>}>
        {payButton}
      </PageHead>
      <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(200px,1fr))' }}>
        <Stat label={t('vendors.owed')} value={money(v.balance)} unit={v.currency} sub={t('vendors.owedSub')} />
        <Stat text label={t('vendors.buyTariff')} value={tariff ? <Link to={`/tariffs/${tariff.id}`}>{tariff.name}</Link> : '—'} sub={tariff ? t('vendors.tariffSub', { currency: tariff.currency }) : t('vendors.noTariff')} />
        <Stat label={t('vendors.connection')} value={conns.data?.length ?? '—'} sub={t('vendors.enabledCount', { n: conns.data?.filter((c) => c.enabled).length ?? 0 })} />
      </div>
      <div className="card clip">
        <Tabs value={tab} tabs={tabs} onChange={setTab} />
        {tab === 'conn' && <Connections vendor={v} rows={conns.data ?? []} loading={conns.isPending} error={conns.error} retry={conns.refetch} />}
        {tab === 'genel' && <GeneralForm vendor={v} tariffs={tariffs.rows} />}
        {tab === 'ledger' && <LedgerTab party="vendor" row={v} action={payButton} />}
        {tab === 'calls' && <CallsTab filter={{ vendor_id: v.id }} />}
        {extra.find((t) => t.key === tab)?.render(v)}
      </div>
      {paying && <PaymentModal party="vendor" row={v} onClose={() => setPaying(false)} />}
    </div>
  )
}

// What a new connection gets for the settings in the form's advanced part;
// the form counts a setting as changed when it is not this.
const connDefaults = {
  tech_prefix: '', strip_digits: '0', number_plus: false, cli_strip_digits: '0', cli_add_prefix: '', pai_mode: 'pass',
  ring_timeout_sec: '0', max_duration_sec: '0', failover_mode: 'standard', options_interval_sec: '0',
  codecs: [] as string[], transcode: false, auth_username: '', max_calls: '0', max_cps: '0', tls_name: '',
  register: false, register_expires_sec: '3600', register_domain: '',
}
const intervals = [0, 30, 60, 300]
const intervalLabel = (sec: number) => (sec === 0 ? t('vendors.ping') : sec % 60 === 0 ? t('vendors.everyMin', { n: sec / 60 }) : t('vendors.everySec', { n: sec }))
const codeList = (v: any) => String(v ?? '').split(/[\s,;]+/).filter((c) => c !== '')

function connectionFields(row: Row | null): FieldDef[] {
  const every = [...new Set([...intervals, Number(row?.options_interval_sec ?? 0)])].sort((a, b) => a - b)
  const adv = (f: FieldDef): FieldDef => ({ ...f, advanced: true })
  return [
    { key: 'name', label: t('vendors.name'), type: 'mono', required: true },
    { key: 'host', label: t('vendors.host'), type: 'mono', required: true, placeholder: 'sip.example.net', hint: t('vendors.hostHint') },
    { key: 'port', label: t('vendors.port'), type: 'int', required: true, hint: t('vendors.portHint') },
    { key: 'transport', label: t('vendors.transport'), type: 'seg', options: transports, hint: t('vendors.transportHint') },

    adv({ key: 's_number', label: t('vendors.numberFormat'), type: 'section', hint: t('vendors.numberFormatHint') }),
    adv({ key: 'strip_digits', label: t('vendors.stripDigits'), type: 'int', hint: t('vendors.stripDigitsHint') }),
    adv({ key: 'tech_prefix', label: t('vendors.techPrefix'), type: 'mono', hint: t('vendors.techPrefixHint') }),
    adv({ key: 'number_plus', label: t('vendors.plus'), type: 'check', span: true, text: t('vendors.plusText'), hint: t('vendors.plusHint') }),

    adv({ key: 's_caller', label: t('vendors.caller'), type: 'section', hint: t('vendors.callerHint') }),
    adv({ key: 'cli_strip_digits', label: t('vendors.cliStrip'), type: 'int', hint: t('vendors.cliStripHint') }),
    adv({ key: 'cli_add_prefix', label: t('vendors.cliAdd'), type: 'mono', placeholder: '+', hint: t('vendors.cliAddHint') }),
    adv({ key: 'pai_mode', label: t('vendors.pai'), type: 'select', required: true, span: true, options: [
      { value: 'pass', label: t('vendors.paiPass') }, { value: 'remove', label: t('vendors.paiRemove') }, { value: 'caller', label: t('vendors.paiCaller') }],
      hint: t('vendors.paiHint') }),

    adv({ key: 's_time', label: t('vendors.times'), type: 'section' }),
    adv({ key: 'ring_timeout_sec', label: t('vendors.ringTimeout'), type: 'int', hint: t('vendors.ringTimeoutHint') }),
    adv({ key: 'max_duration_sec', label: t('vendors.maxDuration'), type: 'int', hint: t('vendors.maxDurationHint') }),

    adv({ key: 's_fail', label: t('vendors.onFailure'), type: 'section' }),
    adv({ key: 'failover_mode', label: t('vendors.failover'), type: 'select', required: true, span: true, options: [
      { value: 'standard', label: t('vendors.failStandard') },
      { value: 'always', label: t('vendors.failAlways') },
      { value: 'never', label: t('vendors.failNever') },
      { value: 'custom', label: t('vendors.failCustom') }],
      hint: t('vendors.failoverHint') }),
    adv({ key: 'failover_codes', label: t('vendors.failCodes'), type: 'mono', span: true, placeholder: '408, 480, 500, 502, 503, 504', show: (v) => v.failover_mode === 'custom',
      hint: t('vendors.failCodesHint') }),

    adv({ key: 's_health', label: t('vendors.health'), type: 'section' }),
    adv({ key: 'options_interval_sec', label: t('vendors.options'), type: 'select', required: true, span: true, options: every.map((n) => ({ value: String(n), label: intervalLabel(n) })),
      hint: t('vendors.optionsHint') }),

    adv({ key: 's_codecs', label: t('vendors.audioCodecs'), type: 'section' }),
    adv({ key: 'codecs', label: t('vendors.codecs'), type: 'pick', span: true, options: codecOptions(),
      hint: t('vendors.codecsHint') }),
    adv({ key: 'transcode', label: t('vendors.transcode'), type: 'check', span: true, text: t('vendors.transcodeText'),
      hint: t('vendors.transcodeHint') }),

    adv({ key: 's_signin', label: t('vendors.signIn'), type: 'section', hint: t('vendors.signInHint') }),
    adv({ key: 'auth_username', label: t('vendors.username'), type: 'mono' }),
    adv({ key: 'auth_password', label: t('vendors.password'), type: 'password', placeholder: row?.auth_set ? t('vendors.passwordSet') : undefined,
      hint: t('vendors.passwordHint') }),
    adv({ key: 'register', label: t('vendors.register'), type: 'check', span: true, text: t('vendors.registerText'),
      hint: t('vendors.registerHint') }),
    adv({ key: 'register_expires_sec', label: t('vendors.registerExpires'), type: 'int', show: (v) => !!v.register,
      hint: t('vendors.registerExpiresHint') }),
    adv({ key: 'register_domain', label: t('vendors.registerDomain'), type: 'mono', placeholder: 'sip.example.net', show: (v) => !!v.register,
      hint: t('vendors.registerDomainHint') }),

    adv({ key: 's_limits', label: t('vendors.limits'), type: 'section' }),
    adv({ key: 'max_calls', label: t('vendors.maxCalls'), type: 'int', hint: t('vendors.maxCallsHint') }),
    adv({ key: 'max_cps', label: t('vendors.maxCps'), type: 'int', hint: t('vendors.maxCpsHint') }),

    adv({ key: 's_cert', label: t('vendors.certificate'), type: 'section', show: (v) => v.transport === 'tls' }),
    adv({ key: 'tls_name', label: t('vendors.tlsName'), type: 'mono', span: true, placeholder: 'sip.example.net', show: (v) => v.transport === 'tls',
      hint: t('vendors.tlsNameHint') }),
  ]
}

/** How many of a connection's advanced settings are not as a new one comes. */
function changedSettings(c: Row) {
  const d = connDefaults
  return [c.tech_prefix !== '', c.strip_digits !== 0, c.number_plus, c.cli_strip_digits !== 0, c.cli_add_prefix !== '', c.pai_mode !== d.pai_mode,
    c.ring_timeout_sec !== 0, c.max_duration_sec !== 0, c.failover_mode !== d.failover_mode, c.options_interval_sec !== 0,
    (c.codecs ?? []).length > 0, !!c.transcode, c.auth_username !== '' || c.auth_set, !!c.register, c.max_calls !== 0, c.max_cps !== 0, c.tls_name !== ''].filter(Boolean).length
}

const ago = (iso: string) => {
  const sec = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000))
  return sec < 90 ? t('vendors.sec', { n: sec }) : sec < 5400 ? t('vendors.min', { n: Math.round(sec / 60) }) : t('vendors.hour', { n: Math.round(sec / 3600) })
}

/** What one question to the vendor came to, in plain words. */
function probeText(r: Row): { tone: 'ok' | 'warn' | 'bad'; title: string; body: string } {
  const took = `${r.ms} ms`
  switch (r.outcome) {
    case 'answered':
      if (r.sip_code >= 200 && r.sip_code < 300) return { tone: 'ok', title: t('vendors.probeOk', { code: r.sip_code, reason: r.sip_reason ?? '', took }), body: t('vendors.probeOkBody') }
      if (r.sip_code === 503) return { tone: 'bad', title: t('vendors.probe503', { took }), body: t('vendors.probe503Body') }
      return { tone: 'warn', title: t('vendors.probeOther', { code: r.sip_code, reason: r.sip_reason ?? '', took }), body: t('vendors.probeOtherBody') }
    case 'no_answer':
      return { tone: 'bad', title: t('vendors.probeNoAnswer', { n: Math.round(r.ms / 1000) }), body: t('vendors.probeNoAnswerBody') }
    case 'unresolved':
      return { tone: 'bad', title: t('vendors.probeUnresolved'), body: t('vendors.probeUnresolvedBody') }
    case 'refused':
      return { tone: 'bad', title: t('vendors.probeRefused'), body: t('vendors.probeRefusedBody') }
    case 'certificate':
      return { tone: 'bad', title: t('vendors.probeCert'), body: t('vendors.probeCertBody', { detail: r.detail ?? '' }) }
  }
  return { tone: 'bad', title: t('vendors.probeFailed'), body: String(r.detail ?? '') }
}

/** "Bağlantıyı dene": asks the vendor once, now, and says what came back. */
function ConnectionTest({ conn, onClose }: { conn: Row; onClose: () => void }) {
  const [result, setResult] = useState<Row | null>(null)
  const [error, setError] = useState('')
  const [round, setRound] = useState(0)
  useEffect(() => {
    let gone = false
    setResult(null)
    setError('')
    api.post(`/v1/vendor-connections/${conn.id}/test`).then((r) => { if (!gone) setResult(r) }, (e) => { if (!gone) setError(errorText(e)) })
    return () => { gone = true }
  }, [conn.id, round])
  const said = result && probeText(result)
  return (
    <Modal width={520} onClose={onClose}>
      <div><div className="modal-title">{t('vendors.testTitle')}</div>
        <div className="tx2" style={{ marginTop: 2 }}><span className="mono">{conn.name}</span> · <span className="mono">{conn.host}:{conn.port}</span> · {String(conn.transport).toUpperCase()}</div></div>
      {!result && !error && <div className="tx2">{t('vendors.asking')}</div>}
      {error && <div className="banner bad">{t('vendors.testError', { error })}</div>}
      {said && <div style={{ display: 'grid', gap: 6 }}>
        <div className="row" style={{ gap: 8 }}><Badge tone={said.tone}>{said.tone === 'ok' ? t('vendors.working') : said.tone === 'warn' ? t('vendors.attention') : t('vendors.problem')}</Badge><span style={{ fontWeight: 600 }}>{said.title}</span></div>
        <div className="tx2 pretty">{said.body}</div>
      </div>}
      <div className="tx3 pretty" style={{ fontSize: 12 }}>{t('vendors.testNote')}</div>
      <div className="modal-foot">
        <button className="btn" disabled={!result && !error} onClick={() => setRound(round + 1)}>{t('action.retry')}</button>
        <button className="btn primary" onClick={onClose}>{t('action.close')}</button>
      </div>
    </Modal>
  )
}

function Connections({ vendor, rows, loading, error, retry }: { vendor: Row; rows: Row[]; loading: boolean; error: any; retry: () => void }) {
  const { canEdit } = useSession()
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const [testing, setTesting] = useState<Row | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()
  // Whether the engine finds the connections it asks reachable; it comes
  // and goes by itself.
  const health = useQuery({ queryKey: ['/v1/vendor-connections/health', { vendor_id: vendor.id }],
    queryFn: () => api.get<{ connections: Record<string, Row>; registrations?: Record<string, Row> }>('/v1/vendor-connections/health', { vendor_id: vendor.id }), refetchInterval: 15000, retry: false })
  const toggle = async (c: Row) => {
    if (c.enabled && !(await ui.confirm({ title: t('vendors.connOffTitle', { name: c.name }), body: t('vendors.connOffBody'), action: t('action.close'), danger: true }))) return
    try {
      await api.patch(`/v1/vendor-connections/${c.id}`, { enabled: !c.enabled })
      invalidate('/v1/vendor-connections')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const limit = (n: number) => (n ? n : '—')
  const reach = (c: Row) => {
    if (!c.enabled || !c.options_interval_sec) return <span className="tx3">{t('vendors.notChecked')}</span>
    if (health.isError) return <span className="tx3" title={t('vendors.engineUnreadable')}>{t('vendors.unknown')}</span>
    const h = health.data?.connections[String(c.id)]
    if (!h || h.state === 'unknown') return <Badge>{t('vendors.checking')}</Badge>
    return h.state === 'up'
      ? <span title={t('vendors.upTitle', { ago: ago(h.since), code: h.last?.sip_code ?? '', ms: h.last?.ms ?? '' })}><Badge tone="ok">{t('vendors.up')}</Badge></span>
      : <span title={t('vendors.downTitle', { ago: ago(h.since) })}><Badge tone="bad">{t('vendors.down')}</Badge></span>
  }
  // How the switch's registration with the vendor stands (T-14).
  const registration = (c: Row) => {
    if (!c.register) return <span className="tx3">—</span>
    if (!c.enabled) return <span className="tx3">{t('vendors.regOff')}</span>
    if (!c.auth_username) return <span className="tx3" title={t('vendors.regNeedsLogin')}>{t('vendors.regNoLogin')}</span>
    if (health.isError) return <span className="tx3" title={t('vendors.engineUnreadable')}>{t('vendors.unknown')}</span>
    const r = health.data?.registrations?.[String(c.id)]
    if (!r || r.state === 'pending') return <Badge>{t('vendors.registering')}</Badge>
    if (r.state === 'unknown') return <span className="tx3" title={t('vendors.coreUnreadable')}>{t('vendors.unknown')}</span>
    if (r.state === 'registered') return <span title={t('vendors.registeredTitle', { ago: ago(r.since), n: r.expires })}><Badge tone="ok">{t('vendors.registered')}</Badge></span>
    const why: Record<string, string> = {
      credentials: t('vendors.whyCredentials'),
      no_answer: t('vendors.whyNoAnswer'),
      refused: t('vendors.whyRefused'),
    }
    return <span title={t('vendors.regFailedTitle', { why: why[r.reason] ?? t('vendors.whyOther'), ago: ago(r.since) })}><Badge tone="bad">{r.reason === 'credentials' ? t('vendors.pwRejected') : t('vendors.notRegistered')}</Badge></span>
  }
  const cols: Col<Row>[] = [
    { label: t('vendors.name'), width: '130px', mono: true, render: (c) => c.name },
    { label: t('vendors.host'), width: 'minmax(170px,1fr)', mono: true, render: (c) => c.host },
    { label: t('vendors.port'), width: '60px', align: 'right', mono: true, render: (c) => c.port },
    { label: t('vendors.transport'), width: '70px', render: (c) => <span title={c.transport === 'tls' ? t('vendors.tlsNameTitle', { name: c.tls_name || c.host }) : undefined}>{String(c.transport).toUpperCase()}</span> },
    { label: t('vendors.maxCalls'), width: '90px', align: 'right', mono: true, render: (c) => limit(c.max_calls) },
    { label: t('vendors.maxCps'), width: '80px', align: 'right', mono: true, render: (c) => limit(c.max_cps) },
    { label: t('vendors.reach'), width: '120px', render: reach },
    { label: t('vendors.register'), width: '130px', render: registration },
    { label: t('vendors.advanced'), width: '100px', render: (c) => (changedSettings(c) ? <Badge tone="acc">{t('vendors.settingsCount', { n: changedSettings(c) })}</Badge> : <span className="tx3">{t('vendors.default')}</span>) },
    { label: '', width: '170px', align: 'right', render: (c) => <span className="row" style={{ gap: 10, justifyContent: 'flex-end' }}>
      {canEdit && <button className="btn inline" onClick={() => setTesting(c)}>{t('vendors.test')}</button>}
      {canEdit && <button className="btn inline" onClick={() => setEditing(c)}>{t('action.edit')}</button>}
      <Toggle on={c.enabled} onChange={canEdit ? () => toggle(c) : undefined} /></span> },
  ]
  const adding = editing === 'new'
  const row = adding ? null : editing
  return (
    <>
      <div className="toolbar">
        <div className="grow tx2 pretty">{t('vendors.connHelp')}</div>
        {canEdit && <button className="btn primary" onClick={() => setEditing('new')}>{t('vendors.addConn')}</button>}
      </div>
      <DataTable cols={cols} rows={rows} minWidth={1250} loading={loading} error={error} onRetry={retry}
        empty={{ title: t('vendors.noConn'), desc: t('vendors.noConnDesc') }} />
      {editing && <FormModal title={adding ? t('vendors.addConn') : t('vendors.editConn')} sub={vendor.name} action={adding ? t('action.add') : t('action.save')} width={640} onClose={() => setEditing(null)}
        defaults={connDefaults}
        initial={row ? {
          name: row.name, host: row.host, port: String(row.port), transport: row.transport, tls_name: row.tls_name, tech_prefix: row.tech_prefix, strip_digits: String(row.strip_digits),
          number_plus: row.number_plus, cli_strip_digits: String(row.cli_strip_digits), cli_add_prefix: row.cli_add_prefix, pai_mode: row.pai_mode,
          ring_timeout_sec: String(row.ring_timeout_sec), max_duration_sec: String(row.max_duration_sec), failover_mode: row.failover_mode,
          failover_codes: (row.failover_codes ?? []).join(', '), options_interval_sec: String(row.options_interval_sec), codecs: row.codecs ?? [], transcode: !!row.transcode,
          auth_username: row.auth_username, max_calls: String(row.max_calls), max_cps: String(row.max_cps),
          register: !!row.register, register_expires_sec: String(row.register_expires_sec ?? 3600), register_domain: row.register_domain ?? '',
        } : { port: '5060', transport: 'udp', ...connDefaults }}
        fields={connectionFields(row)}
        validate={(f) => {
          const codes = codeList(f.failover_codes)
          const user = String(f.auth_username ?? '').trim()
          const renamed = !!row?.auth_set && user !== row.auth_username
          return {
            port: int(f.port) >= 1 && int(f.port) <= 65535 ? undefined : t('vendors.errPort'),
            strip_digits: /^\d*$/.test(String(f.strip_digits ?? '')) ? undefined : t('vendors.digitsOnly'),
            tech_prefix: /^[0-9#*]*$/.test(f.tech_prefix ?? '') ? undefined : t('vendors.errPrefix'),
            cli_strip_digits: /^\d*$/.test(String(f.cli_strip_digits ?? '')) && int(f.cli_strip_digits) <= 32 ? undefined : t('vendors.errCliStrip'),
            cli_add_prefix: /^\+?\d{0,16}$/.test(f.cli_add_prefix ?? '') ? undefined : t('vendors.errCliAdd'),
            ring_timeout_sec: secondsError(f.ring_timeout_sec, 5, 180),
            max_duration_sec: secondsError(f.max_duration_sec, 5, 7200),
            failover_codes: f.failover_mode !== 'custom' ? undefined
              : codes.length === 0 ? t('vendors.errCodesEmpty')
              : codes.some((c) => !/^[3-6]\d\d$/.test(c)) ? t('vendors.errCodes') : undefined,
            auth_username: /[\s"]/.test(user) ? t('vendors.errUser') : undefined,
            auth_password: user !== '' && !f.auth_password && (!row?.auth_set || renamed) ? (renamed ? t('vendors.errRenamed') : t('vendors.errNeedPw'))
              : user === '' && f.auth_password ? t('vendors.errNeedUser') : undefined,
            max_calls: /^\d*$/.test(String(f.max_calls ?? '')) ? undefined : t('vendors.digitsOnly'),
            max_cps: /^\d*$/.test(String(f.max_cps ?? '')) ? undefined : t('vendors.digitsOnly'),
            register: f.register && user === '' ? t('vendors.errRegister') : undefined,
            register_expires_sec: !f.register || (/^\d+$/.test(String(f.register_expires_sec ?? '')) && int(f.register_expires_sec) >= 60 && int(f.register_expires_sec) <= 86400) ? undefined : t('vendors.errExpires'),
            register_domain: !f.register || /^[A-Za-z0-9.-]{0,253}$/.test(String(f.register_domain ?? '').trim()) ? undefined : t('vendors.errDomain'),
          }
        }}
        onSubmit={async (f) => {
          const body = {
            name: text(f.name), host: text(f.host), port: int(f.port, 5060), transport: f.transport, tls_name: f.transport === 'tls' ? text(f.tls_name) ?? '' : '',
            tech_prefix: f.tech_prefix ?? '', strip_digits: int(f.strip_digits), number_plus: !!f.number_plus,
            cli_strip_digits: int(f.cli_strip_digits), cli_add_prefix: f.cli_add_prefix ?? '', pai_mode: f.pai_mode,
            ring_timeout_sec: int(f.ring_timeout_sec), max_duration_sec: int(f.max_duration_sec),
            failover_mode: f.failover_mode, failover_codes: f.failover_mode === 'custom' ? codeList(f.failover_codes).map(Number) : [],
            options_interval_sec: int(f.options_interval_sec), codecs: f.codecs ?? [], transcode: !!f.transcode, max_calls: int(f.max_calls), max_cps: int(f.max_cps),
            register: !!f.register, register_expires_sec: f.register ? int(f.register_expires_sec, 3600) : (row?.register_expires_sec ?? 3600),
            register_domain: f.register ? String(f.register_domain ?? '').trim() : (row?.register_domain ?? ''),
          }
          const saved: Row = row ? await api.patch(`/v1/vendor-connections/${row.id}`, body) : await api.post('/v1/vendor-connections', { ...body, vendor_id: vendor.id })
          // The name and password to sign in with have an operation of
          // their own: the password is stored sealed and never read back.
          const user = String(f.auth_username ?? '').trim()
          try {
            if (user !== '' && f.auth_password) await api.put(`/v1/vendor-connections/${saved.id}/sign-in`, { username: user, password: f.auth_password })
            else if (user === '' && (row?.auth_set || row?.auth_username)) await api.del(`/v1/vendor-connections/${saved.id}/sign-in`)
          } catch (e) {
            // The connection itself is saved: the form closes, so that
            // sending it again cannot add a second one.
            invalidate('/v1/vendor-connections')
            setEditing(null)
            ui.toast(t('vendors.signInFailed', { error: errorText(e as any) }))
            return
          }
          invalidate('/v1/vendor-connections')
          setEditing(null)
          ui.toast(adding ? t('vendors.connAdded') : t('vendors.connUpdated'))
        }} />}
      {testing && <ConnectionTest conn={testing} onClose={() => setTesting(null)} />}
    </>
  )
}

function GeneralForm({ vendor, tariffs }: { vendor: Row; tariffs: Row[] }) {
  const { canEdit } = useSession()
  const initial = () => ({ ...vendor, tariff_id: vendor.tariff_id ?? '', email: vendor.email ?? '', tax_id: vendor.tax_id ?? '', address: vendor.address ?? '' })
  const [v, setV] = useState<Row>(initial)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  useEffect(() => setV(initial()), [vendor]) // eslint-disable-line react-hooks/exhaustive-deps
  const set = (k: string, val: any) => setV((old) => ({ ...old, [k]: val }))
  const bind = (k: string) => ({ value: v[k] ?? '', onChange: (e: { target: { value: string } }) => set(k, e.target.value) })
  const save = async () => {
    if (!text(v.name)) return setError(t('vendors.errName'))
    setBusy(true)
    setError('')
    try {
      await api.patch(`/v1/vendors/${vendor.id}`, {
        name: text(v.name), email: text(v.email), tax_id: text(v.tax_id), address: text(v.address), currency: v.currency, status: v.status, tariff_id: idOrNull(v.tariff_id),
      })
      invalidate('/v1/vendors')
      ui.toast(t('vendors.saved'))
    } catch (e) { setError(errorText(e as any)) }
    setBusy(false)
  }
  return (
    <>
      <fieldset disabled={!canEdit}><div className="form-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(320px,1fr))' }}>
        <div className="form-col">
          <div className="sect">{t('vendors.identity')}</div>
          <Field label={t('vendors.name')}><input className="input" {...bind('name')} /></Field>
          <Field label={t('vendors.email')}><input className="input" {...bind('email')} /></Field>
          <Field label={t('vendors.taxId')}><input className="input mono" {...bind('tax_id')} /></Field>
          <Field label={t('vendors.address')}><textarea className="textarea" rows={2} {...bind('address')} /></Field>
        </div>
        <div className="form-col">
          <div className="sect">{t('vendors.account')}</div>
          <Field label={t('vendors.currency')}><Seg value={v.currency} options={currencies} onChange={(x) => set('currency', x)} /></Field>
          <Field label={t('vendors.status')}><Seg value={v.status} options={[{ value: 'active', label: t('status.active') }, { value: 'suspended', label: t('status.suspended') }]} onChange={(x) => set('status', x)} /></Field>
          <Field label={t('vendors.buyTariff')}><select className="select" {...bind('tariff_id')}><option value="">—</option>{tariffs.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}</select></Field>
          <div className="field"><span className="label">{t('vendors.owed')}</span>
            <div className="readonly-box">{money(vendor.balance)} {vendor.currency}</div>
            <span className="hint">{t('vendors.balanceHint')}</span>
          </div>
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
