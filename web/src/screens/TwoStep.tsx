// SPDX-License-Identifier: AGPL-3.0-only
// Two-step sign-in for the signed-in user: switching it on with an
// authenticator app, new recovery codes, switching it off.
import { QRCodeSVG } from 'qrcode.react'
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { api, useGet, useInvalidate, type ApiError } from '../lib/api'
import { num } from '../lib/format'
import { t } from '../lib/i18n'
import { Badge, Modal, useUI } from '../ui/kit'

type Dialog = 'setup' | 'codes' | 'off' | null
interface Status { enabled: boolean; recovery_codes_left: number }

const wrongCode = () => t('login.wrongCode')
const locked = () => t('twostep.lockedOwn')
export const digits = (v: string) => v.replace(/\D/g, '').slice(0, 6)

/** The six digits an authenticator app shows. */
export function CodeInput({ value, onChange, onEnter, error }: { value: string; onChange: (v: string) => void; onEnter?: () => void; error?: boolean }) {
  return <input className={'input mono' + (error ? ' err' : '')} inputMode="numeric" autoComplete="one-time-code" placeholder="000000" autoFocus value={value}
    style={{ height: 36, maxWidth: 200, fontSize: 18, letterSpacing: '.3em', textAlign: 'center' }}
    onChange={(e) => onChange(digits(e.target.value))} onKeyDown={(e) => e.key === 'Enter' && onEnter?.()} />
}

function Head({ title, sub }: { title: string; sub?: ReactNode }) {
  return <div><div className="modal-title">{title}</div>{sub && <div className="tx2 pretty" style={{ marginTop: 2 }}>{sub}</div>}</div>
}

/** The recovery codes, shown once: the dialog closes only after the user says they are saved. */
function RecoveryCodes({ title, sub, codes, onClose }: { title: string; sub: string; codes: string[]; onClose: () => void }) {
  const [saved, setSaved] = useState(false)
  const ui = useUI()
  const copy = async () => {
    try { await navigator.clipboard.writeText(codes.join('\n')); ui.toast(t('twostep.codesCopied')) } catch { ui.toast(t('twostep.codesCopyFailed')) }
  }
  return (
    <Modal width={520}>
      <Head title={title} sub={sub} />
      <div className="banner warn">
        <span className="warn" style={{ fontWeight: 600 }}>{t('twostep.codesOnce')}</span> {t('twostep.codesOnceDesc')}
      </div>
      <div className="row" style={{ alignItems: 'flex-start' }}>
        <div className="grow mono" style={{ padding: '10px 12px', background: 'var(--panel2)', border: '1px solid var(--line)', borderRadius: 8, display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(110px,1fr))', gap: '4px 12px' }}>
          {codes.map((c) => <span key={c}>{c}</span>)}
        </div>
        <button className="btn" onClick={copy}>{t('action.copy')}</button>
      </div>
      <label className="radio"><input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} />{t('twostep.codesSaved')}</label>
      <div className="modal-foot"><button className="btn primary" disabled={!saved} onClick={onClose}>{t('action.close')}</button></div>
    </Modal>
  )
}

