// SPDX-License-Identifier: AGPL-3.0-only
// Shared components. Measurements follow the design handoff ("Ortak
// bileşenler"); styles live in ui.css.
import { createContext, useCallback, useContext, useEffect, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import type { ApiError, Paged } from '../lib/api'
import { num } from '../lib/format'

// ---- small pieces ----

export type Tone = 'ok' | 'warn' | 'bad' | 'acc' | 'neutral'
const tones: Record<Tone, string> = { ok: 'var(--ok)', warn: 'var(--warn)', bad: 'var(--bad)', acc: 'var(--acct)', neutral: 'var(--tx2)' }

export function Badge({ tone = 'neutral', children }: { tone?: Tone; children: ReactNode }) {
  return <span className="badge" style={{ '--c': tones[tone] } as CSSProperties}>{children}</span>
}

const statuses: Record<string, [Tone, string]> = {
  active: ['ok', 'Aktif'], suspended: ['warn', 'Askıda'], closed: ['neutral', 'Kapalı'],
  payment: ['ok', 'Ödeme'], adjustment: ['warn', 'Düzeltme'], refund: ['acc', 'İade'], usage: ['neutral', 'Kullanım'],
}
/** Lets an edition name the statuses its own records use. */
export const addStatuses = (more: Record<string, [Tone, string]>) => { Object.assign(statuses, more) }

export function StatusBadge({ value }: { value: string }) {
  const [tone, label] = statuses[value] ?? ['neutral', value]
  return <Badge tone={tone}>{label}</Badge>
}

export function Stat({ label, value, unit, sub, tone, text }: { label: string; value: ReactNode; unit?: string; sub?: ReactNode; tone?: Tone; text?: boolean }) {
  return (
    <div className="stat">
      <div className="l">{label}</div>
      <div className={'v ell' + (text ? ' text' : '')} style={tone ? { color: tones[tone] } : undefined}>
        {value} {unit && <span className="u">{unit}</span>}
      </div>
      {sub != null && <div className="s">{sub}</div>}
    </div>
  )
}

export function Toggle({ on, onChange, disabled }: { on: boolean; onChange?: (v: boolean) => void; disabled?: boolean }) {
  return <button type="button" role="switch" aria-checked={on} disabled={disabled || !onChange} className={'toggle' + (on ? ' on' : '')}
    onClick={(e) => { e.stopPropagation(); onChange?.(!on) }} />
}

export interface Option { value: string; label: string }

export function Seg({ value, options, onChange }: { value: string; options: Option[]; onChange: (v: string) => void }) {
  return <div className="seg">{options.map((o) =>
    <button type="button" key={o.value} className={o.value === value ? 'on' : ''} onClick={() => onChange(o.value)}>{o.label}</button>)}</div>
}

export function Split({ value, options, onChange, small }: { value: string; options: Option[]; onChange: (v: string) => void; small?: boolean }) {
  return <div className={'split' + (small ? ' sm' : '')}>{options.map((o) =>
    <button type="button" key={o.value} className={o.value === value ? 'on' : ''} onClick={() => onChange(o.value)}>{o.label}</button>)}</div>
}

/**
 * A choice of several from a short list, kept in the order they were
 * picked: each chosen one shows its place. For lists whose order matters
 * (codecs in order of preference).
 */
export function PickList({ value, options, onChange }: { value: string[]; options: Option[]; onChange: (v: string[]) => void }) {
  return <div className="pick">{options.map((o) => {
    const at = value.indexOf(o.value)
    return <button type="button" key={o.value} className={at >= 0 ? 'on' : ''} aria-pressed={at >= 0}
      onClick={() => onChange(at >= 0 ? value.filter((v) => v !== o.value) : [...value, o.value])}>
      {at >= 0 && <span className="n">{at + 1}</span>}{o.label}</button>
  })}</div>
}

/**
 * A part of a form that stays closed until asked for: settings most people
 * never touch. `mark` says, while it is closed too, what inside is not as
 * it comes, so that nothing set there is overlooked.
 */
export function Disclosure({ title, mark, open, onToggle, children }: { title: string; mark?: ReactNode; open: boolean; onToggle: () => void; children: ReactNode }) {
  return (
    <div className="adv span2">
      <button type="button" className={'adv-head' + (open ? ' open' : '')} aria-expanded={open} onClick={onToggle}>
        <span className="caret" aria-hidden="true">▸</span>{title}{mark}
      </button>
      {open && <div className="grid2">{children}</div>}
    </div>
  )
}

export function Tabs({ value, tabs, onChange, h40 }: { value: string; tabs: Option[]; onChange: (v: string) => void; h40?: boolean }) {
  return <div className="tabs">{tabs.map((t) =>
    <div key={t.value} className={'tab' + (h40 ? ' h40' : '') + (t.value === value ? ' on' : '')} onClick={() => onChange(t.value)}>{t.label}</div>)}</div>
}

export function Field({ label, hint, error, span, children }: { label: string; hint?: ReactNode; error?: string; span?: boolean; children: ReactNode }) {
  return (
    <label className={'field' + (span ? ' span2' : '')}>
      <span className="label">{label}</span>
      {children}
      {error ? <span className="hint err">{error}</span> : hint ? <span className="hint">{hint}</span> : null}
    </label>
  )
}

// ---- table ----

export interface Col<R> {
  label: string
  width: string
  align?: 'right'
  mono?: boolean
  render: (row: R) => ReactNode
  total?: ReactNode
}

const skeleton = [62, 48, 71, 55, 66, 50, 72, 58]

export function errorText(e: ApiError | null | undefined) {
  if (!e) return ''
  return e.status ? `${e.message} (HTTP ${e.status})` : e.message
}

export function DataTable<R extends object>(props: {
  cols: Col<R>[]
  rows: R[]
  minWidth: number
  gap?: number
  loading?: boolean
  error?: ApiError | null
  onRetry?: () => void
  onRow?: (row: R) => void
  selectedId?: number | null
  rowClass?: (row: R) => string
  empty?: { title: string; desc: string; action?: ReactNode }
  totals?: boolean
  errorTitle?: string
}) {
  const { cols, rows, loading, error } = props
  const grid: CSSProperties = { gridTemplateColumns: cols.map((c) => c.width).join(' '), columnGap: props.gap ?? 16 }
  const cell = (c: Col<R>): CSSProperties => ({ textAlign: c.align })
  const state = error ? 'error' : loading ? 'load' : rows.length ? 'data' : 'empty'
  return (
    <>
      <div className="tscroll"><div style={{ minWidth: props.minWidth }}>
        <div className="thead" style={grid}>{cols.map((c, i) => <div key={i} style={cell(c)}>{c.label}</div>)}</div>
        {state === 'data' && rows.map((r, n) => (
          <div key={(r as { id?: number }).id ?? n} style={grid} onClick={props.onRow && (() => props.onRow!(r))}
            className={'trow' + (props.onRow ? ' click' : '') + (props.selectedId != null && (r as { id?: number }).id === props.selectedId ? ' sel' : '') + ' ' + (props.rowClass?.(r) ?? '')}>
            {cols.map((c, i) => <div key={i} className={c.mono ? 'mono' : undefined} style={cell(c)}>{c.render(r)}</div>)}
          </div>
        ))}
        {state === 'data' && props.totals && (
          <div className="ttotal" style={grid}>{cols.map((c, i) => <div key={i} className={c.mono && i ? 'mono' : undefined} style={cell(c)}>{c.total}</div>)}</div>
        )}
      </div></div>
      {state === 'load' && <div>
        {skeleton.map((w, i) => <div className="skel" key={i}><div style={{ width: `${w}%` }} /></div>)}
        <div className="tx3" style={{ padding: '10px 16px', fontSize: 12 }}>Yükleniyor…</div>
      </div>}
      {state === 'empty' && <div className="state">
        <div className="t">{props.empty?.title ?? 'Kayıt bulunamadı'}</div>
        <div className="d">{props.empty?.desc ?? 'Arama ya da filtrelerle eşleşen kayıt yok. Filtreleri temizleyip yeniden deneyin.'}</div>
        {props.empty?.action}
      </div>}
      {state === 'error' && <div className="state">
        <div className="t bad">{props.errorTitle ?? 'Liste yüklenemedi'}</div>
        <div className="d">{error!.status ? <>Sunucu isteği reddetti: {error!.message} <span className="mono">(HTTP {error!.status})</span>.</> : 'Sunucuya ulaşılamadı.'} Veriler değişmedi; yeniden deneyebilirsiniz.</div>
        {props.onRetry && <button className="btn" onClick={props.onRetry}>Yeniden dene</button>}
      </div>}
    </>
  )
}

export function Pager({ p, noun = 'kayıt' }: { p: Paged; noun?: string }) {
  const from = p.rows.length ? p.page * p.pageSize + 1 : 0
  const to = p.page * p.pageSize + p.rows.length
  return (
    <div className="tfoot">
      <div className="grow">
        {p.total != null
          ? <>Sayfa {p.page + 1} / {Math.max(1, Math.ceil(p.total / p.pageSize))} · {num(p.total)} {noun}</>
          : <>{num(from)}–{num(to)} arası {noun}</>} · sayfa başına {p.pageSize}
      </div>
      <button className="btn sm" disabled={p.page === 0} onClick={() => p.setPage(p.page - 1)}>Önceki</button>
      <button className="btn sm" disabled={!p.hasNext} onClick={() => p.setPage(p.page + 1)}>Sonraki</button>
    </div>
  )
}

/** Text input whose value reaches the caller after typing pauses. */
export function Search({ value, onChange, placeholder = 'Ara', mono, width = 260 }: { value: string; onChange: (v: string) => void; placeholder?: string; mono?: boolean; width?: number | string }) {
  const [text, setText] = useState(value)
  useEffect(() => setText(value), [value])
  useEffect(() => {
    if (text === value) return
    const t = setTimeout(() => onChange(text), 300)
    return () => clearTimeout(t)
  }, [text]) // eslint-disable-line react-hooks/exhaustive-deps
  return <input className={'input' + (mono ? ' mono' : '')} style={{ width }} value={text} placeholder={placeholder} onChange={(e) => setText(e.target.value)} />
}

// ---- overlays ----

export function Modal({ width = 560, children, onClose, flush, top }: { width?: number; children: ReactNode; onClose?: () => void; flush?: boolean; top?: boolean }) {
  useEffect(() => {
    const h = (e: KeyboardEvent) => e.key === 'Escape' && onClose?.()
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  }, [onClose])
  return (
    <div className={'scrim' + (top ? ' top2' : '')} onMouseDown={(e) => e.target === e.currentTarget && onClose?.()}>
      <div className={'modal' + (flush ? ' flush' : '')} style={{ width }} role="dialog" aria-modal="true">{children}</div>
    </div>
  )
}

interface ConfirmOpts { title: string; body: ReactNode; action: string; danger?: boolean }
interface UI {
  toast: (text: string) => void
  confirm: (opts: ConfirmOpts) => Promise<boolean>
}
const UIContext = createContext<UI>(null!)
export const useUI = () => useContext(UIContext)

export function UIProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState('')
  const [ask, setAsk] = useState<(ConfirmOpts & { done: (ok: boolean) => void }) | null>(null)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)
  const cancel = useRef<HTMLButtonElement>(null)
  const ui = useRef<UI>({
    toast: (text) => {
      setToast(text)
      clearTimeout(timer.current)
      timer.current = setTimeout(() => setToast(''), 2600)
    },
    confirm: (opts) => new Promise((done) => setAsk({ ...opts, done })),
  }).current
  const answer = useCallback((ok: boolean) => { ask?.done(ok); setAsk(null) }, [ask])
  useEffect(() => { if (ask) cancel.current?.focus() }, [ask])
  return (
    <UIContext.Provider value={ui}>
      {children}
      {ask && (
        <Modal width={420} top onClose={() => answer(false)}>
          <div>
            <div className="modal-title">{ask.title}</div>
            <div className="tx2 pretty" style={{ marginTop: 6 }}>{ask.body}</div>
          </div>
          <div className="modal-foot" style={{ marginTop: 4 }}>
            <button ref={cancel} className="btn" onClick={() => answer(false)}>Vazgeç</button>
            <button className={'btn ' + (ask.danger ? 'danger-fill' : 'primary')} onClick={() => answer(true)}>{ask.action}</button>
          </div>
        </Modal>
      )}
      {toast && <div className="toast" role="status">{toast}</div>}
    </UIContext.Provider>
  )
}

