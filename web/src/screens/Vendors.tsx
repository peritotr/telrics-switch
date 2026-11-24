// SPDX-License-Identifier: AGPL-3.0-only
import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, useGet, useInvalidate, useLookup, type Row } from '../lib/api'
import { money } from '../lib/format'
import { useSession } from '../lib/session'
import { useQuery } from '@tanstack/react-query'
import { Badge, DataTable, Field, FormModal, Modal, PageHead, Seg, Stat, StatusBadge, Tabs, Toggle, errorText, useUI, type Col, type FieldDef } from '../ui/kit'
import { CallsTab, LedgerTab, ListPage, PaymentModal, codecOptions, currencies, idOrNull, int, secondsError, text } from './common'

const transports = ['udp', 'tcp', 'tls'].map((t) => ({ value: t, label: t.toUpperCase() }))

export function Vendors() {
  const navigate = useNavigate()
  const tariffs = useLookup('/v1/tariffs', { kind: 'buy' })
  const cols: Col<Row>[] = [
    { label: 'Ad', width: 'minmax(220px,1.6fr)', render: (r) => r.name },
    { label: 'Sağlayıcıya borcumuz', width: '170px', align: 'right', mono: true, render: (r) => `${money(r.balance)} ${r.currency}` },
    { label: 'Alış tarifesi', width: 'minmax(160px,1fr)', render: (r) => tariffs.name(r.tariff_id) },
    { label: 'Durum', width: '90px', render: (r) => <StatusBadge value={r.status} /> },
  ]
  return <ListPage title="Sağlayıcılar" desc="Trafiği taşıyan operatörler. Bakiye, sağlayıcıya olan borcumuzdur." path="/v1/vendors" noun="sağlayıcı"
    cols={cols} minWidth={760} onRow={(r) => navigate(`/vendors/${r.id}`)}
    add={{
      label: 'Sağlayıcı ekle', title: 'Sağlayıcı ekle', sub: 'SIP bağlantıları oluşturduktan sonra detay sayfasından eklenir.', initial: { currency: 'USD' },
      fields: [
        { key: 'name', label: 'Ad', required: true, span: true },
        { key: 'currency', label: 'Para birimi', type: 'seg', options: currencies },
        { key: 'tariff_id', label: 'Alış tarifesi', type: 'select', options: tariffs.rows.map((t) => ({ value: String(t.id), label: t.name })) },
      ],
      build: (v) => ({ name: text(v.name), currency: v.currency, tariff_id: idOrNull(v.tariff_id) }),
      done: (row) => navigate(`/vendors/${row.id}`),
    }} />
}

export function VendorDetail() {
  const id = Number(useParams().id)
  const { canEdit, ext } = useSession()
  const q = useGet<Row>(`/v1/vendors/${id}`)
  const conns = useGet<Row[]>('/v1/vendor-connections', { vendor_id: id, limit: 1000 })
  const tariffs = useLookup('/v1/tariffs', { kind: 'buy' })
  const [tab, setTab] = useState('conn')
  const [paying, setPaying] = useState(false)
  const v = q.data
  if (q.error) return <div className="page"><Link to="/vendors" style={{ fontSize: 12 }}>← Sağlayıcılar</Link><div className="banner bad">Sağlayıcı yüklenemedi: {errorText(q.error as any)}</div></div>
  if (!v) return <div className="tx3">Yükleniyor…</div>
  const tariff = tariffs.byId.get(v.tariff_id)
  const extra = ext.flatMap((e) => e.vendorTabs ?? [])
  const tabs = [
    { value: 'conn', label: 'Bağlantılar' }, { value: 'genel', label: 'Genel' }, { value: 'ledger', label: 'Hesap hareketleri' }, { value: 'calls', label: 'Çağrı kayıtları' },
    ...extra.map((t) => ({ value: t.key, label: t.label })),
  ]
  const payButton = canEdit && <button className="btn primary" onClick={() => setPaying(true)}>Ödeme ekle</button>
  return (
    <div className="page">
      <PageHead back={<Link to="/vendors" style={{ fontSize: 12 }}>← Sağlayıcılar</Link>}
        title={<><h1>{v.name}</h1><StatusBadge value={v.status} /><span className="mono tx3" style={{ fontSize: 12 }}>ID {v.id}</span></>}>
        {payButton}
      </PageHead>
      <div className="stats" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(200px,1fr))' }}>
        <Stat label="Sağlayıcıya borcumuz" value={money(v.balance)} unit={v.currency} sub="Ödeme yaptıkça azalır" />
        <Stat text label="Alış tarifesi" value={tariff ? <Link to={`/tariffs/${tariff.id}`}>{tariff.name}</Link> : '—'} sub={tariff ? `Alış · ${tariff.currency}` : 'Tarife atanmadı; maliyet 0 hesaplanır'} />
        <Stat label="Bağlantı" value={conns.data?.length ?? '—'} sub={`${conns.data?.filter((c) => c.enabled).length ?? 0} açık`} />
      </div>
      <div className="card clip">
        <Tabs value={tab} tabs={tabs} onChange={setTab} />
        {tab === 'conn' && <Connections vendor={v} rows={conns.data ?? []} loading={conns.isPending} error={conns.error} retry={conns.refetch} />}
        {tab === 'genel' && <GeneralForm vendor={v} tariffs={tariffs.rows} />}
        {tab === 'ledger' && <LedgerTab party="vendor" row={v} action={payButton} />}
        {tab === 'calls' && <CallsTab filter={{ vendor_id: v.id }} />}
        {extra.find((t) => t.key === tab)?.render(v)}
      </div>
      {paying && <PaymentModal party="vendor" row={v} onClose={() => setPaying(false)} />}
    </div>
  )
}

