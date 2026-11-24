// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useGet, useInvalidate, type Row } from '../lib/api'
import { dt } from '../lib/format'
import { t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, PageHead, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { TwoStepCard } from './TwoStep'

const roles = () => [{ value: 'admin', label: t('role.admin') }, { value: 'readonly', label: t('role.readonly') }]
const roleLabel = (r: string) => (r === 'admin' ? t('role.admin') : t('role.readonly'))
const MIN = 10
const passwordHint = () => t('users.passwordHint', { min: MIN })
const tooShort = (v: unknown) => (String(v ?? '').length < MIN ? t('users.tooShort', { min: MIN }) : undefined)
const identity = (): FieldDef[] => [
  { key: 'name', label: t('users.name'), required: true },
  { key: 'email', label: t('users.email'), required: true },
  { key: 'role', label: t('users.role'), type: 'seg', options: roles(), span: true },
]

export function Users() {
  const { canEdit, session } = useSession()
  const q = useGet<Row[]>('/v1/users')
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<Row | null>(null)
  const [resetting, setResetting] = useState<Row | null>(null)
  const [own, setOwn] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  const me = session.user?.id
  const done = (text: string) => { invalidate('/v1/users'); ui.toast(text) }
  const act = async (f: () => Promise<unknown>, text: string) => {
    try { await f(); done(text) } catch (e) { ui.toast(errorText(e as any)) }
  }
  const toggle = async (u: Row) => {
    const off = u.status === 'active'
    if (off && !(await ui.confirm({ title: t('users.disableTitle', { name: u.name }), body: t('users.disableBody'), action: t('users.disable'), danger: true }))) return
    act(() => api.patch(`/v1/users/${u.id}`, { status: off ? 'disabled' : 'active' }), off ? t('users.disabledToast') : t('users.enabledToast'))
  }
  const resetTwoStep = async (u: Row) => {
    if (!(await ui.confirm({ title: t('users.resetTwoStepTitle', { name: u.name }), body: t('users.resetTwoStepBody'), action: t('users.reset'), danger: true }))) return
    act(() => api.post(`/v1/users/${u.id}/two-step/reset`), t('users.twoStepResetToast'))
  }
  const remove = async (u: Row) => {
    if (!(await ui.confirm({ title: t('users.deleteTitle', { name: u.name }), body: t('users.deleteBody'), action: t('users.deleteAction'), danger: true }))) return
    act(() => api.del(`/v1/users/${u.id}`), t('users.deletedToast'))
  }

  const cols: Col<Row>[] = [
    { label: t('users.name'), width: 'minmax(140px,1fr)', render: (u) => <>{u.name} {u.id === me && <Badge tone="acc">{t('users.you')}</Badge>}</> },
    { label: t('users.email'), width: 'minmax(180px,1.2fr)', render: (u) => u.email },
    { label: t('users.role'), width: '100px', render: (u) => roleLabel(u.role) },
    { label: t('users.status'), width: '96px', render: (u) => u.status !== 'active' ? <Badge>{t('users.disabled')}</Badge> : u.locked ? <Badge tone="warn">{t('users.locked')}</Badge> : <Badge tone="ok">{t('users.active')}</Badge> },
    { label: t('users.twoStep'), width: '76px', render: (u) => (u.two_step ? <Badge tone="ok">{t('common.on')}</Badge> : <span className="tx3">{t('common.off')}</span>) },
    { label: t('users.lastLogin'), width: '140px', mono: true, render: (u) => (u.last_login_at ? dt(u.last_login_at) : '—') },
    { label: '', width: '330px', align: 'right', render: (u) => canEdit && <span className="row wrap" style={{ gap: 6, justifyContent: 'flex-end' }}>
      {u.locked && <button className="btn inline" onClick={() => act(() => api.patch(`/v1/users/${u.id}`, { unlock: true }), t('users.unlockedToast'))}>{t('users.unlock')}</button>}
      {u.two_step && u.id !== me && <button className="btn inline" onClick={() => resetTwoStep(u)}>{t('users.resetTwoStep')}</button>}
      <button className="btn inline" onClick={() => setEditing(u)}>{t('action.edit')}</button>
      <button className="btn inline" onClick={() => setResetting(u)}>{t('users.resetPassword')}</button>
      {u.id !== me && <button className="btn inline" onClick={() => toggle(u)}>{u.status === 'active' ? t('users.disable') : t('users.enable')}</button>}
      {u.id !== me && <button className="btn inline danger" onClick={() => remove(u)}>{t('action.delete')}</button>}
    </span> },
  ]
  return (
    <div className="page">
      <PageHead title={t('users.title')} desc={t('users.desc')}>
        {session.user && <button className="btn" onClick={() => setOwn(true)}>{t('users.changeOwn')}</button>}
        {canEdit && <button className="btn primary" onClick={() => setAdding(true)}>{t('users.add')}</button>}
      </PageHead>
      <div className="card clip">
        <DataTable cols={cols} rows={q.data ?? []} minWidth={1100} loading={q.isPending} error={q.error as any} onRetry={q.refetch} errorTitle={t('users.loadError')}
          empty={{ title: t('users.empty'), desc: canEdit ? t('users.emptyAdmin') : t('users.emptyReadonly') }} />
      </div>
      {session.user && <TwoStepCard />}
      <div className="card" style={{ padding: 16, display: 'grid', gap: 10 }}>
        <div className="sect">{t('users.roles')}</div>
        <div className="row" style={{ alignItems: 'baseline', gap: 12 }}><Badge tone="acc">{t('role.admin')}</Badge><span className="tx2">{t('users.adminDesc')}</span></div>
        <div className="row" style={{ alignItems: 'baseline', gap: 12 }}><Badge>{t('role.readonly')}</Badge><span className="tx2">{t('users.readonlyDesc')}</span></div>
      </div>

      {adding && <FormModal title={t('users.add')} sub={t('users.addSub')} action={t('action.create')} width={520}
        initial={{ role: 'readonly' }} onClose={() => setAdding(false)}
        fields={[...identity(), { key: 'password', label: t('users.password'), type: 'password', required: true, span: true, hint: passwordHint() }]}
        validate={(v) => ({ password: tooShort(v.password) })}
        onSubmit={async (v) => {
          await api.post('/v1/users', { name: String(v.name).trim(), email: String(v.email).trim(), role: v.role, password: v.password })
          setAdding(false); done(t('users.createdToast'))
        }} />}
      {editing && <FormModal title={t('users.editTitle', { name: editing.name })} action={t('action.save')} width={520} initial={editing} onClose={() => setEditing(null)}
        fields={editing.id === me ? identity().slice(0, 2) : identity()}
        onSubmit={async (v) => {
          await api.patch(`/v1/users/${editing.id}`, { name: String(v.name).trim(), email: String(v.email).trim(), ...(editing.id === me ? {} : { role: v.role }) })
          setEditing(null); done(t('users.savedToast'))
        }} />}
      {resetting && <FormModal title={t('users.newPasswordFor', { name: resetting.name })} sub={t('users.resetSub')} action={t('users.changePassword')} width={440}
        onClose={() => setResetting(null)} fields={[{ key: 'password', label: t('users.newPassword'), type: 'password', required: true, span: true, hint: passwordHint() }]}
        validate={(v) => ({ password: tooShort(v.password) })}
        onSubmit={async (v) => {
          await api.patch(`/v1/users/${resetting.id}`, { password: v.password })
          setResetting(null); done(t('users.passwordChangedToast'))
        }} />}
      {own && <FormModal title={t('users.changeOwn')} sub={t('users.ownSub')} action={t('users.changePassword')} width={440} onClose={() => setOwn(false)}
        fields={[
          { key: 'current', label: t('users.currentPassword'), type: 'password', required: true, span: true },
          { key: 'next', label: t('users.newPassword'), type: 'password', required: true, span: true, hint: passwordHint() },
        ]}
        validate={(v) => ({ next: tooShort(v.next) })}
        onSubmit={async (v) => {
          try {
            await api.post('/v1/me/password', { current: v.current, new: v.next })
          } catch (e) {
            if ((e as { status?: number }).status === 403) throw new Error(t('users.wrongCurrent'))
            throw e
          }
          setOwn(false); ui.toast(t('users.ownChangedToast'))
        }} />}
    </div>
  )
}
