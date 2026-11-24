// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useLookup, usePaged, type Row } from '../lib/api'
import { dt, money } from '../lib/format'
import { Badge, DataTable, PageHead, Pager, Tabs, type Col } from '../ui/kit'

const tabs = [{ value: 'open', label: 'Açık' }, { value: 'cleared', label: 'Kapananlar' }]

export function BalanceAlerts() {
  const [state, setState] = useState('open')
  const clients = useLookup('/v1/clients')
  const list = usePaged('/v1/balance-alerts', { state }, 50)
  const client = (r: Row) => clients.byId.get(r.client_id)
  const cur = (r: Row) => client(r)?.currency ?? ''

  const cols: Col<Row>[] = [
    { label: 'Müşteri', width: 'minmax(200px,1.6fr)', render: (r) => <Link to={`/clients/${r.client_id}`}>{clients.name(r.client_id)}</Link> },
    { label: 'Eşik', width: '140px', align: 'right', mono: true, render: (r) => `${money(r.threshold)} ${cur(r)}` },
    { label: 'Uyarı anındaki bakiye', width: '170px', align: 'right', mono: true, render: (r) => <span className={r.balance < 0 ? 'bad' : undefined}>{money(r.balance)} {cur(r)}</span> },
    { label: 'Güncel bakiye', width: '150px', align: 'right', mono: true, render: (r) => {
      const c = client(r)
      return c ? <span className={c.balance < 0 ? 'bad' : undefined}>{money(c.balance)} {c.currency}</span> : '—'
    } },
    { label: 'Açıldı', width: '150px', mono: true, render: (r) => dt(r.raised_at, true) },
    state === 'open'
      ? { label: 'Durum', width: '110px', render: () => <Badge tone="warn">Açık</Badge> }
      : { label: 'Kapandı', width: '150px', mono: true, render: (r) => dt(r.cleared_at, true) },
  ]
  return (
    <div className="page">
      <PageHead title="Düşük bakiye uyarıları"
        desc="Bakiyesi, müşteri kaydında belirlenen eşiğe ya da altına inen müşteriler. Bakiye eşiğin üstüne çıkınca uyarı kendiliğinden kapanır." />
      <div className="card clip">
        <Tabs value={state} tabs={tabs} onChange={setState} />
        <DataTable cols={cols} rows={list.rows} minWidth={980} loading={list.loading} error={list.error} onRetry={list.refetch} errorTitle="Uyarılar yüklenemedi"
          empty={state === 'open'
            ? { title: 'Açık uyarı yok', desc: 'Eşiği tanımlı müşterilerin hepsinin bakiyesi eşiğin üstünde. Eşik, müşteri detayındaki Genel sekmesinden girilir.' }
            : { title: 'Kapanmış uyarı yok', desc: 'Bakiyesi eşiğin altına inip yeniden üstüne çıkan müşteri olmadı.' }} />
        <Pager p={list} noun="uyarı" />
      </div>
    </div>
  )
}
