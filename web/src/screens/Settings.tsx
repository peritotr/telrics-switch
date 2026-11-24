// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useGet, useInvalidate, type Row } from '../lib/api'
import { dt, num } from '../lib/format'
import { useSession } from '../lib/session'
import { ExchangeRates } from './ExchangeRates'
import { Badge, DataTable, Field, FormModal, Modal, PageHead, Seg, Toggle, errorText, useUI, type Col } from '../ui/kit'

const roles = [{ value: 'admin', label: 'Yönetici' }, { value: 'readonly', label: 'Salt okunur' }]
const roleLabel = (r: string) => (r === 'admin' ? 'Yönetici' : 'Salt okunur')

// Appearance is a per-browser preference, not account data.
const pref = (k: string, def: string) => { try { return localStorage.getItem(k) ?? def } catch { return def } }
export function applyPrefs() {
  document.documentElement.dataset.theme = pref('tsw.theme', 'light')
  document.documentElement.dataset.density = pref('tsw.density', 'md')
}
function setPref(k: string, v: string) {
  try { localStorage.setItem(k, v) } catch { /* private window */ }
  applyPrefs()
}

export function Settings() {
  const { canEdit } = useSession()
  const q = useGet<Row[]>('/v1/api-keys')
  const [adding, setAdding] = useState(false)
  const [created, setCreated] = useState<Row | null>(null)
  const [theme, setTheme] = useState(pref('tsw.theme', 'light'))
  const [density, setDensity] = useState(pref('tsw.density', 'md'))
  const ui = useUI()
  const invalidate = useInvalidate()
  const revoke = async (k: Row) => {
    if (!(await ui.confirm({ title: `“${k.name}” anahtarı iptal edilsin mi?`, body: 'Bu anahtarı kullanan oturumlar ve entegrasyonlar hemen HTTP 401 almaya başlar. İptal geri alınamaz.', action: 'Anahtarı iptal et', danger: true }))) return
    try {
      await api.del(`/v1/api-keys/${k.id}`)
      invalidate('/v1/api-keys')
      ui.toast('Anahtar iptal edildi')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const cols: Col<Row>[] = [
    { label: 'Ad', width: 'minmax(200px,1fr)', render: (k) => <><span className="mono">{k.name}</span> {k.current && <Badge tone="acc">Bu oturum</Badge>}</> },
    { label: 'Rol', width: '140px', render: (k) => roleLabel(k.role) },
    { label: 'Oluşturma tarihi', width: '170px', mono: true, render: (k) => dt(k.created_at) },
    { label: '', width: '110px', align: 'right', render: (k) => canEdit && !k.current && <button className="btn inline" onClick={() => revoke(k)}>İptal et</button> },
  ]
  return (
    <div className="page">
      <PageHead title="Ayarlar" desc="API anahtarları panele girişte ve entegrasyonlarda kullanılır. Anahtarın rolü, görülebilecek ve değiştirilebilecek şeyleri belirler.">
        {canEdit && <button className="btn primary" onClick={() => setAdding(true)}>Yeni anahtar</button>}
      </PageHead>
      <div className="card clip">
        <div className="sect" style={{ padding: '12px 16px', borderBottom: '1px solid var(--line)' }}>API anahtarları</div>
        <DataTable cols={cols} rows={q.data ?? []} minWidth={640} loading={q.isPending} error={q.error as any} onRetry={q.refetch} />
      </div>
      <RequireTwoStep />
      <ExchangeRates />
      <div className="card" style={{ padding: 16, display: 'flex', gap: 32, flexWrap: 'wrap' }}>
        <div className="sect" style={{ width: '100%' }}>Görünüm <span className="tx3" style={{ fontWeight: 400 }}>· yalnızca bu tarayıcı için</span></div>
        <Field label="Tema"><Seg value={theme} options={[{ value: 'light', label: 'Açık' }, { value: 'dark', label: 'Koyu' }]} onChange={(v) => { setTheme(v); setPref('tsw.theme', v) }} /></Field>
        <Field label="Tablo yoğunluğu"><Seg value={density} options={[{ value: 'md', label: 'Orta' }, { value: 'sm', label: 'Sıkı' }]} onChange={(v) => { setDensity(v); setPref('tsw.density', v) }} /></Field>
      </div>
      {adding && <FormModal title="Yeni API anahtarı" sub="Anahtar değeri yalnızca oluşturulduğu anda bir kez gösterilir." action="Oluştur" width={440} onClose={() => setAdding(false)}
        initial={{ role: 'readonly' }}
        fields={[{ key: 'name', label: 'Ad', type: 'mono', required: true, span: true, placeholder: 'ör. cdr-export' }, { key: 'role', label: 'Rol', type: 'seg', options: roles, span: true }]}
        onSubmit={async (v) => {
          const row = await api.post('/v1/api-keys', { name: String(v.name).trim(), role: v.role })
          invalidate('/v1/api-keys')
          setAdding(false)
          setCreated(row)
        }} />}
      {created && <KeyModal row={created} onClose={() => setCreated(null)} />}
    </div>
  )
}

interface AccountTwoStep { required: boolean; users_without: number }

/** The account-wide rule: every panel user must have two-step sign-in. */
function RequireTwoStep() {
  const { session, canEdit } = useSession()
  const q = useGet<AccountTwoStep>('/v1/account/two-step')
  // A person turning it on must have their own on; an API key has none to have.
  const own = useGet<{ enabled: boolean }>(session.user ? '/v1/me/two-step' : null)
  const invalidate = useInvalidate()
  const ui = useUI()
  const [busy, setBusy] = useState(false)
  const on = q.data?.required === true
  const without = q.data?.users_without ?? 0
  const ownOff = !!session.user && own.data?.enabled === false
  const change = async (next: boolean) => {
    if (next && !(await ui.confirm({
      title: 'İki adımlı doğrulama herkes için zorunlu olsun mu?',
      body: without > 0
        ? `${num(without)} kullanıcı henüz kurmadı. Bu kullanıcılar, açık oturumları dahil, kurulumu bitirene kadar yalnızca kurulum ekranını görür. API anahtarları etkilenmez.`
        : 'Bundan sonra ikinci adımı kurmamış her kullanıcı girişte önce kurulum ekranını görür. API anahtarları etkilenmez.',
      action: 'Zorunlu kıl',
    }))) return
    if (!next && !(await ui.confirm({
      title: 'Zorunluluk kaldırılsın mı?',
      body: 'Kullanıcılar iki adımlı doğrulamayı kendileri açıp kapatabilir. Açık olanların ayarı değişmez.',
      action: 'Zorunluluğu kaldır', danger: true,
    }))) return
    setBusy(true)
    try {
      await api.put('/v1/account/two-step', { required: next })
      invalidate('/v1/account/two-step', '/v1/audit-log')
      ui.toast(next ? 'İki adımlı doğrulama zorunlu' : 'Zorunluluk kaldırıldı')
    } catch (e) { ui.toast(errorText(e as any)) } finally { setBusy(false) }
  }
  return (
    <div className="card" style={{ padding: 16, display: 'grid', gap: 10 }}>
      <div className="row">
        <div className="sect grow">Güvenlik</div>
      </div>
      <div className="row" style={{ alignItems: 'flex-start', gap: 12 }}>
        <div className="grow" style={{ display: 'grid', gap: 4 }}>
          <div style={{ fontWeight: 500 }}>İki adımlı doğrulama zorunlu {q.data && (on ? <Badge tone="ok">Açık</Badge> : <Badge>Kapalı</Badge>)}</div>
          <div className="tx2 pretty">Açıkken ikinci adımı kurmamış panel kullanıcıları parolayla girdikten sonra yalnızca kurulum ekranına ulaşır. API anahtarları etkilenmez.</div>
          {q.error ? <div className="tx2">Durum yüklenemedi. <a onClick={() => q.refetch()}>Yeniden dene</a></div>
            : q.data && <div className={on && without > 0 ? 'warn' : 'tx2'}>
              {without > 0 ? `${num(without)} etkin kullanıcı ikinci adımı henüz kurmadı.` : 'Etkin kullanıcıların tümünde iki adımlı doğrulama açık.'}
            </div>}
          {canEdit && !on && ownOff && <div className="tx3">Zorunlu kılmadan önce kendi hesabınızda açın: Sistem › Kullanıcılar ve roller.</div>}
        </div>
        {q.data && <Toggle on={on} disabled={busy || !canEdit || (!on && ownOff)} onChange={canEdit ? change : undefined} />}
      </div>
    </div>
  )
}

function KeyModal({ row, onClose }: { row: Row; onClose: () => void }) {
  const [saved, setSaved] = useState(false)
  const ui = useUI()
  const copy = async () => {
    try { await navigator.clipboard.writeText(row.key); ui.toast('Anahtar panoya kopyalandı') } catch { ui.toast('Kopyalanamadı; anahtarı elle seçip kopyalayın') }
  }
  return (
    <Modal width={520}>
      <div><div className="modal-title">Anahtar oluşturuldu: {row.name}</div><div className="tx2" style={{ marginTop: 2 }}>Rol: {roleLabel(row.role)}</div></div>
      <div className="banner warn" style={{ borderColor: 'color-mix(in oklch, var(--warn) 40%, transparent)' }}>
        <span className="warn" style={{ fontWeight: 600 }}>Bu anahtar yalnızca şimdi gösterilir.</span> Pencere kapandıktan sonra değeri bir daha görüntülenemez; kaybederseniz yeni anahtar oluşturmanız gerekir.
      </div>
      <div className="row">
        <div className="grow mono" style={{ padding: '10px 12px', background: 'var(--panel2)', border: '1px solid var(--line)', borderRadius: 8, wordBreak: 'break-all' }}>{row.key}</div>
        <button className="btn" onClick={copy}>Kopyala</button>
      </div>
      <label className="radio"><input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} />Anahtarı güvenli bir yere kaydettim</label>
      <div className="modal-foot"><button className="btn primary" disabled={!saved} onClick={onClose}>Kapat</button></div>
    </Modal>
  )
}