// What a new connection gets for the settings in the form's advanced part;
// the form counts a setting as changed when it is not this.
const connDefaults = {
  tech_prefix: '', strip_digits: '0', number_plus: false, cli_strip_digits: '0', cli_add_prefix: '', pai_mode: 'pass',
  ring_timeout_sec: '0', max_duration_sec: '0', failover_mode: 'standard', options_interval_sec: '0',
  codecs: [] as string[], transcode: false, auth_username: '', max_calls: '0', max_cps: '0', tls_name: '',
  register: false, register_expires_sec: '3600', register_domain: '',
}
const intervals = [0, 30, 60, 300]
const intervalLabel = (sec: number) => (sec === 0 ? 'Yoklama' : sec % 60 === 0 ? `${sec / 60} dakikada bir` : `${sec} saniyede bir`)
const codeList = (v: any) => String(v ?? '').split(/[\s,;]+/).filter((c) => c !== '')

function connectionFields(row: Row | null): FieldDef[] {
  const every = [...new Set([...intervals, Number(row?.options_interval_sec ?? 0)])].sort((a, b) => a - b)
  const adv = (f: FieldDef): FieldDef => ({ ...f, advanced: true })
  return [
    { key: 'name', label: 'Ad', type: 'mono', required: true },
    { key: 'host', label: 'Sunucu', type: 'mono', required: true, placeholder: 'sip.example.net', hint: 'Sağlayıcının size verdiği alan adı ya da IP adresi.' },
    { key: 'port', label: 'Port', type: 'int', required: true, hint: 'Sağlayıcı başka bir port söylemediyse 5060 (TLS için 5061).' },
    { key: 'transport', label: 'Taşıma', type: 'seg', options: transports, hint: 'Sağlayıcı belirtmediyse UDP.' },

    adv({ key: 's_number', label: 'Numara biçimi', type: 'section', hint: 'Aranan numaranın bu sağlayıcıya nasıl gönderileceği.' }),
    adv({ key: 'strip_digits', label: 'Baştan silinecek hane', type: 'int', hint: 'Numaranın başından bu kadar hane silinir; önek ondan sonra eklenir.' }),
    adv({ key: 'tech_prefix', label: 'Eklenecek önek', type: 'mono', hint: 'Sağlayıcı numaranın başında özel bir kod istiyorsa buraya yazın; çoğu sağlayıcıda boş kalır.' }),
    adv({ key: 'number_plus', label: 'Artı işareti', type: 'check', span: true, text: 'Numarayı başında + ile gönder', hint: 'Sağlayıcı numarayı +905321112233 biçiminde istiyorsa işaretleyin.' }),

    adv({ key: 's_caller', label: 'Arayan numara', type: 'section', hint: 'Bu sağlayıcıya gösterilecek arayan numara. Çağrı kaydında her zaman gelen numara durur.' }),
    adv({ key: 'cli_strip_digits', label: 'Arayandan silinecek hane', type: 'int', hint: 'Arayan numaranın başından silinecek hane sayısı.' }),
    adv({ key: 'cli_add_prefix', label: 'Arayana eklenecek önek', type: 'mono', placeholder: '+', hint: 'Sağlayıcı arayan numarayı başka biçimde istiyorsa (ör. başında + ya da 0). Boş = numara olduğu gibi gider.' }),
    adv({ key: 'pai_mode', label: 'Kimlik başlığı', type: 'select', required: true, span: true, options: [
      { value: 'pass', label: 'Olduğu gibi ilet (önerilen)' }, { value: 'remove', label: 'Sil' }, { value: 'caller', label: 'Arayan numarayla doldur' }],
      hint: 'Bazı sağlayıcılar arayan numarayı bu ek başlıktan (P-Asserted-Identity) okur. Sağlayıcınız özellikle istemediyse dokunmayın.' }),

    adv({ key: 's_time', label: 'Süreler', type: 'section' }),
    adv({ key: 'ring_timeout_sec', label: 'Çalma süresi sınırı (saniye)', type: 'int', hint: 'Karşı taraf bu süre içinde açmazsa sıradaki rota denenir. 0 = sistemin kendi sınırı (30 sn). Sağlayıcı çalarken ses kanalını açıp (183) hiç ses göndermezse çağrı en çok medya zaman aşımı kadar (varsayılan 60 sn) çalar.' }),
    adv({ key: 'max_duration_sec', label: 'En uzun çağrı süresi (saniye)', type: 'int', hint: 'Bu bağlantıdan giden çağrı bu süre dolunca kapatılır. 0 = ayrı sınır yok (bakiyenin yettiği kadar, en çok 2 saat).' }),

    adv({ key: 's_fail', label: 'Hata durumunda', type: 'section' }),
    adv({ key: 'failover_mode', label: 'Sağlayıcı çağrıyı kuramazsa', type: 'select', required: true, span: true, options: [
      { value: 'standard', label: 'Standart (önerilen): meşgul ya da yanlış numarada çağrı biter, diğer hatalarda sıradaki rota denenir' },
      { value: 'always', label: 'Her hatada sıradaki rotayı dene' },
      { value: 'never', label: 'Hiçbir hatada başka rota deneme' },
      { value: 'custom', label: 'Hangi hata kodlarında deneneceğini ben yazacağım (uzman)' }],
      hint: 'Arayan vazgeçerse çağrı her durumda biter.' }),
    adv({ key: 'failover_codes', label: 'Sıradaki rotaya geçilecek SIP kodları', type: 'mono', span: true, placeholder: '408, 480, 500, 502, 503, 504', show: (v) => v.failover_mode === 'custom',
      hint: 'Virgülle ayırın. Yalnızca bu kodlarla biten denemelerden sonra sıradaki rota denenir.' }),

    adv({ key: 's_health', label: 'Erişilebilirlik denetimi', type: 'section' }),
    adv({ key: 'options_interval_sec', label: 'Sağlayıcıyı yokla', type: 'select', required: true, span: true, options: every.map((n) => ({ value: String(n), label: intervalLabel(n) })),
      hint: 'Açıkken sistem sağlayıcıya düzenli olarak “orada mısın?” diye sorar (SIP OPTIONS). Cevap vermeyen bağlantıya çağrı gönderilmez; cevap verince kendiliğinden geri alınır. Sağlayıcınız bu soruya cevap vermiyorsa kapalı bırakın: “Dene” düğmesiyle öğrenebilirsiniz.' }),

    adv({ key: 's_codecs', label: 'Ses kodekleri', type: 'section' }),
    adv({ key: 'codecs', label: 'Bu sağlayıcıya teklif edilecek kodekler', type: 'pick', span: true, options: codecOptions,
      hint: 'Seçtikleriniz, tıkladığınız sırayla teklif edilir; hiçbiri seçili değilse arayanın sunduğu her kodek geçer (önerilen). Arayan bunlardan hiçbirini sunmuyorsa çağrı bu bağlantıya gönderilmez; aşağıdaki çeviri açıksa ve müşterinin trunk\'ında kodek seçiliyse gönderilir.' }),
    adv({ key: 'transcode', label: 'Kodek çevirisi', type: 'check', span: true, text: 'Ortak kodek yoksa sesi çevir',
      hint: 'Müşterinin trunk\'ında seçili kodeklerle bu listede hiç ortak kodek yoksa çağrı yine de bu bağlantıya gönderilir; ses, sunucuda bir kodekten ötekine çevrilir. Çeviri sunucunun işlemcisini kullanır; aynı anda çevrilen çağrı sayısının bir sınırı vardır, dolunca bu bağlantı o çağrı için atlanır.' }),

    adv({ key: 's_signin', label: 'Sağlayıcıya giriş', type: 'section', hint: 'Çoğu sağlayıcı yalnızca IP adresinize bakar; kullanıcı adı ve parola verdiyse buraya girin.' }),
    adv({ key: 'auth_username', label: 'Kullanıcı adı', type: 'mono' }),
    adv({ key: 'auth_password', label: 'Parola', type: 'password', placeholder: row?.auth_set ? 'Kayıtlı; değiştirmek için yazın' : undefined,
      hint: 'Şifreli saklanır ve bir daha gösterilmez.' }),
    adv({ key: 'register', label: 'Kayıt', type: 'check', span: true, text: 'Sağlayıcıya kayıt ol (REGISTER)',
      hint: 'Bazı sağlayıcılar çağrı kabul etmeden önce santralin kendilerine kaydolmasını ister. Açıkken sistem yukarıdaki kullanıcı adı ve parolayla kaydolur, süresi dolmadan yeniler; durum bağlantı listesindeki “Kayıt” sütununda görünür.' }),
    adv({ key: 'register_expires_sec', label: 'Kayıt süresi (saniye)', type: 'int', show: (v) => !!v.register,
      hint: 'Sağlayıcıdan istenen süre; sağlayıcı daha kısasını verebilir. 60–86400, çoğunlukla 3600.' }),
    adv({ key: 'register_domain', label: 'Kayıt alanı', type: 'mono', placeholder: 'sip.example.net', show: (v) => !!v.register,
      hint: 'Sağlayıcı kaydı sunucu adresinden başka bir alan adıyla istiyorsa (kullanıcı@alan). Boş = sunucu.' }),

    adv({ key: 's_limits', label: 'Limitler', type: 'section' }),
    adv({ key: 'max_calls', label: 'Çağrı limiti', type: 'int', hint: 'Bu bağlantıda aynı anda sürebilecek çağrı sayısı. 0 = sınırsız.' }),
    adv({ key: 'max_cps', label: 'CPS limiti', type: 'int', hint: 'Saniyede gönderilebilecek yeni çağrı sayısı. 0 = sınırsız.' }),

    adv({ key: 's_cert', label: 'Sertifika', type: 'section', show: (v) => v.transport === 'tls' }),
    adv({ key: 'tls_name', label: 'Sertifikadaki ad', type: 'mono', span: true, placeholder: 'sip.example.net', show: (v) => v.transport === 'tls',
      hint: 'Sağlayıcının sertifikası bu ada düzenlenmiş olmalı. Boş = sunucu adı ya da adresi. Uymayan bağlantıya çağrı gönderilmez.' }),
  ]
}

