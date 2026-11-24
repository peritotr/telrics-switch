// SPDX-License-Identifier: AGPL-3.0-only
import { useState, type ReactNode } from 'react'
import { ApiError, request, type Session } from '../lib/api'
import { t } from '../lib/i18n'
import { LangSwitch, Tabs } from '../ui/kit'
import { CodeInput, Setup } from './TwoStep'

const keyFormat = /^tsw_(live|test)_[A-Za-z0-9]{16,}$/
const modes = () => [{ value: 'user', label: t('login.modeUser') }, { value: 'key', label: t('login.modeKey') }]
const unreachable = () => t('login.unreachable')
const locked = () => t('login.locked')

/** The product name above the card, with the language switch: it must be reachable before sign-in. */
function Brand() {
  return (
    <div className="row" style={{ gap: 10 }}>
      <div className="brand-mark" style={{ width: 22, height: 22, borderRadius: 6 }} />
      <div className="grow" style={{ fontWeight: 600, fontSize: 16 }}>Telrics Switch</div>
      <LangSwitch />
    </div>
  )
}

/** The frame every sign-in page shares. */
function Frame({ children }: { children: ReactNode }) {
  return (
    <div className="login">
      <div className="login-col">
        <Brand />
        <div className="login-card">{children}</div>
      </div>
    </div>
  )
}

/** Asks for the e-mail address and says the same thing whoever it belongs to. */
function Forgot({ back }: { back: () => void }) {
  const [email, setEmail] = useState('')
  const [state, setState] = useState<'form' | 'busy' | 'sent'>('form')
  const [error, setError] = useState('')
  const submit = async () => {
    if (!email.trim()) return setError(t('login.forgotEnterEmail'))
    setState('busy')
    setError('')
    try {
      await request('POST', '/v1/password/forgot', { key: '-', body: { email: email.trim() } })
      setState('sent')
    } catch (e) {
      const err = e as ApiError
      setError(err.status === 503 ? t('login.forgotNoMail')
        : err.status ? t('login.forgotFailed', { status: err.status }) : unreachable())
      setState('form')
    }
  }
  if (state === 'sent') return (
    <Frame>
      <div>
        <h1>{t('login.forgotSentTitle')}</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>{t('login.forgotSentDesc')}</div>
      </div>
      <button className="btn tall" onClick={back}>{t('login.backToSignIn')}</button>
    </Frame>
  )
  return (
    <Frame>
      <div>
        <h1>{t('login.forgotTitle')}</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>{t('login.forgotDesc')}</div>
      </div>
      <label className="field">
        <span className="label">{t('login.email')}</span>
        <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="email" value={email} autoFocus autoComplete="username"
          onChange={(e) => { setEmail(e.target.value); setError('') }} onKeyDown={(e) => e.key === 'Enter' && submit()} />
      </label>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <button className="btn primary tall" disabled={state === 'busy'} onClick={submit}>{state === 'busy' ? t('login.sending') : t('login.sendLink')}</button>
      <button className="btn tall" onClick={back}>{t('login.cancel')}</button>
    </Frame>
  )
}

/** Opened from the link in the e-mail: sets a new password. */
function Reset({ token, back }: { token: string; back: () => void }) {
  const [password, setPassword] = useState('')
  const [again, setAgain] = useState('')
  const [state, setState] = useState<'form' | 'busy' | 'done'>('form')
  const [error, setError] = useState('')
  const submit = async () => {
    if (password.length < 10) return setError(t('login.resetTooShort'))
    if (password !== again) return setError(t('login.resetMismatch'))
    setState('busy')
    setError('')
    try {
      await request('POST', '/v1/password/reset', { key: '-', body: { token, password } })
      setState('done')
    } catch (e) {
      const err = e as ApiError
      setError(err.status === 410 ? t('login.resetExpired')
        : err.status ? t('login.resetFailed', { status: err.status }) : unreachable())
      setState('form')
    }
  }
  if (state === 'done') return (
    <Frame>
      <div>
        <h1>{t('login.resetDoneTitle')}</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>{t('login.resetDoneDesc')}</div>
      </div>
      <button className="btn primary tall" onClick={back}>{t('login.signIn')}</button>
    </Frame>
  )
  return (
    <Frame>
      <div>
        <h1>{t('login.resetTitle')}</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>{t('login.resetDesc')}</div>
      </div>
      <label className="field">
        <span className="label">{t('login.newPassword')}</span>
        <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="password" value={password} autoFocus autoComplete="new-password"
          onChange={(e) => { setPassword(e.target.value); setError('') }} />
      </label>
      <label className="field">
        <span className="label">{t('login.newPasswordAgain')}</span>
        <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="password" value={again} autoComplete="new-password"
          onChange={(e) => { setAgain(e.target.value); setError('') }} onKeyDown={(e) => e.key === 'Enter' && submit()} />
      </label>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <button className="btn primary tall" disabled={state === 'busy'} onClick={submit}>{state === 'busy' ? t('login.saving') : t('login.savePassword')}</button>
    </Frame>
  )
}

