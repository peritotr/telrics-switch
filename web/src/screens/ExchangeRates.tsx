// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useGet, useInvalidate, type Row } from '../lib/api'
import { day, dt, money, moneyInput, parseMoney } from '../lib/format'
import { t } from '../lib/i18n'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, errorText, useUI, type Col } from '../ui/kit'

/** The currency the account keeps its books in. */
export function useBaseCurrency(): string {
  const q = useGet<Row[]>('/v1/resellers')
  return q.data?.[0]?.currency ?? 'USD'
}

/** What GET /v1/exchange-rate-feed says: the published rates rates may follow (T-20). */
interface Feed {
  enabled: boolean
  source: string
  published: string | null
  fetched_at: string | null
  failed_at: string | null
  error: string
  missing: string[]
  currencies: string[]
}

/** Rates of exchange into the account's currency; a card of the settings page. */
export function ExchangeRates() {
  const { canEdit } = useSession()
  const base = useBaseCurrency()
  const q = useGet<Row[]>('/v1/exchange-rates')
  const feedQ = useGet<Feed>('/v1/exchange-rate-feed')
  const feed = feedQ.data?.enabled ? feedQ.data : null
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const [fetching, setFetching] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  const row = editing === 'new' ? null : editing
  const refresh = () => { invalidate('/v1/exchange-rates'); invalidate('/v1/exchange-rate-feed') }
  const remove = async (r: Row) => {
    if (!(await ui.confirm({ title: t('rates.deleteTitle', { currency: r.currency }), body: t('rates.deleteBody', { currency: r.currency }), action: t('action.delete'), danger: true }))) return
    try {
      await api.del(`/v1/exchange-rates/${r.id}`)
      invalidate('/v1/exchange-rates')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const fetchNow = async () => {
    setFetching(true)
    try {
      const res = await api.post('/v1/exchange-rate-feed/fetch')
      ui.toast(res.changed ? t('rates.fetchedChanged', { date: day(res.published), n: res.changed }) : t('rates.fetchedSame', { date: day(res.published) }))
    } catch (e) { ui.toast(errorText(e as any)) }
    setFetching(false)
    refresh()
  }
  const missing = (r: Row) => feed?.missing.includes(`${r.currency}/${base}`)
  const cols: Col<Row>[] = [
    { label: t('rates.currency'), width: '120px', mono: true, render: (r) => r.currency },
    { label: t('rates.perUnit', { base }), width: 'minmax(180px,1fr)', mono: true, render: (r) => money(r.rate, 6) },
    {
      label: t('rates.source'), width: '190px', render: (r) => r.source === 'ecb'
        ? <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}><Badge tone="acc">ECB</Badge>
          {missing(r) ? <Badge tone="warn">{t('rates.notPublished')}</Badge> : <span className="tx2 mono">{r.published ? day(r.published) : '—'}</span>}</span>
        : <span className="tx2">{t('rates.manual')}</span>,
    },
    { label: t('rates.updated'), width: '170px', mono: true, render: (r) => dt(r.updated_at) },
    { label: '', width: '80px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>{t('action.edit')}</button> },
    { label: '', width: '64px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>{t('action.delete')}</button> },
  ]
  const offered = (feed?.currencies ?? []).filter((c) => c !== base)
  return (
    <div className="card clip">
      <div className="toolbar">
        <div className="grow"><span className="sect">{t('rates.title')}</span> <span className="tx2 pretty">{t('rates.intro', { base })}{feed ? t('rates.introEcb') : t('rates.introManual')}</span></div>
        {canEdit && feed && <button className="btn" disabled={fetching} onClick={fetchNow}>{fetching ? t('rates.fetching') : t('rates.fetchNow')}</button>}
        {canEdit && <button className="btn" onClick={() => setEditing('new')}>{t('rates.add')}</button>}
      </div>
      {feed && feed.error && <div className="banner warn" style={{ margin: '0 16px 12px' }}>
        {t('rates.fetchFailed', { at: feed.failed_at ? ` (${dt(feed.failed_at)})` : '', error: feed.error })} {feed.published ? t('rates.usingLast', { date: day(feed.published) }) : t('rates.neverFetched')}
      </div>}
      {feed && !feed.error && feed.missing.length > 0 && <div className="banner warn" style={{ margin: '0 16px 12px' }}>
        {t('rates.missing', { list: feed.missing.map((m) => m.split('/')[0]).join(', ') })}
      </div>}
      {feed && <div className="tx2" style={{ padding: '0 16px 12px', fontSize: 12 }}>
        {t('rates.feedInfo', { published: feed.published ? day(feed.published) : '—', fetched: dt(feed.fetched_at) })}
      </div>}
      <DataTable cols={cols} rows={q.data ?? []} minWidth={760} loading={q.isPending} error={q.error as any} onRetry={q.refetch}
        empty={{ title: t('rates.none'), desc: t('rates.noneDesc', { base }) }} />
      {editing && <FormModal title={row ? t('rates.edit', { currency: row.currency }) : t('rates.add')} action={row ? t('action.save') : t('action.add')} width={440} onClose={() => setEditing(null)}
        initial={{ source: row?.source ?? 'manual', ...(row ? { currency: row.currency, rate: moneyInput(row.rate, 4) } : {}) }}
        fields={[
          ...(row ? [] : [{ key: 'currency', label: t('rates.currency'), type: 'mono' as const, required: true, placeholder: 'EUR', hint: t('rates.codeHint') }]),
          ...(feed ? [{
            key: 'source', label: t('rates.source'), type: 'seg' as const, options: [{ value: 'manual', label: t('rates.manual') }, { value: 'ecb', label: t('rates.ecbDaily') }],
            hint: t('rates.sourceHint'),
          }] : []),
          {
            key: 'rate', label: t('rates.perUnit', { base }), type: 'money' as const, required: true, placeholder: money(1_200_000, 6),
            hint: t('rates.rateHint'), show: (v: Record<string, any>) => v.source !== 'ecb',
          },
        ]}
        validate={(v) => {
          const e: Record<string, string> = {}
          const cur = String(v.currency ?? '').trim().toUpperCase()
          if (!row && !/^[A-Z]{3}$/.test(cur)) e.currency = t('rates.errCode')
          if (v.source === 'ecb') {
            if (!feed?.published) e.source = t('rates.errNeverFetched')
            else if (/^[A-Z]{3}$/.test(cur) && !offered.includes(cur)) e[row ? 'source' : 'currency'] = t('rates.errNotOffered', { currency: cur, base })
          } else {
            const rate = parseMoney(String(v.rate ?? ''))
            if (rate == null || rate <= 0) e.rate = t('rates.errRate')
          }
          return e
        }}
        onSubmit={async (v) => {
          const currency = String(v.currency).trim().toUpperCase()
          if (v.source === 'ecb') await api.post('/v1/exchange-rate-feed/follow', { currency })
          else if (row) await api.patch(`/v1/exchange-rates/${row.id}`, { rate: parseMoney(String(v.rate)) })
          else await api.post('/v1/exchange-rates', { currency, rate: parseMoney(String(v.rate)) })
          refresh()
          setEditing(null)
          ui.toast(row ? t('rates.rateUpdated') : t('rates.rateAdded'))
        }} />}
    </div>
  )
}
