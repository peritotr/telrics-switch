# Kurulum, yükseltme ve yedekleme (tek sunucu)

Platform tek bir Linux sunucuya Docker Compose ile kurulur. Dosyalar `deploy/prod/` içindedir; imajlar bu depodan sunucuda derlenir (hazır imaj deposu yok).

> **Durum.** Betikler ve imajlar, özel bir Docker ağında uçtan uca denendi (`make prod-smoke`). Üretim yerleşimi (sunucunun kendi ağı, `docker-compose.host.yml`) henüz gerçek bir Linux sunucuda **çalıştırılmadı**; ilk kurulum aynı zamanda bu yerleşimin ilk denemesi olacak. Yüksek erişilebilirlik (ikinci sunucu) bu kılavuzun konusu değildir (yol haritası A2).

## Ne kurulur

| Servis | İş | Ağ |
|---|---|---|
| `opensips` | SIP çekirdeği (5060 UDP, 5061 TLS) | sunucunun ağı |
| `rtpengine` | Ses aktarımı (varsayılan 30000–39999 UDP) | sunucunun ağı |
| `route-engine` | Çağrı kararları | sunucunun ağı, yalnızca 127.0.0.1 |
| `api` | Yönetim API'si ve panel; şemayı açılışta günceller | sunucunun ağı, yalnızca 127.0.0.1:58080 |
| `rater` | Çağrı kayıtlarını fiyatlar, ClickHouse'a kopyalar | Docker ağı |
| `trace-collector` | SIP çekirdeğinin kopyaladığı sinyalleşmeyi (HEP) ClickHouse'a yazar; çağrı izi buradan okunur | sunucunun ağı, yalnızca 127.0.0.1:59060/UDP |
| `postgres`, `redis`, `nats`, `clickhouse` | Veri | Docker ağı; yalnızca 127.0.0.1'e açılır |
| `caddy` (isteğe bağlı) | Panel için HTTPS (80, 443) | sunucunun ağı |

Çağrı yolundaki üç servis Docker'ın ağ çevirisinden geçmez: ses portları tek tek yayınlanmaz, aralarındaki konuşma 127.0.0.1 üzerindendir. Motorun çağrı arayüzü kurulumun ürettiği bir sırla (`ENGINE_TOKEN`) korunur: SIP çekirdeği ve API bu sırrı gönderir, sırsız istek 401 alır. Sır ağda şifresiz gider; SIP çekirdeğinin yönetim soketinin (ve ölçü dinleyicisindeki XML-RPC kapısının, `/RPC2`) ve rtpengine'in denetim soketinin ise kimlik doğrulaması yoktur. Bu yüzden üçü de yalnızca 127.0.0.1'i dinler (`docs/08-cagri-arayuzu.md`, "Kimlik doğrulama").

Bütün servisler `restart: unless-stopped` ile çalışır: çöken servis ve sunucu yeniden açıldığında hepsi kendiliğinden başlar. SIP çekirdeğinde bir işçi süreç ölürse çekirdek kapanır ve Docker onu yeniden başlatır; süren çağrılar düşer, yenileri birkaç saniye içinde yeniden kabul edilir (duman testinde denenir).

## Gerekenler

- Linux (x86-64 ya da arm64), Docker Engine ve `docker compose` eklentisi, `git`, `openssl`, `curl`.
- Güvenlik duvarında dışarıya açık olması gerekenler: 5060/UDP, 5061/TCP, ses port aralığı/UDP; panel HTTPS ile sunulacaksa 80 ve 443/TCP. Başka hiçbir port dışarıya açılmamalıdır. Örnek kurallar ve taşkına karşı hız sınırı: aşağıda "Ağ katmanı koruması".
- Müşteriler adres çevirisi (NAT) arkasından bağlanacaksa sunucunun önündeki güvenlik duvarı **her kaynak adresten ve her kaynak portundan** 5060/UDP'ye (TLS için 5061/TCP'ye) ve ses port aralığına gelen paketlere izin vermelidir: böyle bir müşterinin dış adresi önceden bilinmez, kaynak portu da 5060 olmaz. Dönüş trafiği için durum izleyen (stateful) bir duvar yeterlidir; durum izlemeyen bir duvarda sunucudan her adrese ve porta giden UDP'ye de izin verilmelidir. Duvar, sunucunun 5060'ı ile müşteri arasındaki UDP eşlemesini kayıt aralığından (`REG_NAT_EXPIRES`, 60 sn) önce düşürmemelidir. Müşteri tarafındaki yönlendiricide SIP ALG kapatılmalıdır: açıkken iletileri kendince yeniden yazar ve sonuç cihazdan cihaza değişir.
- Yoğun trafik için çekirdek ayarları (kurulum betiği sistem ayarlarına dokunmaz; bunları sunucunun yöneticisi yapar): `net.core.rmem_max` ve `net.core.wmem_max` (ör. 16777216), `net.core.netdev_max_backlog`, `nf_conntrack` kullanılıyorsa SIP ve ses portları için izlemenin kapatılması (örnek kurallar bunu yapar: "Ağ katmanı koruması"). Bu değerler Linux yük testinde (A1) ölçülerek kesinleşecek.

## İlk kurulum

