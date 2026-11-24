// SPDX-License-Identifier: AGPL-3.0-only
// Pieces shared by several screens: the generic list page, the payment
// dialog, the ledger and the short call table.
import { useState, type ReactNode } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, useGet, useInvalidate, usePaged, type Params, type Row } from '../lib/api'
import { dt, money, parseMoney, signed, sipReason, sipTone } from '../lib/format'
import { t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { DataTable, Field, FormModal, Modal, PageHead, Pager, Search, Seg, StatusBadge, errorText, useUI, type Col, type FieldDef } from '../ui/kit'

/** A searchable, paged list of one resource with an optional "add" dialog. */
export function ListPage(props: {
  title: string
  desc: string
  path: string
  params?: Params
  cols: Col<Row>[]
  minWidth: number
  noun: string
  onRow?: (row: Row) => void
  add?: { label: string; title: string; sub?: string; fields: FieldDef[]; initial?: Row; build: (v: Row) => Row; done: (row: Row) => void }
}) {
  const { canEdit } = useSession()
  const [search, setSearch] = useSearchParams()
  const q = search.get('q') ?? ''
  const setQ = (v: string) => setSearch(v ? { q: v } : {}, { replace: true })
  const list = usePaged(props.path, { ...props.params, q })
  const [adding, setAdding] = useState(false)
  const invalidate = useInvalidate()
  return (
    <div className="page">
      <PageHead title={props.title} desc={props.desc}>
        {props.add && canEdit && <button className="btn primary" onClick={() => setAdding(true)}>{props.add.label}</button>}
      </PageHead>
      <div className="card clip">
        <div className="toolbar">
          <Search value={q} onChange={setQ} />
          <div className="grow" />
        </div>
        <DataTable cols={props.cols} rows={list.rows} minWidth={props.minWidth} loading={list.loading} error={list.error} onRetry={list.refetch} onRow={props.onRow}
          empty={q ? { title: t('list.notFound'), desc: t('list.notFoundDesc'), action: <button className="btn" onClick={() => setQ('')}>{t('list.clearSearch')}</button> }
            : { title: t('list.none', { noun: props.noun }), desc: canEdit && props.add ? t('list.createFirst', { action: props.add.label }) : t('list.nothing') }} />
        <Pager p={list} noun={props.noun} />
      </div>
      {adding && props.add && (
        <FormModal title={props.add.title} sub={props.add.sub} fields={props.add.fields} initial={props.add.initial} action={t('action.create')} onClose={() => setAdding(false)}
          onSubmit={async (v) => {
            const row = await api.post(props.path, props.add!.build(v))
            invalidate(props.path)
            setAdding(false)
            props.add!.done(row)
          }} />
      )}
    </div>
  )
}

/** Trims text fields and turns empty strings into nulls. */
export const text = (v: any) => { const s = String(v ?? '').trim(); return s === '' ? null : s }
export const int = (v: any, def = 0) => { const n = parseInt(String(v ?? ''), 10); return Number.isFinite(n) ? n : def }

/** The audio codecs a connection or a trunk can be limited to, as the API names them. */
export const codecOptions = () => ['PCMA', 'PCMU', 'G729', 'G722', 'opus', 'GSM', 'iLBC', 'G723', 'AMR', 'AMR-WB', 'speex'].map((c) => ({
  value: c, label: ({ PCMA: t('codec.PCMA'), PCMU: t('codec.PCMU'), G729: 'G.729', G722: 'G.722', opus: 'Opus', G723: 'G.723.1' } as Record<string, string>)[c] ?? c,
}))
/** A whole number of seconds within bounds, or the value that means "no setting". */
export const secondsError = (v: any, least: number, most: number) =>
  /^\d*$/.test(String(v ?? '')) && (int(v) === 0 || (int(v) >= least && int(v) <= most)) ? undefined : t('common.secondsRange', { least, most })
export const idOrNull = (v: any) => (v === '' || v == null ? null : Number(v))

export const currencies = ['USD', 'EUR', 'TRY'].map((c) => ({ value: c, label: c }))

const kinds = () => [{ value: 'payment', label: t('status.payment') }, { value: 'adjustment', label: t('status.adjustment') }, { value: 'refund', label: t('status.refund') }]

/**
 * Records a payment, adjustment or refund. For a client the balance is what
 * they may spend; for a vendor it is what we owe, so a payment lowers it.
 */
export function PaymentModal({ party, row, onClose }: { party: string; row: Row; onClose: () => void }) {
  const [kind, setKind] = useState('payment')
  const [amount, setAmount] = useState('')
  const [reference, setReference] = useState('')
  const [note, setNote] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  const micro = parseMoney(amount)
  const after = micro == null ? null : row.balance + (party === 'vendor' ? -micro : micro)
  const label = party === 'vendor' ? t('payment.vendorBalance') : t('payment.clientBalance')
  const submit = async () => {
    if (micro == null || micro === 0) return setError(t('payment.errAmount'))
    if (kind === 'payment' && micro < 0) return setError(t('payment.errNegative'))
    setBusy(true)
    try {
      await api.post('/v1/payments', { party_type: party, party_id: row.id, amount: micro, kind, reference, note })
      invalidate('/v1/ledger', `/v1/${party}s`)
      ui.toast(t('payment.done'))
      onClose()
    } catch (e) {
      setError(errorText(e as any))
      setBusy(false)
    }
  }
  return (
    <Modal width={440} onClose={onClose}>
      <div>
        <div className="modal-title">{t('payment.title')}</div>
        <div className="tx2" style={{ marginTop: 2 }}>{row.name}</div>
      </div>
      <Field label={t('payment.kind')}><Seg value={kind} options={kinds()} onChange={setKind} /></Field>
      <Field label={t('payment.amount', { currency: row.currency })} error={error} hint={kind === 'payment' ? undefined : t('payment.negativeHint')}>
        <input className={'input num' + (error ? ' err' : '')} autoFocus inputMode="decimal" placeholder={money(0)} value={amount}
          onChange={(e) => { setAmount(e.target.value); setError('') }} onKeyDown={(e) => e.key === 'Enter' && submit()} />
      </Field>
      <div className="grid2">
        <Field label={t('payment.reference')}><input className="input mono" value={reference} onChange={(e) => setReference(e.target.value)} /></Field>
        <Field label={t('payment.note')}><input className="input" value={note} onChange={(e) => setNote(e.target.value)} /></Field>
      </div>
      <div className="kv" style={{ padding: '10px 12px', background: 'var(--panel2)', borderRadius: 8 }}>
        <span>{label}</span>
        <span className="mono">{money(row.balance)} → {after == null ? '—' : money(after)} {row.currency}</span>
      </div>
      <div className="modal-foot">
        <button className="btn" onClick={onClose}>{t('action.cancel')}</button>
        <button className="btn primary" disabled={busy} onClick={submit}>{t('action.save')}</button>
      </div>
    </Modal>
  )
}

export function LedgerTab({ party, row, action }: { party: string; row: Row; action?: ReactNode }) {
  const q = useGet<Row[]>('/v1/ledger', { party_type: party, party_id: row.id })
  const rows = [...(q.data ?? [])].reverse()
  const cols: Col<Row>[] = [
    { label: t('ledger.date'), width: '150px', mono: true, render: (l) => dt(l.created_at) },
    { label: t('ledger.kind'), width: '100px', render: (l) => <StatusBadge value={l.kind} /> },
    { label: t('ledger.amount', { currency: row.currency }), width: '130px', align: 'right', mono: true, render: (l) => <span className={l.amount > 0 === (party !== 'vendor') ? 'ok' : 'bad'}>{signed(l.amount)}</span> },
    { label: t(party === 'vendor' ? 'ledger.debtAfter' : 'ledger.balanceAfter', { currency: row.currency }), width: '150px', align: 'right', mono: true, render: (l) => money(l.balance_after) },
    { label: t('ledger.reference'), width: '130px', mono: true, render: (l) => l.ref },
    { label: t('ledger.note'), width: 'minmax(160px,1fr)', render: (l) => <span className="tx2">{l.note}</span> },
  ]
  return (
    <>
      <div className="toolbar">
        <div className="grow tx2 pretty">{t('ledger.help')}</div>
        {action}
      </div>
      <DataTable cols={cols} rows={rows} minWidth={860} loading={q.isPending} error={q.error as any} onRetry={q.refetch}
        empty={{ title: t('ledger.empty'), desc: t('ledger.emptyDesc') }} />
    </>
  )
}

export function SipCode({ code }: { code: number }) {
  return <><span className="mono" style={{ color: sipTone(code), fontWeight: 500 }}>{code}</span> <span className="tx3">{sipReason(code)}</span></>
}

/** The short call list on a party's detail page. */
export function CallsTab({ filter }: { filter: Params }) {
  const list = usePaged('/v1/cdrs', filter, 15)
  const cols: Col<Row>[] = [
    { label: t('calls.start'), width: '150px', mono: true, render: (r) => dt(r.start_at) },
    { label: t('calls.src'), width: '120px', mono: true, render: (r) => r.src },
    { label: t('calls.dst'), width: 'minmax(130px,1fr)', mono: true, render: (r) => r.dst },
    { label: t('calls.duration'), width: '70px', align: 'right', mono: true, render: (r) => r.duration_sec },
    { label: t('calls.sipCode'), width: '170px', render: (r) => <SipCode code={r.sip_code} /> },
    { label: t('calls.sell'), width: '90px', align: 'right', mono: true, render: (r) => money(r.sell_amount, 4) },
    { label: t('calls.buy'), width: '90px', align: 'right', mono: true, render: (r) => money(r.buy_amount, 4) },
  ]
  return (
    <>
      <DataTable cols={cols} rows={list.rows} minWidth={860} loading={list.loading} error={list.error} onRetry={list.refetch}
        empty={{ title: t('calls.empty'), desc: t('calls.emptyDesc') }} errorTitle={t('calls.error')} />
      <Pager p={list} noun={t('calls.noun')} />
    </>
  )
}
