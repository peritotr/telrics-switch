# Yol Haritası

Her faz bir "bitti" ölçütüyle kapanır. Ölçüt SIPp ile otomatik doğrulanır; faz kapanmadan sonrakine geçilmez.

Sürüm ayrımı (`docs/04-acik-cekirdek-modeli.md`): **Faz 0-3 Community** (açık `core` deposu, tek düğüm), **Faz 4 ve sonrası ağırlıklı olarak Enterprise** (özel depo). Community'nin ilk açık sürümü Faz 3 sonunda yayınlanır.

## Faz 0 — Laboratuvar ve yük testi düzeneği (tamamlandı, 2026-11-21)

- Depoyu lisans dosyası, lisans başlıkları ve genişleme arayüzleri için `pkg/ext/` paketiyle başlat.

- `docker compose` ile: OpenSIPS, rtpengine, PostgreSQL, Redis, ClickHouse, NATS, SIPp (arayan ve cevaplayan).
- OpenSIPS'te en basit geçiş: gelen çağrıyı sabit bir hedefe, medya rtpengine üzerinden.
- `make lab-up`, `make test-call`, `make load CPS=50 CC=1000` komutları.
- **Bitti:** tek komutla ortam kalkıyor, SIPp 1.000 eşzamanlı çağrıyı hatasız tamamlıyor, sonuç raporu üretiliyor.
- Sonuç: 50 CPS, 1.000 eşzamanlı, 3.000 çağrı, 0 hata, 0 yeniden iletim. Test yalnızca sinyalleşme ve rtpengine oturum kurulumunu kapsar; RTP paketi akıtılmadı.
- Öne çekilen iş: yönetim API'si ve yönlendirme karar motoru (Faz 1-3'ün API tarafı) bu depoda; bayi, fatura ve DID (Faz 6'nın API tarafı) Enterprise deposunda yazıldı.

## Faz 1 — Yönlendirme çekirdeği (tamamlandı, 2026-11-21; ClickHouse hariç)

- route-engine: IP ile müşteri tanıma, önek (prefix) tabanlı en uzun eşleşme, LCR sıralaması, numara dönüşümü.
- OpenSIPS: route-engine'e sorgu, hata kodlarına göre sıradaki sağlayıcıya geçiş, topology hiding.
- Veri modeli: müşteri, sağlayıcı, bağlantı (trunk), tarife, yönlendirme planı.
- CDR: OpenSIPS → NATS → ClickHouse.
- **Bitti:** 3 sağlayıcılı senaryoda ilk ikisi hata verince üçüncüye geçiyor; her deneme CDR'de görünüyor; karar süresi p99 < 10 ms (1 milyon önekle).
- Sonuç: `make test-routing` geçiyor (yedek rotaya geçiş, ret, limit, bakiye bitince kapatma). `make load CPS=200 CC=2000`: 6.000 çağrı, 0 hata; karar süresi p50 0,2 ms, p99 0,6 ms (Mac, Docker). 1 milyon önekle bellekteki karar ~1 µs (`go test -bench Authorize ./pkg/routing`). Arayüz: `docs/08-cagri-arayuzu.md`.
- Faz 2'den öne çekildi: müşteri ve sağlayıcı bazında eşzamanlı çağrı ve CPS limitleri, süre dolunca çağrıyı kapatma, rater servisi.
- Ertelendi: CDR'lerin ClickHouse'a yazılması (şimdilik NATS → rater → PostgreSQL); yapılandırma değişikliğinin bildirimle alınması (şimdilik aralıklı yenileme).

## Faz 2 — Faturalama ve limitler

- Tarife motoru: ilk süre/artım (ör. 60/1), bağlantı ücreti, zaman dilimi.
- Prepaid bakiye rezervasyonu, azami çağrı süresi, bakiye bitince çağrıyı sonlandırma. **Tamam (2026-11-21):** 60 saniyelik adımlarla rezervasyon, motorun çağrıyı kapatması; 6.000 çağrılık testte bakiye CDR toplamına eşit, test sonunda ayrılmış tutar kalmıyor.
- CDR'lerin ClickHouse'a akışı; yapılandırma değişikliği bildirimi (Faz 1'den devreden). **Tamam (2026-11-21):** API yazınca motor görüntüyü hemen yeniliyor; her CDR ClickHouse'a kopyalanıyor (29.474 kayıtta sayı ve tutarlar PostgreSQL ile aynı). Raporların ClickHouse'tan okunması Faz 3'e kaldı.
- **Bitti:** 10.000 çağrılık testte bakiye toplamı CDR toplamıyla kuruşu kuruşuna eşit; limit aşan çağrılar doğru SIP koduyla reddediliyor.

## Faz 3 — Yönetim API'si ve arayüz

- REST API (OpenAPI tanımlı), rol tabanlı yetki.
- Web: müşteri/sağlayıcı yönetimi, CSV tarife yükleme, yönlendirme planı, canlı çağrılar, CDR arama, ASR/ACD/PDD raporları.
- **Bitti:** bir müşteri sıfırdan arayüzle tanımlanıp çağrı yapabiliyor.
- **Tamam (2026-11-22):** yönetim API'si ve paneli; kullanıcılar ve roller (e-posta ve parolayla giriş), canlı izleme ve canlı çağrılar, düşük bakiye uyarıları, CDR arama ve CSV dışa aktarma, özet raporunun ClickHouse'tan okunması, e-posta gönderimi (SMTP ya da Resend). Eksik kalanlar: OpenAPI tanımı, PDD'nin geçmiş raporlarda yer alması (yalnızca canlı izlemede var), zamanlanmış raporlar.

## Faz 4 — Yüksek erişilebilirlik ve 20.000 çağrı

- Çoklu OpenSIPS düğümü, dialog çoğaltma, sanal IP.
- rtpengine havuzu, Redis ile çağrı durumu paylaşımı, düğüm kaybında devralma.
- PostgreSQL (Patroni), Redis ve NATS kümeleri.
- Fiziksel Linux sunucularda yük testi.
- **Bitti:** 20.000 eşzamanlı çağrı, 1.000 CPS, 24 saat; paket kaybı < %0,1; bir SIP ve bir medya düğümü kapatıldığında kurulu çağrılar düşmüyor.

## Faz 5 — Operatör özellikleri

- Kalite tabanlı yönlendirme (ASR/ACD/PDD'ye göre sağlayıcıyı geriye alma).
- Dolandırıcılık tespiti (ani trafik artışı, pahalı hedefler).
- Numara taşınabilirliği sorgusu, TLS/SRTP, transcoding havuzu.
- SIP izleme (HEP → Homer), Prometheus/Grafana, alarm.

## Faz 6 — Ürünleştirme

- Bayi (reseller) hiyerarşisi, fatura üretimi, müşteri portalı.
- Tek sunucu kurulum paketi, lisanslama, yükseltme süreci.
- **Durum (2026-11-22):** bayi hiyerarşisi, faturalama, DID, müşteri portalı ve lisanslama Enterprise sürümündedir.

## Geliştirme listesi (2026-11-22)

Eksik kalan işlerin tamamı, öncelik sırasıyla. Büyüklük: **K** küçük, **O** orta, **B** büyük. "Bekleyen" sütunu işin başlaması için dışarıdan gelmesi gerekeni gösterir. Enterprise'a özgü işler burada izlenmez.

### A. Kanıt ve işletilebilirlik — satıştan önce şart

| No | İş | Büyüklük | Bekleyen |
|---|---|---|---|
| A1 | Linux sunucuda yük testi: 1.000 CPS, 20.000 eşzamanlı çağrı, çekirdek (kernel) medya aktarımı. Bugüne kadarki bütün ölçümler Mac üzerinde Docker'dan (200 CPS, 2.000 çağrı) | O | Sunucu |
| A2 | Yüksek erişilebilirlik, ilk adım: aktif/yedek SIP düğümü (dialog çoğaltma, sanal IP), PostgreSQL ve Redis yedeği, NATS kümesi; düğüm kaybında kurulu çağrıların düşmediğinin senaryosu | B | Sunucu (en az iki) |
| A3 | ~~Kurulum paketi ve yükseltme süreci~~ — **tamam (2026-11-22)**, Docker Compose ile: `docs/09-kurulum.md`. Özel Docker ağında uçtan uca denendi (`make prod-smoke`); üretim yerleşimi (sunucunun ağı) ilk Linux sunucuda denenecek | O | Linux sunucu (doğrulama) |
| A4 | ~~İzleme~~ — **tamam (2026-11-22)**: Prometheus ölçüleri (motor, rater, API, OpenSIPS, rtpengine), alarm kuralları ve Alertmanager, HEP ile çağrı izi: `docs/10-izleme.md`. Kalan: alıcı tanımı (kullanıcıda), Grafana paneli, gerçek Homer ile deneme, eşiklerin gerçek trafikle ayarı | O | — |
| A5 | Gerçek hesaplarla denenmemiş sürücüler: Resend ile e-posta (Enterprise'daki numara sağlayıcısı sürücüleri de) | K | Hesaplar |
| A6 | Enterprise motorunun ve rater'ının laboratuvarda çalıştırılması: bayi kuralları ve lisans kapasitesi gerçek çağrıyla | K | — |
| A7 | Pilot kurulum: gerçek bir trafikle, gerçek bir müşteride | B | A1–A4, pilot müşteri |

### B. Kenar ve güvenlik

| No | İş | Büyüklük | Bekleyen |
|---|---|---|---|
| B1 | ~~TLS (sinyalleşme) ve SRTP (ses)~~ — **tamam (2026-11-22)**: ayrıntı ve sınırlar `docs/08-cagri-arayuzu.md`, "Şifreleme" | O | — |
| B1b | ~~Üretim sertifikaları~~ — **tamam (2026-11-22)**: sertifikanın kendiliğinden alınması ve yeniden başlatmadan yüklenmesi, sağlayıcıların kökler dizinine göre doğrulanması, trunk başına "TLS zorunlu" ve istemci sertifikası, DTLS-SRTP teklif eden arayan. Ayrıntı ve sınırlar: `docs/08-cagri-arayuzu.md` "Şifreleme", `docs/09-kurulum.md` "SIP sertifikası" | O | — |
| B1c | Şifrelemede kalanlar. **Tamam (2026-11-22):** sağlayıcı sertifikasında ad denetimi (motorda; sınırları `docs/08-cagri-arayuzu.md`), sertifika bitiş alarmı. **Kalan:** gerçek sertifika otoritesiyle deneme, ad denetiminin çağrının kendi bağlantısında yapılması, istemci sertifikasında alternatif adlar ve iptal listeleri, sağlayıcıya DTLS-SRTP | K | Gerçek sunucu ve alan adı |
| B2 | Adres çevirisi (NAT) arkasındaki arayanlar ve kayıt (REGISTER); bununla birlikte gelen DID çağrısının müşteriye yönlendirilmesi. **Tamam (2026-11-23):** kayıt (motorda, Redis'te; süre sınırları, trunk başına adres sayısı, panelde görme ve silme, denetim kaydı), adres çevirisi arkasındaki müşterinin sinyalleşmesi (cevaplar, çağrı içi istekler ve SIP çekirdeğinin kendi BYE'ı paketlerin geldiği adrese; `fix_nated_contact` kullanılmadan) ve sesi (gerçek RTP ile sınandı), müşterinin kayıtlı trunk'ına çağrı teslimi mekanizması (`routing.ToTrunk`, `engine.Config.Adjust`); ayrıntı ve sınırlar `docs/08-cagri-arayuzu.md`, "Kayıt (REGISTER)", "Adres çevirisi (NAT) arkasındaki uçlar", "Müşterinin trunk'ına çağrı". **Kalan:** gelen DID çağrısının müşteriye yönlendirilmesi (Enterprise E16): çağrı kaydına numaranın yazılması ve Enterprise'taki eşleme (müşteri olmayan kaynaktan gelen çağrıyı bir müşteri adına alan genişleme noktası **tamam**, 2026-11-23, T-04: `engine.Config.Incoming`, ADR-0032); canlı tutma (OPTIONS) gönderimi (şimdilik kısa süreli kayıt); gerçek bir yönlendiriciyle (simetrik çeviri dahil) deneme; adres çevirisi arkasındaki sağlayıcı. IPv6 ile kayıt ve kayda çağrı **tamam** (2026-11-23, T-28). Çağrı kaydında hedef trunk **tamam** (2026-11-23, T-05: `cdrs.dst_trunk_id`, ClickHouse'ta da; uzantının kendi kayıt alanı `AllowCDRField` ile uçtan uca sınandı). Teslim edilen çağrıda hedef trunk'ın limitleri ve kodekleri **tamam** (2026-11-23, T-06). Trunk'ın ve müşterinin sınırlarının iki yönü birlikte sayması, teslim edilen çağrıya hedef müşterinin sınırlarının uygulanması **tamam** (2026-11-23, T-45, ADR-0042). TLS ile kayıtlı trunk'a çağrı ve SRTP **tamam** (2026-11-23, T-07: çağrı yalnızca kaydın bağlantısından gider, yeni bağlantı açılmaz; TLS ile kayıtlıya SRTP, UDP ile kayıtlıya düz RTP) | B | — |
| B3 | ~~Trunk başına birden çok adres ve ağ (CIDR)~~ — **tamam (2026-11-22)**: `client_trunks.addresses` (IPv4 ve IPv6, tek adres ve ağ), en dar ağ kazanır, eşitlikte en uzun teknik önek; eşleşme kuralı `docs/08-cagri-arayuzu.md`, "Kaynağın trunk ile eşleşmesi". IPv6 ile gerçek çağrı ve kayıt **tamam** (2026-11-23, T-28, ADR-0047: laboratuvarın IPv6 ağında IPv6 ağ eşleşmesi, IPv4 ↔ IPv6 ses, IPv6 ile kayıt ve kayda çağrı; `docs/08-cagri-arayuzu.md`, "IPv6"). **Kalan:** IPv6 gerçek sunucuda denenmedi; listede adrese göre arama süzgeci yok; müşteri portalı (Enterprise) yalnızca tek adresli trunk'ın adresini değiştirtir, liste ve ağları yalnızca gösterir | K | — |
| B4 | ~~Motor arayüzünün kimlik doğrulaması~~ — **tamam (2026-11-22)**: ortak sır (`ENGINE_TOKEN`), `Authorization: Bearer` başlığı, sabit süreli karşılaştırma, sırsız isteğe 401; SIP çekirdeği, API ve betikler sırrı gönderir; kurulum sırrı üretir, yükseltme eksikse ekler; ayrıntı `docs/08-cagri-arayuzu.md`, "Kimlik doğrulama". **Kalan:** sır ağda şifresiz gider (arayüzde TLS yok); SIP çekirdeğinin yönetim soketi (`MI_SOCKET`), rtpengine'in denetim soketi, Redis ve NATS hâlâ kimlik doğrulamasız ve özel ağ varsayımına dayanıyor; `/metrics` bilinçli olarak açık; tek ortak sır (arayan başına ayrı sır ya da salt okunur sır yok), kesintisiz sır değişimi yok; üretim yerleşiminde (sunucunun ağı) gerçek sunucuda denenmedi | K | — |
| B5 | ~~Sahte kaynak adresli taşkına karşı ağ katmanı rehberi ve örnek güvenlik duvarı kuralları~~ — **tamam (2026-11-23, T-10)**: `docs/09-kurulum.md`, "Ağ katmanı koruması"; örnek nftables kuralları ve paneldeki trunk/sağlayıcı adreslerinden bilinen adres kümesini üreten betik `deploy/prod/nftables/` içinde (kurulum uygulamaz), `make test-nft`. **Kalan:** gerçek sunucuda ve taşkınla deneme, oranların ölçülmesi; paneldeki kuralların sunucunun güvenlik duvarına yansıtılması (T-27) | K | — |
| B6 | ~~Kod dönüştürme (transcoding)~~ **tamam (2026-11-23, T-09; ADR-0041)**: bağlantıda ve trunk'ta `transcode` ayarı (varsayılan kapalı; panelde "Gelişmiş ayarlar → Ses kodekleri"), listeleri örtüşmeyen trunk ile bağlantı (ya da hedef trunk) arasında ses rtpengine'de çevrilir; motor rotanın isteğe bağlı `transcode` alanıyla bildirir (arayüz v1), çağrı içindeki yeni teklif iki yönde doğru kodekle geçer; aynı anda çevrilen çağrılar `TRANSCODE_MAX` ile sınırlı ve kabul betiğinde sayılır (ek Redis gidişi yok); ölçüler ve `TranscodingAtLimit` alarmı; `make test-routing` "transcode" (gerçek RTP iki yönde) ve `TRANSCODE=1 make load`. **Kalan:** teklife (SDP) göre karar (listesi olmayan trunk için); G.729/Opus ile ve Linux sunucuda ölçüm; çeviri havuzu (Enterprise) | O | — |
| B7 | ~~Denetim kaydı (kim neyi ne zaman değiştirdi), panel kullanıcıları için iki adımlı doğrulama~~ **tamam** (2026-11-22; `docs/06-api.md`, "Denetim kaydı" ve "İki adımlı doğrulama"). İki adımlı doğrulamayı hesaptaki bütün kullanıcılar için zorunlu kılma ve denetim kaydının CSV dışa aktarımı **tamam** (2026-11-23, T-23, ADR-0045). **Kalan:** SSO (tek oturum açma: OIDC/SAML); denetim kaydının syslog/SIEM aktarımı (T-47); Enterprise'da DID ve portal kullanıcısı işlemlerinin ayrıntılı kaydı (şimdilik yalın kayıt düşülüyor); müşteri portalındaki işlemlerin kaydı | O | SSO için kimlik sağlayıcı seçimi |
| B8 | ~~OpenSIPS 3.6.10'da iki komutla (`proxy_challenge`, `fix_nated_contact`) görülen sorunun nedeni~~ — **tamam (2026-11-23, T-11)**: neden komutların kendisi değil; bu sürümde `async()` öncesinde isteği değiştiren komut kullanılmaz, değişiklik devam rotasında yapılır (`docs/ARCHITECTURE.md` §4). **Kalan:** bu kuralın otomatik denetimi ve iki komutun yeniden değerlendirilmesi (T-48) | K | — |

### C. Yönlendirme, ücretlendirme ve raporlama — pazardaki açıklar

| No | İş | Büyüklük | Bekleyen |
|---|---|---|---|
| C1 | Kalite tabanlı yönlendirme: ASR/ACD/PDD'ye göre sağlayıcıyı geriye alma. **Enterprise özelliği** (tamam, 2026-11-22); Community tarafındaki genişleme noktaları tamam (2026-11-22): `engine.Config.Adjust` ve `OnLeg` | O | — |
| C2 | ~~Ağırlıklı (yüzde) trafik dağıtımı~~ **tamam** (2026-11-22) | K | — |
| C3 | Dolandırıcılık alarmları: ani trafik artışı, pahalı hedefler, gece trafiği; otomatik askıya alma. **Enterprise özelliği** (tamam, 2026-11-22); aynı genişleme noktalarını kullanır | O | — |
| C4 | ~~Arayan numara (CLI) kuralları, kara ve beyaz liste~~ **tamam** (2026-11-22). Bağlantıya özel dönüşüm C11 ile geldi. `P-Asserted-Identity`'ye göre kural, numarasız arayanı tek başına reddetme (433) ve `Privacy: id` ile kimliğin sağlayıcıya gitmemesi **tamam** (2026-11-23, T-16). Trunk başına birden çok numara kuralı **tamam** (2026-11-23, T-17) | O | — |
| C5 | Numara taşınabilirliği (MNP/LRN) sorgusu | O | Sorgu kaynağı |
| C6 | Zamana göre fiyat ve yönlendirme, paket dakikalar — **tamam** (2026-11-22; `docs/08-cagri-arayuzu.md`, "Güne ve saate göre fiyat ve rota" ve "Paket dakikalar"). Resmî tatil takvimi ve CSV yüklemede pencere tamam (2026-11-23, T-19, ADR-0044). **Kalan:** paketlerde devreden dakika, taahhüt ve paket ücreti | O | — |
| C7 | ~~Çoklu para birimi ve kur dönüşümü~~ **tamam** (2026-11-22). Kurların Avrupa Merkez Bankası'ndan alınması tamam (2026-11-23, T-20, ADR-0036; isteğe bağlı, `FX_SOURCE=ecb`). Kalan: CSV dışa aktarımında para birimi sütunu | O | — |
| C8 | Raporlar. **Tamam (2026-11-22):** geçmiş raporlarda PDD ve NER (çağrı kaydına `pdd_ms` yazılıyor; önceki kayıtlarda yok). Çağrı kayıtları listesinin ClickHouse'tan okunması (`CDR_LIST_SOURCE`) ve PostgreSQL'deki eski kayıtların arşivlenmesi (`CDR_KEEP_DAYS`), ikisi de isteğe bağlı ve varsayılan olarak kapalı (`docs/09-kurulum.md`). Zamanlanmış raporlar (günlük, haftalık, aylık özet e-postası). Arşivlenen kayıtların tekil görünümü, listesi ve CSV'si ClickHouse'tan **tamam** (2026-11-23, T-24). **Kalan:** büyük veriyle süre ölçümü | O | — |
| C9 | ~~OpenAPI tanımı~~ **tamam** (2026-11-22): `GET /v1/openapi.json`, sunucunun kendi kayıtlarından üretilir. Özel işlemlerin istek ve cevap şemaları **tamam** (2026-11-23, T-21): Go türlerinden üretilir, Redocly denetimi (`make test-openapi`) hatasız ve uyarısız. Kalan: Enterprise uçlarının şemaları (genişleme noktası hazır) | K | — |
| C10 | STIR/SHAKEN | B | ABD trafiği kararı |
| C11 | ~~Bağlantı ayarları, "Temel" ve "Gelişmiş" olarak~~ **tamam** (2026-11-23). Sağlayıcı bağlantısı: sağlayıcıya giriş (Digest; parola `SECRET_KEY` ile şifreli), numara biçimi (`+`), bağlantıya özel arayan numara ve kimlik başlığı, çalma süresi sınırı, en uzun çağrı süresi, hangi cevaplarda sıradaki rota, sağlayıcıyı yoklama (motor OPTIONS gönderir, cevap vermeyeni karardan çıkarır) ve "Bağlantıyı dene", kodek listesi ve sırası. Müşteri trunk'ı: numara yazımı, en uzun süre, trunk limitleri, kodekler. Panelde formlar temel alanlar ve kapalı gelen "Gelişmiş ayarlar" bölümü olarak ikiye ayrıldı. Her ayarın `make test-routing` içinde gerçek çağrılı senaryosu var; ayrıntı ve sınırlar `docs/08-cagri-arayuzu.md`, "Bağlantı ve trunk ayarları". Bu iş sırasında bulunan ve düzeltilen eski hata: yedek rotaya geçen çağrının ikinci ve sonraki denemeleri ses teklifini (SDP) iki kez taşıyordu; teklif artık her denemenin kendi isteğinde (`branch_route`) yazılıyor. Sağlayıcıya girişte `qop=auth` (401 ve 407), SHA-256 ve SHA-512-256 **tamam** (2026-11-23, T-15); yalnızca `qop=auth-int` isteyen sağlayıcı desteklenmez (sınır `docs/08`). **Kalan:** kodek çevirisi (B6); ~~sağlayıcıya kayıt (REGISTER) olma~~ (tamam, T-14: SIP çekirdeği `uac_registrant` ile kendi adresinden kaydolur, ADR-0035); ~~trunk başına birden çok numara kuralı~~ (tamam, T-17); ~~yoklamanın SIP çekirdeğinin adresinden ve portundan gönderilmesi~~ (yapılmayacak: yoklama motorda kalır, ADR-0035); Enterprise müşteri portalında bu ayarların gösterimi | O | — |

### T. Araçlar — panelde işletim araçları (2026-11-22'de eklendi)

Pazardaki ürünlerin çoğunda bulunan, operatörün sunucuya girmeden iş görmesini sağlayan araçlar. Panelde "Araçlar" menüsü altında toplanır; tek sunuculuk hâlleri Community'dedir.

| No | İş | Büyüklük | Bekleyen |
|---|---|---|---|
| T1 | ~~Sistem kaynakları: işlemci, yük, bellek, disk, ağ ve servislerin durumu~~ **tamam** (2026-11-22; `docs/10-izleme.md`, "Paneldeki sistem sayfası"). Kalan: göstergelerin gerçek Linux sunucuda tarayıcıda görülmesi, konteyner başına kaynak kullanımı | K | — |
| T2 | Güvenlik duvarı, uygulama düzeyinde — **tamam** (2026-11-22; `docs/08-cagri-arayuzu.md`, "Güvenlik duvarı"): engellenen adreslerin görülmesi, elle engelleme ve engel kaldırma, kalıcı izin ve yasak listeleri. **Kalan:** İşletim sistemi düzeyi (nftables) sonraya bırakıldı: konteynere ağ yönetimi yetkisi gerektirir ve yanlış kuralla sunucuya erişimi kesebilir | O | — |
| T3 | Çağrı izi — **tamam** (2026-11-22; `docs/10-izleme.md`, "Çağrı izi"): sinyalleşmenin saklanması (kendi HEP toplayıcımız → ClickHouse), çağrı detayında SIP akışı, "Araçlar → Çağrı izi", aynı veriden pcap indirme. Canlı çağrılar ekranından süren çağrının izine geçiş **tamam** (2026-11-23, T-25). Yalnızca sinyalleşme; ses (RTP) kaydı kapsam dışı (CPS'ten feragat demek; ileride ayrı modül olabilir). Müşterinin reddedilen çağrılarının izi **tamam** (2026-11-23, T-26, ADR-0046; yabancıların istekleri ve müşteri tarafındaki 401 izlenmez). **Kalan:** gerçek trafikte disk boyutu ve 1.000 CPS'te toplayıcı ölçümü (A1 ile); Enterprise'da alt hesabın izde üst hesabın sağlayıcı adreslerini görmesinin sınırlanması | O | — |

### D. Bilinen teknik borç

| No | İş | Büyüklük |
|---|---|---|
| D1 | ~~Yapılandırmanın yalnızca değişen kısmının yüklenmesi~~ — **tamam (2026-11-24, T-30, ADR-0048)**: 5 milyon fiyatlı tarifede tam yükleme 7,5–9,8 sn ve 2,7 GiB ayırıyordu (eski görüntüyle en yüksek 3,8 GiB); her 10 sn'lik yenilemede saniyede 1.000 kararda p99 23 ms. Fiyatlar artık veritabanı tetikleyicisinin notlarıyla yalnızca değişen önekleriyle okunur (değişiklik yokken 10 ms, önek değişikliğinde ~0,25 sn, yük altında p99 5–6 ms); her `RELOAD_FULL_SEC` tam yükleme. **Kalan:** yükleme süren ~1 sn içinde karar p99 bütçenin üstünde (büyük haritanın kopyası); büyük deyim ve geçerliliği başlayan fiyat tarifeyi bütünüyle okur | O |
| D2 | Birden çok motor düğümünde bakiye görüntüsünün ve canlı sayaçların ortaklaştırılması | O |
| D3 | Rezervasyon: motor ya da Redis 10 saniyeden uzun kesilirse çağrılar korumasız sürüyor; süren çağrı sonradan yüklenen parayla uzamıyor. NATS kesikken motorun çökmesiyle kaybolan çağrı kayıtları ve bitişi bildirilmeyen çağrının kaydı **tamam** (2026-11-23, T-33, ADR-0029, içeriği ADR-0040: kayıtlar motorun diskindeki günlükte NATS'ı bekler, açılışta yeniden gönderilir; durumu düşen çağrı ücretsiz kaydedilir; senaryolar `make test-journal`). Kalan: motor/Redis kesintisinde korumasız süren çağrılar; bitişi bildirilmeyen çağrının durumu düşene kadar sayılı ve parası ayrılmış kalması | O |
| D4 | Eksik senaryolar: 60 saniyeyi aşan çağrıda rezervasyon uzatma (iki tam adım, çağrı sonunda ücrete inen ve düşümden sonra bırakılan tutar, ikinci adımda parası biten müşterinin motorca kapatılması) **tamam** (2026-11-23, T-03). Arayanın vazgeçmesi (CANCEL) **tamam** (2026-11-23, `docs/TASKS.md` T-01); ACK'lenmeyen cevabın 32 saniyede kapatılması **tamam** (2026-11-23, T-41); çağrı içi yeni teklif (re-INVITE: iki yön, bekletme ve geri alma, kodek listesi, NAT arkasındaki taraf, ret, süre ve kaydın değişmemesi) **tamam** (2026-11-23, T-02); teklifsiz INVITE'ın (teklif 200'de, cevap ACK'te) ilk INVITE'ta ve çağrı içinde iki yönde medya aktarıcısından geçmesi, TLS sağlayıcının şifreli teklifine arayan tarafında düz RTP, cevapsız ACK'te ve aktarıcının 200'deki teklifi almamasında çağrının kapatılması **tamam** (2026-11-23, T-43, ADR-0043); sesi kesilen (ölü) çağrının medya zaman aşımıyla iki taraftan kapatılması, bekletilen çağrının uzun süresi ve oturum zamanlayıcısı başlıklarının değişmeden geçmesi **tamam** (2026-11-23, T-38, ADR-0027); T.38'e geçen yeni teklifin iki yönde ve kodek listesine rağmen olduğu gibi geçmesi, SIP INFO tuşlarının değişmeden iletilmesi, UDPTL akan faks çağrısının medya zaman aşımıyla kapatılmaması **tamam** (2026-11-23, T-40, ADR-0031; DTMF dönüşümü yok); sağlayıcının 100rel'li 183'ünün erken medyasıyla ve PRACK'in iki yönde geçmesi, 100rel'siz SDP'li 183, çağrı içi REFER'in iki yönden 403 ile reddedilip sayılması **tamam** (2026-11-23, T-42, ADR-0028); sessiz erken medyalı çağrının bağlantının çalma süresinden önce, medya zaman aşımında bırakılması **karar** (ADR-0038) ve senaryo ("deadcall", T-44); medya aktarıcısı durmuşken ya da portu kalmamışken yeni çağrının 503 ile reddedilmesi, sıradaki rotanın denenmemesi, sayaç ve alarm **tamam** (2026-11-23, T-39, ADR-0030); gönderilmeyen denemenin bağlantının canlı sayaçlarına ve kalite ölçüsüne yazılmaması (`cause: "media"`) **tamam** (2026-11-23, T-44, ADR-0039); her denemenin yalnızca ilk 18x'inin motora `ringing` olarak bildirilmesi, yedek rotada yeniden **tamam** (2026-11-24, T-46) | K |
| D5 | Tarayıcıda denenmemiş ekranların elden geçirilmesi: yazdırma çıktıları, yeni sayfaların telefon düzeni, salt okunur ve portal rollerinin görünümleri, sağlayıcıdan satın alma penceresi | K |
| D7 | ~~Test düzeneğindeki ara sıra takılmalar~~ — **tamam (2026-11-24, T-29)**: 200 OK'ten sonra arayana ulaşan 180 (SIP çekirdeği kararı doğru verir, gönderim sırası işçiler arasında değişebilir; arayan senaryosu cevapları INVITE'ın işlemine bağlar), kesme senaryosunda sayacın 0 okunması (OpenSIPS'in tam saniyelik dialog zamanlayıcısı motordan önce gelebilir; ikisi de tutarın içinde kapatır), "notify" denetiminin saniye çözünürlüğü, aktarıcı portları (T-39'daki `delete-delay = 0`; rapor en yüksek kullanımı verir) ve yarıda kesilen çalıştırmaların bıraktığı çağrılar (`test/lab-clean.sh`, iki takımın başında). Ürün hatası çıkmadı. `make test-routing` 10, `make load CPS=200 CC=2000 DURATION=30` 5 kez art arda geçti. **Kalan:** port payının üretimdeki karşılığı A1'de ölçülmeli | K |
| D6 | Belgelerin tutarlılığı: özellik kataloğu ile proje notlarının her işin sonunda güncellenmesi | K |

### Yayın

- Panelin İngilizce arayüzü **tamam** (2026-11-24, T-49, ADR-0049): bütün Community ekranları Türkçe ve İngilizce, varsayılan Türkçe; açık depodaki README ve ekran görüntüleri İngilizce olabilsin diye. **Kalan:** Enterprise ekranlarının ve müşteri portalının çevirisi (Enterprise deposu).
- Community deposunun ilk açık sürümü için hazırlık **tamam** (2026-11-24): `README.md`, `CONTRIBUTING.md`, `SECURITY.md`. **Kalan:** CLA metni ve imza yolu.

### Önerilen sıra

1. ~~Enterprise'da kalite tabanlı yönlendirme ve dolandırıcılık alarmları~~ **tamam** (2026-11-22). Community tarafındaki genişleme noktaları, C2 (ağırlıklı dağıtım), C4 (arayan numara kuralları), B1 ve B1b (şifreleme ve sertifikalar), A3 (kurulum) ve A4 (izleme) tamamlandı.
2. **A1–A2** sunucu gelir gelmez: ürünün ana iddiası (kapasite ve süreklilik) ancak bunlarla kanıtlanır.
3. **B2–B5**: adres çevirisi arkasındaki arayanlar ve kalan güvenlik işleri.
4. **A7** pilot.
5. Kalanlar pilotun ve ilk müşterilerin isteklerine göre.
