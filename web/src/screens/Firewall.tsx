// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { api, useInvalidate, usePaged, type ApiError, type Row } from '../lib/api'
import { dt, fromLocalInput, toLocalInput } from '../lib/format'
import { t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, PageHead, Pager, Search, Toggle, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { text } from './common'

const actions = () => [{ value: 'deny', label: t('firewall.deny') }, { value: 'allow', label: t('firewall.allow') }]
// One address or a network, IPv4 or IPv6; the server has the last word.
const address = /^[0-9a-fA-F:.]+(\/\d{1,3})?$/

interface Ban { ip: string; reason: string; since: string; until: string; count: number }

function remaining(until: string) {
  const sec = Math.max(0, Math.round((Date.parse(until) - Date.now()) / 1000))
  return sec >= 60 ? t('firewall.remaining', { m: Math.floor(sec / 60), s: sec % 60 }) : t('firewall.remainingSec', { s: sec })
}

export function Firewall() {
  const { canEdit } = useSession()
  const [q, setQ] = useState('')
  const rules = usePaged('/v1/firewall-rules', { q })
  const bans = useQuery({ queryKey: ['/v1/firewall/bans'], queryFn: () => api.get<{ bans: Ban[] }>('/v1/firewall/bans'), refetchInterval: 5000, staleTime: 0 })
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const [preset, setPreset] = useState<Row>({})
  const ui = useUI()
  const invalidate = useInvalidate()
  const row = editing === 'new' ? null : editing

  const patch = async (r: Row, body: Row) => {
    try {
      await api.patch(`/v1/firewall-rules/${r.id}`, body)
      invalidate('/v1/firewall-rules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const remove = async (r: Row) => {
    if (!(await ui.confirm({ title: t('firewall.deleteTitle'), body: t('firewall.deleteBody', { cidr: r.cidr, action: r.action === 'deny' ? t('firewall.deny') : t('firewall.allow') }), action: t('action.delete'), danger: true }))) return
    try {
      await api.del(`/v1/firewall-rules/${r.id}`)
      invalidate('/v1/firewall-rules')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const unban = async (b: Ban) => {
    try {
      await api.post('/v1/firewall/unban', { ip: b.ip })
      invalidate('/v1/firewall/bans')
      ui.toast(t('firewall.unbanned', { ip: b.ip }))
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const rule = (b: Ban, action: string) => { setPreset({ cidr: b.ip, action, note: action === 'deny' ? t('firewall.floodNote') : '' }); setEditing('new') }

  const expired = (r: Row) => r.expires_at && Date.parse(r.expires_at) <= Date.now()
  const ruleCols: Col<Row>[] = [
    { label: t('firewall.cidr'), width: 'minmax(180px,1fr)', mono: true, render: (r) => r.cidr },
    { label: t('firewall.action'), width: '110px', render: (r) => (r.action === 'deny' ? <Badge tone="bad">{t('firewall.deny')}</Badge> : <Badge tone="ok">{t('firewall.allow')}</Badge>) },
    { label: t('firewall.note'), width: 'minmax(160px,1.4fr)', render: (r) => <span className="tx2">{r.note}</span> },
    { label: t('firewall.expires'), width: '170px', mono: true, render: (r) => (r.expires_at ? <span className={expired(r) ? 'tx3' : undefined}>{dt(r.expires_at)}{expired(r) ? t('firewall.expired') : ''}</span> : <span className="tx3">{t('firewall.forever')}</span>) },
    { label: '', width: '70px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => { setPreset({}); setEditing(r) }}>{t('action.edit')}</button> },
    { label: '', width: '50px', align: 'right', render: (r) => <Toggle on={r.enabled} onChange={canEdit ? (v) => patch(r, { enabled: v }) : undefined} /> },
    { label: '', width: '64px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>{t('action.delete')}</button> },
  ]
  const banCols: Col<Row>[] = [
    { label: t('firewall.address'), width: 'minmax(160px,1fr)', mono: true, render: (b) => b.ip },
    { label: t('firewall.reason'), width: '140px', render: (b) => (b.reason === 'flood' ? t('firewall.flood') : b.reason) },
    { label: t('firewall.since'), width: '170px', mono: true, render: (b) => dt(b.since) },
    { label: t('firewall.left'), width: '110px', align: 'right', mono: true, render: (b) => remaining(b.until) },
    { label: t('firewall.count'), width: '70px', align: 'right', mono: true, render: (b) => b.count },
    { label: '', width: '120px', align: 'right', render: (b) => canEdit && <button className="btn inline" onClick={() => unban(b as Ban)}>{t('firewall.unban')}</button> },
    { label: '', width: '130px', align: 'right', render: (b) => canEdit && <button className="btn inline" onClick={() => rule(b as Ban, 'deny')}>{t('firewall.denyForever')}</button> },
    { label: '', width: '90px', align: 'right', render: (b) => canEdit && <button className="btn inline" onClick={() => rule(b as Ban, 'allow')}>{t('firewall.allow')}</button> },
  ]
  const fields: FieldDef[] = [
    { key: 'cidr', label: t('firewall.cidr'), type: 'mono', required: true, span: true, placeholder: t('firewall.cidrPlaceholder') },
    { key: 'action', label: t('firewall.action'), type: 'seg', options: actions(), span: true },
    { key: 'expires_at', label: t('firewall.expiresUtc'), type: 'datetime', hint: t('firewall.foreverHint') },
    { key: 'note', label: t('firewall.note'), span: true },
  ]
  return (
    <div className="page">
      <PageHead title={t('firewall.title')} desc={t('firewall.desc')}>
        {canEdit && <button className="btn primary" onClick={() => { setPreset({}); setEditing('new') }}>{t('firewall.add')}</button>}
      </PageHead>
      <div className="card clip">
        <div className="toolbar">
          <Search value={q} onChange={setQ} />
          <div className="grow tx2 pretty">{t('firewall.help')}</div>
        </div>
        <DataTable cols={ruleCols} rows={rules.rows} minWidth={860} loading={rules.loading} error={rules.error} onRetry={rules.refetch}
          empty={q ? { title: t('firewall.notFound'), desc: t('firewall.notFoundDesc'), action: <button className="btn" onClick={() => setQ('')}>{t('firewall.clearSearch')}</button> }
            : { title: t('firewall.none'), desc: t('firewall.noneDesc') }} />
        <Pager p={rules} noun={t('firewall.noun')} />
      </div>
      <div className="card clip">
        <div className="toolbar">
          <div className="grow"><span className="sect">{t('firewall.bans')}</span> <span className="tx2 pretty">{t('firewall.bansHelp')}</span></div>
        </div>
        {bans.error
          ? <div className="banner bad" style={{ margin: 16 }}>{t('firewall.bansFailed', { error: errorText(bans.error as ApiError) })}</div>
          : <DataTable cols={banCols} rows={(bans.data?.bans ?? []) as unknown as Row[]} minWidth={980} loading={bans.isPending}
              empty={{ title: t('firewall.noBans'), desc: t('firewall.noBansDesc') }} />}
      </div>
      {editing && <FormModal title={row ? t('firewall.edit') : t('firewall.add')} action={row ? t('action.save') : t('action.add')} onClose={() => setEditing(null)} fields={fields}
        initial={row ? { cidr: row.cidr, action: row.action, note: row.note, expires_at: toLocalInput(row.expires_at) } : { action: 'deny', ...preset }}
        validate={(v) => (address.test(String(v.cidr ?? '').trim()) ? {} : { cidr: t('firewall.errCidr') })}
        onSubmit={async (v) => {
          const body = { cidr: String(v.cidr).trim(), action: v.action, note: text(v.note) ?? '', expires_at: fromLocalInput(v.expires_at ?? '') }
          if (row) await api.patch(`/v1/firewall-rules/${row.id}`, body)
          else await api.post('/v1/firewall-rules', body)
          invalidate('/v1/firewall-rules')
          setEditing(null)
          ui.toast(row ? t('firewall.updated') : t('firewall.added'))
        }} />}
    </div>
  )
}