Sürümler açık deponun etiketleridir (`v0.1.0` …; liste ve notlar: https://github.com/peritotr/telrics-switch/releases). Depoyu o sürümde kopyalayın:

```bash
git clone --branch v0.1.0 https://github.com/peritotr/telrics-switch.git /opt/softswitch && cd /opt/softswitch
```

Kurulum dizini bir git kopyası olarak kalmalıdır: sonraki yükseltmeler (`upgrade.sh <sürüm>`) etiketi oradan indirir. Var olan bir kopyada `install.sh --version <sürüm> …` önce o sürümü indirip ona geçer, sonra kurar.

```bash
./deploy/prod/install.sh --ip <sunucunun adresi>
```

Seçenekler:

- `--ip` — SIP'in sunulduğu adres. Sunucunun dışarıdan görünen adresi doğrudan üzerindeyse o adres.
- `--advertised-ip` — sunucu bire bir adres çevirisi arkasındaysa (bulut sağlayıcıların çoğu): `--ip` özel adres, `--advertised-ip` dış adres olur. **Bu düzen denenmedi.**
- `--sip-domain` — müşterilerin ve sağlayıcıların TLS ile bağlandığı ad. Verilirse bu ad için sertifika kendiliğinden alınır ve yenilenir (aşağıda "SIP sertifikası").
- `--panel-domain` — panel bu alan adıyla HTTPS üzerinden sunulur; sertifikayı Caddy kendisi alır ve yeniler (alan adı sunucuya yönlenmiş, 80 ve 443 açık olmalıdır). Verilmezse panel yalnızca `127.0.0.1:58080`'dedir ve önüne kendi HTTPS vekilinizi koymanız gerekir. Paneli şifresiz olarak dışarıya açmayın.

Betik `deploy/prod/.env` dosyasını oluşturur (yalnızca sahibi okuyabilir), veritabanı parolalarını, motorun sırrını (`ENGINE_TOKEN`) ve ilk API anahtarını üretip oraya yazar (hiçbirini ekrana yazmaz), imajları derler (ilk derleme SIP çekirdeğini kaynaktan derlediği için birkaç dakika sürer) ve servisleri başlatır. Yeniden çalıştırmak güvenlidir: var olan ayarlar ve gizli değerler korunur.

İlk API anahtarı `.env` içindeki `BOOTSTRAP_API_KEY`'dir. Onunla ilk panel kullanıcısını oluşturun (`docs/06-api.md`, `POST /v1/users`), sonra günlük işler için o kullanıcıyla giriş yapın.

### Ayarlar

`deploy/prod/.env` (şablonu `env.example`). Değiştirdikten sonra `docker compose up -d` (aynı dizinde) değişen servisleri yeniden başlatır.

| Ayar | Anlamı |
|---|---|
| `SIP_IP`, `SIP_ADVERTISED_IP`, `RTP_INTERFACE` | Adresler (yukarıda) |
| `SIP_IP6`, `SIP_ADVERTISED_IP6` | IPv6 (isteğe bağlı; T-28). `SIP_IP6` verilince SIP çekirdeği bu IPv6 adresinde de aynı portlarla (5060/UDP, 5061/TLS) dinler; `SIP_ADVERTISED_IP6` yalnızca IPv6 adresi de çevriliyorsa. Köşeli parantezsiz yazılır (`2001:db8::10`). Ses için aynı adres `RTP_INTERFACE`'e noktalı virgülle eklenir: `203.0.113.10;2001:db8::10` (çeviri arkasında `10.0.0.5!203.0.113.10;2001:db8::10`); aktarıcı böylece bir ailedeki arayanı öbüründeki sağlayıcıya bağlar. İkisi boşsa (varsayılan) kurulum yalnızca IPv4'tür, eskisi gibi. Yalnızca IPv6'da olan sağlayıcının bağlantısına ad değil adres yazın: adla tanımlanan sağlayıcıya ses arayanın ailesinde teklif edilir (`docs/08-cagri-arayuzu.md`, "IPv6"; ADR-0047). Değiştirince `docker compose up -d`: SIP çekirdeği ve aktarıcı yeniden başlar, **süren çağrılar düşer**. **Gerçek sunucuda denenmedi** (yalnızca laboratuvarın IPv6 ağında) |
| `RTP_PORT_MIN`, `RTP_PORT_MAX` | Ses port aralığı. Her çağrı dört port kullanır; 20.000 eşzamanlı çağrı için aralık en az 80.000 port olmalıdır, yani tek adres yetmez (medya havuzu, Enterprise). Biten çağrının portları hemen geri verilir (`delete-delay = 0`; aktarıcının varsayılanı 30 sn bekletir ve 200 CPS'te yalnızca bu 24.000 port tutardı). Port kalmazsa yeni çağrı 503 ile reddedilir (ADR-0030) |
| `SIP_REALM` | Digest alanı. Değişirse verilmiş bütün trunk parolaları geçersiz olur |
| `ENGINE_TOKEN` | Motorun, kendisini arayanlardan (SIP çekirdeği, API, sizin betikleriniz) beklediği sır; kurulum 48 onaltılık haneli rastgele bir değer üretir. Yalnızca harf, rakam ve `- . _ ~ + / =`. Motoru kendiniz sorgularken: `curl -H "Authorization: Bearer $ENGINE_TOKEN" http://127.0.0.1:58090/v1/stats`. Değiştirmek için yeni değeri yazıp `docker compose up -d`: motor, SIP çekirdeği ve API yeniden başlar, **süren çağrılar düşer**. Boş bırakılırsa motor herkese cevap verir ve açılışta uyarı yazar; boş bırakmayın |
| `UDP_WORKERS`, `OPENSIPS_SHM_MB`, `RTP_THREADS` | Kapasite ayarları; yük testiyle belirlenecek |
| `RTP_TIMEOUT`, `RTP_HOLD_TIMEOUT` | Sesi kesilen çağrının kapatılması (ADR-0027): iki taraftan da bu kadar saniye hiç ses gelmezse medya aktarıcısı çağrıyı SIP çekirdeğine kapattırır, iki tarafa BYE gider, kayıt gerçek süreyle yazılır. Varsayılan 60; bekletilen çağrıda (bir taraf `sendonly`/`recvonly`/`inactive`) 3600. En az 1, tam saniye. Erken medyası sessiz (SDP'li 183, hiç ses yok) çalan çağrıya da uygulanır: bağlantının çalma süresi daha uzun olsa da çağrı bu kadar saniyede bırakılır (ADR-0038); büyütmek ölü çağrının kapanmasını da geciktirir. Değiştirince `docker compose up -d rtpengine`: aktarıcı yeniden başlar, **süren çağrıların sesi kesilir** (`docs/08-cagri-arayuzu.md`, "Ölü çağrı"). Aktarıcı, SIP çekirdeğine ölçü dinleyicisindeki XML-RPC kapısından (`/RPC2`) ulaşır; adresi compose dosyasında yazılıdır (`RTP_TEARDOWN_URL`) |
| `RTP_KERNEL_TABLE` | `-1`: ses kullanıcı alanında aktarılır. Çekirdek içi aktarım için sunucuya `xt_RTPENGINE` modülü kurulmalı ve konteyner yetkili çalıştırılmalıdır; **bu kurulumda hazır değil ve denenmedi** (A1 ile birlikte) |
| `TLS_CA_LIST` | Sağlayıcı sertifikalarının doğrulanacağı kök sertifika dosyası. Boşsa sistemin güvendiği genel kökler. Sertifikadaki adı motor aynı köklerle denetler; `PEER_VERIFY=off` kapatır (`docs/08-cagri-arayuzu.md`, "Şifreleme") |
| `REG_TIMER_SEC`, `REG_RETRY_SEC` | Switch'in sağlayıcılara kaydı (bağlantıda "Kayıt" açıksa; ADR-0035), saniye: SIP çekirdeğinin kayıtlara bakma aralığı (30; en az 10), kayıt süresi dolmadan bu kadar önce yenilenir; başarısız kaydın yeniden denenmesine kadar geçen süre (60; sabit aralık). İkisi de SIP çekirdeğine verilir. Kayıt listesi `REG_SYNC_SEC` (10) saniyede bir motordan alınır; motor durumu `REG_STATUS_SEC` (10) saniyede bir okur. Liste vendor parolalarını taşıdığı için SIP çekirdeğinin `/run/opensips` klasörü compose dosyasında bellekte (tmpfs) durur (`docs/08-cagri-arayuzu.md`, "Sağlayıcıya kayıt") |
| `REG_MIN_EXPIRES`, `REG_MAX_EXPIRES`, `REG_NAT_EXPIRES` | Trunk kayıtlarının (REGISTER) süresi, saniye: bir trunk'ın isteyebileceği en az (60) ve en çok (3600) süre; adres çevirisi (NAT) arkasındaki ya da TCP/TLS ile bağlı trunk'a verilen en çok süre (60). Böyle bir trunk bu süre dolmadan yeniden kaydolur ve ona giden yol böyle açık kalır; NAT arkasındaki trunk'lara çağrı ulaşmıyorsa `REG_NAT_EXPIRES` düşürülür (`docs/08-cagri-arayuzu.md`, "Kayıt (REGISTER)") |
| `TRANSCODE_MAX` | Medya aktarıcısının aynı anda kodek çevirisi yapabileceği çağrı sayısı (varsayılan 200; 0 = hiçbir çağrı çevrilmez). Yalnızca kodek çevirisine izin verilen bağlantı ve trunk'lar için anlamlıdır; dolunca çevrilecek rota atlanır ve `TranscodingAtLimit` alarmı çalar. Değiştirince `docker compose up -d route-engine`. Aşağıda "Kodek çevirisi" |
| `TLS_CLIENT_CA` | Arayanların sunduğu sertifikaların doğrulanacağı kök sertifika dosyası; istemci sertifikası isteyen trunk'lar için gerekir. Boşsa arayandan sertifika istenmez |
| `SIP_DOMAIN` | SIP'in TLS ile sunulduğu ad; sertifikası kendiliğinden alınır (`https` profili gerekir) |
| `CERTS_DIR` | Sertifika dizini (varsayılan `certs`) |
| `PANEL_DOMAIN`, `PANEL_URL`, `COMPOSE_PROFILES=https` | Panelin HTTPS ile sunulması |
| `CDR_KEEP_DAYS` | PostgreSQL'in çağrı kayıtlarını kaç gün tutacağı (0 = hep). Daha eski kayıt, ClickHouse onu tuttuğunu doğruladıktan sonra silinir; doğrulanamayan silinmez, yeniden aktarıma alınır. API'ye `CLICKHOUSE_URL` verin: tekil kayıt, liste ve CSV arşivlenen kayıtları ClickHouse'tan okur (arşiv ufku `cdr_archive`; T-24). Büyük kurulumda listeyi tümüyle ClickHouse'tan okumak için `CDR_LIST_SOURCE=clickhouse`. Enterprise sürümünde kullanmayın (fatura ve müşteri portalı PostgreSQL'den okur) |
| `RELOAD_FULL_SEC` | Motor fiyatları yalnızca değiştikleri yerde yeniden okur (veritabanındaki tetikleyicinin notlarıyla, ADR-0048); bütün fiyatları bu aralıkla, saniye, baştan okur (varsayılan 300, en çok 1.800 kullanılır; `0`: her yüklemede bütün fiyatlar, T-30'dan önceki davranış). 5 milyon fiyatlı bir tarifede tam okuma ~8 saniye sürer ve geçici olarak ~2,7 GiB bellek ister; motorun belleğini en büyük tarifeye göre ayırın (`docs/02-mimari.md`). `rates` tablosuna tetikleyicileri kapatarak yazdıysanız (ör. `session_replication_role = replica` ile yükleme) değişiklik bu aralıkta görünür; hemen görünsün isterseniz motoru yeniden başlatın |
| `CDR_JOURNAL_MAX_MB` | Motorun, NATS'a henüz ulaşmamış çağrı kayıtlarını tuttuğu diskteki günlüğün en çok boyutu, megabayt (varsayılan 1024; tam yükte, saniyede 1.000 çağrıda yaklaşık bir saatlik NATS kesintisi). Günlük `engine-journal` biriminde durur (konteynerde `CDR_JOURNAL_DIR=/var/lib/softswitch/cdr-journal`, compose dosyasında yazılı); motor yeniden başlasa ya da çökse de kayıtlar orada kalır ve açılışta NATS'a yeniden gönderilir (ADR-0029). Dolarsa yeni kayıtlar bellekte bekler (motor o arada durursa kaybolur) ve `CallRecordJournalFilling` alarmı çalar. Birim yedeklenmez: içindekiler NATS'a ulaşınca silinir. Birimi silmeyin (`docker compose down -v` siler): içinde NATS'a ulaşmamış kayıt varsa onlar gider |
| `CDR_LIST_SOURCE` | `clickhouse`: çağrı kayıtları listesi ClickHouse'tan okunur (aynı süzgeçler, aynı sıra, aynı alanlar); hata olursa PostgreSQL'e düşer |
| `COMPOSE_PROFILES=monitoring`, `METRICS_KEEP` | İzleme (`docs/10-izleme.md`) |
| `TRACE_KEEP_DAYS` | Çağrıların SIP iletilerinin kaç gün saklanacağı (varsayılan 7). Tam yükte diskteki en büyük kalem budur; boyut hesabı `docs/10-izleme.md`, "Çağrı izi" içinde |
| `TRACE_TARGET` | Yazılmazsa SIP çekirdeği sinyalleşmeyi kurulumun kendi toplayıcısına kopyalar (çağrı izi açık). Boş bırakılırsa (`TRACE_TARGET=`) kopya gönderilmez, iz saklanmaz |
| `HEP_TARGET` | Aynı kopyanın ayrıca gönderileceği dış HEP toplayıcısı (Homer), `adres:port`. Önceki sürümlerde izlemeyi açan tek ayar buydu; şimdi kendi toplayıcımıza ek olarak çalışır |
| `HEP_IP` | SIP çekirdeğinin HEP kopyalarını gönderdiği soketin adresi. Boşsa: bütün toplayıcılar bu sunucudaysa (127.0.0.1) 127.0.0.1, değilse `SIP_IP`. Dış `HEP_TARGET` ile o toplayıcıya ulaşan iç adresinizi yazın; soket gelen paketi işlemez ama dinler (T-51) |
| `MAIL_FROM`, `SMTP_URL` ya da `RESEND_API_KEY` | E-posta (isteğe bağlı; `PANEL_URL` de gerekir) |
| `SECRET_KEY` | Panel kullanıcılarının iki adımlı doğrulama sırlarını ve sağlayıcılara giriş parolalarını (bağlantı ayarlarındaki "Sağlayıcıya giriş") veritabanında şifreleyen anahtar; API'ye ve motora (`route-engine`) aynı değerle verilir, çünkü parolayı çağrı sırasında motor okur. 64 onaltılık hane (`openssl rand -hex 32`). Kurulum üretir; eski bir kurulumda `upgrade.sh` ekler (daha önce açık saklanmış sırlar okunmaya devam eder). Boşsa sırlar açık saklanır. **Anahtarı değiştirmeyin ve `.env` ile birlikte yedekleyin:** anahtar olmadan şifreli sırlar okunamaz, o kullanıcılar kodla giremez ve bir yöneticinin (ya da API anahtarıyla sizin) iki adımlı doğrulamalarını sıfırlaması gerekir (`docs/06-api.md`, "İki adımlı doğrulama"); sağlayıcı parolalarının da panelden yeniden girilmesi gerekir (o zamana kadar o sağlayıcıların kimlik sorusu cevapsız kalır ve çağrılar sıradaki rotaya geçer) |
| `AUDIT_RETENTION_DAYS` | Denetim kaydının kaç gün saklanacağı (varsayılan 365; `0` = hep sakla). Süresi dolan kayıtları API süreci saatte bir siler (`docs/06-api.md`, "Denetim kaydı") |
| `FX_SOURCE`, `FX_URL` | Döviz kurlarının dış kaynaktan alınması (T-20, ADR-0036). Boş (varsayılan): bütün kurlar elle girilir. `ecb`: panelde kaynağı "ECB" seçilen kurlar Avrupa Merkez Bankası'nın günlük referans kurlarından, hesabın para birimine çevrilerek güncellenir; elle girilen kurlara dokunulmaz. API açılışta ve her gün 15:30 UTC'den sonra bir kez alır; API'nin `www.ecb.europa.eu`'ya HTTPS ile çıkabilmesi gerekir. Alınamazsa son kurlar kalır, panelde uyarı ve `ExchangeRatesNotFetched` alarmı çıkar. `FX_URL` aynı dosyanın başka bir adresidir (ayna, test); yalnızca `FX_SOURCE=ecb` ile (`docs/08-cagri-arayuzu.md`, "Kurların dış kaynaktan alınması") |
| `TRUST_PROXY` | `1`: API'nin önünde `X-Forwarded-For` yazan bir ters vekil var; denetim kaydına vekilin değil çağıranın adresi yazılır. `https` profili böyle bir vekildir: `install.sh --panel-domain` ile kurulumda kendiliğinden `1` olur; profili sonradan açtıysanız ya da kendi vekilinizi kullanıyorsanız elle yazın. API'ye doğrudan erişiliyorsa boş bırakın: başlık çağıranın yazdığı şey olur |

### Kodek çevirisi

Ortak kodeği olmayan müşteri ile sağlayıcı arasında ses, aynı sunucudaki medya aktarıcısında (rtpengine, kullanıcı alanında) çevrilir; panelde bağlantı ya da trunk formunda "Gelişmiş ayarlar → Ses kodekleri → Kodek çevirisi" ile açılır (`docs/08-cagri-arayuzu.md`, "Kodek çevirisi"). İmajda ne olduğu (`deploy/images/rtpengine`, Debian 12 paketi `rtpengine-daemon` 10.5 ve bağımlılıkları; `rtpengine --codecs` ile görülür):

| Kodek | Durum |
|---|---|
| PCMA, PCMU (G.711), G.722, G.723, GSM, Speex, Opus | İki yönde çevrilir (FFmpeg `libavcodec`, `libopus`) |
| G.729 / G.729a | İki yönde çevrilir: Debian'ın `libbcg729` paketi (Belledonne Communications'ın açık kaynak kodlayıcısı, GPL-3) imaja aktarıcının bağımlılığı olarak girer. G.729'un patent havuzu 2017'de sona erdi; kullanım ve dağıtımın sizin ülkenizdeki hukuki durumunu kendiniz değerlendirin. İstemiyorsanız bağlantılarda ve trunk'larda G.729'u seçmeyin |
| iLBC, AMR, AMR-WB, EVRC, QCELP | Yalnızca çözülür: bu kodeğe çevrilemez (AMR kodlayıcıları lisanslıdır, imajda yoktur). Bu kodekleri isteyen bir sağlayıcıya çeviriyle çağrı kurulamaz |
| `telephone-event` (RFC 4733), CN | Aktarılır; tuş olayları çevrilen çağrıda da geçer, SIP INFO'ya dönüştürülmez (ADR-0031) |

İşlemci: çeviri, aktarıcının kullanıcı alanında çalışır (Linux'ta çekirdek içi aktarım çevrilen akışı taşımaz). G.711 ↔ G.711 ucuzdur; G.729 ve Opus kodlaması birkaç kat pahalıdır. `TRANSCODE_MAX` varsayılanı (200) bir başlangıç değeridir: sunucuda, çeviri açık bir yük testiyle (`make load`'un çeviri senaryosu; ölçüm `docs/TASKS.md`, T-09) aktarıcının işlemci kullanımına bakarak ayarlayın. Ayrı çeviri sunucuları (havuz) Enterprise'tadır.

### SIP sertifikası

SIP çekirdeği sertifikasız açılmaz. Kurulum, `deploy/prod/certs/` içine **kendinden imzalı** bir sertifika yazar; gerçek sertifika gelene kadar o kullanılır. Gerçek sertifika için iki yol var:

- **Kendiliğinden (`--sip-domain sip.ornek.com`):** panelin HTTPS vekili (Caddy) bu ad için de sertifika alır ve süresi dolmadan yeniler; `sip-cert` servisi Caddy'deki sertifikayı beş dakikada bir `certs/` ile karşılaştırır ve değiştiyse kopyalar. Ad bu sunucuya yönlenmiş, 80 ve 443 dışarıya açık olmalıdır (sertifika otoritesi sunucuyu bu portlardan doğrular). Panel bu adla da açılır.
- **Elle:** sertifikayı (ara sertifikalarıyla birlikte) ve anahtarı `certs/opensips.crt` ve `certs/opensips.key` olarak koyun. Kendi yenileme aracınız (ör. certbot) aynı dosyaları güncelleyebilir.

İki yolda da SIP çekirdeği **yeniden başlatılmaz**: dosyalar 30 saniyede bir denetlenir, değişen sertifika yüklenir, süren çağrılar düşmez. Anahtarıyla uyuşmayan ya da okunamayan sertifika yüklenmez ve eskisi kullanılmaya devam eder; ne olduğu `docker compose logs opensips | grep tls:` ile görülür. Her dosya (ara sertifikalarıyla zincir, anahtar, otorite listeleri) 512 KiB'tan küçük olmalıdır; büyüğü aynı biçimde reddedilir (T-50; önceki sürümlerde sınır 4 KiB'tı ve Let's Encrypt zinciri yüklenemiyordu, SIP çekirdeği o zincirle hiç açılmıyordu). `TLS_CA_LIST` ve `TLS_CLIENT_CA` dosyaları da aynı biçimde izlenir.

Sertifikanın bitişine 14 günden az kalınca `SIPCertificateExpiring` alarmı çalar (`docs/10-izleme.md`): motor SIP çekirdeğinin TLS kapısına bağlanıp bitiş tarihini okur.

**Yükseltme sırası:** `upgrade.sh` önce API ve rater'ı, sonra diğerlerini yeniler: rater, motorun göndereceği çağrı kaydındaki her alanı motordan önce tanımalıdır (bilmediği alanı taşıyan kaydı reddeder). Elle yükseltirken de bu sıraya uyun.

**Yükseltme notu (motorun sırrı):** bu sürümden önce kurulmuş bir sunucunun `.env` dosyasında `ENGINE_TOKEN` yoktur. `upgrade.sh` eksikse bir tane üretip `.env`'e ekler (değeri göstermez) ve motor, SIP çekirdeği ve API yeni değerle yeniden başlar; bu sürümde SIP çekirdeğinin imajı da değiştiği için süren çağrılar her durumda düşer. Yeniden başlama sırasında birkaç saniye boyunca yeni çağrılar 503 alabilir (motor sırrı istemeye başlamış, SIP çekirdeği henüz yenilenmemişken). Elle yükseltiyorsanız: önce `.env`'e `ENGINE_TOKEN=$(openssl rand -hex 24)` değerini ekleyin, sonra `docker compose up -d`. Sır eklenmeden yapılan yükseltmede servisler eskisi gibi, sırsız çalışmayı sürdürür (çağrı kesilmez) ve motor her açılışta uyarı yazar. Motoru doğrudan sorgulayan kendi betikleriniz varsa (ör. `/v1/stats`) sırrı göndermeleri gerekir; `/metrics` açık kalır, Prometheus ayarı değişmez.

**Yükseltme notu:** TLS ile gidilen sağlayıcıların sertifikasındaki ad artık denetlenir. Sunucu alanına IP adresi yazılmış ve sertifikası ada düzenlenmiş bir bağlantı, yükseltmeden sonra çağrı almaz; bağlantıda "Sertifikadaki ad" alanını doldurun (ya da geçici olarak `PEER_VERIFY=off`).

## Sürümler ve yükseltme

Sürümler anlamsal sürümlemeye uyar: `vANA.ARA.YAMA` (ilk açık sürüm `v0.1.0`; ADR-0051). Yama sürümü yalnızca hata düzeltir; 1.0.0'a kadar ara sürüm de ayar ya da API değiştirebilir, değişiklikler sürüm notlarında "Upgrade notes" başlığındadır. Her sürümün notları depodaki `CHANGELOG.md`'dedir ve GitHub'daki sürüm sayfasında da yayımlanır.

**Kurulu sürüm:** panelde menünün altında; API'de `GET /v1/session` cevabındaki `version`; her servisin ölçülerinde `softswitch_build_info{version=…}` (`docs/10-izleme.md`); sunucuda `git -C /opt/softswitch describe --tags`. İmajlar kurulum ve yükseltme betiklerinde kopyanın sürümüyle derlenir (`git describe --tags --always --dirty`; izlenen bir dosya elle değiştirilmişse sürüm `-dirty` ile biter).

**Yükseltme** (`deploy/prod/` dizininde):

```bash
./upgrade.sh v0.2.0
```

Betik şunları yapar: yedek alır (`backup.sh`), sürüm etiketini depodan indirir ve etiketin depoda gerçekten olduğunu denetler (yoksa, ağ yoksa ya da aynı adlı yerel etiket depodakinden farklıysa durur; o ana kadar hiçbir şey değişmemiştir), kopyayı o etikete geçirir, sonra **yeni sürümün kendi** `upgrade.sh`'ını çalıştırır: imajları derler, önce API'yi başlatır, sonra değişen diğer servisleri yeniden başlatır. Her geçiş, öncesinde alınan yedekle birlikte `upgrades.log` dosyasına yazılır; betik sonunda geri dönüş komutunu da yazar. İzlenen dosyalarda elle yapılmış değişiklik varsa betik başlamadan durur (`git status` ile görülür); `.env`, `certs/`, `backups/` ve `monitoring/alertmanager.yml` izlenmez, oldukları gibi kalır. Kendiliğinden yükseltme yoktur: betik çalıştırılmadıkça hiçbir şey indirilmez.

Etiket almadan çalıştırılan `./upgrade.sh` eskisi gibi, kopyada o an ne varsa ona yükseltir (elle `git checkout` yapanlar için).

Betik önce yedek alır, imajları derler (değişmeyen servis yeniden başlatılmaz), önce API'yi başlatır (API yeni şema adımlarını uygular; aynı anda başlayan iki servis sırayla bekler), sonra değişen diğer servisleri yeniden başlatır. SIP çekirdeğinin imajı değiştiyse süren çağrılar düşer; yükseltmeyi trafiğin az olduğu saatte yapın. Kesintisiz yükseltme ikinci sunucuyla mümkündür (A2).

**Yükseltme notu (çağrı kayıtlarının günlüğü, T-33):** compose dosyasına motor için yeni bir birim (`engine-journal`) ve `CDR_JOURNAL_DIR`, `CDR_JOURNAL_MAX_MB` ayarları eklendi; `upgrade.sh` birimi kendiliğinden oluşturur, `.env`'e bir şey eklemek gerekmez (`CDR_JOURNAL_MAX_MB` yazılmazsa 1024). Eski motor kapanırken bellekteki kuyruğunda NATS'a ulaşmamış kayıt kalmışsa onlar eskisi gibi kaybolur; yeni motordan sonra kalmaz. Yeni motor her açılışta günlükteki son dakikanın kayıtlarını da yeniden gönderir; rater onları tanır ve ikinci kez ücretlendirmez. Motor artık NATS'a ulaşamadan da başlar ve çağrı alır.

**Yükseltme notu (gönderilmeyen deneme, ADR-0039):** SIP çekirdeği, medya aktarıcısı teklifi almadığında motora giden olaya isteğe bağlı `cause` alanını ekler. Motor bilinmeyen alanı yok saydığı için sıra önemli değildir: eski motor yeni SIP çekirdeğiyle, yeni motor eski SIP çekirdeğiyle eskisi gibi çalışır (gönderilmeyen deneme bağlantının sayaçlarına yazılır); ikisi de yükseltilince yazılmaz. `.env`'e bir şey eklemek gerekmez.

**Yükseltme notu (sınırlar iki yönü birlikte sayar, ADR-0042):** trunk'ın `max_calls`/`max_cps` sınırı artık trunk'ın yaptığı ve ona teslim edilen çağrıları birlikte sayar; müşterinin sınırı da müşterinin yaptığı ve trunk'larına teslim edilen çağrıları. İki yönün ayrı kapasitesine güvenen bir trunk ya da müşteri yükseltmeden sonra daha erken sınırlanır (ör. aynı anda 10 çağrı yapıp 10 çağrı alan, `max_calls = 10` olan trunk toplam 10'da kalır): bu sınırları yükseltmeden önce gözden geçirin. Teslim (bir uzantının `routing.ToTrunk` rotası) olmayan kurulumda davranış değişmez. Sayaçlar Redis'te kendiliğinden geçer: yeni motor açılırken eski `cc:s:<trunk>` kümelerindeki süren çağrıları `cc:t:<trunk>`'a katar ve eski kümeyi siler (günlükte "moved the counts of N trunks"); saniye sayaçları iki saniyede düşer. Yükseltme anında süren ve bir trunk'a teslim edilmiş çağrılar, bitene kadar hedef müşterinin sınırında görünmez. Şema ve `.env` değişmez; yükseltme sırası önemli değildir.

### Geri dönüş

**Şema adımları yalnızca ileri gider; geri alma betikleri yoktur.** Yeni sürümün API'si açılırken veritabanına yeni adımları uygular; eski sürümün kodu bu yeni şemayla çalışmayabilir (yeni sütunu, tetikleyiciyi tanımaz; kaldırılmış bir şeyi arar). Bu yüzden eski sürüme dönmek, veritabanını da yükseltmeden hemen önce alınan yedeğe döndürmek demektir:

```bash
./upgrade.sh v0.1.0 --restore backups/softswitch-<zaman>.dump
```

Betik önce o anki durumun da yedeğini alır, eski etikete geçer, eski sürümün imajlarını derler, sonra `restore.sh` ile veritabanını verilen yedeğe döndürür ve servisleri eski sürümle başlatır. Doğru yedek, yükseltme bitince betiğin yazdığı komutta ve `upgrades.log`'da yazılıdır. Sınırlar:

- Yükseltmeden sonra yazılan her şey kaybolur: çağrı kayıtları, bakiye hareketleri, panelde yapılan değişiklikler, denetim kaydı. Süren çağrılar düşer, Redis boşaltılır. Kaybolan kayıtlar geri dönüşten önce alınan yedekte durur; ama o yedek yeni şemadadır, eski sürüme olduğu gibi yüklenemez, ancak elle ayıklanabilir.
- Yalnızca bir sürüm geriye, o sürümden ayrılırken alınan yedekle dönülür. Yedeksiz geri dönüş (`./upgrade.sh <eski sürüm>` yalnız başına) şema değişmemişse çalışır, değişmişse eski API açılmayabilir ya da yanlış davranabilir; sürüm notunda "schema unchanged" yazmıyorsa yapmayın.
- `.env`'e yeni sürümün eklediği ayarlar (ör. bir sır) kalır; eski sürüm onları yok sayar.
- `v0.1.0`'dan eski bir sürüme bu yolla dönülemez (etiket yoktur).

### Depo adresi

Sürümler `.env`'deki `SOFTSWITCH_REPO` adresinden indirilir; boşsa açık depodan (https://github.com/peritotr/telrics-switch.git). Kendi aynanızı ya da çatalınızı kullanmak için adresini buraya yazın; `.env` henüz yokken (ilk kurulum) aynı adı ortam değişkeni olarak verin: `SOFTSWITCH_REPO=<adres> ./install.sh --version <sürüm> …`. Etiketler imzalı değildir; indirilen sürümün içeriğini doğrulamak isteyen, etiketin işaret ettiği commit'i (`git -C /opt/softswitch rev-parse v0.2.0^{commit}`) GitHub'daki sürüm sayfasındakiyle karşılaştırabilir.

### Git'siz kurulumun bir kez taşınması

Kurulum dizini git kopyası değilse (ör. `git archive` ile kopyalanmış) `upgrade.sh <sürüm>` "is not a git checkout" diyerek durur. Böyle bir kurulum bir kez, aşağıdaki adımlarla bir kopyaya taşınır. Seçilen yol, eski dizini olduğu gibi bırakıp yanına temiz bir kopya kurmaktır: yerinde `git init` yapmak, eski ağaçta kalan ve depoda artık olmayan dosyaları da bırakırdı; eski dizin taşımadan sonra da geri dönüş için durur. Veriler Docker birimlerindedir; birimlerin adı compose proje adıyla başlar ve proje adı (`.env`'de `COMPOSE_PROJECT_NAME` yoksa) dizinin adıdır, `prod`: iki kopyada da aynı olduğu için veriler yerinde kalır. Süren çağrılar düşer; trafiğin az olduğu saatte yapın.

Aşağıda kurulum `/opt/softswitch`'tedir ve yeni sürüm `v0.1.0`'dır; kendi yolunuzu ve sürümünüzü yazın. Komutları kurulumu yapan kullanıcıyla çalıştırın.

1. Yedek alın ve proje adını not edin:
   ```bash
   cd /opt/softswitch/deploy/prod && ./backup.sh
   docker compose ls          # NAME sütunu: prod (ya da .env'deki COMPOSE_PROJECT_NAME)
   ```
   Yedek dosyasını, `.env`'i ve `certs/`'i sunucunun dışına da kopyalayın.
2. Yeni kopyayı eskisinin yanına kurun:
   ```bash
   git clone --branch v0.1.0 https://github.com/peritotr/telrics-switch.git /opt/softswitch.new
   ```
3. Ayarları, sertifikaları ve yedekleri taşıyın (izinler korunur; `.env` yalnızca sahibince okunur kalmalıdır):
   ```bash
   cd /opt/softswitch/deploy/prod
   cp -a .env certs /opt/softswitch.new/deploy/prod/
   [ -d backups ] && cp -a backups /opt/softswitch.new/deploy/prod/
   [ -f monitoring/alertmanager.yml ] && cp -a monitoring/alertmanager.yml /opt/softswitch.new/deploy/prod/monitoring/
   ```
   `.env`'de `CERTS_DIR` ya da `BACKUP_DIR` başka bir yere işaret ediyorsa onları da taşıyın ya da mutlak yol olarak bırakın.
4. Servisleri durdurun ve dizinleri değiştirin. `down` kapları kaldırır, birimlere (verilere) dokunmaz. **`-v` eklemeyin: bütün veriyi siler.**
   ```bash
   cd /opt/softswitch/deploy/prod && docker compose down
   mv /opt/softswitch /opt/softswitch.old && mv /opt/softswitch.new /opt/softswitch
   ```
   (Kaplar eski dizinin sertifika ve ayar dosyalarını bağlı tutar; `down` olmadan yeni dizinden başlatılan bir yükseltme, değişmeyen kapları eski dizine bağlı bırakırdı.)
5. Veritabanını başlatıp yeni kopyayla yükseltin (yedek alır, imajları derler, önce API'yi başlatır):
   ```bash
   cd /opt/softswitch/deploy/prod && docker compose up -d postgres && ./upgrade.sh
   ```
6. Denetleyin: `docker compose ls` aynı proje adını, `docker compose ps` bütün servisleri çalışır gösterir; panel açılır ve menünün altında `v0.1.0` yazar; bir deneme çağrısı geçer. Zamanlayıcıdaki yedek satırı (`/opt/softswitch/deploy/prod/backup.sh`) aynı yolu gösterdiği için değişmez.
7. Her şey yolundaysa birkaç gün sonra `/opt/softswitch.old`'u silin. O zamana kadar geri dönüş mümkündür; taşımadan sonra yazılan veriler kaybolur:
   ```bash
   cd /opt/softswitch/deploy/prod && docker compose down
   mv /opt/softswitch /opt/softswitch.new && mv /opt/softswitch.old /opt/softswitch
   cd /opt/softswitch/deploy/prod && docker compose build && docker compose up -d postgres
   ./restore.sh backups/softswitch-<1. adımdaki zaman>.dump --yes
   ```
   İmajları yeniden derlemek şarttır: imaj adları proje adından gelir ve yeni kopyanın derledikleri eskilerin yerini almıştır. Veritabanı 1. adımdaki yedeğe döner, çünkü yeni sürüm şemayı ilerletmiş olabilir.

Bundan sonraki yükseltmeler `./upgrade.sh <sürüm>` ile yapılır.

### Sürüm yayımlama (bakımcılar)

Açık depo, bakımcıların kaynak ağacından üretilir; sürümü hazırlayan betik (`tools/release.sh`) o ağaçtadır, açık depoda yoktur. Adımlar:

1. `CHANGELOG.md`'de "Unreleased" altındakiler yeni sürümün başlığına taşınır (`## [0.2.0] - YYYY-AA-GG`, sondaki bağlantı satırıyla), `web/package.json`'daki sürüm aynı yapılır (`npm --prefix web version 0.2.0 --no-git-tag-version`), değişiklik `main`'e girer.
2. `tools/release.sh v0.2.0` (yalnızca denetim için `--check`): `main`'de ve temiz olunduğunu, sürümün yeni ve sonuncusundan büyük olduğunu, değişiklik günlüğü bölümünü, panelin sürümünü ve İngilizce belgelerin güncelliğini denetler; açık ağacı dışa aktarır (kendi denetimleriyle), açık deponun yerel kopyasında "Release v0.2.0" commit'ini ve açıklamalı `v0.2.0` etiketini, kaynak ağacında da aynı adlı etiketi oluşturur, sürüm notlarını (değişiklik günlüğünün o bölümü) bir dosyaya yazar. **Hiçbir şey göndermez** ve GitHub'da sürüm oluşturmaz.
3. Sahip onaylayınca betiğin yazdığı komutlar çalıştırılır: `git push origin main`, `git push origin v0.2.0` ve `gh release create v0.2.0 --repo peritotr/telrics-switch --verify-tag --title v0.2.0 --notes-file <notlar>`.
4. CI etiket için de çalışır (`.github/workflows/ci.yml`); kırmızıysa sürüm duyurulmaz, düzeltme yeni bir yama sürümüyle çıkar (yayımlanmış etiket taşınmaz).

CI her gönderimde ve çekme isteğinde laboratuvar gerektirmeyen denetimleri çalıştırır: `gofmt`, `go vet`, `go build`, `go test ./...` (PostgreSQL, Redis ya da ClickHouse isteyen testler adresleri verilmediğinde kendini atlar), panelde `npm ci`, tür denetimi, metin katalogları ve derleme. Yerelde aynısı: `make ci`. SIPp senaryoları (`make test-routing`), API'nin veritabanıyla uçtan uca testi (`make test-api`), yük testi ve kurulum duman testi (`make prod-smoke`) laboratuvar ister; CI'da yoktur, sürümden önce bakımcı çalıştırır.

## Yedekleme ve geri yükleme

```bash
./deploy/prod/backup.sh
```

`deploy/prod/backups/` içine PostgreSQL dökümü yazar, okunabildiğini denetler ve 14 günden eskileri siler (`KEEP_DAYS`, `BACKUP_DIR`). Her gece çalışması için sunucunun zamanlayıcısına ekleyin, örneğin: `15 3 * * * /opt/softswitch/deploy/prod/backup.sh`.

- Yeniden üretilemeyen her şey PostgreSQL'dedir: yapılandırma, bakiyeler, çağrı kayıtları. ClickHouse'taki kopya geri yüklemede bu kayıtlardan yeniden kurulur; Redis'teki süren çağrı durumu yedeklenmez.
- **Çağrı izleri (SIP iletileri) yedeklenmez.** Yalnızca ClickHouse'tadır ve zaten birkaç gün saklanır; ClickHouse'un diski giderse o günlerin izleri de gider. Geri yükleme izlere dokunmaz.
- **`.env` ve `certs/` yedeğin içinde değildir.** Bunları ayrıca, güvenli bir yerde saklayın: `.env` olmadan parolalar ve iki adımlı doğrulama sırlarının anahtarı (`SECRET_KEY`), sertifika anahtarı olmadan TLS kimliği kaybolur.
- Denetim kaydı PostgreSQL'dedir ve yedeğe girer.
- Yedekler sunucunun kendi diskindedir. Disk ya da sunucu kaybına karşı başka bir yere kopyalanması bu kurulumun dışında, sizin sorumluluğunuzdadır.
- Son yedekten sonraki veriler geri yüklemede kaybolur (zaman içinde bir ana dönüş, yani sürekli arşivleme yok).

```bash
./deploy/prod/restore.sh deploy/prod/backups/softswitch-<zaman>.dump --yes
```

Veritabanının tamamını yedektekiyle değiştirir: servisleri durdurur, süren çağrıları düşürür, Redis'i boşaltır, ClickHouse kopyasını yeniden kurar ve servisleri başlatır. Yeni bir sunucuya taşımak için: yeni sunucuda kurulumu yapın, eski `.env` ve `certs/` dosyalarını yerine koyup `install.sh`'ı yeniden çalıştırın, sonra yedeği geri yükleyin.

## Günlük işler

Komutlar `deploy/prod/` dizininde çalıştırılır.

- Durum: `docker compose ps`
- Günlükler: `docker compose logs -f opensips` (servis adıyla). Günlükler servis başına 5 × 50 MB ile sınırlıdır.
- Tek servisi yeniden başlatma: `docker compose restart rater`
- Durdurma: `docker compose stop` (veriler kalır). `docker compose down -v` **bütün veriyi siler.**

## Deneme kurulumu

```bash
make prod-smoke
```

Aynı imajları özel bir Docker ağında (`docker-compose.bridge.yml`), ayrı bir proje adıyla ve kendi sertifika dizininde (`certs-smoke`) kurar ve şunları dener: kurulum, panelin HTTP ve HTTPS üzerinden sunulması, SIP adı için alınan sertifikanın (burada Caddy'nin kendi otoritesinden) zinciriyle birlikte ve yeniden başlatma olmadan SIP çekirdeğine geçmesi, motorun sırrının üretilmesi ve sırsız ya da yanlış sırlı isteğin reddedilmesi, gerçek bir çağrı, kaydı ve saklanan izi (iletiler ve pcap dosyası), SIP çekirdeğinde ölen bir sürecin ardından toparlanma ve bunun alarmı, yedek alma ve geri yükleme (ClickHouse'un yeniden kurulması dahil), yükseltme, sırrı olmayan bir kurulumun yükseltmede sır alması ve çağrıların sürmesi, kopyanın sürümünün API'de ve motorun ölçülerinde görünmesi. Bittiğinde kurduğu her şeyi siler. Üretim sunucusu olmayan makinelerde (Docker Desktop dahil) çalışır; trafik taşımak için değildir.

## Ağ katmanı koruması

Kurulum sunucunun güvenlik duvarına **dokunmaz**. Bu bölüm, sunucunun yöneticisinin kendi uygulayacağı işletim sistemi katmanını anlatır; örnek kurallar `deploy/prod/nftables/` içindedir (`softswitch.nft`, `known-sources.sh`). Amaç sahte kaynak adresli ve büyük hacimli taşkını (yol haritası B5) SIP çekirdeğine varmadan, çekirdekte (kernel) ucuzca düşürmek ve dışarıya açık olmaması gereken portları ikinci kez kapatmaktır. Uygulama katmanındaki korumanın (`pike`, `ban:`, `firewall_rules`; ADR-0017) yerine geçmez, önüne eklenir (aşağıda "Uygulama katmanıyla ilişkisi"). Paneldeki kuralların sunucunun güvenlik duvarına ürün tarafından yansıtılması ayrı bir görevdir (T-27) ve yapılmadı.

> **Durum.** Kurallar `make test-nft` ile bir Docker kabının kendi ağ ad alanında sözdizimi denetiminden geçer, yüklenir ve eşitleme betiğiyle birlikte sınanır (gerekenler: Docker ve kabın Debian aynalarına erişimi; kabın içine `nftables` kurulur). Gerçek bir sunucuda, gerçek trafikle ve gerçek bir taşkınla **denenmedi**; oranlar ölçülmüş değil, başlangıç değeridir.

### Açık olacak portlar

| Port | Protokol | Ne | Ayar |
|---|---|---|---|
| 5060 | UDP | SIP | `SIP_PORT` (SIP çekirdeğinin kabında; varsayılan 5060) |
| 5061 | TCP | SIP, TLS ile | `SIP_TLS_PORT` (varsayılan 5061) |
| 30000–39999 | UDP | Ses (RTP, SRTP; faksta UDPTL) | `RTP_PORT_MIN`, `RTP_PORT_MAX` (`.env`) |
| 80, 443 | TCP | Panel HTTPS ile ve SIP sertifikasının alınması (Caddy) | yalnızca `COMPOSE_PROFILES=https` ile |
| 22 | TCP | SSH | sunucunun kendi ayarı |

SIP çekirdeği yalnızca bu iki soketi açar (`sip/opensips/opensips.cfg`: `udp:` ve `tls:`); düz TCP ve WebSocket dinleyicisi yoktur, bunlar için port açmayın. SIP'in ve sesin portları `SIP_IP` ve `RTP_INTERFACE` adresindedir (IPv6 açıksa `SIP_IP6` ve `RTP_INTERFACE`'teki IPv6 adresinde de; örnek kurallar iki aileyi de kapsar); sunucunun birden çok adresi varsa kuralı o adresle de daraltabilirsiniz (`ip daddr`, `ip6 daddr`).

### Dışarıya açılmaması gerekenler

Sunucunun ağındaki yerleşimde (`docker-compose.host.yml`) hepsi yalnızca 127.0.0.1'i dinler; güvenlik duvarı bunu ikinci kez sağlar. Kimlik doğrulaması olmayanlar kalın.

| Port | Ne |
|---|---|
| 58080/TCP | Yönetim API'si ve panel (`API_PORT`); dışarıya yalnızca Caddy (443) üzerinden |
| 58090/TCP | Motor (`ENGINE_TOKEN` ister, ama sır ve parola özetleri ağda şifresiz gider) |
| 58091, 58092, 58093/TCP | API'nin, rater'ın, çağrı izi toplayıcısının ölçüleri |
| **8888/TCP** | SIP çekirdeğinin ölçüleri ve **`/RPC2` (XML-RPC yönetim kapısı: çağrı kapatır)** |
| **8899/UDP** | **SIP çekirdeğinin yönetim soketi (`MI_SOCKET`)** |
| **2223/UDP** | **rtpengine'in denetim soketi** |
| 2225/TCP | rtpengine'in ölçüleri |
| **59060/UDP** | **Çağrı izi toplayıcısının HEP girişi** |
| 9061/UDP | SIP çekirdeğinin HEP kopyalarını gönderdiği soket (`HEP_PORT`). Gelen HEP işlenmez (T-51); yine de yalnızca 127.0.0.1'de açılır. Sunucu dışında bir `HEP_TARGET` verilince `SIP_IP`'de açılır: `HEP_IP` ile o toplayıcıya bakan iç adrese alın ya da güvenlik duvarında kapalı tutun |
| 56432, **56379**, **54222**, 58123/TCP | PostgreSQL, **Redis**, **NATS**, ClickHouse |
| 59090, 59093, 59100/TCP | Prometheus, Alertmanager, node-exporter (`--monitoring`) |

### Örnek kurallar

`deploy/prod/nftables/softswitch.nft` kendi tablosunu (`inet softswitch`) kurar ve sunucudaki başka kurallara (Docker'ınkiler dahil) dokunmaz. Değiştirilecek değerler dosyanın başındadır (`define`): SIP portları, ses aralığı, SSH portu ve SSH'ye girebilecek adresler (`ADMIN_V4`, `ADMIN_V6`; varsayılan herkes, kendi ağlarınızla daraltın), panel portları. Oranlar kuralların içindedir (nftables orada değişken almaz) ve `RATE:` ile işaretlidir.

Sırasıyla:

1. **Durum izlemesiz SIP ve ses.** 5060/UDP'ye ve ses aralığına gelen ve oradan çıkan paketler bağlantı tablosuna (`nf_conntrack`) yazılmaz (`notrack`). Sahte kaynaklı taşkın her pakette yeni bir kaynak kullanır; izlenseydi tabloyu doldurur ve SSH dahil bütün bağlantıları düşürürdü. Bu yüzden SIP ve ses kuralları duruma değil porta bakar.
2. **Giriş zinciri** varsayılan olarak düşürür (`policy drop`); `lo`, kurulumun Docker ağları (`docker0`, `br-*`), kurulmuş bağlantıların dönüşleri, gerekli ICMP ve IPv6 komşu keşfi, sınırlı ping, SSH (yeni bağlantı dakikada 10), 80/443 ve aşağıdaki SIP zincirleri geçer. Ses aralığı herkese açıktır: arayanın sesi çoğu zaman sinyalleşmesinden başka bir adresten gelir, aktarıcı da hiçbir çağrının tutmadığı porta gelen paketi yok sayar. Geri kalan her şey (yukarıdaki yönetim portları dahil) düşer; düşenler zincirin sonundaki sayaçta görünür.
3. **SIP (UDP).** Taşkın yüzünden engellenmiş kaynak düşer; bilinen adres (`known_v4`, `known_v6`; aşağıda) geçer; bilinmeyen kaynak başına saniyede 100 paket (200'lük sıçrama), aşan kaynak iki dakika düşer (`sip_banned_v4`/`_v6`); bilinmeyen kaynakların **hepsine birden** saniyede 2.000 paket. Son sınır sahte kaynaklı taşkın içindir: kaynak başına sınırı her paket yeni kaynakla geçer, ama hepsi birlikte bu tavana takılır. Bilinen adresler daha önce geçtiği için tavandan etkilenmez; taşkın sırasında da müşterilerin ve sağlayıcıların trafiği geçmeye devam eder.
4. **SIP (TLS).** Yalnızca yeni bağlantılar sayılır: bilinmeyen kaynak başına aynı anda en çok 50 bağlantı (aşan reddedilir) ve saniyede 10 yeni bağlantı. TCP sahte adresten kurulamaz; yarım açık bağlantı taşkınına karşı `net.ipv4.tcp_syncookies = 1` (çoğu dağıtımda varsayılan) yeterlidir.

Oranlar SIP çekirdeğinin kendi sınırından (bilinmeyen kaynak için 2 saniyede 30 istek, `pike`) bilerek gevşektir: kesin sınır uygulama katmanında kalır (orada engeller panelde görünür ve kaldırılabilir), ağ katmanı yalnızca taşkını keser. Kaynak başına 100 paket, tek bir adresten çok sayıda kullanıcıyla parola ile giren bir müşteriye (ör. arkasında yüzlerce telefon olan bir NAT) dar gelebilir: böyle bir adresi bilinen adreslere ekleyin (`KNOWN_EXTRA`, aşağıda) ya da oranı yükseltin. 2.000'lik tavan, bilinmeyen kaynaklardan gelen bütün yeni çağrı ve kayıt trafiğinin üst sınırıdır; parola ile giren trunk'larınız çoksa ona göre ayarlayın.

Uygulamak için (kök kullanıcıyla, değerleri değiştirdikten sonra):

```bash
install -d -m 755 /etc/softswitch
cp deploy/prod/nftables/softswitch.nft deploy/prod/nftables/known-sources.sh /etc/softswitch/
nft -c -f /etc/softswitch/softswitch.nft          # önce yalnızca denetim
# Süreli geri alma: iki dakika içinde iptal edilmezse tablo silinir.
systemd-run --on-active=120 --unit=softswitch-nft-rollback /usr/sbin/nft delete table inet softswitch
nft -f /etc/softswitch/softswitch.nft && /etc/softswitch/known-sources.sh --apply
# Yeni bir SSH oturumu açın, bir çağrı deneyin; her şey yolundaysa:
systemctl stop softswitch-nft-rollback.timer
```

Kalıcı kılmak için yeniden açılışta aynı sırayla yükleyin (ör. `/etc/nftables.conf` içinde `include "/etc/softswitch/softswitch.nft"` ve `include "/etc/softswitch/known-sources.nft"`, `systemctl enable nftables`). **Dikkat:** dağıtımların `/etc/nftables.conf` örneği `flush ruleset` ile başlar; bu, Docker'ın kendi kurallarını da siler (konteynerlerin ağı kopar, Docker yeniden başlatılana kadar). O satırı kaldırın. Kural dosyası her yüklemede kendi tablosunu silip yeniden kurar: bilinen adres kümeleri ve taşkın engelleri boşalır, bu yüzden hemen ardından `known-sources.nft` yüklenmelidir (`--apply` bunu yapar). "Yalnızca bilinen adresler" kipinde boş küme bütün SIP'i keser.

Ayrıca:

- **Docker'ın yayınladığı portlar** giriş zincirinden değil yönlendirme zincirinden geçer; bu dosya onları korumaz. Üretim yerleşiminde yayınlanan portların hepsi 127.0.0.1'e bağlıdır; dışarıya bir port yayınlamayın. Özel Docker ağındaki deneme yerleşimi (`docker-compose.bridge.yml`) için bu kurallar geçerli değildir.
- **Çekirdek içi ses aktarımı** (`RTP_KERNEL_TABLE` ≥ 0) iptables'ın giriş zincirine kendi kuralını ekler; bu dosya ses aralığını kabul ettiği için ikisi birlikte çalışır.
- **Geçici portlar:** Linux'un giden bağlantılar için seçtiği port aralığı (`net.ipv4.ip_local_port_range`, çoğunlukla 32768–60999) ses aralığıyla çakışır; bir programın aldığı port aktarıcıya kalmaz. `net.ipv4.ip_local_reserved_ports = 30000-39999` (ses aralığınız) ile ayırın.
- Taşkın engellerini ve düşenleri görmek için: `nft list set inet softswitch sip_banned_v4`, `nft list chain inet softswitch input` (sayaçlar). Bu katmanın engelleri panelde görünmez (SIP çekirdeği o paketleri hiç görmez).

### "Yalnızca bilinen adresler" kipi

`sip` ve `sip_tls` zincirlerinde `#known-only drop` ile işaretli iki satırın başındaki `#known-only ` kaldırılınca bilinen kümelerde olmayan hiçbir adresin SIP paketi geçmez. Taşkına karşı en güçlü kural budur: rastgele sahte kaynaklar çekirdekte, hiçbir sayaca ve hiçbir sürece dokunmadan düşer. Bedeli:

- Parola ile giren ve adresi değişen trunk'lar (adres çevirisi arkasındaki santraller, telefonlar; kayıt olanlar dahil) bağlanamaz. Sabit adresten parola ile giren bir trunk'ın adresini `KNOWN_EXTRA` ile ekleyin.
- Sağlayıcının çağrı içi istekleri (BYE, re-INVITE) ve cevapları da bu kümeden geçer: sağlayıcının, bağlantıda yazan adres dışından gönderdiği her şey düşer. Ad ile tanımlı sağlayıcı (`host` bir ad ise) eşitleme anında çözülen adresleriyle kümeye girer; adı başka adreslere yönlenen sağlayıcı bir sonraki eşitlemeye kadar düşer. SRV kaydıyla başka sunuculara dağıtan sağlayıcıların adresleri çözülmez: onları `KNOWN_EXTRA` ile ekleyin.
- Panelde eklenen trunk'ın adresi bir sonraki eşitlemeye kadar düşer (uygulama katmanında hemen tanınır).
- Ses aralığı bu kipte de herkese açıktır (yukarıda); isterseniz ses için de aynı kümeyi kullanabilirsiniz, ama sesi başka adresten gelen müşteriler sessiz kalır.

Kipi kullanmasanız da kümeler işe yarar: bilinen adresler hız sınırının ve tavanın önünde geçer.

**Kümelerin eşitlenmesi.** `known-sources.sh` paneldeki verilerden `known-sources.nft` dosyasını yazar: adresle tanınan bütün açık müşteri trunk'larının adresleri ve ağları (`GET /v1/client-trunks`, `auth_type: "ip"`, `enabled`), bütün açık sağlayıcı bağlantılarının adresleri (`GET /v1/vendor-connections`; ad ise çözülür), isteğe bağlı `KNOWN_EXTRA` dosyasındakiler (satır başına bir adres ya da ağ, `#` açıklama). Paneldeki güvenlik duvarının `allow` kuralları kümeye **girmez** (süreli olabilirler; gerekiyorsa `KNOWN_EXTRA`'ya yazın); `deny` kuralları da bu katmana yansımaz (T-27).

```bash
# Salt okunur bir API anahtarı (POST /v1/api-keys, "role": "readonly"),
# yalnızca kökün okuyabildiği bir dosyada:
install -m 600 /dev/null /etc/softswitch/nft-sync.key   # sonra anahtarı içine yazın
# Her dakika (cron):
* * * * * root /etc/softswitch/known-sources.sh --apply >/dev/null
```

Ayarlar ortam değişkenleriyle verilir: `SOFTSWITCH_API` (varsayılan `http://127.0.0.1:58080`), `SOFTSWITCH_API_KEY_FILE` (varsayılan `/etc/softswitch/nft-sync.key`), `KNOWN_EXTRA`; çıktı dosyası ilk argümandır (varsayılan `/etc/softswitch/known-sources.nft`). Gerekenler: `curl`, `jq`, `getent`, `nft`. Betik:

- Anahtarı hiçbir yere yazmaz: `curl`'e standart girişten verir (süreç listesinde de görünmez).
- Dosyayı yalnızca içerik değiştiğinde yeniler (aynı içerikte dokunmaz); `--apply` dosyayı tek işlemde yükler (kümeler boşaltılıp aynı anda doldurulur, arada boş kalmaz). Art arda çalıştırmak güvenlidir.
- Bir şey ters giderse eski dosyayı ve yüklü kümeleri olduğu gibi bırakır ve sıfırdan farklı kodla çıkar: API okunamazsa ya da anahtar yanlışsa, bir sağlayıcının adı çözülmezse (hangi bağlantı olduğunu söyler), adres gibi görünmeyen bir değer gelirse, liste boşsa (boş küme "yalnızca bilinen adresler" kipinde bütün trunk'ları keser; bilerek istiyorsanız `--allow-empty`).
- Adları sunucunun adresi olan aileler için çözer (IPv6 adresi olmayan sunucu bir adın IPv6 adreslerini almaz); bağlantıda adres olarak yazılı IPv6 adresi olduğu gibi alınır.

### Sahte kaynak adresleri ve BCP 38

UDP'de el sıkışma yoktur: saldırgan paketin kaynağına istediği adresi yazabilir. Kaynak başına her sınır (bizim `pike`'ımız, yukarıdaki hız sınırı) bu yüzden sahte kaynaklı taşkını durdurmaz, yalnızca tek kaynaklı taşkını durdurur. Sahte adresle başkasının adresi otomatik engele de sokulabilir (uygulama katmanında da, burada da; bilinen adresler engel kuralına varmadan geçtiği için onlar sokulamaz).

- **BCP 38 (RFC 2827)** kaynak adresinin süzülmesidir ve paketin **çıktığı** ağda uygulanır: bir ağ, kendi müşterilerinden kendine ait olmayan kaynak adresli paket çıkarmaz. Tek bir sunucu başkalarının ağlarında bunu sağlayamaz. Yapabileceğiniz: kendi ağınızın (veri merkezi, bulut sağlayıcısı) bunu uyguladığını ve size sahte kaynaklı paketi kenarında süzdüğünü (uRPF) sormak.
- **`rp_filter`** (`net.ipv4.conf.all.rp_filter = 1`, sıkı) gelen paketin kaynağına dönüş yolunun aynı arayüzden olup olmadığına bakar. Tek arayüzlü ve varsayılan yolu olan bir sunucuda internetteki her adres aynı arayüzden döner; yani internetten gelen sahte kaynakları **durdurmaz**. Yalnızca birden çok arayüzü olan sunucuda, ör. iç ağın adresini taşıyan ama dış arayüzden gelen paketi durdurur. Asimetrik yönlendirmede (gidiş ve dönüş farklı arayüzden) gevşek kip (`2`) gerekir. Kural dosyasındaki `fib saddr . iif oif missing drop` satırı aynı denetimin nftables biçimidir ve bu yüzden kapalı gelir.
- **Bant genişliği dolarsa** sunucuda hiçbir kural işe yaramaz: paket kuralı görene kadar hat zaten doludur. Buna karşı önlem sunucunun önündedir: sağlayıcınızın taşkın koruması (scrubbing), üst yönlendiricide erişim listesi (5060'a yalnızca bilinen adresler: aynı küme, aynı betikle üretilebilir), saldırı altındaki adresin geçici olarak yönlendirilmemesi (blackhole). Bunları sağlayıcınızla önceden konuşun.
- Sunucunun kendisinin yapabildiği: bilinen adresleri sınırların önünde geçirmek (taşkında müşteri trafiği sürer), bilinmeyenlere toplam tavan koymak (SIP çekirdeğine varan yük sınırlı kalır), SIP ve sesi bağlantı tablosunun dışında tutmak (tablo dolup sunucu erişilmez olmaz) ve gerekiyorsa yalnızca bilinen adresleri kabul etmek. Bilinen bir adresi taklit eden sahte paket yine geçer; onu uygulama katmanı (trunk eşleşmesi, parola, limitler) karşılar.

### Uygulama katmanıyla ilişkisi

Katmanlar birbirinin yerine geçmez; dışarıdan içeriye:

| Katman | Nerede | Ne yapar | Görünür mü |
|---|---|---|---|
| Sağlayıcının ağı | Sunucunun önünde | Hat dolmasına karşı süzme, BCP 38 | Sağlayıcıda |
| nftables (bu bölüm) | Sunucunun çekirdeği | Kapalı portlar; kaynak başına ve toplam hız sınırı; isteğe bağlı yalnızca bilinen adresler | `nft list …`; panelde değil |
| `pike` ve `ban:` | SIP çekirdeği | Bilinmeyen kaynak 2 saniyede 30 istek, aşan iki dakika cevapsız; çağrı dışı isteklere cevap yok | Panelde "otomatik engeller", kaldırılabilir (`docs/08-cagri-arayuzu.md`, "Yabancı kaynaklar") |
| `firewall_rules` | Motor (görüntüyle) | Operatörün `deny`/`allow` kuralları; bir dakika içinde, sunucuya girmeden | Panelde, denetim kaydında |
| Trunk eşleşmesi, parola, limitler | Motor | Kimin çağrı yapabileceği | Çağrı kayıtlarında |

Tek bir adresi engellemek ya da izin vermek için panelin güvenlik duvarını kullanın (kök yetkisi istemez, kaydı tutulur, süresi verilebilir). nftables'a yalnızca hacme karşı ve portları kapatmak için başvurun; oranlarını `pike`'tan gevşek tutun ki kesin karar uygulama katmanında kalsın. Paneldeki kuralların sunucunun güvenlik duvarına yansıtılması (yönetim portları her zaman muaf, değişiklik süreli onayla) T-27'nin işidir; o gelene kadar bu dosyalar örnektir ve elle uygulanır.

## Bilinen sınırlar

- Üretim yerleşimi (sunucunun ağı) ve adres çevirisi arkasındaki kurulum gerçek sunucuda denenmedi.
- SIP sertifikasının gerçek bir sertifika otoritesinden (Let's Encrypt) alınması ve yenilenmesi denenmedi; denenen, Caddy'nin kendi otoritesiyle aynı akıştır. Sertifika bitiş alarmı yok.
- Çekirdek içi ses aktarımı hazır değil; ses kullanıcı alanında aktarılır ve kapasiteyi bu sınırlar.
- SIP çekirdeği, bellek hatalarını yakalayan hata ayıklama ayırıcısıyla derlenir; üretim için hızlı ayırıcıya geçiş yük testiyle birlikte değerlendirilecek.
- İzleme isteğe bağlıdır (`--monitoring`, `docs/10-izleme.md`) ve alarmların kime gideceği siz tanımlayana kadar boştur.
- Çağrı izinin diskte tutacağı yer gerçek trafikte ölçülmedi; üretim yerleşiminde toplayıcının SIP çekirdeğinin yanında (sunucunun ağında) çalışması gerçek sunucuda denenmedi.
- Motorun sırrı tek ve ortaktır, ağda şifresiz gider; SIP çekirdeğinin yönetim soketi, rtpengine'in denetim soketi, Redis ve NATS kimlik doğrulamasızdır. Hepsi yalnızca 127.0.0.1'de (ya da Docker ağında) açık kalmalıdır.
- Tek sunucu: sunucu giderse hizmet durur. Motorun çağrı kayıtları günlüğü (`engine-journal`) o sunucunun diskindedir: disk giderse NATS'a ulaşmamış kayıtlar da gider.
- Ağ katmanı kuralları (`deploy/prod/nftables/`) yalnızca örnektir, kurulum uygulamaz; gerçek sunucuda ve gerçek taşkınla denenmedi, oranları ölçülmedi. Paneldeki kurallar sunucunun güvenlik duvarına yansımaz (T-27).
- Enterprise sürümünün kurulumu bu dosyalarla yapılmaz; özel depoda ayrıca hazırlanacak.