/** How many of a connection's advanced settings are not as a new one comes. */
function changedSettings(c: Row) {
  const d = connDefaults
  return [c.tech_prefix !== '', c.strip_digits !== 0, c.number_plus, c.cli_strip_digits !== 0, c.cli_add_prefix !== '', c.pai_mode !== d.pai_mode,
    c.ring_timeout_sec !== 0, c.max_duration_sec !== 0, c.failover_mode !== d.failover_mode, c.options_interval_sec !== 0,
    (c.codecs ?? []).length > 0, !!c.transcode, c.auth_username !== '' || c.auth_set, !!c.register, c.max_calls !== 0, c.max_cps !== 0, c.tls_name !== ''].filter(Boolean).length
}

const ago = (iso: string) => {
  const sec = Math.max(0, Math.round((Date.now() - new Date(iso).getTime()) / 1000))
  return sec < 90 ? `${sec} sn` : sec < 5400 ? `${Math.round(sec / 60)} dk` : `${Math.round(sec / 3600)} sa`
}

/** What one question to the vendor came to, in plain words. */
function probeText(r: Row): { tone: 'ok' | 'warn' | 'bad'; title: string; body: string } {
  const took = `${r.ms} ms`
  switch (r.outcome) {
    case 'answered':
      if (r.sip_code >= 200 && r.sip_code < 300) return { tone: 'ok', title: `Sağlayıcı cevap verdi (${r.sip_code} ${r.sip_reason ?? ''}, ${took})`, body: 'Adres, port ve taşıma doğru; karşı taraf orada.' }
      if (r.sip_code === 503) return { tone: 'bad', title: `Sağlayıcı hizmet dışı olduğunu bildirdi (503, ${took})`, body: 'Karşı taraf orada ama şu an çağrı almıyor. Yoklama açıksa bu bağlantıya çağrı gönderilmez.' }
      return { tone: 'warn', title: `Sağlayıcı cevap verdi: ${r.sip_code} ${r.sip_reason ?? ''} (${took})`, body: 'Karşı taraf orada, ama bu soruyu olumlu cevaplamadı. Bazı sağlayıcılar yoklamayı böyle cevaplar ve çağrıları yine de alır; çağrılarınız da reddediliyorsa IP adresinizin sağlayıcıda tanımlı olduğunu denetleyin.' }
    case 'no_answer':
      return { tone: 'bad', title: `Cevap gelmedi (${Math.round(r.ms / 1000)} saniye beklendi)`, body: 'Adresi, portu ve taşımayı denetleyin. Bazı sağlayıcılar yoklamaya hiç cevap vermez; çağrılarınız çalışıyorsa bu bağlantıda “Sağlayıcıyı yokla” ayarını kapalı bırakın.' }
    case 'unresolved':
      return { tone: 'bad', title: 'Sunucu adı bulunamadı', body: '“Sunucu” alanındaki adı yazım hatası için denetleyin.' }
    case 'refused':
      return { tone: 'bad', title: 'Bu adreste ve portta dinleyen yok', body: 'Bağlantı reddedildi. Portu ve taşımayı (UDP / TCP / TLS) denetleyin.' }
    case 'certificate':
      return { tone: 'bad', title: 'Sağlayıcının sertifikası kabul edilmedi', body: `Gelişmiş ayarlardaki “Sertifikadaki ad” alanını denetleyin. Ayrıntı: ${r.detail ?? ''}` }
  }
  return { tone: 'bad', title: 'Denenemedi', body: String(r.detail ?? '') }
}

