// SPDX-License-Identifier: AGPL-3.0-only
import { useState } from 'react'
import { api, useGet, useInvalidate, type Row } from '../lib/api'
import { day, dt, money, moneyInput, parseMoney } from '../lib/format'
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
    if (!(await ui.confirm({ title: `${r.currency} kuru silinsin mi?`, body: `Para birimi ${r.currency} olan müşteri ya da sağlayıcı varken kur silinemez.`, action: 'Sil', danger: true }))) return
    try {
      await api.del(`/v1/exchange-rates/${r.id}`)
      invalidate('/v1/exchange-rates')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const fetchNow = async () => {
    setFetching(true)
    try {
      const res = await api.post('/v1/exchange-rate-feed/fetch')
      ui.toast(res.changed ? `${day(res.published)} kurları alındı; ${res.changed} kur güncellendi` : `${day(res.published)} kurları alındı; değişen kur yok`)
    } catch (e) { ui.toast(errorText(e as any)) }
    setFetching(false)
    refresh()
  }
  const missing = (r: Row) => feed?.missing.includes(`${r.currency}/${base}`)
  const cols: Col<Row>[] = [
    { label: 'Para birimi', width: '120px', mono: true, render: (r) => r.currency },
    { label: `1 birimin karşılığı (${base})`, width: 'minmax(180px,1fr)', mono: true, render: (r) => money(r.rate, 6) },
    {
      label: 'Kaynak', width: '190px', render: (r) => r.source === 'ecb'
        ? <span style={{ display: 'inline-flex', gap: 6, alignItems: 'center' }}><Badge tone="acc">ECB</Badge>
          {missing(r) ? <Badge tone="warn">Yayında yok</Badge> : <span className="tx2 mono">{r.published ? day(r.published) : '—'}</span>}</span>
        : <span className="tx2">Elle</span>,
    },
    { label: 'Son değişiklik', width: '170px', mono: true, render: (r) => dt(r.updated_at) },
    { label: '', width: '80px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => setEditing(r)}>Düzenle</button> },
    { label: '', width: '50px', align: 'right', render: (r) => canEdit && <button className="btn inline" onClick={() => remove(r)}>Sil</button> },
  ]
  const offered = (feed?.currencies ?? []).filter((c) => c !== base)
  return (
    <div className="card clip">
      <div className="toolbar">
        <div className="grow"><span className="sect">Döviz kurları</span> <span className="tx2 pretty">· Hesabın para birimi {base}. Başka para biriminde müşteri ya da sağlayıcı açmadan önce kurunu girin; kur, sağlayıcı fiyatlarını karşılaştırmak ve raporları {base} olarak toplamak için kullanılır{feed
          ? '. Kaynağı ECB olan kurlar Avrupa Merkez Bankası\'nın günlük referans kurlarından her gün güncellenir; elle girilen kurlara dokunulmaz.'
          : ', elle güncellenir.'}</span></div>
        {canEdit && feed && <button className="btn" disabled={fetching} onClick={fetchNow}>{fetching ? 'Alınıyor…' : 'Kurları şimdi al'}</button>}
        {canEdit && <button className="btn" onClick={() => setEditing('new')}>Kur ekle</button>}
      </div>
      {feed && feed.error && <div className="banner warn" style={{ margin: '0 16px 12px' }}>
        Kurlar alınamadı{feed.failed_at ? ` (${dt(feed.failed_at)})` : ''}: {feed.error}. {feed.published ? `${day(feed.published)} tarihli son kurlar kullanılıyor.` : 'Henüz hiç kur alınmadı; ECB kaynaklı kur eklenemez.'}
      </div>}
      {feed && !feed.error && feed.missing.length > 0 && <div className="banner warn" style={{ margin: '0 16px 12px' }}>
        Avrupa Merkez Bankası şu kurları yayınlamadı, son değerleri kullanılıyor: {feed.missing.map((m) => m.split('/')[0]).join(', ')}.
      </div>}
      {feed && <div className="tx2" style={{ padding: '0 16px 12px', fontSize: 12 }}>
        Avrupa Merkez Bankası · son yayın {feed.published ? day(feed.published) : '—'} · son alma {dt(feed.fetched_at)}. Banka iş günlerinde yayınlar; hafta sonu ve tatilde son kur geçerli kalır.
      </div>}
      <DataTable cols={cols} rows={q.data ?? []} minWidth={760} loading={q.isPending} error={q.error as any} onRetry={q.refetch}
        empty={{ title: 'Kur yok', desc: `Bütün hesaplar ${base} kullanıyor.` }} />
      {editing && <FormModal title={row ? `${row.currency} kurunu düzenle` : 'Kur ekle'} action={row ? 'Kaydet' : 'Ekle'} width={440} onClose={() => setEditing(null)}
        initial={{ source: row?.source ?? 'manual', ...(row ? { currency: row.currency, rate: moneyInput(row.rate, 4) } : {}) }}
        fields={[
          ...(row ? [] : [{ key: 'currency', label: 'Para birimi', type: 'mono' as const, required: true, placeholder: 'EUR', hint: 'Üç harfli kod' }]),
          ...(feed ? [{
            key: 'source', label: 'Kaynak', type: 'seg' as const, options: [{ value: 'manual', label: 'Elle' }, { value: 'ecb', label: 'ECB (günlük)' }],
            hint: 'ECB seçilirse kur son yayından hemen hesaplanır ve her gün güncellenir. Elle girilen kur otomatik güncellenmez.',
          }] : []),
          {
            key: 'rate', label: `1 birimin karşılığı (${base})`, type: 'money' as const, required: true, placeholder: '1,200000',
            hint: 'Yeni kur sonraki çağrılardan itibaren geçerlidir; eski kayıtlar değişmez.', show: (v: Record<string, any>) => v.source !== 'ecb',
          },
        ]}
        validate={(v) => {
          const e: Record<string, string> = {}
          const cur = String(v.currency ?? '').trim().toUpperCase()
          if (!row && !/^[A-Z]{3}$/.test(cur)) e.currency = 'Üç harfli bir kod girin, ör. EUR.'
          if (v.source === 'ecb') {
            if (!feed?.published) e.source = 'Henüz hiç kur alınmadı; önce kurları alın.'
            else if (/^[A-Z]{3}$/.test(cur) && !offered.includes(cur)) e[row ? 'source' : 'currency'] = `Avrupa Merkez Bankası ${cur} için ${base} karşılığı yayınlamıyor.`
          } else {
            const rate = parseMoney(String(v.rate ?? ''))
            if (rate == null || rate <= 0) e.rate = 'Sıfırdan büyük, en fazla 6 ondalık haneli bir sayı girin.'
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
          ui.toast(row ? 'Kur güncellendi' : 'Kur eklendi')
        }} />}
    </div>
  )
}
