// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useGet, useInvalidate, type Row } from '../lib/api'
import { dt, num } from '../lib/format'
import { languages, t, useLang } from '../lib/i18n'
import { useSession } from '../lib/session'
import { ExchangeRates } from './ExchangeRates'
import { Badge, DataTable, Field, FormModal, Modal, PageHead, Seg, Toggle, errorText, useUI, type Col } from '../ui/kit'

const roles = () => [{ value: 'admin', label: t('role.admin') }, { value: 'readonly', label: t('role.readonly') }]
const roleLabel = (r: string) => (r === 'admin' ? t('role.admin') : t('role.readonly'))

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
  const { lang, setLang } = useLang()
  const ui = useUI()
  const invalidate = useInvalidate()
  const revoke = async (k: Row) => {
    if (!(await ui.confirm({ title: t('settings.revokeTitle', { name: k.name }), body: t('settings.revokeBody'), action: t('settings.revokeAction'), danger: true }))) return
    try {
      await api.del(`/v1/api-keys/${k.id}`)
      invalidate('/v1/api-keys')
      ui.toast(t('settings.revokedToast'))
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const cols: Col<Row>[] = [
    { label: t('settings.name'), width: 'minmax(200px,1fr)', render: (k) => <><span className="mono">{k.name}</span> {k.current && <Badge tone="acc">{t('settings.thisSession')}</Badge>}</> },
    { label: t('settings.role'), width: '140px', render: (k) => roleLabel(k.role) },
    { label: t('settings.created'), width: '170px', mono: true, render: (k) => dt(k.created_at) },
    { label: '', width: '110px', align: 'right', render: (k) => canEdit && !k.current && <button className="btn inline" onClick={() => revoke(k)}>{t('settings.revoke')}</button> },
  ]
  return (
    <div className="page">
      <PageHead title={t('settings.title')} desc={t('settings.desc')}>
        {canEdit && <button className="btn primary" onClick={() => setAdding(true)}>{t('settings.newKey')}</button>}
      </PageHead>
      <div className="card clip">
        <div className="sect" style={{ padding: '12px 16px', borderBottom: '1px solid var(--line)' }}>{t('settings.apiKeys')}</div>
        <DataTable cols={cols} rows={q.data ?? []} minWidth={640} loading={q.isPending} error={q.error as any} onRetry={q.refetch} />
      </div>
      <RequireTwoStep />
      <ExchangeRates />
      <div className="card" style={{ padding: 16, display: 'flex', gap: 32, flexWrap: 'wrap' }}>
        <div className="sect" style={{ width: '100%' }}>{t('settings.appearance')} <span className="tx3" style={{ fontWeight: 400 }}>{t('settings.thisBrowser')}</span></div>
        <Field label={t('lang.label')}><Seg value={lang} options={languages.map((l) => ({ value: l.value, label: l.name }))} onChange={(v) => setLang(v as typeof lang)} /></Field>
        <Field label={t('settings.theme')}><Seg value={theme} options={[{ value: 'light', label: t('settings.light') }, { value: 'dark', label: t('settings.dark') }]} onChange={(v) => { setTheme(v); setPref('tsw.theme', v) }} /></Field>
        <Field label={t('settings.density')}><Seg value={density} options={[{ value: 'md', label: t('settings.densityMd') }, { value: 'sm', label: t('settings.densitySm') }]} onChange={(v) => { setDensity(v); setPref('tsw.density', v) }} /></Field>
      </div>
      {adding && <FormModal title={t('settings.newKeyTitle')} sub={t('settings.newKeySub')} action={t('action.create')} width={440} onClose={() => setAdding(false)}
        initial={{ role: 'readonly' }}
        fields={[{ key: 'name', label: t('settings.name'), type: 'mono', required: true, span: true, placeholder: t('settings.namePlaceholder') }, { key: 'role', label: t('settings.role'), type: 'seg', options: roles(), span: true }]}
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
      title: t('settings.requireTitle'),
      body: without > 0 ? t('settings.requireBodyWithout', { n: num(without) }) : t('settings.requireBody'),
      action: t('settings.requireAction'),
    }))) return
    if (!next && !(await ui.confirm({
      title: t('settings.unrequireTitle'),
      body: t('settings.unrequireBody'),
      action: t('settings.unrequireAction'), danger: true,
    }))) return
    setBusy(true)
    try {
      await api.put('/v1/account/two-step', { required: next })
      invalidate('/v1/account/two-step', '/v1/audit-log')
      ui.toast(next ? t('settings.requiredToast') : t('settings.unrequiredToast'))
    } catch (e) { ui.toast(errorText(e as any)) } finally { setBusy(false) }
  }
  return (
    <div className="card" style={{ padding: 16, display: 'grid', gap: 10 }}>
      <div className="row">
        <div className="sect grow">{t('settings.security')}</div>
      </div>
      <div className="row" style={{ alignItems: 'flex-start', gap: 12 }}>
        <div className="grow" style={{ display: 'grid', gap: 4 }}>
          <div style={{ fontWeight: 500 }}>{t('settings.requireLabel')} {q.data && (on ? <Badge tone="ok">{t('common.on')}</Badge> : <Badge>{t('common.off')}</Badge>)}</div>
          <div className="tx2 pretty">{t('settings.requireDesc')}</div>
          {q.error ? <div className="tx2">{t('settings.statusFailed')} <a onClick={() => q.refetch()}>{t('action.retry')}</a></div>
            : q.data && <div className={on && without > 0 ? 'warn' : 'tx2'}>
              {without > 0 ? t('settings.usersWithout', { n: num(without) }) : t('settings.allHaveIt')}
            </div>}
          {canEdit && !on && ownOff && <div className="tx3">{t('settings.ownFirst')}</div>}
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
    try { await navigator.clipboard.writeText(row.key); ui.toast(t('settings.keyCopied')) } catch { ui.toast(t('settings.keyCopyFailed')) }
  }
  return (
    <Modal width={520}>
      <div><div className="modal-title">{t('settings.keyCreated', { name: row.name })}</div><div className="tx2" style={{ marginTop: 2 }}>{t('settings.keyRole', { role: roleLabel(row.role) })}</div></div>
      <div className="banner warn" style={{ borderColor: 'color-mix(in oklch, var(--warn) 40%, transparent)' }}>
        <span className="warn" style={{ fontWeight: 600 }}>{t('settings.keyOnce')}</span> {t('settings.keyOnceDesc')}
      </div>
      <div className="row">
        <div className="grow mono" style={{ padding: '10px 12px', background: 'var(--panel2)', border: '1px solid var(--line)', borderRadius: 8, wordBreak: 'break-all' }}>{row.key}</div>
        <button className="btn" onClick={copy}>{t('action.copy')}</button>
      </div>
      <label className="radio"><input type="checkbox" checked={saved} onChange={(e) => setSaved(e.target.checked)} />{t('settings.keySaved')}</label>
      <div className="modal-foot"><button className="btn primary" disabled={!saved} onClick={onClose}>{t('action.close')}</button></div>
    </Modal>
  )
}
