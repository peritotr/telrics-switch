# Özellik Kataloğu

Sippy, PortaSwitch, VOS3000, Sansay, Kolmisoft M4 ve Yeti-Switch'te standart kabul edilen özelliklerin listesi ve bu projedeki durumu.

Sürüm: adında **(E)** olan satırlar yalnızca Enterprise deposundadır; diğerleri Community'dedir.

Durum: **Var** = yazıldı ve testle doğrulandı · **Kısmi** = veri modeli/API var, çağrı yoluna bağlı değil ya da eksik · **Yok** = henüz başlanmadı (hedef faz parantez içinde).

## Hesaplar ve hiyerarşi

| Özellik | Durum | Not |
|---|---|---|
| Müşteri (client) hesapları, prepaid/postpaid, kredi limiti | Var | |
| Sağlayıcı (vendor) hesapları ve bağlantıları | Var | Bakiye = sağlayıcıya borcumuz |
| Bağlantı ve trunk ayarları, "Temel" ve "Gelişmiş" olarak | Var | Sağlayıcı bağlantısında: sağlayıcıya kullanıcı adı/parolayla giriş (Digest MD5, SHA-256, SHA-512-256; `qop=auth`; yalnızca `auth-int` isteyen sağlayıcı desteklenmez, T-15), numara biçimi (`+` dahil), bağlantıya özel arayan numara ve kimlik başlığı, çalma süresi sınırı, en uzun çağrı süresi, hangi hatada sıradaki rota, kodek listesi ve sırası. Trunk'ta: müşterinin numara yazımı (birden çok kural, en uzun eşleşen uygulanır; T-17), en uzun çağrı süresi, trunk limitleri, kodek listesi, kodek çevirisine izin. Her ayarın gerçek çağrılı senaryosu var (`docs/08-cagri-arayuzu.md`, "Bağlantı ve trunk ayarları") |
| Sağlayıcıyı yoklama (SIP OPTIONS) ve "Bağlantıyı dene" | Var | Motor kendi sorar; cevap vermeyen bağlantıya çağrı gönderilmez, cevap verince geri alınır. Panelde durum ve tek seferlik deneme. Varsayılan kapalı |
| Sağlayıcıya kayıt (REGISTER) olma | Var | Bağlantıda açılır (giriş adı ve parolasıyla, istenen süre ve isteğe bağlı alan). SIP çekirdeği kendi adresinden kaydolur, 401/407'ye Digest ile cevap verir, süre dolmadan yeniler; başarısızlıkta sabit aralıkla (`REG_RETRY_SEC`) yeniden dener (artan aralık yok). Durum panelde ("Kayıt" sütunu), `GET /v1/peers`'ta, ölçüde ve alarmda (`VendorRegistrationFailing`). Yalnızca UDP ve TLS (`docs/08-cagri-arayuzu.md`, "Sağlayıcıya kayıt"; ADR-0035) |
| (E) Çok seviyeli bayi (reseller) hiyerarşisi | Var | Her bayi yalnızca kendi alt ağacını görür; test edildi |
| (E) Bayiye toptan tarife, çağrı başına bayi ücretlendirmesi | Var | Zincirdeki her bayi üstüne ödeme yapar |
| (E) Bayi askıya alınınca alt müşterilerin çağrılarının reddi | Var | |
| API anahtarları (yönetici / salt okunur) | Var | Yalnızca özeti (hash) saklanır |
| API tanımı (OpenAPI 3.0) | Var | `GET /v1/openapi.json` çalışan sunucunun kayıtlarından üretilir; genel kaynak uçları ve çekirdeğin bütün özel işlemleri istek ve cevap şemalarıyla (T-21); Redocly denetimi `make test-openapi` |
| Müşteri trunk'ları: IP ile kimlik doğrulama, teknik önek | Var | Trunk başına birden çok adres ve ağ (CIDR), IPv4 ve IPv6; en dar ağ, eşitlikte en uzun teknik önek kazanır |
| IPv6: sinyalleşme, kayıt ve ses | Var | `SIP_IP6` ile SIP çekirdeği IPv6'da da dinler (UDP, TLS); IPv6 kaynaklı trunk'tan çağrı, IPv6 ile kayıt ve kayda çağrı, IPv6 sağlayıcı ve yoklaması; medya aktarıcısı IPv4 ile IPv6 arasında köprü kurar. Varsayılan kapalı (yalnızca IPv4). Laboratuvarın IPv6 ağında sınandı, gerçek sunucuda denenmedi; adla tanımlanan sağlayıcıya ses arayanın ailesinde teklif edilir (`docs/08-cagri-arayuzu.md`, "IPv6"; ADR-0047) |
| Digest (kullanıcı/parola) kimlik doğrulama | Var | Parolayı API üretir |
| Kayıt (REGISTER): giriş yapan trunk nerede olduğunu bildirir | Var | Kayıtlar motorda (Redis); süre sınırları, trunk başına adres sayısı, panelde görme ve silme. Canlı tutma yerine kısa süreli kayıt; TLS ile kayıtlı trunk'a çağrı kaydın bağlantısından gider ve SRTP teklif edilir, bağlantı kapanmışsa sıradaki kayıt denenir (T-07) (`docs/08-cagri-arayuzu.md`, "Kayıt (REGISTER)") |
| Adres çevirisi (NAT) arkasındaki müşteri: sinyalleşme ve ses | Var | Cevaplar ve çağrı içi istekler paketlerin geldiği adrese; sesin gerçek kaynağını medya aktarıcısı öğrenir. Laboratuvarda yönlendirici yok, uyuşmazlık taklit edildi; gerçek yönlendiriciyle denenmedi |
| Çağrı içi yeni teklif (re-INVITE): bekletme, geri alma, yeniden görüşme | Var | İki yönde de medya aktarıcısından ve dialogdaki kodek listesiyle geçer; NAT arkasındaki tarafa gerçek adresinden ulaşır; reddedilen teklif çağrıyı düşürmez; süre, ücret ve kayıt değişmez. Teklifsiz INVITE (teklif 200'de, cevap ACK'te) da ilk INVITE'ta ve çağrı içinde aktarıcıdan geçer (ADR-0043; `docs/08-cagri-arayuzu.md`, "Çağrı içi yeni teklif", "Teklifsiz INVITE") |
| Ölü çağrının kapatılması (medya zaman aşımı); oturum zamanlayıcıları (RFC 4028) | Var | İki taraftan da 60 saniye (`RTP_TIMEOUT`) ses gelmeyen çağrıyı medya aktarıcısı SIP çekirdeğine kapattırır: iki tarafa BYE, kayıt gerçek süreyle. Bekletilen çağrıda süre bir saat (`RTP_HOLD_TIMEOUT`). Oturum zamanlayıcısı başlıkları olduğu gibi geçer, switch kendisi yenileme istemez (`docs/08-cagri-arayuzu.md`, "Ölü çağrı") |
| Faks (T.38) ve tuşlar (DTMF) | Var | Çağrı içinde T.38'e geçiş ve sese dönüş iki yönde de medya aktarıcısından olduğu gibi geçer; kodek listesi faksı düşürmez; faks paketleri akan çağrı ölü sayılmaz. Tuşlar `telephone-event` ya da SIP INFO ile uçtan uca geçer, aralarında dönüşüm yok; T.38 ağ geçidi ve video ayarı yok (`docs/08-cagri-arayuzu.md`, "Faks (T.38) ve tuşlar") |
| Medya aktarıcısı yokken çağrı reddi | Var | Aktarıcı cevap vermez ya da portu kalmazsa yeni çağrı 503 ile reddedilir, sıradaki rota denenmez, kayıt 503 ve ücretsiz; ret sayılır ve alarm verir (`CallsRefusedNoMedia`, `MediaPortsLow`). Aktarıcı geri gelince çağrılar ~10 saniye içinde yeniden geçer. Kayıt seçilen bağlantıyı gösterir, ama gönderilmeyen deneme o bağlantının canlı sayaçlarına ve kalite ölçüsüne (`OnLeg`, `NotSent`) yazılmaz (`cause: "media"`, ADR-0039). Kurulmuş çağrının yeni teklifi aktarıcı yoksa değiştirilmeden geçer (ADR-0030) |
| Erken medya (183), 100rel/PRACK; çağrı içi aktarma (REFER) | Var | SDP'li 183 medya aktarıcısından geçer (arayan aktarıcının adresini görür); `Require: 100rel`, `RSeq`, `RAck` ve PRACK iki yönde olduğu gibi geçer. Çağrı içindeki REFER 403 ile reddedilir, çağrı sürer, ret sayılır (`opensips_refer_refused`, alarm `TransfersRefused`); switch aktarma taşımaz. Erken medyası sessiz ve 60 saniyeden uzun çalan çağrıyı medya aktarıcısı kapatır, bağlantının çalma süresi daha uzun olsa da (ADR-0038; paneldeki ayarın açıklamasında yazılı) (`docs/08-cagri-arayuzu.md`, "Erken medya, PRACK ve aktarma") |
| Müşterinin kayıtlı trunk'ına çağrı teslimi | Kısmen | Mekanizma var (motor rotayı kayda çevirir, SIP çekirdeği kaydın geldiği adrese gönderir); hangi numaranın hangi müşteriye gideceği bir uzantının işi. Çağrı kaydı hedef trunk'ı taşır (`dst_trunk_id`; panelde, listede ve CSV'de görünür; T-05). Hedef trunk'ın kendi limitleri (`max_calls`, `max_cps`; dolunca sıradaki rota ya da 503) ve kodek listesi uygulanır (T-06); hedef müşterinin limitleri de uygulanır, trunk'ın ve müşterinin sınırı yaptığı ve aldığı çağrıları birlikte sayar (T-45, ADR-0042). Müşteri olmayan kaynaktan (numara sağlayıcısı) gelen çağrıyı bir müşteri adına alan genişleme noktası var (`engine.Config.Incoming`, T-04; ADR-0032): müşterinin limitleri, rezervasyonu ve kaydı uygulanır |
| Kullanıcı hesapları ve roller | Var | E-posta ve parolayla giriş, iki rol (yönetici, salt okunur), hesap kilitleme, oturum sonlandırma |
| Parola sıfırlama e-postası | Var | 30 dakika geçerli, tek kullanımlık bağlantı |
| Denetim kaydı (kim, neyi, ne zaman, hangi adresten; değişen alanların önceki ve yeni değerleri) | Var | Bütün yazma uçları, uzantıların kaynakları dahil; sırlar maskelenir; saklama süresi ayarlı; süzgeçleriyle CSV dışa aktarımı (sunucu taraflı akış, T-23). Syslog/SIEM aktarımı yok (T-47) (`docs/06-api.md`, "Denetim kaydı") |
| Panel kullanıcıları için iki adımlı doğrulama (TOTP, yedek kodlar) | Var | Yönetici sıfırlayabilir; sır şifreli saklanır (`SECRET_KEY`). Hesap ayarıyla herkes için zorunlu kılınabilir: kurmamış kullanıcı girişte yalnızca kuruluma ulaşır, API anahtarları etkilenmez (T-23, ADR-0045) |
| SSO (tek oturum açma) | Yok | Yol haritasında (B7'nin kalanı) |
| Müşteri self-servis portalı | Var (Enterprise) | Özel depoda; ödeme bildirme, numara kiralama, iki adımlı doğrulama dahil |

## Tarifeler ve ücretlendirme

| Özellik | Durum | Not |
|---|---|---|
| Önek tabanlı satış ve alış tarifeleri, en uzun eşleşme | Var | Milyonlarca fiyatlı tarife: motor fiyatları yalnızca değiştikleri yerde yeniden okur (5 milyon fiyatta olağan yenileme ~10 ms, önek değişikliği ~0,25 sn; tam okuma ~8 sn, her `RELOAD_FULL_SEC`; T-30, ADR-0048) |
| İlk süre / artım (ör. 60/60, 1/1), bağlantı ücreti, ücretsiz süre | Var | |
| Geçerlilik tarihli tarife sürümleri | Var | |
| CSV tarife yükleme | Var | |
| Hedef engelleme (blocked) | Var | |
| CDR ücretlendirme (müşteri, sağlayıcı) ve tekrar gönderime dayanıklılık | Var | Bayi zinciri ücretlendirmesi (E) |
| Azami çağrı süresinin bakiyeden hesaplanması, süre dolunca çağrıyı kapatma | Var | Para bitmeden 1 sn önce kapatılır; bakiye aşılmaz |
| Gerçek zamanlı bakiye rezervasyonu (Redis) | Var | 60 sn'lik adımlarla; süren çağrılar bakiyeyi paylaşır (`docs/08-cagri-arayuzu.md`) |
| Gün/saat dilimine göre fiyat | Var | Fiyatta gün ve saat penceresi, tarifede saat dilimi (`docs/08-cagri-arayuzu.md`); CSV yüklemede pencere sütunları (T-19) |
| Resmî tatil takvimi | Var | Tatil günü fiyat ve rota pencerelerinde pazar (ya da seçilen gün) sayılır; hesap başına takvim (T-19, ADR-0044) |
| Paket dakikalar | Var | Müşteri ve önek kümesi başına aylık ücretsiz süre; motor saniyeyi rezervasyonla ayırır, rater düşer (`docs/08-cagri-arayuzu.md`, "Paket dakikalar"). Devreden dakika ve paket ücreti yok |
| Taahhütler (asgari harcama) | Yok | |
| Çoklu para birimi ve kur dönüşümü | Var | Taraf ve tarifesi aynı para biriminde; kurlar elle ya da isteğe bağlı olarak Avrupa Merkez Bankası'nın günlük referans kurlarından (`FX_SOURCE=ecb`; günde bir kez, hesabın para birimine çevrilerek; elle girilen kur korunur; T-20); yönlendirme ve raporlar hesabın para biriminde (`docs/08-cagri-arayuzu.md`) |
| Düşük bakiye uyarısı | Var | Müşteri başına eşik; panelde açık ve kapanmış uyarılar; uyarı açılınca müşteriye e-posta. Webhook yok |
| E-posta gönderimi (SMTP ya da Resend) | Var | Kuyruklu, yeniden denemeli; düz metin, Türkçe |
| Otomatik askıya alma | Yok (Faz 5) | Prepaid müşteride bakiye bitince çağrılar zaten 402 ile reddedilir |

## Yönlendirme

| Özellik | Durum | Not |
|---|---|---|
| Yönlendirme planları, önek bazlı sağlayıcı listesi | Var | |
| LCR (en ucuz önce) ve öncelik tabanlı sıralama | Var | |
| Zarar koruması (alış > satış ise rotayı ele) | Var | Plan bazında kapatılabilir |
| Numara dönüşümü (önek sil/ekle) | Var | Sağlayıcı bağlantısı bazında (başa `+` dahil); müşteri trunk'ı bazında numara yazımı (`00…`, `+…`, ulusal) |
| Rota simülasyonu (test aracı) | Var | `POST /v1/route/simulate` |
| (E) Bayi planının müşteriye miras kalması | Var | |
| Karar motorunun OpenSIPS'e bağlanması, hata durumunda sıradaki rotaya geçiş | Var | `docs/08-cagri-arayuzu.md`; karar p99 < 1 ms (laboratuvar) |
| Gerçek çağrılardan CDR (her deneme ayrı bacak), kuyruk üzerinden ücretlendirme | Var | NATS JetStream → rater → PostgreSQL |
| NATS kesintisinde ve motor çökmesinde CDR'nin korunması | Var | Motorun diskindeki günlük (`CDR_JOURNAL_DIR`), açılışta yeniden gönderim; bitişi bildirilmeyen çağrının süresiz ve ücretsiz kaydı; sağlayıcının faturasıyla elle uzlaşılır (ADR-0029, ADR-0040, T-33) |
| Ağırlıklı (yüzde) trafik dağıtımı | Var | Aynı basamaktaki rotalar arasında çağrı başına ağırlıklı kura; ağırlık 0 = yalnızca yedek (`docs/08-cagri-arayuzu.md`) |
| Eşzamanlı çağrı ve CPS limitlerinin uygulanması | Var | Müşteri, müşteri trunk'ı ve sağlayıcı bağlantısı bazında, Redis sayaçları |
| Kalite tabanlı yönlendirme (ASR/ACD/PDD) | Yok (Enterprise) | Motorda genişleme noktaları hazır (`Adjust`, `OnLeg`) |
| Numara taşınabilirliği (MNP/LRN) sorgusu | Yok (Faz 5) | |
| Güvenlik duvarı (uygulama düzeyi) | Var | Adres ve ağ için engelle / izin ver kuralları, otomatik engellerin listesi ve kaldırılması; işletim sistemi düzeyi yok |
| Ağ katmanı koruması (işletim sistemi) | Rehber | Örnek nftables kuralları (kapalı portlar, kaynak başına ve toplam hız sınırı, "yalnızca bilinen adresler" kipi) ve bilinen adres kümesini panelden eşitleyen betik; yönetici elle uygular, ürün yönetmez (T-10; `docs/09-kurulum.md`, "Ağ katmanı koruması"; ürünün yönettiği duvar T-27) |
| Arayan numara (CLI) kuralları, kara/beyaz liste | Var | Öneke göre engelle / izin ver / dönüştür; müşteriye özel ya da genel; kural From'daki ya da `P-Asserted-Identity`'deki numaraya bakar; numarasız arayanı reddetme (433) tek başına tanımlanır; `Privacy: id` isteyen arayanın kimliği sağlayıcıya gitmez (T-16; `docs/08-cagri-arayuzu.md`). Bağlantıya özel dönüşüm bağlantı ayarlarında |
| Zaman bazlı yönlendirme | Var | Rota satırında gün ve saat penceresi, planda saat dilimi; resmî tatile uyar |

## DID (E)

Bu bölümün tamamı Enterprise'dadır.

| Özellik | Durum | Not |
|---|---|---|
| DID sağlayıcıları (kurulum, aylık, dakika maliyeti) | Var | Maliyet bir sağlayıcı hesabına yazılır |
| DID havuzu: toplu içe aktarma, boş/rezerve/atanmış/karantina | Var | |
| Müşteriye atama, kurulum ücreti, serbest bırakma | Var | |
| Aylık DID ücretlerinin satış ve alış faturasına yansıması | Var | |
| DID çağrı kayıtlarının dakika ücretlendirmesi | Var | |
| Gelen DID çağrısının müşteriye yönlendirilmesi (SIP) | Yok (Faz 1-2) | `forward_to` alanı hazır; çekirdekte gereken uçlar hazır (`engine.Config.Incoming`, `routing.ToTrunk`, `AllowCDRField`) |
| Sağlayıcı API entegrasyonu (otomatik sipariş/iade) | Kısmi (Enterprise) | Sağlayıcı sürücüleri yazıldı; gerçek hesapla denenmedi |
| Kısmi ay (pro-rata) ücretlendirme | Yok | |

## Faturalama ve muhasebe

| Özellik | Durum | Not |
|---|---|---|
| (E) Satış faturası (müşteriye): kullanım + DID + vergi | Var | |
| (E) Alış faturası (sağlayıcıdan): kullanım + DID maliyeti | Var | |
| (E) Fatura yaşam döngüsü: taslak → kesildi → ödendi / iptal | Var | İptal, bakiye etkilerini geri alır |
| Ödeme, düzeltme, iade kayıtları; hesap defteri | Var | |
| (E) Aynı döneme mükerrer fatura engeli | Var | |
| Fatura yazdırma ve e-posta bildirimi | Kısmi (Enterprise) | Tarayıcıdan yazdır/PDF ve "yeni fatura" e-postası var; sunucuda üretilen PDF ve ek yok |
| Otomatik dönemsel faturalama | Yok (Faz 6) | Dönem alanı var, zamanlayıcı yok |
| e-Fatura / e-Arşiv (GİB) entegrasyonu | Yok | Türkiye'de satış için gerekli olabilir |
| Sağlayıcı faturası mutabakatı (dispute) | Yok (Faz 6) | |
| Ödeme ağ geçidi entegrasyonu | Yok (Faz 6) | |

## Raporlama ve izleme

| Özellik | Durum | Not |
|---|---|---|
| CDR sorgulama | Var | Şimdilik PostgreSQL'de |
| Özet rapor: çağrı, ASR, ACD, gelir, maliyet, kâr | Var | Müşteri, sağlayıcı, önek, gün kırılımı |
| CDR'lerin ClickHouse'a akışı | Var | Aktarım kuyruğu üzerinden, en-az-bir-kez; özet raporu ClickHouse'tan okunur, çağrı listesi PostgreSQL'den (isteğe bağlı ClickHouse'tan); PostgreSQL'den arşivlenen kayıtların tekil görünümü, listesi ve CSV'si ClickHouse'tan, aynı biçimde (T-24) |
| Canlı çağrı listesi, çağrı sonlandırma | Var | Panel ekranı ve API (`/v1/live/calls`) |
| Canlı izleme ölçüleri (eşzamanlı çağrı, CPS, ASR, ACD, PDD, NER, ülke/sağlayıcı/müşteri kırılımı) | Var | Panel ekranı ve API (`/v1/live`); sayaçlar motorun belleğinde, son 60 saniye |
| CDR dışa aktarma (CSV) | Var | Süzgeçle eşleşen tüm kayıtlar, istek başına en çok 200.000 |
| Zamanlanmış raporlar | Var | Günlük, haftalık ya da aylık özet, düz metin e-posta; ek dosya yok |
| Çağrı izi: SIP iletilerinin saklanması, çağrı detayında SIP akışı, pcap indirme | Var | Kurulumun kendi toplayıcısı (HEP → ClickHouse, varsayılan 7 gün); panelde akış diyagramı ve Call-ID ile arama; canlı çağrılar satırından süren çağrının izine geçiş, "Yenile" ile o ana kadarki iletiler (T-25); pcap saklanan iletilerden üretilir (hat görüntüsü değil); parola özeti saklanmaz. Yalnızca sinyalleşme: ses kaydı yok. Müşterinin reddedilen çağrısı da izlenir (INVITE, ret, ACK; T-26); yabancı kaynakların istekleri izlenmez (`docs/10-izleme.md`) |
| Dış iz toplayıcısına gönderim (HEP/Homer), Prometheus ölçüleri, alarmlar | Var | `HEP_TARGET`; ölçüler ve kurallar `docs/10-izleme.md`. Hazır gösterge paneli (Grafana) yok |
| Dolandırıcılık tespiti | Yok (Enterprise) | Motorda genişleme noktaları hazır (`Adjust`, `OnLeg`) |

## Sinyalleşme ve medya

| Özellik | Durum | Not |
|---|---|---|
| SIP çağrı geçişi, dialog takibi | Var | OpenSIPS 3.6; laboratuvar testi geçti |
| Medyanın sistem üzerinden geçirilmesi | Var | rtpengine, kullanıcı alanı modu |
| Topoloji gizleme | Var | Via, Record-Route ve Contact gizlenir; Call-ID aynı kalır |
| Yabancı kaynaklardan taşkın ve tarama koruması | Var | Adres başına hız sınırı ve otomatik engelleme; yabancının çağrı dışı isteklerine cevap verilmez. Sahte kaynak adresli taşkına karşı değil |
| TLS (sinyalleşme) | Var | Arayan 5061'e TLS ile bağlanabilir; taşıması `tls` olan sağlayıcıya TLS ile gidilir ve sertifikası doğrulanır. Trunk başına "TLS zorunlu" ya da "TLS ve istemci sertifikası" kuralı var. Sertifika kendiliğinden alınır ve yeniden başlatmadan yüklenir (gerçek otoriteyle denenmedi). Sağlayıcı sertifikasındaki adı motor denetler; adı uymayan bağlantıya çağrı gönderilmez |
| SRTP (ses) | Var | SDES anahtarlı. Sağlayıcı ayağı, bağlantısı TLS ise şifrelidir; kayıtlı trunk'a giden ayak, trunk TLS ile kayıtlıysa (T-07); arayan ayağı arayanın teklif ettiği gibidir; aradaki çeviriyi rtpengine yapar. Arayan DTLS-SRTP teklif edebilir (yalnızca anlaşma denendi); sağlayıcıya teklif edilmez |
| Transcoding (kodek çevirisi) | Var (tek düğüm) | Bağlantıda ya da trunk'ta açılır (varsayılan kapalı): trunk'ın ve bağlantının kodek listeleri örtüşmezse çağrı yine gönderilir, ses aynı sunucudaki rtpengine'de çevrilir (iki yön, çağrı içindeki yeni teklif dahil); aynı anda çevrilen çağrı sınırı `TRANSCODE_MAX` (200), dolunca rota atlanır; ölçü ve alarm var. Karar teklife değil listelere göre (listesi olmayan trunk çevrilmez). İmajda G.711, G.722, G.729 (bcg729), Opus, Speex, GSM; AMR/iLBC yalnızca çözülür. Laboratuvarda yalnızca PCMA ↔ PCMU sınandı (T-09, ADR-0041). Ayrı çeviri havuzu Enterprise |
| Çekirdek (kernel) medya aktarımı | Yok (Faz 4) | Linux sunucu gerekir |
| Kümeleme, yüksek erişilebilirlik | Yok (Faz 4) | |
| STIR/SHAKEN | Yok | ABD trafiği taşınacaksa gerekli |
| H.323 | Yok | Kapsam dışı varsayıldı |
