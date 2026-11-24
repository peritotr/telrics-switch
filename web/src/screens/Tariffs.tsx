// SPDX-License-Identifier: AGPL-3.0-only
import { useRef, useState, type CSSProperties } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, request, useGet, useInvalidate, usePaged, type Row } from '../lib/api'
import { downloadCSV, dt, fromLocalInput, money, moneyCSV, moneyInput, num, parseMoney, toLocalInput } from '../lib/format'
import { useSession } from '../lib/session'
import { Badge, DataTable, FormModal, Modal, PageHead, Pager, Search, StepBar, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { clock, windowBody, windowErrors, windowFields, windowInitial, windowLabel } from '../lib/window'
import { ListPage, currencies, int, text } from './common'

const kinds = [{ value: 'sell', label: 'Satış' }, { value: 'buy', label: 'Alış' }]
const kindLabel = (k: string) => (k === 'buy' ? 'Alış' : 'Satış')

export function Tariffs() {
  const navigate = useNavigate()
  const cols: Col<Row>[] = [
    { label: 'Ad', width: 'minmax(240px,1.6fr)', render: (r) => r.name },
    { label: 'Tür', width: '120px', render: (r) => <Badge tone={r.kind === 'sell' ? 'acc' : 'neutral'}>{kindLabel(r.kind)}</Badge> },
    { label: 'Para birimi', width: '120px', mono: true, render: (r) => r.currency },
    { label: 'Oluşturma', width: '160px', mono: true, render: (r) => dt(r.created_at) },
  ]
  return <ListPage title="Tarifeler" desc="Satış tarifeleri müşterilere, alış tarifeleri sağlayıcılara atanır. Fiyatlar önek bazındadır ve geçerlilik tarihi taşır."
    path="/v1/tariffs" noun="tarife" cols={cols} minWidth={700} onRow={(r) => navigate(`/tariffs/${r.id}`)}
    add={{
      label: 'Tarife ekle', title: 'Tarife ekle', initial: { kind: 'sell', currency: 'USD', timezone: 'UTC' },
      fields: [
        { key: 'name', label: 'Ad', required: true, span: true },
        { key: 'kind', label: 'Tür', type: 'seg', options: kinds },
        { key: 'currency', label: 'Para birimi', type: 'seg', options: currencies },
        { key: 'timezone', label: 'Saat dilimi', type: 'mono', span: true, placeholder: 'Europe/Istanbul', hint: 'Güne ve saate göre fiyatlar bu saat dilimiyle okunur (IANA adı). Boş = UTC' },
      ],
      build: (v) => ({ name: text(v.name), kind: v.kind, currency: v.currency, timezone: text(v.timezone) ?? 'UTC' }),
      done: (row) => navigate(`/tariffs/${row.id}`),
    }} />
}

const validity = [['', 'Tümü'], ['current', 'Şu an geçerli'], ['future', 'Gelecek'], ['expired', 'Süresi dolmuş']]

const rateFields: FieldDef[] = [
  { key: 'prefix', label: 'Önek', type: 'mono', required: true, placeholder: 'ör. 9053' },
  { key: 'price_per_min', label: 'Dakika fiyatı', type: 'money', required: true, placeholder: '0,000000' },
  { key: 'connect_fee', label: 'Bağlantı ücreti', type: 'money' },
  { key: 'initial_sec', label: 'İlk süre (sn)', type: 'int' },
  { key: 'increment_sec', label: 'Artım (sn)', type: 'int' },
  { key: 'grace_sec', label: 'Ücretsiz süre (sn)', type: 'int' },
  { key: 'effective_from', label: 'Geçerlilik başlangıç (UTC)', type: 'datetime', required: true },
  { key: 'effective_to', label: 'Geçerlilik bitiş (UTC)', type: 'datetime', hint: 'Boş = süresiz' },
  { key: 'blocked', label: 'Engelli', type: 'check', hint: 'Bu öneke çağrı kabul edilmez', span: true },
]

function validateRate(v: Row) {
  const e: Record<string, string> = {}
  if (!/^\d*$/.test(v.prefix ?? '')) e.prefix = 'Önek yalnızca rakam içerebilir.'
  const price = parseMoney(String(v.price_per_min ?? ''))
  if (price == null) e.price_per_min = 'En fazla 6 ondalık haneli bir tutar girin.'
  else if (price < 0) e.price_per_min = 'Dakika fiyatı eksi olamaz.'
  const fee = parseMoney(String(v.connect_fee || '0'))
  if (fee == null || fee < 0) e.connect_fee = 'Eksi olmayan bir tutar girin.'
  if (int(v.increment_sec, 1) < 1) e.increment_sec = 'Artım en az 1 sn olmalı.'
  return { ...e, ...windowErrors(v) }
}
const rateBody = (v: Row) => ({
  prefix: String(v.prefix).trim(), price_per_min: parseMoney(String(v.price_per_min)), connect_fee: parseMoney(String(v.connect_fee || '0')),
  initial_sec: int(v.initial_sec, 1), increment_sec: int(v.increment_sec, 1), grace_sec: int(v.grace_sec), blocked: !!v.blocked,
  effective_from: fromLocalInput(v.effective_from), effective_to: fromLocalInput(v.effective_to ?? ''),
  ...windowBody(v),
})

export function TariffDetail() {
  const id = Number(useParams().id)
  const { canEdit } = useSession()
  const q = useGet<Row>(`/v1/tariffs/${id}`)
  const [prefix, setPrefix] = useState('')
  const [valid, setValid] = useState('')
  const [blocked, setBlocked] = useState(false)
  const list = usePaged('/v1/rates', { tariff_id: id, prefix, validity: valid, blocked: blocked ? 'true' : '' }, 50)
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const [csv, setCsv] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  const t = q.data
  if (q.error) return <div className="page"><Link to="/tariffs" style={{ fontSize: 12 }}>← Tarifeler</Link><div className="banner bad">Tarife yüklenemedi: {errorText(q.error as any)}</div></div>
  if (!t) return <div className="tx3">Yükleniyor…</div>

  const now = Date.now()
  const cols: Col<Row>[] = [
    { label: 'Önek', width: '100px', mono: true, render: (r) => <span style={{ fontWeight: 500 }}>{r.prefix || '(tümü)'}</span> },
    { label: `Dakika fiyatı ${t.currency}`, width: '130px', align: 'right', mono: true, render: (r) => (r.blocked ? '—' : money(r.price_per_min, 6)) },
    { label: `Bağlantı ücreti ${t.currency}`, width: '130px', align: 'right', mono: true, render: (r) => (r.blocked ? '—' : money(r.connect_fee, 6)) },
    { label: 'İlk süre sn', width: '70px', align: 'right', mono: true, render: (r) => r.initial_sec },
    { label: 'Artım sn', width: '70px', align: 'right', mono: true, render: (r) => r.increment_sec },
    { label: 'Ücretsiz süre sn', width: '90px', align: 'right', mono: true, render: (r) => r.grace_sec },
    { label: 'Gün ve saat', width: '150px', render: (r) => windowLabel(r) || <span className="tx3">her zaman</span> },
    { label: 'Engelli', width: '90px', render: (r) => r.blocked && <Badge tone="bad">Engelli</Badge> },
    { label: 'Geçerlilik başlangıç', width: '150px', mono: true, render: (r) => <>{dt(r.effective_from)} {Date.parse(r.effective_from) > now && <span style={{ fontFamily: 'var(--sans)', fontSize: 11, color: 'var(--acct)' }}>gelecek</span>}</> },
    { label: 'Geçerlilik bitiş', width: '150px', mono: true, render: (r) => <span className="tx2">{r.effective_to ? dt(r.effective_to) : '—'}</span> },
    { label: '', width: 'minmax(60px,1fr)', align: 'right', render: (r) => canEdit && <a style={{ fontSize: 12 }} onClick={() => setEditing(r)}>Düzenle</a> },
  ]

  const exportCSV = async () => {
    const all: Row[] = []
    try {
      for (let offset = 0; offset < 200_000; offset += 1000) {
        const { data } = await request<Row[]>('GET', '/v1/rates', { params: { tariff_id: id, validity: 'current', limit: 1000, offset } })
        all.push(...data)
        if (data.length < 1000) break
      }
    } catch (e) { return ui.toast(errorText(e as any)) }
    const hhmm = (r: Row, m: number) => (r.time_from === r.time_to ? '' : clock(m))
    downloadCSV(`${t.name}.csv`, ['prefix', 'price_per_min', 'connect_fee', 'initial_sec', 'increment_sec', 'grace_sec', 'blocked', 'days', 'time_from', 'time_to'],
      all.map((r) => [r.prefix, moneyCSV(r.price_per_min), moneyCSV(r.connect_fee), r.initial_sec, r.increment_sec, r.grace_sec, r.blocked,
        r.days === 127 ? '' : r.days, hhmm(r, r.time_from), hhmm(r, r.time_to)]))
    ui.toast(`${num(all.length)} geçerli fiyat indirildi`)
  }

  const edited = editing && editing !== 'new' ? editing : null
  return (
    <div className="page">
      <PageHead back={<Link to="/tariffs" style={{ fontSize: 12 }}>← Tarifeler</Link>} title={t.name} desc={`${kindLabel(t.kind)} tarifesi · ${t.currency} · saat dilimi ${t.timezone}`}>
        <button className="btn" onClick={exportCSV} title="Şu an geçerli fiyatları indirir">CSV indir</button>
        {canEdit && <div className="row">
          <button className="btn" onClick={() => setEditing('new')}>Fiyat ekle</button>
          <button className="btn primary" onClick={() => setCsv(true)}>CSV yükle</button>
        </div>}
      </PageHead>
      <div className="card clip">
        <div className="toolbar">
          <Search mono width={220} value={prefix} onChange={(v) => setPrefix(v.replace(/\D/g, ''))} placeholder="Önek ara, ör. 9053" />
          <select className="select" style={{ width: 'auto' }} value={valid} onChange={(e) => setValid(e.target.value)}>{validity.map(([v, l]) => <option key={v} value={v}>{l}</option>)}</select>
          <label className="radio tx2"><input type="checkbox" checked={blocked} onChange={(e) => setBlocked(e.target.checked)} />Yalnız engelli</label>
          <div className="grow" />
        </div>
        <DataTable cols={cols} rows={list.rows} minWidth={1080} loading={list.loading} error={list.error} onRetry={list.refetch}
          empty={prefix || valid || blocked
            ? { title: 'Fiyat bulunamadı', desc: 'Önek ya da filtrelerle eşleşen fiyat yok.', action: <button className="btn" onClick={() => { setPrefix(''); setValid(''); setBlocked(false) }}>Filtreleri temizle</button> }
            : { title: 'Bu tarifede fiyat yok', desc: 'Fiyatları tek tek ekleyin ya da bir CSV dosyasıyla toplu yükleyin.' }} />
        <Pager p={list} noun="fiyat" />
      </div>
      {editing && <FormModal title={edited ? `Fiyatı düzenle · ${edited.prefix}` : 'Fiyat ekle'} sub={`${t.name} · ${kindLabel(t.kind)} · ${t.currency}`} action={edited ? 'Kaydet' : 'Ekle'}
        fields={[...rateFields.slice(0, -1), ...windowFields(t.timezone, edited?.days), rateFields[rateFields.length - 1]]} validate={validateRate} onClose={() => setEditing(null)}
        initial={edited
          ? { ...edited, ...windowInitial(edited), price_per_min: moneyInput(edited.price_per_min, 4), connect_fee: moneyInput(edited.connect_fee, 2), effective_from: toLocalInput(edited.effective_from), effective_to: toLocalInput(edited.effective_to) }
          : { ...windowInitial(), initial_sec: '1', increment_sec: '1', grace_sec: '0', connect_fee: '0', effective_from: toLocalInput(new Date().toISOString()) }}
        onSubmit={async (v) => {
          if (edited) await api.patch(`/v1/rates/${edited.id}`, rateBody(v))
          else await api.post('/v1/rates', { tariff_id: id, ...rateBody(v) })
          invalidate('/v1/rates')
          setEditing(null)
          ui.toast(edited ? 'Fiyat kaydedildi' : 'Fiyat eklendi')
        }} />}
      {csv && <CsvImport tariff={t} onClose={() => { setCsv(false); invalidate('/v1/rates') }} />}
    </div>
  )
}

const importErrors: Record<string, (e: Row) => string> = {
  prefix_empty: () => 'Önek boş',
  prefix_not_digits: () => 'Önek yalnızca rakam içerebilir',
  price_invalid: () => 'Fiyat sayı değil',
  price_negative: () => 'Dakika fiyatı eksi olamaz',
  too_many_decimals: () => 'En fazla 6 ondalık hane',
  increment_min: () => 'Artım en az 1 sn olmalı',
  value_invalid: () => 'Süre ya da engel değeri geçersiz',
  days_invalid: () => 'Günler 1–127 arası bir sayı olmalı',
  time_invalid: () => 'Saat SS:DD biçiminde olmalı',
  time_pair: () => 'Saat başlangıcı ve bitişi birlikte, farklı girilir',
  duplicate_prefix: (e) => `Yinelenen önek ve pencere (satır ${e.ref_line})`,
  conflict: () => 'Bu önek için geçerli bir fiyat var',
}

function CsvImport({ tariff, onClose }: { tariff: Row; onClose: () => void }) {
  const [step, setStep] = useState(0)
  const [file, setFile] = useState<{ name: string; text: string; lines: number; size: number } | null>(null)
  const [over, setOver] = useState(false)
  const [date, setDate] = useState(() => new Date(Date.now() + 86_400_000).toISOString().slice(0, 10))
  const [time, setTime] = useState('00:00')
  const [mode, setMode] = useState('close')
  const [result, setResult] = useState<Row | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const input = useRef<HTMLInputElement>(null)
  const effective = new Date(`${date}T${time}:00Z`)
  const effectiveOK = !isNaN(effective.getTime())

  const read = async (f: File | undefined) => {
    if (!f) return
    const content = await f.text()
    const lines = content.split(/\r?\n/).filter((l) => l.trim() !== '').length - 1
    setFile({ name: f.name, text: content, lines: Math.max(0, lines), size: f.size })
    setError('')
  }
  const send = async (dryRun: boolean) => {
    setBusy(true)
    setError('')
    try {
      const { data } = await request<Row>('POST', `/v1/tariffs/${tariff.id}/rates/import`, {
        text: file!.text, params: { effective_from: effective.toISOString(), mode, dry_run: dryRun ? 1 : null },
      })
      setResult(data)
      setStep(dryRun ? 2 : 3)
    } catch (e) { setError(errorText(e as any)) }
    setBusy(false)
  }
  const errors: Row[] = result?.errors ?? []
  const report = () => downloadCSV('hata-raporu.csv', ['satir', 'onek', 'deger', 'hata'], errors.map((e) => [e.line, e.prefix, e.value, importErrors[e.code]?.(e) ?? e.code]))
  const size = (n: number) => (n < 1024 ? `${n} B` : n < 1024 * 1024 ? `${Math.round(n / 1024)} KB` : `${(n / 1024 / 1024).toFixed(1).replace('.', ',')} MB`)
  const errGrid: CSSProperties = { display: 'grid', gridTemplateColumns: '50px 80px 150px minmax(0,1fr)', columnGap: 12, alignItems: 'center', padding: '2px 12px' }
  const body: CSSProperties = { padding: 20, display: 'flex', flexDirection: 'column', gap: 12 }

  return (
    <Modal width={640} flush onClose={step === 3 ? onClose : undefined}>
      <div style={{ padding: '20px 20px 14px', borderBottom: '1px solid var(--line)' }}>
        <div className="modal-title">CSV ile fiyat yükle</div>
        <div className="tx2" style={{ marginTop: 2 }}>{tariff.name} · {kindLabel(tariff.kind)} · {tariff.currency}</div>
        <StepBar steps={['Dosya', 'Geçerlilik', 'Doğrulama', 'Sonuç']} current={step} />
      </div>
      {step === 0 && <div style={body}>
        <input ref={input} type="file" accept=".csv,text/csv" hidden onChange={(e) => read(e.target.files?.[0])} />
        {!file
          ? <div className={'drop' + (over ? ' over' : '')} onClick={() => input.current?.click()}
              onDragOver={(e) => { e.preventDefault(); setOver(true) }} onDragLeave={() => setOver(false)}
              onDrop={(e) => { e.preventDefault(); setOver(false); read(e.dataTransfer.files[0]) }}>
              <div className="sect">CSV dosyasını buraya bırakın</div><div className="tx2">ya da seçmek için tıklayın</div>
            </div>
          : <div className="row" style={{ gap: 10, padding: '12px 14px', border: '1px solid var(--line)', borderRadius: 10 }}>
              <div className="grow"><div className="mono">{file.name}</div><div className="tx3" style={{ fontSize: 12, marginTop: 2 }}>{num(file.lines)} satır · {size(file.size)}</div></div>
              <Badge tone="ok">Okundu</Badge>
              <button className="btn inline" onClick={() => setFile(null)}>Değiştir</button>
            </div>}
        <div className="tx2">Beklenen sütunlar <span className="tx3">· ilk satır başlık olmalı; prefix ve price zorunlu, ondalık ayırıcı nokta</span></div>
        <div className="codebox">prefix,price,connect_fee,initial,increment,grace,blocked,days,time_from,time_to</div>
        <div className="tx3 pretty" style={{ fontSize: 12 }}>Son üç sütun isteğe bağlıdır ve fiyatı güne ve saate sınırlar: days gün kümesidir (pazartesi 1, salı 2, çarşamba 4, perşembe 8, cuma 16, cumartesi 32, pazar 64; toplamları, ör. hafta içi 31, hafta sonu 96; boş = her gün), time_from ve time_to {tariff.timezone} saatiyle SS:DD (ikisi birlikte; bitiş başlangıçtan küçükse gece yarısını aşar). Aynı önek farklı pencerelerle birden çok satırda olabilir; yüklenen dosya önekin bütün eski fiyatlarının yerini alır.</div>
        <div className="modal-foot" style={{ marginTop: 4 }}>
          <button className="btn" onClick={onClose}>Vazgeç</button>
          <button className="btn primary" disabled={!file || file.lines < 1} onClick={() => setStep(1)}>İleri</button>
        </div>
      </div>}
      {step === 1 && <div style={body}>
        <div className="grid2">
          <label className="field"><span className="label">Geçerlilik başlangıç tarihi</span><input type="date" className="input mono" value={date} onChange={(e) => setDate(e.target.value)} /></label>
          <label className="field"><span className="label">Saat (UTC)</span><input type="time" className="input mono" value={time} onChange={(e) => setTime(e.target.value)} /></label>
        </div>
        <div className="tx2 pretty">Dosyadaki tüm satırlar bu tarihten itibaren geçerli olur.</div>
        <label className="row" style={{ alignItems: 'flex-start' }}><input type="radio" name="csvm" checked={mode === 'close'} onChange={() => setMode('close')} style={{ marginTop: 3 }} />
          <span>Aynı önekteki mevcut fiyatın bitişini bu tarihe çek<br /><span className="tx3" style={{ fontSize: 12 }}>{effectiveOK ? `Eski fiyat ${dt(effective.toISOString())} UTC'de sona erer.` : 'Geçerli bir tarih ve saat girin.'}</span></span></label>
        <label className="row" style={{ alignItems: 'flex-start' }}><input type="radio" name="csvm" checked={mode === 'keep'} onChange={() => setMode('keep')} style={{ marginTop: 3 }} />
          <span>Mevcut fiyatlara dokunma<br /><span className="tx3" style={{ fontSize: 12 }}>Çakışan önekler doğrulamada hata olarak listelenir.</span></span></label>
        {error && <div className="banner bad">{error}</div>}
        <div className="modal-foot" style={{ marginTop: 4 }}>
          <button className="btn" onClick={() => setStep(0)}>Geri</button>
          <button className="btn primary" disabled={busy || !effectiveOK} onClick={() => send(true)}>Doğrula</button>
        </div>
      </div>}
      {step === 2 && result && <div style={body}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 10 }}>
          {([['Okunan satır', result.read, ''], ['Geçerli', result.valid, 'ok'], ['Hatalı', result.invalid, result.invalid ? 'bad' : '']] as [string, number, string][]).map(([l, n, c]) =>
            <div key={l} style={{ padding: '10px 12px', background: 'var(--panel2)', borderRadius: 8 }}><div className="tx3" style={{ fontSize: 12 }}>{l}</div><div className={'mono ' + c} style={{ fontSize: 16, fontWeight: 500 }}>{num(n)}</div></div>)}
        </div>
        {errors.length > 0 && <div style={{ border: '1px solid var(--line)', borderRadius: 8, overflow: 'auto', maxHeight: 260 }}>
          <div className="tx3" style={{ ...errGrid, height: 32, fontSize: 11.5, fontWeight: 500, borderBottom: '1px solid var(--line)' }}><div className="right">Satır</div><div>Önek</div><div>Değer</div><div>Hata</div></div>
          {errors.map((e) => <div key={e.line + e.code} className="mono" style={{ ...errGrid, minHeight: 30, fontSize: 12, borderBottom: '1px solid var(--line2)' }}>
            <div className="right tx3">{e.line}</div><div className="ell">{e.prefix}</div><div className="ell">{e.value}</div>
            <div className="bad" style={{ fontFamily: 'var(--sans)', fontSize: 12.5 }}>{importErrors[e.code]?.(e) ?? e.code}</div>
          </div>)}
        </div>}
        <div className="tx2 pretty">{result.invalid ? "Hatalı satırlar atlanır; geçerli satırlar tek işlemde yüklenir. Dosyayı düzeltip baştan yüklemek için Geri'ye dönün." : 'Tüm satırlar geçerli.'}{result.invalid > errors.length ? ` İlk ${num(errors.length)} hata gösteriliyor.` : ''}</div>
        {error && <div className="banner bad">{error}</div>}
        <div className="modal-foot" style={{ marginTop: 4 }}>
          <button className="btn" onClick={() => setStep(1)}>Geri</button>
          {errors.length > 0 && <button className="btn" onClick={report}>Hata raporunu indir</button>}
          <button className="btn primary" disabled={busy || !result.valid} onClick={() => send(false)}>{num(result.valid)} geçerli satırı yükle</button>
        </div>
      </div>}
      {step === 3 && result && <div style={body}>
        <div className="verdict" style={{ '--c': 'var(--ok)', display: 'block', padding: 16 } as CSSProperties}>
          <div className="t">{num(result.rates)} satır yüklendi</div>
          <div style={{ marginTop: 4 }}>Yeni fiyatlar <span className="mono">{dt(result.effective_from)} UTC</span> itibarıyla geçerli.</div>
        </div>
        <div className="kv"><span className="tx2">Bitişi güncellenen mevcut fiyat</span><span className="mono">{num(result.closed)}</span></div>
        <div className="kv"><span className="tx2">Yeni önek</span><span className="mono">{num(result.new_prefixes)}</span></div>
        <div className="kv"><span className="tx2">Atlanan hatalı satır</span><span className={'mono' + (result.invalid ? ' bad' : '')}>{num(result.invalid)}</span></div>
        <div className="modal-foot" style={{ marginTop: 4 }}>
          {errors.length > 0 && <button className="btn" onClick={report}>Hata raporunu indir</button>}
          <button className="btn primary" onClick={onClose}>Kapat</button>
        </div>
      </div>}
    </Modal>
  )
}
