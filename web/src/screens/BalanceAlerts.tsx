// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useLookup, usePaged, type Row } from '../lib/api'
import { dt, money } from '../lib/format'
import { t } from '../lib/i18n'
import { Badge, DataTable, PageHead, Pager, Tabs, type Col } from '../ui/kit'

const tabs = () => [{ value: 'open', label: t('alerts.open') }, { value: 'cleared', label: t('alerts.cleared') }]

export function BalanceAlerts() {
  const [state, setState] = useState('open')
  const clients = useLookup('/v1/clients')
  const list = usePaged('/v1/balance-alerts', { state }, 50)
  const client = (r: Row) => clients.byId.get(r.client_id)
  const cur = (r: Row) => client(r)?.currency ?? ''

  const cols: Col<Row>[] = [
    { label: t('alerts.client'), width: 'minmax(200px,1.6fr)', render: (r) => <Link to={`/clients/${r.client_id}`}>{clients.name(r.client_id)}</Link> },
    { label: t('alerts.threshold'), width: '140px', align: 'right', mono: true, render: (r) => `${money(r.threshold)} ${cur(r)}` },
    { label: t('alerts.balanceAtAlert'), width: '170px', align: 'right', mono: true, render: (r) => <span className={r.balance < 0 ? 'bad' : undefined}>{money(r.balance)} {cur(r)}</span> },
    { label: t('alerts.balanceNow'), width: '150px', align: 'right', mono: true, render: (r) => {
      const c = client(r)
      return c ? <span className={c.balance < 0 ? 'bad' : undefined}>{money(c.balance)} {c.currency}</span> : '—'
    } },
    { label: t('alerts.raised'), width: '150px', mono: true, render: (r) => dt(r.raised_at, true) },
    state === 'open'
      ? { label: t('alerts.status'), width: '110px', render: () => <Badge tone="warn">{t('alerts.open')}</Badge> }
      : { label: t('alerts.clearedAt'), width: '150px', mono: true, render: (r) => dt(r.cleared_at, true) },
  ]
  return (
    <div className="page">
      <PageHead title={t('alerts.title')} desc={t('alerts.desc')} />
      <div className="card clip">
        <Tabs value={state} tabs={tabs()} onChange={setState} />
        <DataTable cols={cols} rows={list.rows} minWidth={980} loading={list.loading} error={list.error} onRetry={list.refetch} errorTitle={t('alerts.loadError')}
          empty={state === 'open'
            ? { title: t('alerts.noOpen'), desc: t('alerts.noOpenDesc') }
            : { title: t('alerts.noCleared'), desc: t('alerts.noClearedDesc') }} />
        <Pager p={list} noun={t('alerts.noun')} />
      </div>
    </div>
  )
}
