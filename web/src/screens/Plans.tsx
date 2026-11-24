// SPDX-License-Identifier: AGPL-3.0-only
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, useGet, useInvalidate, useLookup, type Row } from '../lib/api'
import { useSession } from '../lib/session'
import { Badge, DataTable, Field, FormModal, PageHead, Seg, Stat, Tabs, Toggle, errorText, useUI, type Col } from '../ui/kit'
import { windowBody, windowErrors, windowFields, windowInitial, windowLabel } from '../lib/window'
import { ListPage, int, text } from './common'

const policies = [{ value: 'lcr', label: 'LCR' }, { value: 'priority', label: 'Öncelik' }]
const policyLabel = (p: string) => (p === 'lcr' ? 'LCR' : 'Öncelik')

export function Plans() {
  const navigate = useNavigate()
  const cols: Col<Row>[] = [
    { label: 'Ad', width: 'minmax(240px,1.6fr)', render: (r) => r.name },
    { label: 'Politika', width: '140px', render: (r) => policyLabel(r.policy) },
    { label: 'Zararına rota', width: '140px', render: (r) => (r.allow_loss ? <Badge tone="warn">İzinli</Badge> : <span className="tx2">Yasak</span>) },
  ]
  return <ListPage title="Yönlendirme planları" desc="Bir plan, hangi önekin hangi sağlayıcı bağlantılarına hangi sırayla gönderileceğini belirler."
    path="/v1/routing-plans" noun="plan" cols={cols} minWidth={620} onRow={(r) => navigate(`/plans/${r.id}`)}
    add={{
      label: 'Plan ekle', title: 'Yönlendirme planı ekle', initial: { policy: 'lcr' },
      fields: [
        { key: 'name', label: 'Ad', required: true, span: true },
        { key: 'policy', label: 'Politika', type: 'seg', options: policies },
        { key: 'allow_loss', label: 'Zararına rota', type: 'check', hint: 'Alış fiyatı satıştan yüksek rotalara izin ver' },
      ],
      build: (v) => ({ name: text(v.name), policy: v.policy, allow_loss: !!v.allow_loss }),
      done: (row) => navigate(`/plans/${row.id}`),
    }} />
}