/** The second step of sign-in: a code from the app, or a recovery code. */
function SecondStep({ challenge, onToken, back }: { challenge: string; onToken: (token: string) => Promise<void>; back: (why?: string) => void }) {
  const [recovery, setRecovery] = useState(false)
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const submit = async () => {
    if (busy) return
    const value = code.trim()
    if (recovery ? !value : value.length !== 6) return setError(recovery ? t('login.enterRecovery') : t('login.enterCode'))
    setBusy(true)
    setError('')
    try {
      const { data } = await request<{ token: string }>('POST', '/v1/login/verify', { key: '-', body: { challenge, code: value } })
      await onToken(data.token)
    } catch (e) {
      const err = e as ApiError
      // The password step is asked again once this one has run out of time.
      if (err.status === 410) return back(t('login.stepExpired'))
      setError(err.status === 401 ? (recovery ? t('login.wrongRecovery') : t('login.wrongCode'))
        : err.status === 429 ? locked()
        : err.status ? t('login.serverRefused', { status: err.status }) : unreachable())
      setBusy(false)
    }
  }
  return (
    <Frame>
      <div>
        <h1>{t('login.twoStepTitle')}</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>
          {recovery ? t('login.recoveryDesc') : t('login.codeDesc')}
        </div>
      </div>
      <label className="field">
        <span className="label">{recovery ? t('login.recoveryCode') : t('login.verificationCode')}</span>
        {recovery
          ? <input className={'input mono' + (error ? ' err' : '')} style={{ height: 36 }} value={code} autoFocus autoComplete="off" spellCheck={false} placeholder="abcde-fghij"
              onChange={(e) => { setCode(e.target.value); setError('') }} onKeyDown={(e) => e.key === 'Enter' && submit()} />
          : <CodeInput value={code} onChange={(v) => { setCode(v); setError('') }} onEnter={submit} error={!!error} />}
      </label>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <button className="btn primary tall" disabled={busy} onClick={submit}>{busy ? t('login.verifying') : t('login.signIn')}</button>
      <a style={{ fontSize: 12.5, textAlign: 'center' }} onClick={() => { setRecovery(!recovery); setCode(''); setError('') }}>
        {recovery ? t('login.useApp') : t('login.useRecovery')}
      </a>
      <button className="btn tall" onClick={() => back()}>{t('login.cancel')}</button>
      <div className="tx3 pretty" style={{ borderTop: '1px solid var(--line2)', paddingTop: 12, fontSize: 12 }}>
        {t('login.noPhone')}
      </div>
    </Frame>
  )
}

