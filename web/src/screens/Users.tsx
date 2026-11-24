// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useGet, useInvalidate, type Row } from '../lib/api'
import { dt } from '../lib/format'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, PageHead, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { TwoStepCard } from './TwoStep'

const roles = [{ value: 'admin', label: 'Yönetici' }, { value: 'readonly', label: 'Salt okunur' }]
const roleLabel = (r: string) => (r === 'admin' ? 'Yönetici' : 'Salt okunur')
const MIN = 10
const passwordHint = `En az ${MIN} karakter.`
const tooShort = (v: unknown) => (String(v ?? '').length < MIN ? `Parola en az ${MIN} karakter olmalı.` : undefined)
const identity: FieldDef[] = [
  { key: 'name', label: 'Ad', required: true },
  { key: 'email', label: 'E-posta', required: true },
  { key: 'role', label: 'Rol', type: 'seg', options: roles, span: true },
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
    if (off && !(await ui.confirm({ title: `${u.name} devre dışı bırakılsın mı?`, body: 'Kullanıcının açık oturumları hemen kapanır ve yeniden etkinleştirilene kadar giriş yapamaz.', action: 'Devre dışı bırak', danger: true }))) return
    act(() => api.patch(`/v1/users/${u.id}`, { status: off ? 'disabled' : 'active' }), off ? 'Kullanıcı devre dışı bırakıldı' : 'Kullanıcı etkinleştirildi')
  }
  const resetTwoStep = async (u: Row) => {
    if (!(await ui.confirm({ title: `${u.name} için iki adımlı doğrulama sıfırlansın mı?`, body: 'Kullanıcının doğrulama uygulaması kaydı ve yedek kodları silinir; bir sonraki girişte yalnızca parolası istenir. Telefonunu ve yedek kodlarını kaybeden kullanıcı için kullanın; kullanıcı sonradan yeniden etkinleştirebilir. İşlem denetim kaydına yazılır.', action: 'Sıfırla', danger: true }))) return
    act(() => api.post(`/v1/users/${u.id}/two-step/reset`), 'İki adımlı doğrulama sıfırlandı')
  }
  const remove = async (u: Row) => {
    if (!(await ui.confirm({ title: `${u.name} silinsin mi?`, body: 'Kullanıcının hesabı ve açık oturumları silinir. Bu işlem geri alınamaz.', action: 'Kullanıcıyı sil', danger: true }))) return
    act(() => api.del(`/v1/users/${u.id}`), 'Kullanıcı silindi')
  }

  const cols: Col<Row>[] = [
    { label: 'Ad', width: 'minmax(140px,1fr)', render: (u) => <>{u.name} {u.id === me && <Badge tone="acc">Siz</Badge>}</> },
    { label: 'E-posta', width: 'minmax(180px,1.2fr)', render: (u) => u.email },
    { label: 'Rol', width: '100px', render: (u) => roleLabel(u.role) },
    { label: 'Durum', width: '96px', render: (u) => u.status !== 'active' ? <Badge>Devre dışı</Badge> : u.locked ? <Badge tone="warn">Kilitli</Badge> : <Badge tone="ok">Aktif</Badge> },
    { label: 'İki adım', width: '76px', render: (u) => (u.two_step ? <Badge tone="ok">Açık</Badge> : <span className="tx3">Kapalı</span>) },
    { label: 'Son giriş', width: '140px', mono: true, render: (u) => (u.last_login_at ? dt(u.last_login_at) : '—') },
    { label: '', width: '330px', align: 'right', render: (u) => canEdit && <span className="row wrap" style={{ gap: 6, justifyContent: 'flex-end' }}>
      {u.locked && <button className="btn inline" onClick={() => act(() => api.patch(`/v1/users/${u.id}`, { unlock: true }), 'Kilit açıldı')}>Kilidi aç</button>}
      {u.two_step && u.id !== me && <button className="btn inline" onClick={() => resetTwoStep(u)}>İki adımı sıfırla</button>}
      <button className="btn inline" onClick={() => setEditing(u)}>Düzenle</button>
      <button className="btn inline" onClick={() => setResetting(u)}>Parola sıfırla</button>
      {u.id !== me && <button className="btn inline" onClick={() => toggle(u)}>{u.status === 'active' ? 'Devre dışı bırak' : 'Etkinleştir'}</button>}
      {u.id !== me && <button className="btn inline danger" onClick={() => remove(u)}>Sil</button>}
    </span> },
  ]
  return (
    <div className="page">
      <PageHead title="Kullanıcılar ve roller" desc="Panele e-posta adresi ve parolayla giren kişiler. Programlar için Ayarlar ekranındaki API anahtarları kullanılır.">
        {session.user && <button className="btn" onClick={() => setOwn(true)}>Parolamı değiştir</button>}
        {canEdit && <button className="btn primary" onClick={() => setAdding(true)}>Kullanıcı ekle</button>}
      </PageHead>
      <div className="card clip">
        <DataTable cols={cols} rows={q.data ?? []} minWidth={1100} loading={q.isPending} error={q.error as any} onRetry={q.refetch} errorTitle="Kullanıcılar yüklenemedi"
          empty={{ title: 'Henüz kullanıcı yok', desc: canEdit ? 'Şu an panele yalnızca API anahtarıyla giriliyor. İlk kullanıcıyı “Kullanıcı ekle” ile oluşturun.' : 'Bu hesapta tanımlı kullanıcı yok.' }} />
      </div>
      {session.user && <TwoStepCard />}
      <div className="card" style={{ padding: 16, display: 'grid', gap: 10 }}>
        <div className="sect">Roller</div>
        <div className="row" style={{ alignItems: 'baseline', gap: 12 }}><Badge tone="acc">Yönetici</Badge><span className="tx2">Her şeyi görür ve değiştirir: müşteri, sağlayıcı, tarife, yönlendirme, ödeme, kullanıcı ve API anahtarı; süren çağrıyı kapatabilir.</span></div>
        <div className="row" style={{ alignItems: 'baseline', gap: 12 }}><Badge>Salt okunur</Badge><span className="tx2">Her şeyi görür, hiçbir şeyi değiştiremez. Kendi parolasını değiştirebilir.</span></div>
      </div>

      {adding && <FormModal title="Kullanıcı ekle" sub="Parolayı kullanıcıya güvenli bir yoldan iletin; ilk girişten sonra kendisi değiştirebilir." action="Oluştur" width={520}
        initial={{ role: 'readonly' }} onClose={() => setAdding(false)}
        fields={[...identity, { key: 'password', label: 'Parola', type: 'password', required: true, span: true, hint: passwordHint }]}
        validate={(v) => ({ password: tooShort(v.password) })}
        onSubmit={async (v) => {
          await api.post('/v1/users', { name: String(v.name).trim(), email: String(v.email).trim(), role: v.role, password: v.password })
          setAdding(false); done('Kullanıcı oluşturuldu')
        }} />}
      {editing && <FormModal title={`${editing.name} düzenle`} action="Kaydet" width={520} initial={editing} onClose={() => setEditing(null)}
        fields={editing.id === me ? identity.slice(0, 2) : identity}
        onSubmit={async (v) => {
          await api.patch(`/v1/users/${editing.id}`, { name: String(v.name).trim(), email: String(v.email).trim(), ...(editing.id === me ? {} : { role: v.role }) })
          setEditing(null); done('Kullanıcı kaydedildi')
        }} />}
      {resetting && <FormModal title={`${resetting.name} için yeni parola`} sub="Kullanıcının açık oturumları kapanır; yeni parolayla yeniden girmesi gerekir." action="Parolayı değiştir" width={440}
        onClose={() => setResetting(null)} fields={[{ key: 'password', label: 'Yeni parola', type: 'password', required: true, span: true, hint: passwordHint }]}
        validate={(v) => ({ password: tooShort(v.password) })}
        onSubmit={async (v) => {
          await api.patch(`/v1/users/${resetting.id}`, { password: v.password })
          setResetting(null); done('Parola değiştirildi')
        }} />}
      {own && <FormModal title="Parolamı değiştir" sub="Diğer cihazlardaki oturumlarınız kapanır; bu oturum açık kalır." action="Parolayı değiştir" width={440} onClose={() => setOwn(false)}
        fields={[
          { key: 'current', label: 'Şimdiki parola', type: 'password', required: true, span: true },
          { key: 'next', label: 'Yeni parola', type: 'password', required: true, span: true, hint: passwordHint },
        ]}
        validate={(v) => ({ next: tooShort(v.next) })}
        onSubmit={async (v) => {
          try {
            await api.post('/v1/me/password', { current: v.current, new: v.next })
          } catch (e) {
            if ((e as { status?: number }).status === 403) throw new Error('Şimdiki parola hatalı.')
            throw e
          }
          setOwn(false); ui.toast('Parolanız değiştirildi')
        }} />}
    </div>
  )
}