// ---- descriptor-driven form dialog ----

export interface FieldDef {
  key: string
  label: string
  /** 'section' is a heading between fields; 'pick' an ordered choice of several (its value is a list). */
  type?: 'text' | 'mono' | 'int' | 'money' | 'select' | 'seg' | 'textarea' | 'date' | 'datetime' | 'check' | 'password' | 'section' | 'pick'
  options?: Option[]
  span?: boolean
  required?: boolean
  hint?: string
  placeholder?: string
  /** A textarea for technical text (addresses): fixed-width type, `rows` lines tall. */
  mono?: boolean
  rows?: number
  /** What an optional select calls its empty choice; a dash by default. */
  blank?: string
  show?: (values: Record<string, any>) => boolean
  /** Shown in the form's "Gelişmiş ayarlar" part, which starts closed. */
  advanced?: boolean
  /** With `type: 'check'`: the text next to the box (`hint` then goes below, like any field's). */
  text?: string
}

export function Input({ def, value, onChange, error, autoFocus }: { def: FieldDef; value: any; onChange: (v: any) => void; error?: boolean; autoFocus?: boolean }) {
  const cls = (base: string) => base + (error ? ' err' : '')
  switch (def.type) {
    case 'select':
      return <select className="select" value={value ?? ''} onChange={(e) => onChange(e.target.value)}>
        {!def.required && <option value="">{def.blank ?? '—'}</option>}
        {def.options!.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
    case 'seg':
      return <Seg value={String(value ?? '')} options={def.options!} onChange={onChange} />
    case 'textarea':
      return <textarea className={cls('textarea' + (def.mono ? ' mono' : ''))} rows={def.rows ?? 2} value={value ?? ''} placeholder={def.placeholder}
        spellCheck={def.mono ? false : undefined} onChange={(e) => onChange(e.target.value)} />
    case 'check':
      return <span className="radio"><input type="checkbox" checked={!!value} onChange={(e) => onChange(e.target.checked)} /> {def.text ?? def.hint}</span>
    case 'pick':
      return <PickList value={Array.isArray(value) ? value : []} options={def.options!} onChange={onChange} />
    case 'password':
      return <input type="password" className={cls('input')} value={value ?? ''} autoComplete="new-password" autoFocus={autoFocus} placeholder={def.placeholder} onChange={(e) => onChange(e.target.value)} />
    case 'date':
      return <input type="date" className={cls('input mono')} value={value ?? ''} onChange={(e) => onChange(e.target.value)} />
    case 'datetime':
      return <input type="datetime-local" className={cls('input mono')} value={value ?? ''} onChange={(e) => onChange(e.target.value)} />
    default:
      return <input className={cls('input' + (def.type === 'mono' ? ' mono' : def.type === 'int' || def.type === 'money' ? ' num' : ''))}
        value={value ?? ''} placeholder={def.placeholder} autoFocus={autoFocus} onChange={(e) => onChange(e.target.value)}
        inputMode={def.type === 'int' ? 'numeric' : def.type === 'money' ? 'decimal' : undefined} />
  }
}

/**
 * A dialog built from field descriptors. `onSubmit` receives the raw values
 * (strings as typed); it throws to keep the dialog open and show the error.
 *
 * Fields marked `advanced` go into a "Gelişmiş ayarlar" part below the
 * others, closed at first. Its heading counts the settings that differ from
 * `defaults` (what a new row gets), so that a setting made there is seen
 * without opening it; it opens by itself when one of its fields is in error.
 */
export function FormModal(props: {
  title: string
  sub?: string
  fields: FieldDef[]
  initial?: Record<string, any>
  action: string
  width?: number
  onClose: () => void
  onSubmit: (values: Record<string, any>) => Promise<void>
  validate?: (values: Record<string, any>) => Record<string, string | undefined>
  defaults?: Record<string, any>
}) {
  const [values, setValues] = useState<Record<string, any>>(props.initial ?? {})
  const [more, setMore] = useState(false)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [failure, setFailure] = useState('')
  const [busy, setBusy] = useState(false)
  const fields = props.fields.filter((f) => !f.show || f.show(values))
  const submit = async () => {
    const errs: Record<string, string> = {}
    for (const [k, msg] of Object.entries(props.validate?.(values) ?? {})) if (msg) errs[k] = msg
    for (const f of fields) if (f.required && (values[f.key] == null || String(values[f.key]).trim() === '')) errs[f.key] = 'Bu alan zorunlu.'
    setErrors(errs)
    if (fields.some((f) => f.advanced && errs[f.key])) setMore(true)
    if (Object.keys(errs).length) return
    setBusy(true)
    setFailure('')
    try {
      await props.onSubmit(values)
    } catch (e) {
      setFailure(errorText(e as ApiError) || String(e))
      setBusy(false)
    }
  }
  const advanced = fields.filter((f) => f.advanced)
  // Settings in the closed part that are not what a new row gets, whether
  // or not their field is on show right now.
  const same = (a: any, b: any) => (Array.isArray(a) || Array.isArray(b) ? (a ?? []).join() === (b ?? []).join() : String(a ?? '') === String(b ?? ''))
  const changed = props.fields.filter((f) => f.advanced && f.type !== 'section' && props.defaults && f.key in props.defaults && !same(values[f.key], props.defaults[f.key])).length
  const one = (f: FieldDef, first: boolean) => f.type === 'section'
    ? <div key={f.key} className="sect span2 adv-sect">{f.label}{f.hint && <span className="sub">{f.hint}</span>}</div>
    : f.type === 'pick' ? (
      // Not a <label>: a click on the caption would press the first choice.
      <div key={f.key} className={'field' + (f.span ? ' span2' : '')}>
        <span className="label">{f.label}</span>
        <Input def={f} value={values[f.key]} onChange={(v) => setValues((old) => ({ ...old, [f.key]: v }))} />
        {f.hint && <span className="hint">{f.hint}</span>}
      </div>
    ) : (
      <Field key={f.key} label={f.label} span={f.span} error={errors[f.key]} hint={f.type === 'check' && !f.text ? undefined : f.hint}>
        <Input def={f} value={values[f.key]} error={!!errors[f.key]} autoFocus={first} onChange={(v) => setValues((old) => ({ ...old, [f.key]: v }))} />
      </Field>
    )
  return (
    <Modal width={props.width ?? 560} onClose={props.onClose}>
      <div>
        <div className="modal-title">{props.title}</div>
        {props.sub && <div className="tx2 pretty" style={{ marginTop: 2 }}>{props.sub}</div>}
      </div>
      <form className="grid2" onSubmit={(e) => { e.preventDefault(); submit() }}>
        {fields.filter((f) => !f.advanced).map((f, i) => one(f, i === 0))}
        {advanced.length > 0 && (
          <Disclosure title="Gelişmiş ayarlar" open={more} onToggle={() => setMore(!more)}
            mark={changed > 0 ? <Badge tone="acc">{changed} ayar değiştirilmiş</Badge> : <span className="sub">varsayılan değerlerde; çoğu kurulumda dokunmak gerekmez</span>}>
            {advanced.map((f) => one(f, false))}
          </Disclosure>
        )}
        <button type="submit" hidden />
      </form>
      {failure && <div className="banner bad">{failure}</div>}
      <div className="modal-foot">
        <button className="btn" onClick={props.onClose}>Vazgeç</button>
        <button className="btn primary" disabled={busy} onClick={submit}>{props.action}</button>
      </div>
    </Modal>
  )
}

export function StepBar({ steps, current }: { steps: string[]; current: number }) {
  return <div className="steps">{steps.map((s, i) =>
    <div key={s} className={'step' + (i < current ? ' done' : i === current ? ' on' : '')}><span className="n">{i + 1}</span>{s}</div>)}</div>
}

export function PageHead({ title, desc, back, children }: { title: ReactNode; desc?: ReactNode; back?: ReactNode; children?: ReactNode }) {
  return (
    <div className="page-head">
      <div className="grow" style={{ minWidth: 240 }}>
        {back}
        <div className="row wrap" style={{ gap: 10, marginTop: back ? 4 : 0 }}>{typeof title === 'string' ? <h1>{title}</h1> : title}</div>
        {desc && <div className="tx2 pretty" style={{ marginTop: 4 }}>{desc}</div>}
      </div>
      {children}
    </div>
  )
}
