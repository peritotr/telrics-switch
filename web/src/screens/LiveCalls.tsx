// SPDX-License-Identifier: AGPL-3.0-only
import { useEffect, useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { api, useLookup, type ApiError, type Paged } from '../lib/api'
import { dt, num } from '../lib/format'
import { getLang, t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { Badge, DataTable, Pager, errorText, useUI, type Col } from '../ui/kit'

interface LiveCall {
  call_id: string; client_id: number; connection_id: number; src: string; src_ip: string; dst: string
  country: string; leg: number; start_at: string; ring_at: string | null; answer_at: string | null
}
interface Page {
  total: number; calls: LiveCall[]
  clients: Record<string, string>
  connections: Record<string, { name: string; vendor: string }>
}

const PAGE = 50
const country = (cc: string) => { try { return cc ? new Intl.DisplayNames([getLang()], { type: 'region' }).of(cc) ?? cc : '—' } catch { return cc } }

/** Elapsed time as m:ss or h:mm:ss. */
function clock(ms: number) {
  const s = Math.max(0, Math.floor(ms / 1000)), p = (n: number) => String(n).padStart(2, '0')
  return s >= 3600 ? `${Math.floor(s / 3600)}:${p(Math.floor(s / 60) % 60)}:${p(s % 60)}` : `${Math.floor(s / 60)}:${p(s % 60)}`
}

export function LiveCalls() {
  const ui = useUI()
  const { canEdit } = useSession()
  const clients = useLookup('/v1/clients')
  const [client, setClient] = useState('')
  const [page, setPage] = useState(0)
  const [paused, setPaused] = useState(false)
  const [now, setNow] = useState(Date.now())
  useEffect(() => { const t = setInterval(() => setNow(Date.now()), 1000); return () => clearInterval(t) }, [])

  const q = useQuery({
    queryKey: ['/v1/live/calls', client, page],
    queryFn: () => api.get<Page>('/v1/live/calls', { client_id: client, offset: page * PAGE, limit: PAGE }),
    enabled: !paused, refetchInterval: 2000, staleTime: 0, placeholderData: keepPreviousData,
  })
  const data = q.data
  const rows = data?.calls ?? []
  const total = data?.total ?? 0

  const hangup = async (c: LiveCall) => {
    const ok = await ui.confirm({
      title: t('live.hangupTitle'),
      body: t('live.hangupBody', { src: c.src || t('live.unknownCaller'), dst: c.dst }),
      action: t('live.hangup'), danger: true,
    })
    if (!ok) return
    try {
      await api.post('/v1/live/calls/hangup', { call_id: c.call_id })
      ui.toast(t('live.hungUp'))
      q.refetch()
    } catch (e) {
      const err = e as ApiError
      ui.toast(err.status === 404 ? t('live.alreadyEnded') : err.status === 409 ? t('live.notAnswered') : errorText(err))
      q.refetch()
    }
  }

  const cols: Col<LiveCall>[] = [
    { label: t('live.start'), width: '140px', mono: true, render: (c) => dt(c.start_at, true) },
    { label: t('live.state'), width: '96px', render: (c) => c.answer_at ? <Badge tone="ok">{t('live.talking')}</Badge> : c.ring_at ? <Badge tone="acc">{t('live.ringing')}</Badge> : <Badge>{t('live.setup')}</Badge> },
    { label: t('live.duration'), width: '56px', align: 'right', mono: true, render: (c) => clock(now - Date.parse(c.answer_at ?? c.start_at)) },
    { label: t('live.src'), width: '100px', mono: true, render: (c) => c.src || '—' },
    { label: t('live.dst'), width: '120px', mono: true, render: (c) => c.dst },
    { label: t('live.country'), width: '88px', render: (c) => country(c.country) },
    { label: t('live.client'), width: 'minmax(110px,1fr)', render: (c) => data?.clients[c.client_id] ?? `#${c.client_id}` },
    { label: t('live.vendorConnection'), width: 'minmax(130px,1fr)', render: (c) => {
      const v = data?.connections[c.connection_id]
      return v ? <>{v.vendor} · <span className="mono">{v.name}</span></> : `#${c.connection_id}`
    } },
    { label: t('live.attempt'), width: '50px', align: 'right', mono: true, render: (c) => c.leg },
    // The trace is read by Call-ID like any other: whoever sees this list
    // sees every client, so the trace API's rule lets them in (mayTrace).
    { label: '', width: '84px', align: 'right', render: (c) => (
      <span className="row" style={{ gap: 6, justifyContent: 'flex-end' }}>
        <Link className="btn inline" style={{ display: 'inline-flex', alignItems: 'center' }} title={t('live.traceHint')}
          to={`/tools/trace?call_id=${encodeURIComponent(c.call_id)}`}>{t('live.trace')}</Link>
        {canEdit && c.answer_at && <button className="btn inline danger" onClick={() => hangup(c)}>{t('live.end')}</button>}
      </span>
    ) },
  ]
  const pager = { rows, total, page, setPage, pageSize: PAGE, hasNext: (page + 1) * PAGE < total } as unknown as Paged

  return (
    <div className="page">
      <div className="row wrap" style={{ gap: 12 }}>
        <h1>{t('live.callsTitle')}</h1>
        <span className={'lv-pill' + (paused ? ' off' : '')}><i />{paused ? t('live.paused') : t('live.liveEvery', { n: 2 })}</span>
        <span className="lv-note">{data ? t('live.ongoing', { n: num(total) }) : ''}</span>
        <div className="grow" />
        <button className="btn" onClick={() => setPaused(!paused)}>{paused ? t('live.resume') : t('live.pause')}</button>
      </div>
      <div className="card clip">
        <div className="filters">
          <label className="field"><span className="label">{t('live.client')}</span>
            <select className="select" value={client} onChange={(e) => { setClient(e.target.value); setPage(0) }}>
              <option value="">{t('live.all')}</option>{clients.rows.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </select></label>
        </div>
        <DataTable cols={cols} rows={rows} minWidth={1100} gap={14} loading={q.isPending && !paused} error={q.error as ApiError | null}
          onRetry={q.refetch} errorTitle={t('live.callsError')}
          empty={{ title: t('live.callsEmpty'), desc: client ? t('live.callsEmptyClient') : t('live.callsEmptyDesc') }} />
        <Pager p={pager} noun={t('calls.noun')} />
      </div>
    </div>
  )
}