/** "Bağlantıyı dene": asks the vendor once, now, and says what came back. */
function ConnectionTest({ conn, onClose }: { conn: Row; onClose: () => void }) {
  const [result, setResult] = useState<Row | null>(null)
  const [error, setError] = useState('')
  const [round, setRound] = useState(0)
  useEffect(() => {
    let gone = false
    setResult(null)
    setError('')
    api.post(`/v1/vendor-connections/${conn.id}/test`).then((r) => { if (!gone) setResult(r) }, (e) => { if (!gone) setError(errorText(e)) })
    return () => { gone = true }
  }, [conn.id, round])
  const said = result && probeText(result)
  return (
    <Modal width={520} onClose={onClose}>
      <div><div className="modal-title">Bağlantıyı dene</div>
        <div className="tx2" style={{ marginTop: 2 }}><span className="mono">{conn.name}</span> · <span className="mono">{conn.host}:{conn.port}</span> · {String(conn.transport).toUpperCase()}</div></div>
      {!result && !error && <div className="tx2">Sağlayıcıya soruluyor…</div>}
      {error && <div className="banner bad">Deneme yapılamadı: {error}</div>}
      {said && <div style={{ display: 'grid', gap: 6 }}>
        <div className="row" style={{ gap: 8 }}><Badge tone={said.tone}>{said.tone === 'ok' ? 'Çalışıyor' : said.tone === 'warn' ? 'Dikkat' : 'Sorun var'}</Badge><span style={{ fontWeight: 600 }}>{said.title}</span></div>
        <div className="tx2 pretty">{said.body}</div>
      </div>}
      <div className="tx3 pretty" style={{ fontSize: 12 }}>Sistem sağlayıcıya tek bir “orada mısın?” sorusu (SIP OPTIONS) gönderir; çağrı kurulmaz, hiçbir ayar değişmez.</div>
      <div className="modal-foot">
        <button className="btn" disabled={!result && !error} onClick={() => setRound(round + 1)}>Yeniden dene</button>
        <button className="btn primary" onClick={onClose}>Kapat</button>
      </div>
    </Modal>
  )
}