export function PlanDetail() {
  const id = Number(useParams().id)
  const { canEdit } = useSession()
  const q = useGet<Row>(`/v1/routing-plans/${id}`)
  const entries = useGet<Row[]>('/v1/routing-entries', { routing_plan_id: id, limit: 1000 })
  const conns = useLookup('/v1/vendor-connections')
  const vendors = useLookup('/v1/vendors')
  const [tab, setTab] = useState('routes')
  const [adding, setAdding] = useState(false)
  const [editing, setEditing] = useState<Row | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()
  const p = q.data
  if (q.error) return <div className="page"><Link to="/plans" style={{ fontSize: 12 }}>← Yönlendirme planları</Link><div className="banner bad">Plan yüklenemedi: {errorText(q.error as any)}</div></div>
  if (!p) return <div className="tx3">Yükleniyor…</div>

  const rows = entries.data ?? []
  const prefixes = new Set(rows.map((r) => r.prefix))
  const patch = async (r: Row, body: Row) => {
    try {
      await api.patch(`/v1/routing-entries/${r.id}`, body)
      invalidate('/v1/routing-entries')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const remove = async (r: Row) => {
    if (!(await ui.confirm({ title: 'Rota satırı silinsin mi?', body: `Önek ${r.prefix || '(tümü)'} için ${conns.name(r.connection_id)} bağlantısı bu plandan çıkarılır. Bu işlem geri alınamaz.`, action: 'Sil', danger: true }))) return
    try {
      await api.del(`/v1/routing-entries/${r.id}`)
      invalidate('/v1/routing-entries')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  // The share of calls a route is tried first for, among the enabled routes
  // of its prefix and priority. Only known here when the order does not
  // depend on prices.
  const share = (r: Row) => {
    if (p.policy !== 'priority' || !r.enabled) return '—'
    const total = rows.filter((x) => x.enabled && x.prefix === r.prefix && x.priority === r.priority).reduce((n, x) => n + x.weight, 0)
    return total > 0 ? `%${Math.round((r.weight * 100) / total)}` : '—'
  }
  const cols: Col<Row>[] = [
    { label: 'Önek', width: '120px', mono: true, render: (r) => (rows.findIndex((x) => x.prefix === r.prefix) === rows.indexOf(r) ? <span style={{ fontWeight: 500 }}>{r.prefix || '(tümü)'}</span> : '') },
    { label: 'Sağlayıcı', width: 'minmax(160px,1fr)', render: (r) => vendors.name(conns.byId.get(r.connection_id)?.vendor_id) },
    { label: 'Bağlantı', width: 'minmax(140px,1fr)', mono: true, render: (r) => conns.name(r.connection_id) },
    { label: 'Öncelik', width: '80px', align: 'right', mono: true, render: (r) => r.priority },
    { label: 'Ağırlık', width: '80px', align: 'right', mono: true, render: (r) => r.weight },
    { label: 'Gün ve saat', width: '140px', render: (r) => windowLabel(r) || <span className="tx3">her zaman</span> },
    ...(p.policy === 'priority' ? [{ label: 'Pay', width: '70px', align: 'right', mono: true, render: share } as Col<Row>] : []),
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>Düzenle</button> },
    { label: '', width: '50px', align: 'right', render: (r) => <Toggle on={r.enabled} onChange={canEdit ? (v) => patch(r, { enabled: v }) : undefined} /> },
    { label: '', width: '50px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>Sil</button> },
  ]
  return (
    <div className="page">
      <PageHead back={<Link to="/plans" style={{ fontSize: 12 }}>← Yönlendirme planları</Link>}
        title={<><h1>{p.name}</h1><span className="mono tx3" style={{ fontSize: 12 }}>ID {p.id}</span></>} />
      <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(200px,1fr))' }}>
        <Stat text label="Politika" value={policyLabel(p.policy)} sub={p.policy === 'lcr' ? 'En ucuz alış fiyatı önce' : 'Tanımlı öncelik sırası'} />
        <Stat text label="Zararına rota" value={p.allow_loss ? 'İzinli' : 'Yasak'} tone={p.allow_loss ? 'warn' : undefined} sub={p.allow_loss ? 'Alış > satış olsa da kullanılır' : 'Alış > satış olan rota atlanır'} />
        <Stat label="Tanımlı önek" value={prefixes.size} sub={`${rows.length} rota satırı`} />
      </div>
      <div className="card clip">
        <Tabs value={tab} tabs={[{ value: 'routes', label: 'Rotalar' }, { value: 'genel', label: 'Genel' }]} onChange={setTab} />
        {tab === 'routes' && <>
          <div className="toolbar">
            <div className="grow tx2 pretty">LCR politikasında sıra alış fiyatına göre belirlenir; öncelik eşit fiyatta kullanılır. Ağırlık, aynı sıradaki (LCR'de fiyatı ve önceliği, öncelik politikasında önceliği eşit) bağlantılar arasında çağrıları oranlı paylaştırır; ağırlığı 0 olan yalnızca yedektir.</div>
            {canEdit && <button className="btn primary" onClick={() => setAdding(true)}>Rota ekle</button>}
          </div>
          <DataTable cols={cols} rows={rows} minWidth={1040} loading={entries.isPending} error={entries.error as any} onRetry={entries.refetch}
            empty={{ title: 'Rota yok', desc: 'Bu plana bağlı müşterilerin çağrıları, en az bir önek için rota eklenene kadar reddedilir.' }} />
        </>}
        {tab === 'genel' && <GeneralForm plan={p} />}
      </div>
      {adding && <FormModal title="Rota ekle" sub={p.name} action="Ekle" onClose={() => setAdding(false)} initial={{ priority: '10', weight: '1', ...windowInitial() }}
        fields={[
          { key: 'prefix', label: 'Önek', type: 'mono', placeholder: 'ör. 90', hint: 'Boş = tüm numaralar' },
          { key: 'connection_id', label: 'Sağlayıcı bağlantısı', type: 'select', required: true,
            options: conns.rows.map((c) => ({ value: String(c.id), label: `${vendors.name(c.vendor_id)} · ${c.name}` })) },
          { key: 'priority', label: 'Öncelik', type: 'int', hint: 'Küçük sayı önce denenir' },
          { key: 'weight', label: 'Ağırlık', type: 'int', hint: 'Aynı sıradakiler arasındaki pay; 0 = yalnızca yedek' },
          ...windowFields(p.timezone),
        ]}
        validate={(v) => ({ ...(/^\d*$/.test(v.prefix ?? '') ? {} : { prefix: 'Önek yalnızca rakam içerebilir.' }), ...windowErrors(v) })}
        onSubmit={async (v) => {
          await api.post('/v1/routing-entries', { routing_plan_id: id, prefix: v.prefix ?? '', connection_id: Number(v.connection_id), priority: int(v.priority, 10), weight: int(v.weight, 1), ...windowBody(v) })
          invalidate('/v1/routing-entries')
          setAdding(false)
          ui.toast('Rota eklendi')
        }} />}
      {editing && <FormModal title="Rotayı düzenle" sub={`${editing.prefix || '(tümü)'} · ${conns.name(editing.connection_id)}`} action="Kaydet" onClose={() => setEditing(null)}
        initial={{ priority: String(editing.priority), weight: String(editing.weight), ...windowInitial(editing) }}
        validate={windowErrors}
        fields={[
          { key: 'priority', label: 'Öncelik', type: 'int', hint: 'Küçük sayı önce denenir' },
          { key: 'weight', label: 'Ağırlık', type: 'int', hint: 'Aynı sıradakiler arasındaki pay; 0 = yalnızca yedek' },
          ...windowFields(p.timezone, editing.days),
        ]}
        onSubmit={async (v) => {
          await api.patch(`/v1/routing-entries/${editing.id}`, { priority: int(v.priority, editing.priority), weight: int(v.weight, editing.weight), ...windowBody(v) })
          invalidate('/v1/routing-entries')
          setEditing(null)
          ui.toast('Rota güncellendi')
        }} />}
    </div>
  )
}

function GeneralForm({ plan }: { plan: Row }) {
  const { canEdit } = useSession()
  const [v, setV] = useState<Row>(plan)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  useEffect(() => setV(plan), [plan])
  const save = async () => {
    if (!text(v.name)) return setError('Ad boş olamaz.')
    setBusy(true)
    setError('')
    try {
      await api.patch(`/v1/routing-plans/${plan.id}`, { name: text(v.name), policy: v.policy, allow_loss: !!v.allow_loss, timezone: text(v.timezone) ?? 'UTC' })
      invalidate('/v1/routing-plans')
      ui.toast('Plan kaydedildi')
    } catch (e) { setError(errorText(e as any)) }
    setBusy(false)
  }
  return (
    <>
      <fieldset disabled={!canEdit}><div className="form-col" style={{ padding: 20, maxWidth: 420 }}>
        <Field label="Ad"><input className="input" value={v.name} onChange={(e) => setV({ ...v, name: e.target.value })} /></Field>
        <Field label="Politika"><Seg value={v.policy} options={policies} onChange={(x) => setV({ ...v, policy: x })} /></Field>
        <Field label="Saat dilimi" hint="Güne ve saate göre rotalar bu saat dilimiyle okunur (IANA adı, ör. Europe/Istanbul)."><input className="input mono" value={v.timezone ?? ''} onChange={(e) => setV({ ...v, timezone: e.target.value })} /></Field>
        <label className="radio"><input type="checkbox" checked={!!v.allow_loss} onChange={(e) => setV({ ...v, allow_loss: e.target.checked })} />Zararına rotaya izin ver</label>
      </div></fieldset>
      {error && <div className="banner bad" style={{ margin: '0 20px 12px' }}>{error}</div>}
      {canEdit && <div className="form-foot">
        <button className="btn" onClick={() => { setV(plan); setError('') }}>Vazgeç</button>
        <button className="btn primary" disabled={busy} onClick={save}>Kaydet</button>
      </div>}
    </>
  )
}
