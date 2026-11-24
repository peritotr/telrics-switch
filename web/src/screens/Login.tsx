// SPDX-License-Identifier: AGPL-3.0-only
import { useState, type ReactNode } from 'react'
import { ApiError, request, type Session } from '../lib/api'
import { Tabs } from '../ui/kit'
import { CodeInput, Setup } from './TwoStep'

const keyFormat = /^tsw_(live|test)_[A-Za-z0-9]{16,}$/
const modes = [{ value: 'user', label: 'E-posta ve parola' }, { value: 'key', label: 'API anahtarı' }]

/** The frame every sign-in page shares. */
function Frame({ children }: { children: ReactNode }) {
  return (
    <div className="login">
      <div className="login-col">
        <div className="row" style={{ gap: 10 }}>
          <div className="brand-mark" style={{ width: 22, height: 22, borderRadius: 6 }} />
          <div style={{ fontWeight: 600, fontSize: 16 }}>Telrics Switch</div>
        </div>
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
    if (!email.trim()) return setError('E-posta adresinizi girin.')
    setState('busy')
    setError('')
    try {
      await request('POST', '/v1/password/forgot', { key: '-', body: { email: email.trim() } })
      setState('sent')
    } catch (e) {
      const err = e as ApiError
      setError(err.status === 503 ? 'Bu kurulumda e-posta gönderimi açık değil. Parolanızı bir yöneticinin sıfırlaması gerekir.'
        : err.status ? `İstek gönderilemedi (HTTP ${err.status}).` : 'Sunucuya ulaşılamadı. Bağlantıyı kontrol edip yeniden deneyin.')
      setState('form')
    }
  }
  if (state === 'sent') return (
    <Frame>
      <div>
        <h1>E-postanızı kontrol edin</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>Bu adres bir hesaba aitse parola sıfırlama bağlantısı gönderildi. Bağlantı 30 dakika geçerlidir ve bir kez kullanılabilir.</div>
      </div>
      <button className="btn tall" onClick={back}>Oturum açmaya dön</button>
    </Frame>
  )
  return (
    <Frame>
      <div>
        <h1>Parolamı unuttum</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>Hesabınızın e-posta adresini yazın; yeni parola belirlemeniz için bir bağlantı göndeririz.</div>
      </div>
      <label className="field">
        <span className="label">E-posta</span>
        <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="email" value={email} autoFocus autoComplete="username"
          onChange={(e) => { setEmail(e.target.value); setError('') }} onKeyDown={(e) => e.key === 'Enter' && submit()} />
      </label>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <button className="btn primary tall" disabled={state === 'busy'} onClick={submit}>{state === 'busy' ? 'Gönderiliyor…' : 'Bağlantı gönder'}</button>
      <button className="btn tall" onClick={back}>Vazgeç</button>
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
    if (password.length < 10) return setError('Parola en az 10 karakter olmalı.')
    if (password !== again) return setError('İki parola aynı değil.')
    setState('busy')
    setError('')
    try {
      await request('POST', '/v1/password/reset', { key: '-', body: { token, password } })
      setState('done')
    } catch (e) {
      const err = e as ApiError
      setError(err.status === 410 ? 'Bu bağlantı artık geçerli değil. Süresi dolmuş ya da daha önce kullanılmış olabilir; yeni bir bağlantı isteyin.'
        : err.status ? `Parola değiştirilemedi (HTTP ${err.status}).` : 'Sunucuya ulaşılamadı. Bağlantıyı kontrol edip yeniden deneyin.')
      setState('form')
    }
  }
  if (state === 'done') return (
    <Frame>
      <div>
        <h1>Parolanız değiştirildi</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>Açık oturumlarınız kapatıldı. Yeni parolanızla oturum açabilirsiniz.</div>
      </div>
      <button className="btn primary tall" onClick={back}>Oturum aç</button>
    </Frame>
  )
  return (
    <Frame>
      <div>
        <h1>Yeni parola belirleyin</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>En az 10 karakter. Kaydettiğinizde açık oturumlarınız kapanır.</div>
      </div>
      <label className="field">
        <span className="label">Yeni parola</span>
        <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="password" value={password} autoFocus autoComplete="new-password"
          onChange={(e) => { setPassword(e.target.value); setError('') }} />
      </label>
      <label className="field">
        <span className="label">Yeni parola (yeniden)</span>
        <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="password" value={again} autoComplete="new-password"
          onChange={(e) => { setAgain(e.target.value); setError('') }} onKeyDown={(e) => e.key === 'Enter' && submit()} />
      </label>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <button className="btn primary tall" disabled={state === 'busy'} onClick={submit}>{state === 'busy' ? 'Kaydediliyor…' : 'Parolayı kaydet'}</button>
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
    if (recovery ? !value : value.length !== 6) return setError(recovery ? 'Yedek kodlarınızdan birini girin.' : 'Uygulamadaki 6 haneli kodu girin.')
    setBusy(true)
    setError('')
    try {
      const { data } = await request<{ token: string }>('POST', '/v1/login/verify', { key: '-', body: { challenge, code: value } })
      await onToken(data.token)
    } catch (e) {
      const err = e as ApiError
      // The password step is asked again once this one has run out of time.
      if (err.status === 410) return back('Doğrulama için ayrılan süre doldu. Parolanızla yeniden başlayın.')
      setError(err.status === 401 ? (recovery ? 'Yedek kod hatalı ya da daha önce kullanılmış.' : 'Kod hatalı ya da süresi doldu. Uygulamadaki güncel kodu girin.')
        : err.status === 429 ? 'Çok fazla hatalı deneme yapıldı. Hesap 15 dakika kilitlendi; sonra yeniden deneyin ya da bir yöneticiden kilidi açmasını isteyin.'
        : err.status ? `Sunucu oturumu açamadı (HTTP ${err.status}).` : 'Sunucuya ulaşılamadı. Bağlantıyı kontrol edip yeniden deneyin.')
      setBusy(false)
    }
  }
  return (
    <Frame>
      <div>
        <h1>İki adımlı doğrulama</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>
          {recovery ? 'İki adımlı doğrulamayı açarken kaydettiğiniz yedek kodlardan birini girin. Her kod bir kez kullanılabilir.'
            : 'Parolanız doğrulandı. Telefonunuzdaki doğrulama uygulamasının gösterdiği 6 haneli kodu girin.'}
        </div>
      </div>
      <label className="field">
        <span className="label">{recovery ? 'Yedek kod' : 'Doğrulama kodu'}</span>
        {recovery
          ? <input className={'input mono' + (error ? ' err' : '')} style={{ height: 36 }} value={code} autoFocus autoComplete="off" spellCheck={false} placeholder="abcde-fghij"
              onChange={(e) => { setCode(e.target.value); setError('') }} onKeyDown={(e) => e.key === 'Enter' && submit()} />
          : <CodeInput value={code} onChange={(v) => { setCode(v); setError('') }} onEnter={submit} error={!!error} />}
      </label>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <button className="btn primary tall" disabled={busy} onClick={submit}>{busy ? 'Doğrulanıyor…' : 'Oturum aç'}</button>
      <a style={{ fontSize: 12.5, textAlign: 'center' }} onClick={() => { setRecovery(!recovery); setCode(''); setError('') }}>
        {recovery ? 'Uygulamadaki kodu kullan' : 'Telefonuma ulaşamıyorum, yedek kod kullan'}
      </a>
      <button className="btn tall" onClick={() => back()}>Vazgeç</button>
      <div className="tx3 pretty" style={{ borderTop: '1px solid var(--line2)', paddingTop: 12, fontSize: 12 }}>
        Telefonunuz da yedek kodlarınız da yoksa bir yöneticiden Kullanıcılar ve roller ekranından iki adımlı doğrulamanızı sıfırlamasını isteyin.
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
      : err.status === 429 ? 'Çok fazla hatalı deneme yapıldı. Hesap 15 dakika kilitlendi; sonra yeniden deneyin ya da bir yöneticiden kilidi açmasını isteyin.'
      : err.status ? `Sunucu oturumu açamadı (HTTP ${err.status}).` : 'Sunucuya ulaşılamadı. Bağlantıyı kontrol edip yeniden deneyin.')
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
      if (!k) return setError('API anahtarını girin.')
      if (!keyFormat.test(k)) return setError('Anahtar biçimi tanınmadı. Anahtarlar tsw_live_ ile başlar.')
      return open(k, 'Anahtar geçersiz ya da iptal edilmiş (HTTP 401).')
    }
    if (!email.trim() || !password) return setError('E-posta adresinizi ve parolanızı girin.')
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
      await open(data.token ?? '', 'Oturum açılamadı (HTTP 401).')
    } catch (e) { fail(e, 'E-posta adresi ya da parola hatalı.') }
  }
  const enter = (e: { key: string }) => e.key === 'Enter' && submit()
  if (resetToken) return <Reset token={resetToken} back={() => { window.history.replaceState(null, '', '/'); setResetToken(null) }} />
  if (forgot) return <Forgot back={() => setForgot(false)} />
  if (challenge) return <SecondStep challenge={challenge} back={(why) => { setChallenge(''); setError(why ?? '') }}
    onToken={async (token) => onLogin(token, (await request<Session>('GET', '/v1/session', { key: token })).data)} />

  return (
    <div className="login">
      <div className="login-col">
        <div className="row" style={{ gap: 10 }}>
          <div className="brand-mark" style={{ width: 22, height: 22, borderRadius: 6 }} />
          <div style={{ fontWeight: 600, fontSize: 16 }}>Telrics Switch</div>
        </div>
        <div className="login-card">
          <div>
            <h1>Oturum aç</h1>
            <div className="tx2 pretty" style={{ marginTop: 4 }}>Görebileceğiniz ve değiştirebileceğiniz şeyler hesabınızın rolüne göre belirlenir.</div>
          </div>
          <div style={{ margin: '0 -20px' }}><Tabs value={mode} tabs={modes} onChange={(m) => { setMode(m); setError('') }} /></div>
          {mode === 'user' ? <>
            <label className="field">
              <span className="label">E-posta</span>
              <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="email" value={email} autoFocus autoComplete="username"
                onChange={(e) => { setEmail(e.target.value); setError('') }} onKeyDown={enter} />
            </label>
            <label className="field">
              <span className="label">Parola</span>
              <input className={'input' + (error ? ' err' : '')} style={{ height: 36 }} type="password" value={password} autoComplete="current-password"
                onChange={(e) => { setPassword(e.target.value); setError('') }} onKeyDown={enter} />
            </label>
          </> : (
            <label className="field">
              <span className="label">API anahtarı</span>
              <div className="row">
                <input className={'input mono' + (error ? ' err' : '')} style={{ height: 36 }} type={show ? 'text' : 'password'} value={key} autoFocus
                  placeholder="tsw_live_…" autoComplete="off" spellCheck={false}
                  onChange={(e) => { setKey(e.target.value); setError('') }} onKeyDown={enter} />
                <button type="button" className="btn tall" onClick={() => setShow(!show)}>{show ? 'Gizle' : 'Göster'}</button>
              </div>
            </label>
          )}
          {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
          <button className="btn primary tall" disabled={busy} onClick={submit}>{busy ? 'Doğrulanıyor…' : 'Oturum aç'}</button>
          {mode === 'user' && <a style={{ fontSize: 12.5, textAlign: 'center' }} onClick={() => setForgot(true)}>Parolamı unuttum</a>}
          <div className="tx3 pretty" style={{ borderTop: '1px solid var(--line2)', paddingTop: 12, fontSize: 12 }}>
            {mode === 'user'
              ? 'Hesabınız yoksa bir yöneticiden Kullanıcılar ve roller ekranından hesap açmasını isteyin.'
              : 'Anahtarınız yoksa bir yöneticiden Ayarlar › API anahtarları bölümünden oluşturmasını isteyin.'} Oturum bu tarayıcıda saklanmaz; sekme kapanınca yeniden girilir.
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
    try { await recheck() } catch { setError('Durum denetlenemedi. Birkaç saniye sonra yeniden deneyin.') }
  }
  return (
    <Frame>
      <div>
        <h1>İki adımlı doğrulamayı kurun</h1>
        <div className="tx2 pretty" style={{ marginTop: 4 }}>
          Bu hesapta panel kullanıcılarının tümü için iki adımlı doğrulama zorunludur. Devam etmek için telefonunuzdaki doğrulama uygulamasını hesabınıza bağlayın; kurulum bitince panel açılır.
        </div>
      </div>
      <div className="kv"><span>Oturum</span><span className="ell">{session.user?.email}</span></div>
      {error && <div className="banner bad" style={{ padding: '8px 12px' }}>{error}</div>}
      <button className="btn primary tall" onClick={() => { setError(''); setOpen(true) }}>Kurulumu başlat</button>
      <a style={{ fontSize: 12.5, textAlign: 'center' }} onClick={logout}>Oturumu kapat</a>
      <div className="tx3 pretty" style={{ borderTop: '1px solid var(--line2)', paddingTop: 12, fontSize: 12 }}>
        Telefonunuz yanınızda değilse oturumu kapatın ve bir yöneticiye başvurun. API anahtarları bu kuraldan etkilenmez.
      </div>
      {open && <Setup onClose={close} />}
    </Frame>
  )
}