function Connections({ vendor, rows, loading, error, retry }: { vendor: Row; rows: Row[]; loading: boolean; error: any; retry: () => void }) {
  const { canEdit } = useSession()
  const [editing, setEditing] = useState<Row | 'new' | null>(null)
  const [testing, setTesting] = useState<Row | null>(null)
  const ui = useUI()
  const invalidate = useInvalidate()
  // Whether the engine finds the connections it asks reachable; it comes
  // and goes by itself.
  const health = useQuery({ queryKey: ['/v1/vendor-connections/health', { vendor_id: vendor.id }],
    queryFn: () => api.get<{ connections: Record<string, Row>; registrations?: Record<string, Row> }>('/v1/vendor-connections/health', { vendor_id: vendor.id }), refetchInterval: 15000, retry: false })
  const toggle = async (c: Row) => {
    if (c.enabled && !(await ui.confirm({ title: `${c.name} kapatılsın mı?`, body: 'Bağlantı kapatılınca bu hedefe yeni çağrı gönderilmez; rota listesinde sıradaki bağlantı kullanılır.', action: 'Kapat', danger: true }))) return
    try {
      await api.patch(`/v1/vendor-connections/${c.id}`, { enabled: !c.enabled })
      invalidate('/v1/vendor-connections')
    } catch (e) { ui.toast(errorText(e as any)) }
  }
  const limit = (n: number) => (n ? n : '—')
  const reach = (c: Row) => {
    if (!c.enabled || !c.options_interval_sec) return <span className="tx3">Denetlenmiyor</span>
    if (health.isError) return <span className="tx3" title="Motor okunamadı">bilinmiyor</span>
    const h = health.data?.connections[String(c.id)]
    if (!h || h.state === 'unknown') return <Badge>Denetleniyor</Badge>
    return h.state === 'up'
      ? <span title={`Cevap veriyor (${ago(h.since)}); son cevap ${h.last?.sip_code ?? ''}, ${h.last?.ms ?? ''} ms`}><Badge tone="ok">Erişilebilir</Badge></span>
      : <span title={`Cevap vermiyor (${ago(h.since)}); bu bağlantıya çağrı gönderilmiyor`}><Badge tone="bad">Cevap vermiyor</Badge></span>
  }
  // How the switch's registration with the vendor stands (T-14).
  const registration = (c: Row) => {
    if (!c.register) return <span className="tx3">—</span>
    if (!c.enabled) return <span className="tx3">Kapalı</span>
    if (!c.auth_username) return <span className="tx3" title="Kayıt için kullanıcı adı ve parola gerekli">Giriş bilgisi yok</span>
    if (health.isError) return <span className="tx3" title="Motor okunamadı">bilinmiyor</span>
    const r = health.data?.registrations?.[String(c.id)]
    if (!r || r.state === 'pending') return <Badge>Kaydediliyor</Badge>
    if (r.state === 'unknown') return <span className="tx3" title="SIP çekirdeğine sorulamadı">bilinmiyor</span>
    if (r.state === 'registered') return <span title={`${ago(r.since)} önce kaydoldu; sağlayıcının verdiği süre ${r.expires} sn, süresi dolmadan yenilenir`}><Badge tone="ok">Kayıtlı</Badge></span>
    const why: Record<string, string> = {
      credentials: 'Sağlayıcı kullanıcı adını ya da parolayı kabul etmedi',
      no_answer: 'Sağlayıcı kayıt isteğine cevap vermedi',
      refused: 'Sağlayıcı kaydı reddetti',
    }
    return <span title={`${why[r.reason] ?? 'Kayıt isteği gönderilemedi'} (${ago(r.since)}); sistem kısa aralıklarla yeniden dener`}><Badge tone="bad">{r.reason === 'credentials' ? 'Parola reddedildi' : 'Kayıt olmadı'}</Badge></span>
  }
  const cols: Col<Row>[] = [
    { label: 'Ad', width: '130px', mono: true, render: (c) => c.name },
    { label: 'Sunucu', width: 'minmax(170px,1fr)', mono: true, render: (c) => c.host },
    { label: 'Port', width: '60px', align: 'right', mono: true, render: (c) => c.port },
    { label: 'Taşıma', width: '70px', render: (c) => <span title={c.transport === 'tls' ? `Sertifikadaki ad: ${c.tls_name || c.host}` : undefined}>{String(c.transport).toUpperCase()}</span> },
    { label: 'Çağrı limiti', width: '90px', align: 'right', mono: true, render: (c) => limit(c.max_calls) },
    { label: 'CPS limiti', width: '80px', align: 'right', mono: true, render: (c) => limit(c.max_cps) },
    { label: 'Erişim', width: '120px', render: reach },
    { label: 'Kayıt', width: '130px', render: registration },
    { label: 'Gelişmiş ayar', width: '100px', render: (c) => (changedSettings(c) ? <Badge tone="acc">{changedSettings(c)} ayar</Badge> : <span className="tx3">varsayılan</span>) },
    { label: '', width: '170px', align: 'right', render: (c) => <span className="row" style={{ gap: 10, justifyContent: 'flex-end' }}>
      {canEdit && <button className="btn inline" onClick={() => setTesting(c)}>Dene</button>}
      {canEdit && <button className="btn inline" onClick={() => setEditing(c)}>Düzenle</button>}
      <Toggle on={c.enabled} onChange={canEdit ? () => toggle(c) : undefined} /></span> },
  ]
  const adding = editing === 'new'
  const row = adding ? null : editing
  return (
    <>
      <div className="toolbar">
        <div className="grow tx2 pretty">Çağrılar bu SIP hedeflerine gönderilir. Ad, sunucu, port ve taşıma çoğu sağlayıcı için yeterlidir; gerisi “Gelişmiş ayarlar”dadır.</div>
        {canEdit && <button className="btn primary" onClick={() => setEditing('new')}>Bağlantı ekle</button>}
      </div>
      <DataTable cols={cols} rows={rows} minWidth={1250} loading={loading} error={error} onRetry={retry}
        empty={{ title: 'Bağlantı yok', desc: 'Bu sağlayıcıya çağrı gönderebilmek için en az bir SIP bağlantısı ekleyin.' }} />
      {editing && <FormModal title={adding ? 'Bağlantı ekle' : 'Bağlantıyı düzenle'} sub={vendor.name} action={adding ? 'Ekle' : 'Kaydet'} width={640} onClose={() => setEditing(null)}
        defaults={connDefaults}
        initial={row ? {
          name: row.name, host: row.host, port: String(row.port), transport: row.transport, tls_name: row.tls_name, tech_prefix: row.tech_prefix, strip_digits: String(row.strip_digits),
          number_plus: row.number_plus, cli_strip_digits: String(row.cli_strip_digits), cli_add_prefix: row.cli_add_prefix, pai_mode: row.pai_mode,
          ring_timeout_sec: String(row.ring_timeout_sec), max_duration_sec: String(row.max_duration_sec), failover_mode: row.failover_mode,
          failover_codes: (row.failover_codes ?? []).join(', '), options_interval_sec: String(row.options_interval_sec), codecs: row.codecs ?? [], transcode: !!row.transcode,
          auth_username: row.auth_username, max_calls: String(row.max_calls), max_cps: String(row.max_cps),
          register: !!row.register, register_expires_sec: String(row.register_expires_sec ?? 3600), register_domain: row.register_domain ?? '',
        } : { port: '5060', transport: 'udp', ...connDefaults }}
        fields={connectionFields(row)}
        validate={(f) => {
          const codes = codeList(f.failover_codes)
          const user = String(f.auth_username ?? '').trim()
          const renamed = !!row?.auth_set && user !== row.auth_username
          return {
            port: int(f.port) >= 1 && int(f.port) <= 65535 ? undefined : '1–65535 arası bir port.',
            strip_digits: /^\d*$/.test(String(f.strip_digits ?? '')) ? undefined : 'Yalnızca rakam.',
            tech_prefix: /^[0-9#*]*$/.test(f.tech_prefix ?? '') ? undefined : 'Yalnızca rakam, # ve *.',
            cli_strip_digits: /^\d*$/.test(String(f.cli_strip_digits ?? '')) && int(f.cli_strip_digits) <= 32 ? undefined : '0–32 arası bir sayı.',
            cli_add_prefix: /^\+?\d{0,16}$/.test(f.cli_add_prefix ?? '') ? undefined : 'Yalnızca rakam; başında + olabilir.',
            ring_timeout_sec: secondsError(f.ring_timeout_sec, 5, 180),
            max_duration_sec: secondsError(f.max_duration_sec, 5, 7200),
            failover_codes: f.failover_mode !== 'custom' ? undefined
              : codes.length === 0 ? 'En az bir kod yazın ya da başka bir seçenek seçin.'
              : codes.some((c) => !/^[3-6]\d\d$/.test(c)) ? '300–699 arası SIP kodları, virgülle ayrılmış.' : undefined,
            auth_username: /[\s"]/.test(user) ? 'Boşluk ve tırnak içeremez.' : undefined,
            auth_password: user !== '' && !f.auth_password && (!row?.auth_set || renamed) ? (renamed ? 'Kullanıcı adını değiştirirken parolayı yeniden girin.' : 'Kullanıcı adı yazdıysanız parola da gerekli.')
              : user === '' && f.auth_password ? 'Parola için kullanıcı adı da gerekli.' : undefined,
            max_calls: /^\d*$/.test(String(f.max_calls ?? '')) ? undefined : 'Yalnızca rakam.',
            max_cps: /^\d*$/.test(String(f.max_cps ?? '')) ? undefined : 'Yalnızca rakam.',
            register: f.register && user === '' ? 'Kayıt için yukarıya kullanıcı adı ve parola girin.' : undefined,
            register_expires_sec: !f.register || (/^\d+$/.test(String(f.register_expires_sec ?? '')) && int(f.register_expires_sec) >= 60 && int(f.register_expires_sec) <= 86400) ? undefined : '60–86400 arası bir sayı.',
            register_domain: !f.register || /^[A-Za-z0-9.-]{0,253}$/.test(String(f.register_domain ?? '').trim()) ? undefined : 'Alan adı ya da IPv4 adresi.',
          }
        }}
        onSubmit={async (f) => {
          const body = {
            name: text(f.name), host: text(f.host), port: int(f.port, 5060), transport: f.transport, tls_name: f.transport === 'tls' ? text(f.tls_name) ?? '' : '',
            tech_prefix: f.tech_prefix ?? '', strip_digits: int(f.strip_digits), number_plus: !!f.number_plus,
            cli_strip_digits: int(f.cli_strip_digits), cli_add_prefix: f.cli_add_prefix ?? '', pai_mode: f.pai_mode,
            ring_timeout_sec: int(f.ring_timeout_sec), max_duration_sec: int(f.max_duration_sec),
            failover_mode: f.failover_mode, failover_codes: f.failover_mode === 'custom' ? codeList(f.failover_codes).map(Number) : [],
            options_interval_sec: int(f.options_interval_sec), codecs: f.codecs ?? [], transcode: !!f.transcode, max_calls: int(f.max_calls), max_cps: int(f.max_cps),
            register: !!f.register, register_expires_sec: f.register ? int(f.register_expires_sec, 3600) : (row?.register_expires_sec ?? 3600),
            register_domain: f.register ? String(f.register_domain ?? '').trim() : (row?.register_domain ?? ''),
          }
          const saved: Row = row ? await api.patch(`/v1/vendor-connections/${row.id}`, body) : await api.post('/v1/vendor-connections', { ...body, vendor_id: vendor.id })
          // The name and password to sign in with have an operation of
          // their own: the password is stored sealed and never read back.
          const user = String(f.auth_username ?? '').trim()
          try {
            if (user !== '' && f.auth_password) await api.put(`/v1/vendor-connections/${saved.id}/sign-in`, { username: user, password: f.auth_password })
            else if (user === '' && (row?.auth_set || row?.auth_username)) await api.del(`/v1/vendor-connections/${saved.id}/sign-in`)
          } catch (e) {
            // The connection itself is saved: the form closes, so that
            // sending it again cannot add a second one.
            invalidate('/v1/vendor-connections')
            setEditing(null)
            ui.toast(`Bağlantı kaydedildi, ama giriş bilgileri kaydedilemedi: ${errorText(e as any)}`)
            return
          }
          invalidate('/v1/vendor-connections')
          setEditing(null)
          ui.toast(adding ? 'Bağlantı eklendi' : 'Bağlantı güncellendi')
        }} />}
      {testing && <ConnectionTest conn={testing} onClose={() => setTesting(null)} />}
    </>
  )
}

function GeneralForm({ vendor, tariffs }: { vendor: Row; tariffs: Row[] }) {
  const { canEdit } = useSession()
  const initial = () => ({ ...vendor, tariff_id: vendor.tariff_id ?? '', email: vendor.email ?? '', tax_id: vendor.tax_id ?? '', address: vendor.address ?? '' })
  const [v, setV] = useState<Row>(initial)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const ui = useUI()
  const invalidate = useInvalidate()
  useEffect(() => setV(initial()), [vendor]) // eslint-disable-line react-hooks/exhaustive-deps
  const set = (k: string, val: any) => setV((old) => ({ ...old, [k]: val }))
  const bind = (k: string) => ({ value: v[k] ?? '', onChange: (e: { target: { value: string } }) => set(k, e.target.value) })
  const save = async () => {
    if (!text(v.name)) return setError('Ad boş olamaz.')
    setBusy(true)
    setError('')
    try {
      await api.patch(`/v1/vendors/${vendor.id}`, {
        name: text(v.name), email: text(v.email), tax_id: text(v.tax_id), address: text(v.address), currency: v.currency, status: v.status, tariff_id: idOrNull(v.tariff_id),
      })
      invalidate('/v1/vendors')
      ui.toast('Sağlayıcı kaydedildi')
    } catch (e) { setError(errorText(e as any)) }
    setBusy(false)
  }
  return (
    <>
      <fieldset disabled={!canEdit}><div className="form-grid" style={{ gridTemplateColumns: 'repeat(auto-fit,minmax(320px,1fr))' }}>
        <div className="form-col">
          <div className="sect">Kimlik</div>
          <Field label="Ad"><input className="input" {...bind('name')} /></Field>
          <Field label="E-posta"><input className="input" {...bind('email')} /></Field>
          <Field label="Vergi no"><input className="input mono" {...bind('tax_id')} /></Field>
          <Field label="Adres"><textarea className="textarea" rows={2} {...bind('address')} /></Field>
        </div>
        <div className="form-col">
          <div className="sect">Hesap</div>
          <Field label="Para birimi"><Seg value={v.currency} options={currencies} onChange={(x) => set('currency', x)} /></Field>
          <Field label="Durum"><Seg value={v.status} options={[{ value: 'active', label: 'Aktif' }, { value: 'suspended', label: 'Askıda' }]} onChange={(x) => set('status', x)} /></Field>
          <Field label="Alış tarifesi"><select className="select" {...bind('tariff_id')}><option value="">—</option>{tariffs.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}</select></Field>
          <div className="field"><span className="label">Sağlayıcıya borcumuz</span>
            <div className="readonly-box">{money(vendor.balance)} {vendor.currency}</div>
            <span className="hint">Elle düzenlenemez; yalnızca ödeme, düzeltme ve iade kaydıyla değişir.</span>
          </div>
        </div>
      </div></fieldset>
      {error && <div className="banner bad" style={{ margin: '0 20px 12px' }}>{error}</div>}
      {canEdit && <div className="form-foot">
        <button className="btn" onClick={() => { setV(initial()); setError('') }}>Vazgeç</button>
        <button className="btn primary" disabled={busy} onClick={save}>Kaydet</button>
      </div>}
    </>
  )
}