export function Login({ onLogin }: { onLogin: (key: string, session: Session) => void }) {
  // The link in a reset e-mail lands here before anyone is signed in.
  const [resetToken, setResetToken] = useState(() =>
    (window.location.pathname === '/reset' ? new URLSearchParams(window.location.search).get('token') : null))
  const [forgot, setForgot] = useState(false)
  const [mode, setMode] = useState('user')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [key, setKey] = useState('')
  const [show, setShow] = useState(false)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const [challenge, setChallenge] = useState('')

  const fail = (e: unknown, refused: string) => {
    const err = e as ApiError
    setError(err.status === 401 ? refused
      : err.status === 429 ? locked()
      : err.status ? t('login.serverRefused', { status: err.status }) : unreachable())
    setBusy(false)
  }
  const open = async (token: string, refused: string) => {
    setBusy(true)
    setError('')
    try {
      const { data } = await request<Session>('GET', '/v1/session', { key: token })
      onLogin(token, data)
    } catch (e) { fail(e, refused) }
  }
  const submit = async () => {
    if (mode === 'key') {
      const k = key.trim()
      if (!k) return setError(t('login.enterKey'))
      if (!keyFormat.test(k)) return setError(t('login.badKeyFormat'))
      return open(k, t('login.badKey'))
    }
    if (!email.trim() || !password) return setError(t('login.enterCredentials'))
    setBusy(true)
    setError('')
    try {
      const { data } = await request<{ token?: string; two_step?: boolean; challenge?: string }>('POST', '/v1/login', { key: '-', body: { email: email.trim(), password } })
      if (data.two_step && data.challenge) {
        // The password was right; the code is asked for on a page of its own.
        setBusy(false)
        setPassword('')
        return setChallenge(data.challenge)
      }
      await open(data.token ?? '', t('login.sessionRefused'))
    } catch (e) { fail(e, t('login.wrongCredentials')) }
  }
  const enter = (e: { key: string }) => e.key === 'Enter' && submit()
  if (resetToken) return <Reset token={resetToken} back={() => { window.history.replaceState(null, '', '/'); setResetToken(null) }} />
  if (forgot) return <Forgot back={() => setForgot(false)} />
  if (challenge) return <SecondStep challenge={challenge} back={(why) => { setChallenge(''); setError(why ?? '') }}
    onToken={async (token) => onLogin(token, (await request<Session>('GET', '/v1/session', { key: token })).data)} />

  return (
    <div className="login">
      <div className="login-col">
        <Brand />
        <div className="login-card">
          <div>
            <h1>{t('login.signIn')}</h1>
            <div className="tx2 pretty" style={{ marginTop: 4 }}>{t('login.desc')}</div>
          </div>
          <div style={{ margin: '0 -20px' }}><Tabs value={mode} tabs={modes()} onChange={(m) => { setMode(m); setError('') }} /></div>
          {mode === 'user' ? <>
            <label className="field">
              <span className="label">{t('login.email')}</span>
              <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="email" value={email} autoFocus autoComplete="username"
                onChange={(e) => { setEmail(e.target.value); setError('') }} onKeyDown={enter} />
            </label>
            <label className="field">
              <span className="label">{t('login.password')}</span>
              <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="password" value={password} autoComplete="current-password"
                onChange={(e) => { setPassword(e.target.value); setError('') }} onKeyDown={enter} />
            </label>
          </> : (
            <label className="field">
              <span className="label">{t('login.apiKey')}</span>
              <div className="row">
                <input className={'input mono' + (error ? ' err' : '')} style={{ height: 36 }} type={show ? 'text' : 'password'} value={key} autoFocus
                  placeholder="tsw_live_…" autoComplete="off" spellCheck={false}
                  onChange={(e) => { setKey(e.target.value); setError('') }} onKeyDown={enter} />
                <button type="button" className="btn tall" onClick={() => setShow(!show)}>{show ? t('login.hide') : t('login.show')}</button>
              </div>
            </label>
          )}
          {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
          <button className="btn primary tall" disabled={busy} onClick={submit}>{busy ? t('login.verifying') : t('login.signIn')}</button>
          {mode === 'user' && <a style={{ fontSize: 12.5, textAlign: 'center' }} onClick={() => setForgot(true)}>{t('login.forgot')}</a>}
          <div className="tx3 pretty" style={{ borderTop: '1px solid var(--line2)', paddingTop: 12, fontSize: 12 }}>
            {mode === 'user'
              ? t('login.noAccount')
              : t('login.noKey')} {t('login.notStored')}
          </div>
        </div>
      </div>
    </div>
  )
}

/**
 * What a signed-in user sees while the account requires two-step sign-in
 * and they have not set it up: the server answers nothing else until then.
 */
export function TwoStepRequired({ session, recheck, logout }: { session: Session; recheck: () => Promise<void>; logout: () => void }) {
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const close = async () => {
    setOpen(false)
    try { await recheck() } catch { setError(t('login.requiredCheckFailed')) }
  }
  return (
    <Frame>
      <div>
        <h1>{t('login.requiredTitle')}</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>
          {t('login.requiredDesc')}
        </div>
      </div>
      <div className="kv"><span>{t('login.session')}</span><span className="ell">{session.user?.email}</span></div>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <button className="btn primary tall" onClick={() => { setError(''); setOpen(true) }}>{t('login.startSetup')}</button>
      <a style={{ fontSize: 12.5, textAlign: 'center' }} onClick={logout}>{t('login.signOut')}</a>
      <div className="tx3 pretty" style={{ borderTop: '1px solid var(--line2)', paddingTop: 12, fontSize: 12 }}>
        {t('login.requiredNoPhone')}
      </div>
      {open && <Setup onClose={close} />}
    </Frame>
  )
}