export function Setup({ onClose }: { onClose: () => void }) {
  const [start, setStart] = useState<{ secret: string; uri: string } | null>(null)
  const [failed, setFailed] = useState(false)
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [codes, setCodes] = useState<string[] | null>(null)
  // The secret is asked for once, when the dialog opens: a second request
  // would replace it on the server while the first is on screen.
  const asked = useRef(false)
  useEffect(() => {
    if (asked.current) return
    asked.current = true
    api.post<{ secret: string; uri: string }>('/v1/me/two-step/setup').then(setStart, () => setFailed(true))
  }, [])
  const confirm = async () => {
    if (busy) return
    if (code.length !== 6) return setError(t('login.enterCode'))
    setBusy(true)
    setError('')
    try {
      setCodes((await api.post<{ recovery_codes: string[] }>('/v1/me/two-step/confirm', { code })).recovery_codes)
    } catch (e) {
      setError((e as ApiError).status === 422 ? wrongCode() : t('twostep.verifyFailed'))
    } finally { setBusy(false) }
  }
  if (codes) return <RecoveryCodes title={t('twostep.onTitle')} sub={t('twostep.onDesc')} codes={codes} onClose={onClose} />
  return (
    <Modal width={540} onClose={onClose}>
      <Head title={t('twostep.setupTitle')} sub={t('twostep.setupDesc')} />
      {failed ? <div className="banner bad">{t('twostep.setupFailed')}</div>
        : !start ? <div className="tx3">{t('action.preparing')}</div>
        : <>
          <div className="row wrap" style={{ gap: 16 }}>
            {/* A QR code is read by cameras: dark on white in both themes. */}
            <div style={{ padding: 10, background: '#fff', borderRadius: 8, border: '1px solid var(--line)', lineHeight: 0 }}>
              <QRCodeSVG value={start.uri} size={156} level="M" title={t('twostep.qrTitle')} />
            </div>
            <div className="grow" style={{ minWidth: 200, display: 'flex', flexDirection: 'column', gap: 6 }}>
              <div className="tx2">{t('twostep.manual')}</div>
              <div className="mono" style={{ padding: '10px 12px', background: 'var(--panel2)', border: '1px solid var(--line)', borderRadius: 8, wordBreak: 'break-all' }}>
                {start.secret.replace(/(.{4})/g, '$1 ').trim()}
              </div>
            </div>
          </div>
          <label className="field">
            <span className="label">{t('twostep.appCode')}</span>
            <CodeInput value={code} onChange={(v) => { setCode(v); setError('') }} onEnter={confirm} error={!!error} />
          </label>
          {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
        </>}
      <div className="modal-foot">
        <button className="btn" onClick={onClose}>{t('action.cancel')}</button>
        {start && <button className="btn primary" disabled={busy} onClick={confirm}>{busy ? t('login.verifying') : t('twostep.verifyEnable')}</button>}
      </div>
    </Modal>
  )
}

function NewCodes({ onClose }: { onClose: () => void }) {
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [codes, setCodes] = useState<string[] | null>(null)
  const submit = async () => {
    if (busy) return
    if (code.length !== 6) return setError(t('login.enterCode'))
    setBusy(true)
    setError('')
    try {
      setCodes((await api.post<{ recovery_codes: string[] }>('/v1/me/two-step/recovery-codes', { code })).recovery_codes)
    } catch (e) {
      const status = (e as ApiError).status
      setError(status === 403 ? wrongCode() : status === 429 ? locked() : t('twostep.renewFailed'))
    } finally { setBusy(false) }
  }
  if (codes) return <RecoveryCodes title={t('twostep.newCodesTitle')} sub={t('twostep.newCodesDesc')} codes={codes} onClose={onClose} />
  return (
    <Modal width={440} onClose={onClose}>
      <Head title={t('twostep.renewTitle')} sub={t('twostep.renewDesc')} />
      <label className="field">
        <span className="label">{t('twostep.appCode')}</span>
        <CodeInput value={code} onChange={(v) => { setCode(v); setError('') }} onEnter={submit} error={!!error} />
      </label>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <div className="modal-foot">
        <button className="btn" onClick={onClose}>{t('action.cancel')}</button>
        <button className="btn primary" disabled={busy} onClick={submit}>{busy ? t('twostep.creating') : t('twostep.createCodes')}</button>
      </div>
    </Modal>
  )
}

function TurnOff({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [password, setPassword] = useState('')
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async () => {
    if (busy) return
    if (!password || !code.trim()) return setError(t('twostep.offEnter'))
    setBusy(true)
    setError('')
    try {
      await api.post('/v1/me/two-step/disable', { password, code: code.trim() })
      onDone()
    } catch (e) {
      const err = e as ApiError
      setError(err.status === 403 ? (err.message.includes('password') ? t('twostep.wrongPassword') : t('twostep.wrongCodeOrRecovery'))
        : err.status === 429 ? locked() : t('twostep.offFailed'))
      setBusy(false)
    }
  }
  return (
    <Modal width={440} onClose={onClose}>
      <Head title={t('twostep.offTitle')} sub={t('twostep.offDesc')} />
      <label className="field">
        <span className="label">{t('login.password')}</span>
        <input className="input" style={{ height: 36 }} type="password" autoComplete="current-password" autoFocus value={password} onChange={(e) => { setPassword(e.target.value); setError('') }} />
      </label>
      <label className="field">
        <span className="label">{t('twostep.codeOrRecovery')}</span>
        <input className="input mono" style={{ height: 36 }} autoComplete="one-time-code" value={code} onChange={(e) => { setCode(e.target.value); setError('') }} onKeyDown={(e) => e.key === 'Enter' && submit()} />
      </label>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <div className="modal-foot">
        <button className="btn" onClick={onClose}>{t('action.cancel')}</button>
        <button className="btn danger-fill" disabled={busy} onClick={submit}>{busy ? t('twostep.turningOff') : t('twostep.turnOff')}</button>
      </div>
    </Modal>
  )
}

/** The signed-in user's own two-step sign-in, as a card. */
export function TwoStepCard() {
  const q = useGet<Status>('/v1/me/two-step')
  const invalidate = useInvalidate()
  const ui = useUI()
  const [dialog, setDialog] = useState<Dialog>(null)
  const on = q.data?.enabled === true
  const left = q.data?.recovery_codes_left ?? 0
  const close = () => { setDialog(null); invalidate('/v1/me/two-step', '/v1/users') }
  return (
    <div className="card" style={{ padding: 16, display: 'grid', gap: 10 }}>
      <div className="row">
        <div className="sect grow">{t('twostep.cardTitle')} <span className="tx3" style={{ fontWeight: 400 }}>{t('twostep.cardOwn')}</span></div>
        {q.data && (on ? <Badge tone="ok">{t('common.on')}</Badge> : <Badge>{t('common.off')}</Badge>)}
      </div>
      <div className="tx2 pretty">{t('twostep.cardDesc')}</div>
      {q.error ? <div className="tx2">{t('twostep.statusFailed')} <a onClick={() => q.refetch()}>{t('action.retry')}</a></div> : q.data && <>
        {on && <div className={left <= 2 ? 'warn' : 'tx2'}>{left > 0 ? t('twostep.codesLeft', { n: num(left) }) : t('twostep.noCodesLeft')}</div>}
        <div className="row wrap">
          {on && <button className="btn" onClick={() => setDialog('codes')}>{t('twostep.renewTitle')}</button>}
          <button className={on ? 'btn' : 'btn primary'} onClick={() => setDialog(on ? 'off' : 'setup')}>{on ? t('twostep.turnOff') : t('twostep.enable')}</button>
        </div>
      </>}
      {dialog === 'setup' && <Setup onClose={close} />}
      {dialog === 'codes' && <NewCodes onClose={close} />}
      {dialog === 'off' && <TurnOff onClose={close} onDone={() => { close(); ui.toast(t('twostep.turnedOff')) }} />}
    </div>
  )
}
