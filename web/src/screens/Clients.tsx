// SPDX-License-Identifier: AGPL-3.0-only
import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, useGet, useInvalidate, useLookup, type Row } from '../lib/api'
import { dt, money, moneyInput, parseMoney } from '../lib/format'
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
    { label: 'Ad', width: 'minmax(200px,1.6fr)', render: (r) => r.name },
    { label: 'Faturalama', width: '100px', render: (r) => <span className="tx2">{billingLabel(r.billing_mode)}</span> },
    { label: 'Kullanılabilir bakiye', width: '150px', align: 'right', mono: true, render: (r) => <span className={r.balance < 0 ? 'bad' : undefined}>{money(r.balance)} {r.currency}</span> },
    { label: 'Kredi limiti', width: '130px', align: 'right', mono: true, render: (r) => money(r.credit_limit) },
    { label: 'Tarife', width: 'minmax(140px,1fr)', render: (r) => tariffs.name(r.tariff_id) },
    { label: 'Yönlendirme planı', width: 'minmax(130px,1fr)', render: (r) => plans.name(r.routing_plan_id) },
    { label: 'Durum', width: '90px', render: (r) => <StatusBadge value={r.status} /> },
  ]
  return <ListPage title="Müşteriler" desc="Trafik gönderen hesaplar. Bakiye, müşterinin harcayabileceği tutardır." path="/v1/clients" noun="müşteri"
    cols={cols} minWidth={1040} onRow={(r) => navigate(`/clients/${r.id}`)}
    add={{
      label: 'Müşteri ekle', title: 'Müşteri ekle', sub: 'Trunk, limit ve iletişim bilgileri oluşturduktan sonra detay sayfasından girilir.',
      initial: { billing_mode: 'prepaid', currency: 'USD' },
      fields: [
        { key: 'name', label: 'Ad', required: true, span: true },
        { key: 'billing_mode', label: 'Faturalama türü', type: 'seg', options: billing },
        { key: 'currency', label: 'Para birimi', type: 'seg', options: currencies },
        { key: 'tariff_id', label: 'Tarife', type: 'select', options: tariffs.rows.map((t) => ({ value: String(t.id), label: t.name })) },
        { key: 'routing_plan_id', label: 'Yönlendirme planı', type: 'select', options: plans.rows.map((t) => ({ value: String(t.id), label: t.name })) },
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
  if (q.error) return <div className="page"><Link to="/clients" style={{ fontSize: 12 }}>← Müşteriler</Link><div className="banner bad">Müşteri yüklenemedi: {errorText(q.error as any)}</div></div>
  if (!c) return <div className="tx3">Yükleniyor…</div>

  const suspended = c.status !== 'active'
  const toggleStatus = async () => {
    const ok = await ui.confirm(suspended
      ? { title: `${c.name} etkinleştirilsin mi?`, body: 'Müşterinin trunk\'larından gelen çağrılar yeniden kabul edilir.', action: 'Etkinleştir' }
      : { title: `${c.name} askıya alınsın mı?`, body: 'Müşteri askıya alınınca tüm trunk\'larından gelen çağrılar SIP 403 ile reddedilir. Süren çağrılar etkilenmez.', action: 'Askıya al', danger: true })
    if (!ok) return
    try {
      await api.patch(`/v1/clients/${id}`, { status: suspended ? 'active' : 'suspended' })
      invalidate('/v1/clients')
      ui.toast(suspended ? 'Müşteri etkinleştirildi' : 'Müşteri askıya alındı')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const plan = plans.byId.get(c.routing_plan_id)
  const tariff = tariffs.byId.get(c.tariff_id)
  const extra = ext.flatMap((e) => e.clientTabs ?? [])
  const tabs = [
    { value: 'genel', label: 'Genel' }, { value: 'trunk', label: "Trunk'lar" }, { value: 'bundles', label: 'Paket dakikalar' }, { value: 'ledger', label: 'Hesap hareketleri' }, { value: 'calls', label: 'Çağrı kayıtları' },
    ...extra.map((t) => ({ value: t.key, label: t.label })),
  ]
  const payButton = canEdit && <button className="btn primary" onClick={() => setPaying(true)}>Ödeme ekle</button>
  return (
    <div className="page">
      <PageHead back={<Link to="/clients" style={{ fontSize: 12 }}>← Müşteriler</Link>}
        title={<><h1>{c.name}</h1><StatusBadge value={c.status} /><span className="mono tx3" style={{ fontSize: 12 }}>ID {c.id}</span></>}>
        {canEdit && <div className="row">
          <button className="btn" onClick={toggleStatus}>{suspended ? 'Etkinleştir' : 'Askıya al'}</button>
          {payButton}
        </div>}
      </PageHead>
      <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(170px,1fr))' }}>
        <Stat label="Kullanılabilir bakiye" value={money(c.balance)} unit={c.currency} sub="Müşterinin harcayabileceği tutar" tone={c.balance < 0 ? 'bad' : undefined} />
        <Stat label="Kredi limiti" value={money(c.credit_limit)} unit={c.currency}
          sub={c.billing_mode === 'prepaid' ? "Prepaid · bakiye 0'da çağrı kesilir" : 'Postpaid · bakiye limite kadar eksiye inebilir'} />
        <Stat text label="Tarife" value={tariff ? <Link to={`/tariffs/${tariff.id}`}>{tariff.name}</Link> : '—'} sub={tariff ? `Satış · ${tariff.currency}` : 'Tarife atanmadı; çağrılar reddedilir'} />
        <Stat text label="Yönlendirme planı" value={plan ? <Link to={`/plans/${plan.id}`}>{plan.name}</Link> : '—'}
          sub={plan ? `${plan.policy === 'lcr' ? 'LCR' : 'Öncelik'} · zararına rota ${plan.allow_loss ? 'izinli' : 'yasak'}` : 'Plan atanmadı; çağrılar reddedilir'} />
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
    if (!text(v.name)) return setError('Ad boş olamaz.')
    if (limit == null || limit < 0) return setError('Kredi limiti eksi olmayan bir tutar olmalı.')
    const lowText = String(v.low_balance ?? '').trim()
    const low = lowText === '' ? null : parseMoney(lowText)
    if (lowText !== '' && low == null) return setError('Düşük bakiye eşiği geçerli bir tutar olmalı ya da boş bırakılmalı.')
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
      ui.toast('Müşteri kaydedildi')
    } catch (e) { setError(errorText(e as any)) }
    setBusy(false)
  }
  return (
    <>
      <fieldset disabled={!canEdit}><div className="form-grid">
        <div className="form-col">
          <div className="sect">Kimlik</div>
          <Field label="Ad"><input className="input" {...bind('name')} /></Field>
          <Field label="E-posta"><input className="input" {...bind('email')} /></Field>
          <Field label="Vergi no"><input className="input mono" {...bind('tax_id')} /></Field>
          <Field label="Adres"><textarea className="textarea" rows={2} {...bind('address')} /></Field>
          <div className="field"><span className="label">Durum</span>
            <div className="row wrap" style={{ gap: 16 }}>
              {[['active', 'Aktif'], ['suspended', 'Askıda'], ['closed', 'Kapalı']].map(([val, l]) =>
                <label className="radio" key={val}><input type="radio" name="cst" checked={v.status === val} onChange={() => set('status', val)} />{l}</label>)}
            </div>
          </div>
        </div>
        <div className="form-col">
          <div className="sect">Faturalama ve yönlendirme</div>
          <div className="grid2">
            <Field label="Para birimi"><select className="select" {...bind('currency')}>{currencies.map((o) => <option key={o.value}>{o.value}</option>)}</select></Field>
            <Field label="Faturalama türü"><select className="select" {...bind('billing_mode')}>{billing.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}</select></Field>
          </div>
          <div className="field"><span className="label">Kullanılabilir bakiye</span>
            <div className="readonly-box">{money(client.balance)} {client.currency}</div>
            <span className="hint">Elle düzenlenemez; yalnızca ödeme, düzeltme ve iade kaydıyla değişir.</span>
          </div>
          <Field label={`Kredi limiti (${v.currency})`}><input className="input num" inputMode="decimal" {...bind('credit_limit')} /></Field>
          <Field label={`Düşük bakiye eşiği (${v.currency})`} hint="Bakiye bu tutara ya da altına inince uyarı açılır. Boş = uyarı yok."><input className="input num" inputMode="decimal" {...bind('low_balance')} /></Field>
          <Field label="Tarife"><select className="select" {...bind('tariff_id')}><option value="">—</option>{tariffs.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}</select></Field>
          <Field label="Yönlendirme planı"><select className="select" {...bind('routing_plan_id')}><option value="">—</option>{plans.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}</select></Field>
        </div>
        <div className="form-col">
          <div className="sect">Limitler</div>
          <div className="grid2">
            <Field label="Eşzamanlı çağrı limiti" hint="0 = sınırsız"><input className="input num" inputMode="numeric" {...bind('max_calls')} /></Field>
            <Field label="CPS limiti" hint="0 = sınırsız"><input className="input num" inputMode="numeric" {...bind('max_cps')} /></Field>
          </div>
          {ext.map((e, i) => e.clientForm && <div key={i} className="form-col">{e.clientForm({ values: v, set, disabled: !canEdit })}</div>)}
        </div>
      </div></fieldset>
      {error && <div className="banner bad" style={{ margin: '0 20px 12px' }}>{error}</div>}
      {canEdit && <div className="form-foot">
        <button className="btn" onClick={() => { setV(initial()); setError('') }}>Vazgeç</button>
        <button className="btn primary" disabled={busy} onClick={save}>Kaydet</button>
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
  if (bad) return `“${bad}” okunamadı. Her satıra “silinecek > eklenecek” yazın (ör. 011 > 1 ya da > 90); en çok 8 rakam, silinecekte başta + olabilir.`
  const rules = numberRules(v)
  const twice = rules.find((r, i) => rules.findIndex((o) => o.strip === r.strip) !== i)
  if (twice) return twice.strip === '' ? 'Silinecek kısmı boş iki kural var.' : `“${twice.strip}” ile başlayan numaralar için iki kural var.`
  return rules.length > 20 ? 'En çok 20 kural.' : undefined
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
  const regsOf = (t: Row) => (regs.data?.registrations ?? []).filter((r) => r.trunk_id === t.id)
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const [issued, setIssued] = useState<Row | null>(null)
  const [shown, setShown] = useState<Row | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()
  // A trunk that signs in has no password until one is issued; issuing
  // again replaces it, and only the newest is ever shown.
  const issue = async (t: Row, renew: boolean) => {
    if (renew && !(await ui.confirm({ title: `${t.name} için yeni parola üretilsin mi?`, body: 'Eski parola hemen geçersiz olur; müşterinin cihazı yeni parola girilene kadar çağrı gönderemez.', action: 'Yeni parola üret', danger: true }))) return
    try {
      setIssued(await api.post(`/v1/client-trunks/${t.id}/password`))
      invalidate('/v1/client-trunks')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const toggle = async (t: Row) => {
    if (t.enabled && !(await ui.confirm({ title: `${t.name} kapatılsın mı?`, body: 'Trunk kapatılınca bu kaynaktan gelen yeni çağrılar reddedilir. Süren çağrılar etkilenmez.', action: 'Kapat', danger: true }))) return
    try {
      await api.patch(`/v1/client-trunks/${t.id}`, { enabled: !t.enabled })
      invalidate('/v1/client-trunks')
    } catch (e) {
      const err = e as any
      ui.toast(err?.status === 409 ? 'Açılamadı: adreslerinden biri aynı teknik önekle başka bir açık trunk\'ta kullanılıyor.' : errorText(err))
    }
  }
  // As many addresses as fit, and how many there are when there are several.
  const addresses = (t: Row) => {
    const list = (t.addresses as string[] | undefined) ?? []
    if (list.length === 0) return '—'
    if (list.length === 1) return list[0]
    return <span className="row" style={{ gap: 6, minWidth: 0 }} title={list.join('\n')}>
      <span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{list.join(', ')}</span>
      <span className="tx2" style={{ flex: 'none' }}>({list.length})</span></span>
  }
  // Whether a trunk that signs in is registered right now; the details
  // open from the mark.
  const registered = (t: Row) => {
    if (regs.isPending) return null
    if (regs.error) return <span className="tx2" style={{ flex: 'none', fontFamily: 'var(--sans)' }} title={errorText(regs.error as any)}>kayıt bilinmiyor</span>
    const list = regsOf(t)
    if (list.length === 0) return <span className="tx2" style={{ flex: 'none', fontFamily: 'var(--sans)' }}>kayıtlı değil</span>
    return <button className="btn inline" style={{ flex: 'none', fontFamily: 'var(--sans)' }} title={list.map((r) => `${r.src_ip}:${r.src_port}`).join('\n')} onClick={() => setShown(t)}>
      <span className="ok">Kayıtlı</span>{list.length > 1 ? ` (${list.length})` : ''}</button>
  }
  const cols: Col<Row>[] = [
    { label: 'Ad', width: '140px', mono: true, render: (t) => t.name },
    { label: 'Kimlik doğrulama', width: '140px', render: (t) => <span className="tx2">{t.auth_type === 'ip' ? 'IP' : 'Kullanıcı adı / parola'}</span> },
    { label: 'Adresler / kullanıcı', width: 'minmax(180px,1fr)', mono: true, render: (t) => (t.auth_type === 'ip' ? addresses(t)
      : <span className="row" style={{ gap: 8, minWidth: 0 }}><span style={{ overflow: 'hidden', textOverflow: 'ellipsis' }}>{t.username}</span>{registered(t)}</span>) },
    { label: 'Teknik önek', width: '100px', mono: true, render: (t) => t.tech_prefix || '—' },
    { label: 'Şifreleme', width: '180px', render: (t) => t.security === 'cert' ? <span>Sertifika · <span className="mono">{t.tls_cert_name}</span></span>
      : <span className="tx2">{t.security === 'tls' ? 'TLS zorunlu' : 'Serbest'}</span> },
    { label: 'Gelişmiş ayar', width: '100px', render: (t) => (trunkSettings(t) ? <Badge tone="acc">{trunkSettings(t)} ayar</Badge> : <span className="tx3">varsayılan</span>) },
    { label: '', width: '190px', align: 'right', render: (t) => <span className="row" style={{ gap: 10, justifyContent: 'flex-end' }}>
      {canEdit && t.auth_type === 'digest' && <button className="btn inline" onClick={() => issue(t, true)}>Parola üret</button>}
      {canEdit && <button className="btn inline" onClick={() => setEditing(t)}>Düzenle</button>}
      <Toggle on={t.enabled} onChange={canEdit ? () => toggle(t) : undefined} /></span> },
  ]
  const adding = editing === 'new'
  const byAddress = (v: Row) => (adding ? v.auth_type !== 'digest' : (editing as Row).auth_type === 'ip')
  return (
    <>
      <div className="toolbar">
        <div className="grow tx2">Çağrılar kaynak adres (tek IP ya da ağ) veya kullanıcı adı/parola ile bu trunk'lara eşlenir.</div>
        {canEdit && <button className="btn" onClick={() => setEditing('new')}>Trunk ekle</button>}
      </div>
      <DataTable cols={cols} rows={q.data ?? []} minWidth={1120} loading={q.isPending} error={q.error as any} onRetry={q.refetch}
        empty={{ title: 'Trunk yok', desc: 'Müşterinin çağrı gönderebilmesi için en az bir trunk gerekir.' }} />
      {editing && <FormModal title={adding ? 'Trunk ekle' : 'Trunk\'ı düzenle'} action={adding ? 'Ekle' : 'Kaydet'} onClose={() => setEditing(null)}
        sub={adding ? `${client.name} · sabit adresi olan müşteri IP ile, olmayan kullanıcı adı ve parolayla tanınır`
          : `${client.name} · ${editing.auth_type === 'ip' ? 'kaynak adresiyle tanınır' : `kullanıcı adı ${editing.username}`}`}
        width={640} defaults={trunkDefaults}
        initial={adding ? { auth_type: 'ip', ...trunkDefaults }
          : { name: editing.name, addresses: ((editing.addresses as string[] | undefined) ?? []).join('\n'), tech_prefix: editing.tech_prefix, security: editing.security, tls_cert_name: editing.tls_cert_name, max_contacts: editing.max_contacts ?? 1,
            ...numberForms(editing),
            max_duration_sec: String(editing.max_duration_sec), max_calls: String(editing.max_calls), max_cps: String(editing.max_cps), codecs: editing.codecs ?? [], transcode: !!editing.transcode }}
        fields={[
          { key: 'name', label: 'Ad', type: 'mono', required: true },
          { key: 'auth_type', label: 'Müşteri nasıl tanınır', type: 'seg', options: [{ value: 'ip', label: 'Kaynak IP' }, { value: 'digest', label: 'Kullanıcı adı ve parola' }], show: () => adding,
            hint: 'Sabit IP adresi olan müşteri adresiyle, olmayan kullanıcı adı ve parolayla tanınır.' },
          { key: 'addresses', label: 'Kaynak adresler', type: 'textarea', mono: true, rows: 3, required: true, span: true, placeholder: '203.0.113.10\n198.51.100.0/24\n2001:db8:12::/48', show: byAddress,
            hint: 'Her satıra bir IP adresi ya da ağ (CIDR); virgülle de ayrılabilir. IPv4 ve IPv6. Bir kaynak birden çok trunk\'a uyuyorsa en dar ağ, eşitlikte en uzun teknik önek seçilir.' },
          { key: 'username', label: 'Kullanıcı adı', type: 'mono', required: true, show: (v) => adding && v.auth_type === 'digest', hint: 'Sistem genelinde benzersiz olmalı. Parola eklendikten sonra üretilir ve bir kez gösterilir.' },

          { key: 's_security', label: 'Güvenlik', type: 'section', advanced: true },
          { key: 'security', label: 'Şifreleme', type: 'seg', span: true, advanced: true, options: [{ value: 'any', label: 'Serbest' }, { value: 'tls', label: 'TLS zorunlu' }, { value: 'cert', label: 'TLS ve istemci sertifikası' }],
            hint: 'Serbest: müşteri şifreli de şifresiz de bağlanabilir. Diğer seçenekler şifresiz gelen çağrıyı reddeder.' },
          { key: 'tls_cert_name', label: 'Sertifikadaki ad', type: 'mono', required: true, span: true, advanced: true, placeholder: 'pbx.ornek.com', show: (v) => v.security === 'cert',
            hint: 'Müşterinin sunduğu sertifikanın ortak adı (CN). Sertifika, sunucuda tanımlı istemci sertifika otoritesince imzalanmış olmalıdır; tanımlı değilse bu trunk çağrı kabul etmez.' },
          { key: 'max_contacts', label: 'Kayıt sayısı', type: 'int', span: true, advanced: true, show: (v) => !byAddress(v),
            hint: 'Trunk\'ın aynı anda kayıtlı (REGISTER) olabileceği adres sayısı, 1–10. Sınır dolunca en eski kayıt yenisine yer açar.' },

          { key: 's_number', label: 'Numara biçimi', type: 'section', advanced: true, hint: 'Müşterinin aradığı numarayı nasıl yazdığı. Sistem numarayı tarifelerin kullandığı biçime (ülke koduyla, 905321112233 gibi) çevirir.' },
          { key: 'tech_prefix', label: 'Teknik önek', type: 'mono', span: true, advanced: true, hint: 'Müşteri numaranın başına ayırt edici bir kod koyuyorsa buraya yazın: numaranın başında beklenir ve silinir. Çoğu müşteride boş kalır.' },
          { key: 'number_forms', label: 'Müşteri numarayı nasıl gönderiyor', type: 'pick', span: true, advanced: true, options: [
            { value: 'plus', label: 'Başında + ile (+905321112233)' }, { value: 'zeros', label: 'Başında 00 ile (00905321112233)' },
            { value: 'national', label: 'Ülke kodu olmadan, başında 0 ile (05321112233)' }],
            hint: 'Müşteri birden çok biçimde gönderiyorsa hepsini seçin. Hiçbiri seçili değilse numara ülke koduyla, olduğu gibi (905321112233) beklenir. Seçilen biçimlerle başlamayan numaralar olduğu gibi bırakılır; yanlış seçilirse çağrılar “hedef tarifede yok” diye reddedilir.' },
          { key: 'number_add', label: 'Ülke kodu', type: 'mono', advanced: true, placeholder: '90', show: (v) => (v.number_forms ?? []).includes('national'), hint: 'Baştaki 0 silinir, yerine bu kod eklenir.' },
          { key: 'number_other', label: 'Başka kurallar', type: 'textarea', mono: true, rows: 2, span: true, advanced: true, placeholder: '011 > 1\n> 90',
            hint: 'Her satıra bir kural: “silinecek > eklenecek”. Numara silinecek kısımla başlıyorsa o kısım silinir, yerine eklenecek konur; silinecek boşsa (“> 90”) başka kurala uymayan her numaranın başına eklenir. Bir numaraya birden çok kural uyuyorsa en uzun silinecek kısmı olan uygulanır.' },

          { key: 's_limits', label: 'Süre ve limitler', type: 'section', advanced: true, hint: 'Müşterinin genel limitleri ayrıca geçerlidir; bunlar yalnızca bu trunk içindir.' },
          { key: 'max_duration_sec', label: 'En uzun çağrı süresi (saniye)', type: 'int', span: true, advanced: true, hint: 'Bu trunk\'tan gelen çağrı bu süre dolunca kapatılır. 0 = ayrı sınır yok (bakiyenin yettiği kadar, en çok 2 saat).' },
          { key: 'max_calls', label: 'Çağrı limiti', type: 'int', advanced: true, hint: 'Aynı anda sürebilecek çağrı sayısı. 0 = sınırsız.' },
          { key: 'max_cps', label: 'CPS limiti', type: 'int', advanced: true, hint: 'Saniyede kabul edilecek yeni çağrı sayısı. 0 = sınırsız.' },

          { key: 's_codecs', label: 'Ses kodekleri', type: 'section', advanced: true },
          { key: 'codecs', label: 'Bu müşteriyle kullanılacak kodekler', type: 'pick', span: true, advanced: true, options: codecOptions,
            hint: 'Müşterinin sunduklarından yalnızca seçtikleriniz, tıkladığınız sırayla sağlayıcıya teklif edilir; hiçbiri seçili değilse hepsi geçer (önerilen).' },
          { key: 'transcode', label: 'Kodek çevirisi', type: 'check', span: true, advanced: true, text: 'Ortak kodek yoksa sesi çevir',
            hint: 'Bu müşterinin kodekleriyle sağlayıcının kodekleri hiç örtüşmüyorsa çağrı yine de gönderilir; ses, sunucuda bir kodekten ötekine çevrilir. Yalnızca yukarıda kodek seçiliyse işe yarar. Çeviri sunucunun işlemcisini kullanır; aynı anda çevrilen çağrı sayısının bir sınırı vardır.' },
        ]}
        validate={(v) => {
          const list = addressList(v.addresses)
          const bad = list.find((a) => !sourceAddress.test(a))
          return {
            tech_prefix: /^\d*$/.test(v.tech_prefix ?? '') ? undefined : 'Yalnızca rakam.',
            number_add: !(v.number_forms ?? []).includes('national') ? undefined : !v.number_add ? 'Ülke kodunu yazın (Türkiye için 90).' : /^\d{1,8}$/.test(v.number_add) ? undefined : 'En çok 8 rakam.',
            number_other: numberRulesError(v),
            max_duration_sec: secondsError(v.max_duration_sec, 5, 7200),
            max_calls: /^\d*$/.test(String(v.max_calls ?? '')) ? undefined : 'Yalnızca rakam.',
            max_cps: /^\d*$/.test(String(v.max_cps ?? '')) ? undefined : 'Yalnızca rakam.',
            max_contacts: byAddress(v) || (Number(v.max_contacts) >= 1 && Number(v.max_contacts) <= 10) ? undefined : '1 ile 10 arasında bir sayı.',
            username: adding && v.auth_type === 'digest' && !/^[A-Za-z0-9._-]{3,64}$/.test(String(v.username ?? '')) ? 'Harf, rakam, nokta, tire ve alt çizgi; 3–64 karakter.' : undefined,
            addresses: !byAddress(v) ? undefined : bad ? `“${bad}” bir IP adresi ya da ağ değil. Örnek: 203.0.113.10 ya da 198.51.100.0/24.`
              : list.length > 200 ? 'En çok 200 adres.' : undefined,
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
            if (err?.status === 409 && !digest) throw new Error('Bu adreslerden biri aynı teknik önekle başka bir açık trunk\'ta kullanılıyor.')
            if (err?.status === 422 && /number.rule/.test(String(err?.message))) throw new Error('Numara kuralları kabul edilmedi; “Başka kurallar” alanını denetleyin.')
            if (err?.status === 422 && !digest) throw new Error('Adreslerden biri geçersiz ya da çok geniş bir ağ (IPv4\'te en çok /8, IPv6\'da en çok /16).')
            throw e
          }
          invalidate('/v1/client-trunks')
          setEditing(null)
          ui.toast(adding ? 'Trunk eklendi' : 'Trunk güncellendi')
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
    if (!(await ui.confirm({ title: 'Kayıt silinsin mi?', body: `${trunk.name} trunk'ının ${r.src_ip} adresinden gelen kaydı silinir; trunk'a yönlendirilen çağrılar bu adrese gönderilmez. Cihaz yeniden kayıt olabilir. Süren çağrılar etkilenmez.`, action: 'Kaydı sil', danger: true }))) return
    try {
      await api.del(`/v1/client-trunks/${trunk.id}/registrations?contact=${encodeURIComponent(r.contact)}`)
      ui.toast('Kayıt silindi')
    } catch (e) { ui.toast((e as any)?.status === 404 ? 'Kayıt zaten yok' : errorText(e as any)) }
    invalidate('/v1/registrations')
    if (list.length <= 1) onClose()
  }
  return (
    <Modal width={640} onClose={onClose}>
      <div><div className="modal-title">Trunk kaydı</div><div className="tx2" style={{ marginTop: 2 }}><span className="mono">{trunk.name}</span> · kullanıcı adı <span className="mono">{trunk.username}</span> · en çok {trunk.max_contacts ?? 1} kayıt</div></div>
      {list.length === 0 && <div className="tx2">Trunk şu an kayıtlı değil.</div>}
      {list.map((r) => (
        <div key={r.contact} style={{ border: '1px solid var(--line)', borderRadius: 8, padding: '12px 14px', display: 'grid', gap: 8 }}>
          <div className="row" style={{ gap: 8 }}>
            <span className="mono grow" style={{ fontWeight: 600 }}>{r.src_ip}:{r.src_port}</span>
            <Badge>{transportLabel(r.transport)}</Badge>
            {r.nat && <Badge tone="acc">{r.transport === 'udp' ? 'NAT arkasında' : 'Bağlantı üzerinden'}</Badge>}
            {canEdit && <button className="btn inline" onClick={() => remove(r)}>Kaydı sil</button>}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '150px 1fr', rowGap: 4 }}>
            <span className="tx2">Bildirdiği adres</span><span className="mono" style={{ wordBreak: 'break-all' }}>{r.contact}</span>
            <span className="tx2">İlk kayıt</span><span className="mono">{dt(r.since, true)}</span>
            <span className="tx2">Geçerlilik sonu</span><span className="mono">{dt(r.until, true)}</span>
            {r.user_agent && <><span className="tx2">Cihaz</span><span style={{ wordBreak: 'break-all' }}>{r.user_agent}</span></>}
          </div>
        </div>
      ))}
      <div className="tx2">Saatler UTC. Kayıt, cihaz yenilemedikçe geçerlilik sonunda kendiliğinden düşer; NAT arkasındaki ve bağlantı üzerinden ulaşılan cihazlardan kısa aralıklarla yeniden kayıt istenir.</div>
      <div className="modal-foot"><button className="btn" onClick={onClose}>Kapat</button></div>
    </Modal>
  )
}

/** Seconds as the panel shows talk time: whole minutes, and the seconds over. */
const talk = (sec: number) => (sec % 60 === 0 ? `${sec / 60} dk` : `${Math.floor(sec / 60)} dk ${sec % 60} sn`)
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
    if (b.enabled && !(await ui.confirm({ title: `${b.name || 'Paket'} kapatılsın mı?`, body: 'Paket kapatılınca bu öneklere giden çağrılar tarifedeki fiyattan ücretlenir. Dönemin kullanımı silinmez.', action: 'Kapat', danger: true }))) return
    try {
      await api.patch(`/v1/bundles/${b.id}`, { enabled: !b.enabled })
      invalidate(path)
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const remove = async (b: Row) => {
    if (!(await ui.confirm({ title: `${b.name || 'Paket'} silinsin mi?`, body: 'Paketin kullanım sayaçları da silinir; geçmiş çağrı kayıtları değişmez.', action: 'Sil', danger: true }))) return
    try {
      await api.del(`/v1/bundles/${b.id}`)
      invalidate(path)
      ui.toast('Paket silindi')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const cols: Col<Row>[] = [
    { label: 'Ad', width: 'minmax(140px,1fr)', render: (b) => b.name || '—' },
    { label: 'Önekler', width: 'minmax(160px,1.4fr)', mono: true, render: (b) => (b.prefixes as string[]).map((p) => p || 'tümü').join(', ') },
    { label: 'Aylık süre', width: '110px', align: 'right', mono: true, render: (b) => talk(b.seconds) },
    { label: 'Bu ay kullanılan', width: '140px', align: 'right', mono: true, render: (b) => talk(b.used_sec) },
    { label: 'Kalan', width: '110px', align: 'right', mono: true, render: (b) => <span className={b.used_sec >= b.seconds ? 'bad' : undefined}>{talk(Math.max(b.seconds - b.used_sec, 0))}</span> },
    { label: 'Durum', width: '90px', render: (b) => <Badge tone={b.enabled ? 'ok' : 'neutral'}>{b.enabled ? 'Açık' : 'Kapalı'}</Badge> },
    { label: '', width: '190px', align: 'right', render: (b) => <span className="row" style={{ gap: 10, justifyContent: 'flex-end' }}>
      {canEdit && <button className="btn inline" onClick={() => setEditing(b)}>Düzenle</button>}
      {canEdit && <button className="btn inline" onClick={() => remove(b)}>Sil</button>}
      <Toggle on={b.enabled} onChange={canEdit ? () => toggle(b) : undefined} /></span> },
  ]
  const period = q.data?.[0]?.period
  return (
    <>
      <div className="toolbar">
        <div className="grow tx2">Paketteki süre her takvim ayında yenilenir; paket bitince çağrılar tarifedeki fiyattan ücretlenir.{period ? ` Dönem: ${period}.` : ''}</div>
        {canEdit && <button className="btn" onClick={() => setEditing('new')}>Paket ekle</button>}
      </div>
      <DataTable cols={cols} rows={q.data ?? []} minWidth={980} loading={q.isPending} error={q.error as any} onRetry={q.refetch}
        empty={{ title: 'Paket yok', desc: 'Müşterinin bütün çağrıları tarifedeki fiyattan ücretlenir.' }} />
      {editing && <FormModal title={editing === 'new' ? 'Paket ekle' : 'Paketi düzenle'} action={editing === 'new' ? 'Ekle' : 'Kaydet'} onClose={() => setEditing(null)}
        sub={`${client.name} · süre, faturalanan saniye üzerinden düşülür; ay, tarifenin saat dilimine göre başlar`}
        initial={editing === 'new' ? {} : { name: editing.name, prefixes: (editing.prefixes as string[]).join(', '), minutes: String(Math.floor(editing.seconds / 60)), extra: String(editing.seconds % 60 || '') }}
        fields={[
          { key: 'name', label: 'Ad', span: true, placeholder: 'Mobil 1000 dk' },
          { key: 'prefixes', label: 'Önekler', type: 'mono', required: true, span: true, placeholder: '9053, 9054, 9055', hint: 'Virgülle ayrılmış hedef önekleri. Bir numara birden çok pakete uyuyorsa en uzun önekli paket kullanılır.' },
          { key: 'minutes', label: 'Aylık süre (dakika)', type: 'int', required: true },
          { key: 'extra', label: 'Ek saniye', type: 'int', placeholder: '0' },
        ]}
        validate={(v) => {
          const list = prefixList(v.prefixes)
          const sec = int(v.minutes) * 60 + int(v.extra)
          return {
            prefixes: list.some((p) => !/^\d+$/.test(p)) ? 'Yalnızca rakam ve virgül.' : list.length > 200 ? 'En çok 200 önek.' : undefined,
            minutes: /^\d*$/.test(String(v.minutes ?? '')) && sec > 0 && sec <= 2_000_000_000 ? undefined : 'Sıfırdan büyük bir süre girin.',
            extra: /^\d*$/.test(String(v.extra ?? '')) && int(v.extra) < 60 ? undefined : '0–59 arası.',
          }
        }}
        onSubmit={async (v) => {
          const body = { name: String(v.name ?? '').trim(), prefixes: prefixList(v.prefixes), seconds: int(v.minutes) * 60 + int(v.extra) }
          if (editing === 'new') await api.post('/v1/bundles', { ...body, client_id: client.id })
          else await api.patch(`/v1/bundles/${editing.id}`, body)
          invalidate(path)
          setEditing(null)
          ui.toast(editing === 'new' ? 'Paket eklendi' : 'Paket güncellendi')
        }} />}
    </>
  )
}

/** The one time a trunk's password can be read: only its hash is stored. */
function TrunkPassword({ row, onClose }: { row: Row; onClose: () => void }) {
  const [saved, setSaved] = useState(false)
  const ui = useUI()
  const copy = async () => {
    try { await navigator.clipboard.writeText(row.password); ui.toast('Parola panoya kopyalandı') } catch { ui.toast('Kopyalanamadı; parolayı elle seçip kopyalayın') }
  }
  return (
    <Modal width={520}>
      <div><div className="modal-title">Trunk parolası üretildi</div><div className="tx2" style={{ marginTop: 2 }}>Kullanıcı adı <span className="mono">{row.username}</span> · alan (realm) <span className="mono">{row.realm}</span></div></div>
      <div className="banner warn" style={{ borderColor: 'color-mix(in oklch, var(--warn) 40%, transparent)' }}>
        <span className="warn" style={{ fontWeight: 600 }}>Bu parola yalnızca şimdi gösterilir.</span> Pencere kapandıktan sonra bir daha görüntülenemez; kaybolursa yeni parola üretmeniz gerekir.
      </div>
      <div className="row">
        <div className="grow mono" style={{ padding: '10px 12px', background: 'var(--panel2)', border: '1px solid var(--line)', borderRadius: 8, wordBreak: 'break-all' }}>{row.password}</div>
        <button className="btn" onClick={copy}>Kopyala</button>
      </div>
      <label className="radio"><input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} />Parolayı müşteriye güvenli bir yoldan ilettim</label>
      <div className="modal-foot"><button className="btn primary" disabled={!saved} onClick={onClose}>Kapat</button></div>
    </Modal>
  )
}
