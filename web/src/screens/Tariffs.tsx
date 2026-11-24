// SPDX-License-Identifier: AGPL-3.0-only
import { useRef, useState, type CSSProperties } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, request, useGet, useInvalidate, usePaged, type Row } from '../lib/api'
import { dec, downloadCSV, dt, fromLocalInput, money, moneyCSV, moneyInput, num, parseMoney, toLocalInput } from '../lib/format'
import { rich, t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, Modal, PageHead, Pager, Search, StepBar, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { clock, windowBody, windowErrors, windowFields, windowInitial, windowLabel } from '../lib/window'
import { ListPage, currencies, int, text } from './common'

const kinds = () => [{ value: 'sell', label: t('tariffs.sell') }, { value: 'buy', label: t('tariffs.buy') }]
const kindLabel = (k: string) => (k === 'buy' ? t('tariffs.buy') : t('tariffs.sell'))

export function Tariffs() {
  const navigate = useNavigate()
  const cols: Col<Row>[] = [
    { label: t('tariffs.name'), width: 'minmax(240px,1.6fr)', render: (r) => r.name },
    { label: t('tariffs.kind'), width: '120px', render: (r) => <Badge tone={r.kind === 'sell' ? 'acc' : 'neutral'}>{kindLabel(r.kind)}</Badge> },
    { label: t('tariffs.currency'), width: '120px', mono: true, render: (r) => r.currency },
    { label: t('tariffs.created'), width: '160px', mono: true, render: (r) => dt(r.created_at) },
  ]
  return <ListPage title={t('tariffs.title')} desc={t('tariffs.desc')}
    path="/v1/tariffs" noun={t('tariffs.noun')} cols={cols} minWidth={700} onRow={(r) => navigate(`/tariffs/${r.id}`)}
    add={{
      label: t('tariffs.add'), title: t('tariffs.add'), initial: { kind: 'sell', currency: 'USD', timezone: 'UTC' },
      fields: [
        { key: 'name', label: t('tariffs.name'), required: true, span: true },
        { key: 'kind', label: t('tariffs.kind'), type: 'seg', options: kinds() },
        { key: 'currency', label: t('tariffs.currency'), type: 'seg', options: currencies },
        { key: 'timezone', label: t('tariffs.timezone'), type: 'mono', span: true, placeholder: 'Europe/Istanbul', hint: t('tariffs.timezoneHint') },
      ],
      build: (v) => ({ name: text(v.name), kind: v.kind, currency: v.currency, timezone: text(v.timezone) ?? 'UTC' }),
      done: (row) => navigate(`/tariffs/${row.id}`),
    }} />
}

const validity = () => [['', t('tariffs.validAll')], ['current', t('tariffs.validCurrent')], ['future', t('tariffs.validFuture')], ['expired', t('tariffs.validExpired')]]

const rateFields = (): FieldDef[] => [
  { key: 'prefix', label: t('tariffs.prefix'), type: 'mono', required: true, placeholder: t('tariffs.prefixPlaceholder') },
  { key: 'price_per_min', label: t('tariffs.pricePerMin'), type: 'money', required: true, placeholder: money(0, 6) },
  { key: 'connect_fee', label: t('tariffs.connectFee'), type: 'money' },
  { key: 'initial_sec', label: t('tariffs.initialSec'), type: 'int' },
  { key: 'increment_sec', label: t('tariffs.incrementSec'), type: 'int' },
  { key: 'grace_sec', label: t('tariffs.graceSec'), type: 'int' },
  { key: 'effective_from', label: t('tariffs.effectiveFrom'), type: 'datetime', required: true },
  { key: 'effective_to', label: t('tariffs.effectiveTo'), type: 'datetime', hint: t('tariffs.effectiveToHint') },
  { key: 'blocked', label: t('tariffs.blocked'), type: 'check', hint: t('tariffs.blockedHint'), span: true },
]

function validateRate(v: Row) {
  const e: Record<string, string> = {}
  if (!/^\d*$/.test(v.prefix ?? '')) e.prefix = t('tariffs.errPrefix')
  const price = parseMoney(String(v.price_per_min ?? ''))
  if (price == null) e.price_per_min = t('tariffs.errPrice')
  else if (price < 0) e.price_per_min = t('tariffs.errPriceNegative')
  const fee = parseMoney(String(v.connect_fee || '0'))
  if (fee == null || fee < 0) e.connect_fee = t('tariffs.errFee')
  if (int(v.increment_sec, 1) < 1) e.increment_sec = t('tariffs.errIncrement')
  return { ...e, ...windowErrors(v) }
}
const rateBody = (v: Row) => ({
  prefix: String(v.prefix).trim(), price_per_min: parseMoney(String(v.price_per_min)), connect_fee: parseMoney(String(v.connect_fee || '0')),
  initial_sec: int(v.initial_sec, 1), increment_sec: int(v.increment_sec, 1), grace_sec: int(v.grace_sec), blocked: !!v.blocked,
  effective_from: fromLocalInput(v.effective_from), effective_to: fromLocalInput(v.effective_to ?? ''),
  ...windowBody(v),
})

export function TariffDetail() {
  const id = Number(useParams().id)
  const { canEdit } = useSession()
  const q = useGet<Row>(`/v1/tariffs/${id}`)
  const [prefix, setPrefix] = useState('')
  const [valid, setValid] = useState('')
  const [blocked, setBlocked] = useState(false)
  const list = usePaged('/v1/rates', { tariff_id: id, prefix, validity: valid, blocked: blocked ? 'true' : '' }, 50)
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const [csv, setCsv] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  const tf = q.data
  if (q.error) return <div className="page"><Link to="/tariffs" style={{ fontSize: 12 }}>{t('tariffs.back')}</Link><div className="banner bad">{t('tariffs.loadFailed', { error: errorText(q.error as any) })}</div></div>
  if (!tf) return <div className="tx3">{t('kit.loading')}</div>

  const now = Date.now()
  const cols: Col<Row>[] = [
    { label: t('tariffs.prefix'), width: '100px', mono: true, render: (r) => <span style={{ fontWeight: 500 }}>{r.prefix || t('tariffs.all')}</span> },
    { label: t('tariffs.colPrice', { currency: tf.currency }), width: '130px', align: 'right', mono: true, render: (r) => (r.blocked ? '—' : money(r.price_per_min, 6)) },
    { label: t('tariffs.colFee', { currency: tf.currency }), width: '130px', align: 'right', mono: true, render: (r) => (r.blocked ? '—' : money(r.connect_fee, 6)) },
    { label: t('tariffs.colInitial'), width: '70px', align: 'right', mono: true, render: (r) => r.initial_sec },
    { label: t('tariffs.colIncrement'), width: '70px', align: 'right', mono: true, render: (r) => r.increment_sec },
    { label: t('tariffs.colGrace'), width: '90px', align: 'right', mono: true, render: (r) => r.grace_sec },
    { label: t('tariffs.colWindow'), width: '150px', render: (r) => windowLabel(r) || <span className="tx3">{t('tariffs.always')}</span> },
    { label: t('tariffs.blocked'), width: '90px', render: (r) => r.blocked && <Badge tone="bad">{t('tariffs.blocked')}</Badge> },
    { label: t('tariffs.colFrom'), width: '150px', mono: true, render: (r) => <>{dt(r.effective_from)} {Date.parse(r.effective_from) > now && <span style={{ fontFamily: 'var(--sans)', fontSize: 11, color: 'var(--acct)' }}>{t('tariffs.future')}</span>}</> },
    { label: t('tariffs.colTo'), width: '150px', mono: true, render: (r) => <span className="tx2">{r.effective_to ? dt(r.effective_to) : '—'}</span> },
    { label: '', width: 'minmax(60px,1fr)', align: 'right', render: (r) => canEdit && <a style={{ fontSize: 12 }} onClick={() => setEditing(r)}>{t('action.edit')}</a> },
  ]

  const exportCSV = async () => {
    const all: Row[] = []
    try {
      for (let offset = 0; offset < 200_000; offset += 1000) {
        const { data } = await request<Row[]>('GET', '/v1/rates', { params: { tariff_id: id, validity: 'current', limit: 1000, offset } })
        all.push(...data)
        if (data.length < 1000) break
      }
    } catch (e) { return ui.toast(errorText(e as any)) }
    const hhmm = (r: Row, m: number) => (r.time_from === r.time_to ? '' : clock(m))
    downloadCSV(`${tf.name}.csv`, ['prefix', 'price_per_min', 'connect_fee', 'initial_sec', 'increment_sec', 'grace_sec', 'blocked', 'days', 'time_from', 'time_to'],
      all.map((r) => [r.prefix, moneyCSV(r.price_per_min), moneyCSV(r.connect_fee), r.initial_sec, r.increment_sec, r.grace_sec, r.blocked,
        r.days === 127 ? '' : r.days, hhmm(r, r.time_from), hhmm(r, r.time_to)]))
    ui.toast(t('tariffs.exported', { n: num(all.length) }))
  }

  const edited = editing && editing !== 'new' ? editing : null
  return (
    <div className="page">
      <PageHead back={<Link to="/tariffs" style={{ fontSize: 12 }}>{t('tariffs.back')}</Link>} title={tf.name} desc={t('tariffs.detailDesc', { kind: kindLabel(tf.kind), currency: tf.currency, timezone: tf.timezone })}>
        <button className="btn" onClick={exportCSV} title={t('tariffs.exportTitle')}>{t('action.downloadCsv')}</button>
        {canEdit && <div className="row">
          <button className="btn" onClick={() => setEditing('new')}>{t('tariffs.addRate')}</button>
          <button className="btn primary" onClick={() => setCsv(true)}>{t('tariffs.uploadCsv')}</button>
        </div>}
      </PageHead>
      <div className="card clip">
        <div className="toolbar">
          <Search mono width={220} value={prefix} onChange={(v) => setPrefix(v.replace(/\D/g, ''))} placeholder={t('tariffs.searchPrefix')} />
          <select className="select" style={{ width: 'auto' }} value={valid} onChange={(e) => setValid(e.target.value)}>{validity().map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>
          <label className="radio tx2"><input type="checkbox" checked={blocked} onChange={(e) => setBlocked(e.target.checked)} />{t('tariffs.onlyBlocked')}</label>
          <div className="grow" />
        </div>
        <DataTable cols={cols} rows={list.rows} minWidth={1080} loading={list.loading} error={list.error} onRetry={list.refetch}
          empty={prefix || valid || blocked
            ? { title: t('tariffs.noRateFound'), desc: t('tariffs.noRateFoundDesc'), action: <button className="btn" onClick={() => { setPrefix(''); setValid(''); setBlocked(false) }}>{t('tariffs.clearFilters')}</button> }
            : { title: t('tariffs.noRates'), desc: t('tariffs.noRatesDesc') }} />
        <Pager p={list} noun={t('tariffs.rateNoun')} />
      </div>
      {editing && <FormModal title={edited ? t('tariffs.editRate', { prefix: edited.prefix }) : t('tariffs.addRate')} sub={`${tf.name} · ${kindLabel(tf.kind)} · ${tf.currency}`} action={edited ? t('action.save') : t('action.add')}
        fields={[...rateFields().slice(0, -1), ...windowFields(tf.timezone, edited?.days), rateFields()[rateFields().length - 1]]} validate={validateRate} onClose={() => setEditing(null)}
        initial={edited
          ? { ...edited, ...windowInitial(edited), price_per_min: moneyInput(edited.price_per_min, 4), connect_fee: moneyInput(edited.connect_fee, 2), effective_from: toLocalInput(edited.effective_from), effective_to: toLocalInput(edited.effective_to) }
          : { ...windowInitial(), initial_sec: '1', increment_sec: '1', grace_sec: '0', connect_fee: '0', effective_from: toLocalInput(new Date().toISOString()) }}
        onSubmit={async (v) => {
          if (edited) await api.patch(`/v1/rates/${edited.id}`, rateBody(v))
          else await api.post('/v1/rates', { tariff_id: id, ...rateBody(v) })
          invalidate('/v1/rates')
          setEditing(null)
          ui.toast(edited ? t('tariffs.rateSaved') : t('tariffs.rateAdded'))
        }} />}
      {csv && <CsvImport tariff={tf} onClose={() => { setCsv(false); invalidate('/v1/rates') }} />}
    </div>
  )
}

// By the API's error code; the texts are read when shown, in the language in force.
const importErrors: Record<string, (e: Row) => string> = {
  prefix_empty: () => t('tariffs.impPrefixEmpty'),
  prefix_not_digits: () => t('tariffs.impPrefixDigits'),
  price_invalid: () => t('tariffs.impPriceInvalid'),
  price_negative: () => t('tariffs.impPriceNegative'),
  too_many_decimals: () => t('tariffs.impDecimals'),
  increment_min: () => t('tariffs.impIncrement'),
  value_invalid: () => t('tariffs.impValue'),
  days_invalid: () => t('tariffs.impDays'),
  time_invalid: () => t('tariffs.impTime'),
  time_pair: () => t('tariffs.impTimePair'),
  duplicate_prefix: (e) => t('tariffs.impDuplicate', { line: e.ref_line }),
  conflict: () => t('tariffs.impConflict'),
}

function CsvImport({ tariff, onClose }: { tariff: Row; onClose: () => void }) {
  const [step, setStep] = useState(0)
  const [file, setFile] = useState<{ name: string; text: string; lines: number; size: number } | null>(null)
  const [over, setOver] = useState(false)
  const [date, setDate] = useState(() => new Date(Date.now() + 86_400_000).toISOString().slice(0, 10))
  const [time, setTime] = useState('00:00')
  const [mode, setMode] = useState('close')
  const [result, setResult] = useState<Row | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const effective = new Date(`${date}T${time}:00Z`)
  const effectiveOK = !isNaN(effective.getTime())

  const read = async (f: File | undefined) => {
    if (!f) return
    const content = await f.text()
    const lines = content.split(/\r?\n/).filter((l) => l.trim() !== '').length - 1
    setFile({ name: f.name, text: content, lines: Math.max(0, lines), size: f.size })
    setError('')
  }
  const send = async (dryRun: boolean) => {
    setBusy(true)
    setError('')
    try {
      const { data } = await request<Row>('POST', `/v1/tariffs/${tariff.id}/rates/import`, {
        text: file!.text, params: { effective_from: effective.toISOString(), mode, dry_run: dryRun ? 1 : null },
      })
      setResult(data)
      setStep(dryRun ? 2 : 3)
    } catch (e) { setError(errorText(e as any)) }
    setBusy(false)
  }
  const errors: Row[] = result?.errors ?? []
  const report = () => downloadCSV(t('tariffs.reportFile'), ['satir', 'onek', 'deger', 'hata'], errors.map((e) => [e.line, e.prefix, e.value, importErrors[e.code]?.(e) ?? e.code]))
  const size = (n: number) => (n < 1024 ? `${n} B` : n < 1024 * 1024 ? `${Math.round(n / 1024)} KB` : `${dec(n / 1024 / 1024, 1)} MB`)
  const errGrid: CSSProperties = { display: 'grid', gridTemplateColumns: '50px 80px 150px minmax(0,1fr)', columnGap: 12, alignItems: 'center', padding: '2px 12px' }
  const body: CSSProperties = { padding: 20, display: 'flex', flexDirection: 'column', gap: 12 }

  return (
    <Modal width={640} flush onClose={step === 3 ? onClose : undefined}>
      <div style={{ padding: '20px 20px 14px', borderBottom: '1px solid var(--line)' }}>
        <div className="modal-title">{t('tariffs.csvTitle')}</div>
        <div className="tx2" style={{ marginTop: 2 }}>{tariff.name} · {kindLabel(tariff.kind)} · {tariff.currency}</div>
        <StepBar steps={[t('tariffs.stepFile'), t('tariffs.stepValidity'), t('tariffs.stepCheck'), t('tariffs.stepResult')]} current={step} />
      </div>
      {step === 0 && <div style={body}>
        <input ref={input} type="file" accept=".csv,text/csv" hidden onChange={(e) => read(e.target.files?.[0])} />
        {!file
          ? <div className={'drop' + (over ? ' over' : '')} onClick={() => input.current?.click()}
              onDragOver={(e) => { e.preventDefault(); setOver(true) }} onDragLeave={() => setOver(false)}
              onDrop={(e) => { e.preventDefault(); setOver(false); read(e.dataTransfer.files[0]) }}>
              <div className="sect">{t('tariffs.drop')}</div><div className="tx2">{t('tariffs.dropOr')}</div>
            </div>
          : <div className="row" style={{ gap: 10, padding: '12px 14px', border: '1px solid var(--line)', borderRadius: 10 }}>
              <div className="grow"><div className="mono">{file.name}</div><div className="tx3" style={{ fontSize: 12, marginTop: 2 }}>{t('tariffs.fileLines', { lines: num(file.lines), size: size(file.size) })}</div></div>
              <Badge tone="ok">{t('tariffs.fileRead')}</Badge>
              <button className="btn inline" onClick={() => setFile(null)}>{t('tariffs.change')}</button>
            </div>}
        <div className="tx2">{t('tariffs.expected')} <span className="tx3">{t('tariffs.expectedNote')}</span></div>
        <div className="codebox">prefix,price,connect_fee,initial,increment,grace,blocked,days,time_from,time_to</div>
        <div className="tx3 pretty" style={{ fontSize: 12 }}>{t('tariffs.windowNote', { zone: tariff.timezone })}</div>
        <div className="modal-foot" style={{ marginTop: 4 }}>
          <button className="btn" onClick={onClose}>{t('action.cancel')}</button>
          <button className="btn primary" disabled={!file || file.lines < 1} onClick={() => setStep(1)}>{t('tariffs.next')}</button>
        </div>
      </div>}
      {step === 1 && <div style={body}>
        <div className="grid2">
          <label className="field"><span className="label">{t('tariffs.effectiveDate')}</span><input type="date" className="input mono" value={date} onChange={(e) => setDate(e.target.value)} /></label>
          <label className="field"><span className="label">{t('tariffs.timeUtc')}</span><input type="time" className="input mono" value={time} onChange={(e) => setTime(e.target.value)} /></label>
        </div>
        <div className="tx2 pretty">{t('tariffs.allRowsFrom')}</div>
        <label className="row" style={{ alignItems: 'flex-start' }}><input type="radio" name="csvm" checked={mode === 'close'} onChange={() => setMode('close')} style={{ marginTop: 3 }} />
          <span>{t('tariffs.modeClose')}<br /><span className="tx3" style={{ fontSize: 12 }}>{effectiveOK ? t('tariffs.oldEnds', { at: dt(effective.toISOString()) }) : t('tariffs.badDate')}</span></span></label>
        <label className="row" style={{ alignItems: 'flex-start' }}><input type="radio" name="csvm" checked={mode === 'keep'} onChange={() => setMode('keep')} style={{ marginTop: 3 }} />
          <span>{t('tariffs.modeKeep')}<br /><span className="tx3" style={{ fontSize: 12 }}>{t('tariffs.modeKeepNote')}</span></span></label>
        {error && <div className="banner bad">{error}</div>}
        <div className="modal-foot" style={{ marginTop: 4 }}>
          <button className="btn" onClick={() => setStep(0)}>{t('tariffs.back2')}</button>
          <button className="btn primary" disabled={busy || !effectiveOK} onClick={() => send(true)}>{t('tariffs.check')}</button>
        </div>
      </div>}
      {step === 2 && result && <div style={body}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 10 }}>
          {([[t('tariffs.readRows'), result.read, ''], [t('tariffs.valid'), result.valid, 'ok'], [t('tariffs.invalid'), result.invalid, result.invalid ? 'bad' : '']] as [string, number, string][]).map(([l, n, c]) =>
            <div key={l} style={{ padding: '10px 12px', background: 'var(--panel2)', borderRadius: 8 }}><div className="tx3" style={{ fontSize: 12 }}>{l}</div><div className={'mono ' + c} style={{ fontSize: 16, fontWeight: 500 }}>{num(n)}</div></div>)}
        </div>
        {errors.length > 0 && <div style={{ border: '1px solid var(--line)', borderRadius: 8, overflow: 'auto', maxHeight: 260 }}>
          <div className="tx3" style={{ ...errGrid, height: 32, fontSize: 11.5, fontWeight: 500, borderBottom: '1px solid var(--line)' }}><div className="right">{t('tariffs.errLine')}</div><div>{t('tariffs.errPrefixCol')}</div><div>{t('tariffs.errValue')}</div><div>{t('tariffs.errError')}</div></div>
          {errors.map((e) => <div key={e.line + e.code} className="mono" style={{ ...errGrid, minHeight: 30, fontSize: 12, borderBottom: '1px solid var(--line2)' }}>
            <div className="right tx3">{e.line}</div><div className="ell">{e.prefix}</div><div className="ell">{e.value}</div>
            <div className="bad" style={{ fontFamily: 'var(--sans)', fontSize: 12.5 }}>{importErrors[e.code]?.(e) ?? e.code}</div>
          </div>)}
        </div>}
        <div className="tx2 pretty">{result.invalid ? t('tariffs.skipInvalid') : t('tariffs.allValid')}{result.invalid > errors.length ? t('tariffs.firstErrors', { n: num(errors.length) }) : ''}</div>
        {error && <div className="banner bad">{error}</div>}
        <div className="modal-foot" style={{ marginTop: 4 }}>
          <button className="btn" onClick={() => setStep(1)}>{t('tariffs.back2')}</button>
          {errors.length > 0 && <button className="btn" onClick={report}>{t('tariffs.downloadReport')}</button>}
          <button className="btn primary" disabled={busy || !result.valid} onClick={() => send(false)}>{t('tariffs.loadValid', { n: num(result.valid) })}</button>
        </div>
      </div>}
      {step === 3 && result && <div style={body}>
        <div className="verdict" style={{ '--c': 'var(--ok)', display: 'block', padding: 16 } as CSSProperties}>
          <div className="t">{t('tariffs.loaded', { n: num(result.rates) })}</div>
          <div style={{ marginTop: 4 }}>{rich('tariffs.newFrom', { at: <span className="mono">{dt(result.effective_from)} UTC</span> })}</div>
        </div>
        <div className="kv"><span className="tx2">{t('tariffs.closedCount')}</span><span className="mono">{num(result.closed)}</span></div>
        <div className="kv"><span className="tx2">{t('tariffs.newPrefixes')}</span><span className="mono">{num(result.new_prefixes)}</span></div>
        <div className="kv"><span className="tx2">{t('tariffs.skipped')}</span><span className={'mono' + (result.invalid ? ' bad' : '')}>{num(result.invalid)}</span></div>
        <div className="modal-foot" style={{ marginTop: 4 }}>
          {errors.length > 0 && <button className="btn" onClick={report}>{t('tariffs.downloadReport')}</button>}
          <button className="btn primary" onClick={onClose}>{t('action.close')}</button>
        </div>
      </div>}
    </Modal>
  )
}
