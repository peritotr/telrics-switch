# Çağrı Arayüzü (sürüm 1)

SIP çekirdeği ile route-engine arasındaki arayüz. HTTP + JSON; SIP sunucusuna özgü hiçbir kavram taşımaz. OpenSIPS betiği (`sip/opensips/opensips.cfg`) yalnızca sorar, cevabı uygular ve olanı bildirir.

Her istekte `"v": 1` bulunur; motor tanımadığı sürümü 400 ile reddeder. Arayüzü bozan her değişiklik sürümü artırır.

Her istek motorun ortak sırrını taşır (aşağıda "Kimlik doğrulama"); taşımayan istek 401 alır.

## Kimlik doğrulama

Motoru arayan herkes (SIP çekirdeği, yönetim API'si, operatörün kendi betikleri) aynı ortak sırrı her istekte standart HTTP başlığıyla gönderir:

```
Authorization: Bearer <ENGINE_TOKEN>
```

- **Ayar:** sır, motora ve onu arayan her servise `ENGINE_TOKEN` ortam değişkeniyle verilir; üçünde de aynı değer olmalıdır. Yalnızca harf, rakam ve `- . _ ~ + / =` içerebilir (RFC 6750'deki belirteç karakterleri); başka karakter taşıyan değerle motor ve SIP çekirdeği başlamaz. Üretimde kurulum betiği 48 onaltılık haneli rastgele bir değer üretir (`docs/09-kurulum.md`).
- **Denetim:** motor gelen değerin ve kendi değerinin SHA-256 özetlerini sabit sürede karşılaştırır (`crypto/subtle`); cevap süresi sırrın ne kadarının tutturulduğunu ele vermez. Sırsız, yanlış sırlı ya da başka türde kimlik bilgisi (`Basic`, başka başlık) taşıyan istek, gövdesi okunmadan `401 Unauthorized` ve `WWW-Authenticate: Bearer` ile reddedilir. Var olmayan yollar da 401 alır.
- **Kapsam:** `/v1/` altındaki bütün uçlar: `authorize`, `events`, `source`, `credentials`, `register`, `registrations`, `unregister`, `ban`, `bans`, `unban`, `reload`, `stats`, `peers`, `probe`, `live`, `calls`, `hangup`.
- **Açık kalan iki yol (bilinçli karar):** `GET /metrics` ve `GET /healthz`. `/metrics` yalnızca sayaç verir (çağrı sayıları, karar süresi, hata sayaçları; numara, müşteri adı, parola özeti ya da sır içermez) ve Prometheus onu, kurulumdaki diğer beş servisin aynı biçimde açık olan ölçü uçlarıyla birlikte okur; tek birini sırla kapatmak trafiğin hacmini gizlemez (SIP çekirdeğinin ve medya aktarıcısının sayaçları aynı şeyi söyler), ama izlemeye bir sır daha taşıtır. `/healthz` yalnızca "ok" der. İkisi de özel ağ varsayımına dayanmaya devam eder.
- **Sır verilmemişse** (`ENGINE_TOKEN` boş) motor eskisi gibi herkese cevap verir ve açılışta günlüğe bir uyarı yazar. Bu, sırrı olmayan eski bir kurulumun yükseltme sırasında çağrı kaybetmemesi içindir; kalıcı bir çalışma biçimi değildir.
- **Sır nerede görünmez:** günlüklerde (reddedilen istek dakikada en çok bir kez, yöntemi, yolu ve kaynak adresiyle yazılır; gönderilen değer yazılmaz), hata iletilerinde, panelde ve `GET /v1/system` cevabında. Yönetim API'si motorun 401 cevabını çağırana 503 olarak iletir ("ENGINE_TOKEN eksik ya da farklı"); 401 iletseydi panel kendi oturumunun bittiğini sanardı.
- **Yanlış ayarın belirtisi:** SIP çekirdeğinin sırrı motorunkiyle uyuşmazsa motor her soruyu reddeder ve çağrılar 503 alır (yabancı adreslerin istekleri cevapsız kalır). Sayaç `softswitch_engine_unauthorized_total`, alarm `EngineRefusingCallers` (`docs/10-izleme.md`).
- **SIP sunucusundan bağımsızlık:** başlık HTTP'nin kendi kimlik başlığıdır; arayüzün gövdesi ve sürümü (`v: 1`) değişmedi. OpenSIPS betiği başlığı her sorudan önce `rest_append_hf` ile ekler (eklenen başlık yalnızca bir sonraki isteğe gider; `route[engine_token]`). Satırın kendisi konteyner açılırken `/run/opensips/engine-token.cfg` dosyasına yazılır (yalnızca sahibi okur), şablondaki yer tutuculardan geçmez.

Senaryo (`make test-routing`, "token"): sırsız ve yanlış sırlı isteklerin her uçta 401 alması (parola özeti, çağrı kararı, trafik, çağrı kapatma, engel kaldırma, yeniden yükleme), sırrın `Basic` ya da başka başlıkla kabul edilmemesi, doğru sırla 200, `/metrics` ve `/healthz`'in açık kalması, reddedilenlerin sayılması, bu sırada bir çağrının kurulup kaydedilmesi ve sırrın SIP çekirdeği ile motorun günlüklerinde geçmemesi. Laboratuvar sırla çalışır (`deploy/compose/lab.env`); bütün senaryolar bu yüzden başlığı sınar.

Sınırlar — bu değişiklikten sonra hâlâ özel ağ varsayımına dayananlar:

- **Sır ağda açık metin gider:** arayüz HTTP'dir, TLS yoktur. Motor ile SIP çekirdeği arasındaki ağı dinleyebilen biri sırrı da, `credentials` cevabındaki parola özetlerini de okur. Sır, porta erişebilen ama trafiği dinleyemeyen birine karşı korur. Üretim yerleşiminde motor yalnızca `127.0.0.1`'de dinler; servisler ayrı sunuculara taşınırsa (Faz 4) aradaki ağ şifrelenmeli ya da arayüze TLS eklenmelidir.
- **SIP çekirdeğinin yönetim soketi** (`MI_SOCKET`, `mi_datagram`, UDP): kimlik doğrulaması yoktur ve bu işte ele alınmadı. Bu sokete erişebilen biri çağrıları kapatabilir (`dlg_end_dlg`), taşkın engellerini kaldırabilir ve OpenSIPS'in diğer yönetim komutlarını çalıştırabilir. Üretimde `127.0.0.1:8899`'da dinler; yalnızca motorun eriştiği adreste açık olmalıdır.
- **Ölçü uçları:** motorun `/metrics`'i ve diğer servislerinkiler (API ve rater `METRICS_LISTEN`, SIP çekirdeği 8888, rtpengine 2225, trace-collector) sırsızdır.
- **rtpengine'in denetim soketi** (`RTP_CONTROL`) ve **toplayıcının HEP portu** kimlik doğrulamasızdır.
- **Veri depoları:** Redis ve NATS parolasızdır (PostgreSQL ve ClickHouse parolalıdır); Redis'e erişen, çağrı durumunu ve ayrılmış tutarları değiştirebilir.
- **Tek sır:** bütün arayanlar aynı sırrı kullanır; hangi arayanın ne yaptığı ayırt edilmez ve yalnızca okuma yetkisi olan bir sır yoktur. Sırrı değiştirmek üç servisin birlikte yeniden başlatılmasını gerektirir (süren çağrılar düşer); eski ve yeni sırrın bir süre birlikte geçerli olduğu bir geçiş yoktur.
- Yanlış sır denemeleri sayılır ama sınırlanmaz ya da engellenmez; 48 onaltılık hanelik rastgele bir değer için deneme-yanılma gerçekçi değildir, kısa ve elle seçilmiş bir değer için öyledir.

## Akış

```
INVITE ─▶ POST /v1/authorize ─▶ izin + ilk rota            (ya da ret kodu)
           sağlayıcı hata verdi ─▶ POST /v1/events failed   ─▶ sıradaki rota ya da "bitti"
           200 OK               ─▶ POST /v1/events answered
           BYE / süre doldu     ─▶ POST /v1/events ended
```

## `POST /v1/authorize`

```json
{"v": 1, "call_id": "abc@10.0.0.5", "src_ip": "10.0.0.5", "transport": "tls", "tls_name": "pbx.ornek.com",
 "src": "902120000000", "dst": "905321112233"}
```

`transport` isteğin SIP çekirdeğine nasıl ulaştığıdır (`udp`, `tcp`, `tls`); `tls_name` arayanın sunduğu ve SIP çekirdeğinin doğruladığı sertifikanın ortak adıdır (CN), yoksa boştur. İkisi de isteğe bağlıdır; göndermeyen bir SIP çekirdeğinin çağrıları şifresiz sayılır.

İki isteğe bağlı alan daha arayanın kimliğini taşır (T-16; sürüm 1 kalır, ADR-0002): `asserted`, arayan tarafın bildirdiği numaradır (`P-Asserted-Identity` başlığındaki URI'nin kullanıcı kısmı; `sip:`/`sips:` URI'de `@` öncesi, `tel:` URI'de numara, parametresiz; başlıkta birden çok değer varsa ilki); `privacy`, `Privacy` başlığının geldiği gibi değeridir (birden çok başlık virgülle birleşir). SIP çekirdeği yalnızca başlık varsa alanı koyar ve değerlerini yorumlamaz. Alanlar yoksa karar bugünküyle aynıdır: kimlik numarasına bakan kural yoksa `asserted` hiçbir şeyi değiştirmez, `privacy`'de `id` yoksa kimlik başlığı bağlantının ayarına göre işlenir (aşağıda "Arayan numara kuralları" ve "Arayanın gizlilik isteği").

Cevap her zaman 200'dür; karar gövdededir:

```json
{"v": 1, "allowed": true, "sip_code": 200, "reason": "ok", "max_duration_sec": 3000,
 "route": {"uri": "sip:905321112233@198.51.100.7:5060;transport=udp"}}
```

`route` içinde iki isteğe bağlı alan daha olabilir; ikisi de yalnızca çağrı bir müşterinin kayıtlı trunk'ına gidiyorsa gelir (aşağıda "Müşterinin trunk'ına çağrı"): `next_hop`, isteğin gerçekten gönderileceği adrestir (`uri` isteğin kime yazıldığıdır ve değişmez); `media`, o tarafa ne teklif edileceğidir (`"rtp"`: şifresiz ses). Alanlar yoksa eski kural geçerlidir: istek `uri`'ye gider, ses sinyalleşme TLS ise şifrelidir; `next_hop` varsa sinyalleşmenin TLS olup olmadığına `next_hop`'a bakılır (isteğin gerçekten gittiği yer). `next_hop` `;transport=tls` taşıyorsa istek yalnızca o adresle zaten açık olan bağlantıdan gönderilir; bağlantı yoksa SIP çekirdeği yenisini açmaz, deneme gönderilemeyen deneme olarak `failed` 503 ile bildirilir.

`route` ayrıca, bağlantının ya da arayanın trunk'ının kendi ayarı varsa, şu isteğe bağlı alanları taşır (aşağıda "Bağlantı ve trunk ayarları"); hiçbiri yoksa SIP çekirdeği eskisi gibi davranır:

| Alan | Anlamı |
|---|---|
| `auth` | `{"username":…,"password":…}`: karşı uç kimlik sorarsa (401/407) bununla bir kez cevaplanır |
| `caller` | Bu rotada gösterilecek arayan numara; cevabın üst düzeydeki `caller` alanından önce gelir |
| `identity` | Arayanın bildirilen kimliği (P-Asserted-Identity) için: `remove` (çıkar) ya da `caller` (bu rotada gösterilen arayan numarayla yaz); yoksa olduğu gibi iletilir. Gizlilik isteyen arayanın (`privacy`'de `id`) her rotasında `remove` |
| `ring_timeout_sec` | İlk ilerleme cevabından sonra kesin cevap için beklenecek süre; dolunca deneme 408 ile başarısız sayılır |
| `max_duration_sec` | Bu rotada çağrının sürebileceği azami süre; cevabın üst düzeydeki `max_duration_sec` değeriyle küçük olanı geçerlidir |
| `codecs` | Karşı uca teklif edilecek ses kodekleri, sırasıyla (`["PCMA","PCMU"]`); diğerleri tekliften çıkarılır, tuş sinyali (`telephone-event`) kalır |
| `transcode` | Yalnızca ses çevrilecekse: arayan tarafının kodekleri (`["PCMA"]`). O zaman `codecs` arayan sunmasa da teklif edilir, medya aktarıcısı iki taraf arasında çevirir; çağrı içinde karşı uçtan gelen yeni teklife bu liste uygulanır (aşağıda "Kodek çevirisi", T-09) |

`caller` yalnızca bir kural arayan numarayı değiştirdiğinde gelir: sağlayıcılara arayan olarak gösterilecek numaradır (aşağıda "Arayan numara kuralları"). Alan yoksa SIP çekirdeği gelen numaraya dokunmaz.

`source_known`, çağrının sonucundan bağımsız olarak kaynak adresin bir müşteriye ait olup olmadığını söyler; SIP çekirdeği yabancılara karşı korumasını buna göre uygular (aşağıda "Yabancı kaynaklar").

`trace` (isteğe bağlı; yoksa yanlış) çağrının bir müşteriye ait olduğunu söyler, kabul edilmiş ya da reddedilmiş: SIP çekirdeği reddedilen çağrının sinyalleşmesini (INVITE, ret, ACK) yalnızca bu alan doğruysa iz toplayıcısına kopyalar (ADR-0046, `docs/10-izleme.md`, "Çağrı izi"). Motor alanı reddi kaydettiği durumda verir; bilinmeyen kaynakta ve güvenlik duvarının engellediği adreste vermez. `source_known`'dan farkı: parolayla tanınan arayanda ve müşteri olmayan kaynaktan bir müşteri adına alınan çağrıda `source_known` yanlış, `trace` doğrudur. Alanı göndermeyen eski motorla SIP çekirdeği yalnızca kabul edilen çağrıları izler.

Reddedilen çağrıda `allowed` false, `route` yoktur; SIP çekirdeği arayana `sip_code` ve `reason` ile cevap verir.

### Kaynağın trunk ile eşleşmesi

`auth_user` doluysa trunk yalnızca o kullanıcı adına göre bulunur (aşağıda "Kullanıcı adı ve parolayla giriş"). Boşsa `src_ip` belirler. Adresle tanınan bir trunk'ın istenen sayıda kaynağı olabilir: tek adresler ve ağlar (CIDR), IPv4 ve IPv6 (`client_trunks.addresses`).

- Aday trunk'lar, kaynak adresi adreslerinden birine eşit olan ya da ağlarından birinin içinde kalan **ve** teknik öneki aranan numaranın başına uyan açık trunk'lardır.
- Adaylar arasında **en dar ağ** kazanır: tek adres ağdan, `/28` `/24`'ten önce gelir. Aynı ağı paylaşan trunk'lar arasında, eskiden olduğu gibi, **en uzun teknik önek** kazanır. Daha dar ağdaki trunk'ın öneki numaraya uymuyorsa çağrı daha geniş ağdaki uyan trunk'a kalır; daha geniş ağdaki uzun önek, daha dar ağdaki kısa öneki geçemez.
- Aynı adres ya da ağ, aynı teknik önekle iki açık trunk'ta bulunamaz (veritabanı reddeder); bu yüzden eşleşme her zaman tektir.
- IPv4 adresinin IPv6 yazımı (`::ffff:203.0.113.10`) IPv4 sayılır. Adres olarak okunamayan `src_ip` hiçbir trunk'a uymaz.
- IPv6 adresi her yazımda aynı adrestir: OpenSIPS onu tam ve büyük harfle gönderir (`FD30:0:0:0:0:0:0:21`); motor girişte olağan kısa biçime çevirir (`fd30::21`) ve çağrı kaydına, kayıtlara ve yasaklara öyle yazar (aşağıda "IPv6"; ADR-0047).
- Arama bellekte yapılır: adresler ve ağlar tek bir tabloda durur, kaynak adres kullanımdaki her ağ uzunluğu için (en uzundan kısaya) bir kez aranır. Süre ağ sayısına değil, kullanılan farklı uzunluk sayısına bağlıdır (5.000 trunk ve 15.000 kaynakla karar süresi tek adresli kurulumla aynı düzeyde, mikrosaniyenin altında fark).
- Trunk kuralı (`security`) eşleşen trunk'a göre uygulanır; ağdaki her adres için aynıdır.

| Kod | Neden |
|---|---|
| 403 | Bilinmeyen kaynak adresi (bir uzantı çağrıyı bir müşteri adına almadıysa; aşağıda "Müşteri olmayan kaynaktan gelen çağrı"), askıdaki hesap, tarifede olmayan ya da engelli hedef; arayan numara bir kuralla engellendi (`caller not allowed`); trunk TLS istiyor ve çağrı TLS ile gelmedi (`tls required`); trunk istemci sertifikası istiyor ve doğrulanmış sertifikanın adı uymuyor (`client certificate required`) |
| 433 | Numarasız arayan bir "numarasızı reddet" kuralına takıldı (`anonymity disallowed`; RFC 5079 "Anonymity Disallowed") |
| 402 | Bakiye yetersiz |
| 480 | Çağrının gideceği trunk kayıtlı değil (`not registered`; yalnızca bir uzantı çağrıyı trunk'a yönlendirdiğinde) |
| 503 | Yönlendirme planı ya da rota yok; müşteri limiti doldu (`call limit reached`, `cps limit reached`); trunk'ın kendi limiti doldu (`trunk call limit reached`, `trunk cps limit reached`); tüm rotalar dolu (`all routes busy`); sistemin toplam çağrı sınırı doldu (`system capacity reached`; motor `MaxCalls` ile kurulduysa); durum deposu erişilemez |

`max_duration_sec` cevaplandıktan sonra çağrının sürebileceği azami süredir: o an boşta olan bakiyenin yettiği süre (en çok 7200 sn), trunk'ın kendi azami süresi varsa ikisinin küçüğü. SIP çekirdeği süre dolunca çağrıyı iki tarafa BYE göndererek kapatır. Bu bir üst sınırdır; bakiye başka çağrılara giderse motor çağrıyı daha erken kapatır (aşağıda "Çağrıyı kapatma").

## `POST /v1/events`

```json
{"v": 1, "call_id": "abc@10.0.0.5", "event": "failed", "sip_code": 503}
```

| Olay | Anlamı | Cevap |
|---|---|---|
| `failed` | Denenen rota kesin bir hata cevabı verdi ya da zaman aşımına uğradı (`sip_code`: gelen kod, cevap yoksa 408, arayan vazgeçtiyse 487, rotanın kodeklerinden hiçbirini arayan sunmadığı için hiç gönderilmediyse 488) | Denenecek rota varsa `{"v":1,"route":{"uri":…}}`; yoksa `{"v":1}` ve çağrı biter |
| `ringing` | Denenen rota ilk ilerleme cevabını (180/183) verdi; motor çevirme sonrası gecikmeyi (PDD) bundan ölçer. İsteğe bağlıdır | `{"v":1}` |
| `answered` | Çağrı cevaplandı | `{"v":1}` |
| `ended` | Çağrı kapandı. Cevaplanmamış çağrıda `sip_code` 300 ve üstüyse çağrı o kodla biter ve kaydedilir; sıradaki rotaya hiçbir kuralda geçilmez (SIP çekirdeği bunu, çağrıyı kendi sorunu yüzünden kuramadığında gönderir: medya aktarıcısı teklifi almadı, 503 ve `cause: "media"`; aşağıda, "Medya aktarıcısı teklifi almazsa") | `{"v":1}` |

İsteğe bağlı alan `cause` (ADR-0039; sürüm 1'de, eski motor alanı yok sayar):

| Değer | Hangi olayda | Anlamı |
|---|---|---|
| `media` | `ended`, cevaptan önce | Deneme hiç gönderilmedi: SIP çekirdeğinin medya aktarıcısı teklifi almadı. Teklifsiz INVITE'ta teklif sağlayıcının 200'ündedir: aktarıcı onu almazsa SIP çekirdeği `answered` yerine bunu gönderir ve çağrıyı kapatır (ADR-0043; sağlayıcıya ulaşılmıştır, ama ses kurulamadığı için durum aynıdır). Motor kaydı her `ended` gibi yazar (motorun seçtiği bağlantıyla, `sip_code` ile, ücretsiz), ama denemeyi o bağlantının canlı sayaçlarına yazmaz ve `Config.OnLeg`'e `NotSent` ile bildirir |

Başka olayda, cevaptan sonra ya da bilinmeyen bir değerle `cause` yok sayılır. Motor istek gövdesindeki bilinmeyen alanları reddetmez; isteğe bağlı alanlar bu yüzden SIP çekirdeği ile motorun hangisi önce yükseltilirse yükseltilsin zararsızdır.

```json
{"v": 1, "call_id": "abc@10.0.0.5", "event": "ended", "sip_code": 503, "cause": "media"}
```

Sıradaki rotaya geçip geçmemeye motor karar verir: 404, 480, 484, 486, 487 ve tüm 6xx kodları çağrıyı bitirir (karşı uç reddetmiştir); diğer hata kodlarında sıradaki rota denenir. Hata veren bağlantının kendi kuralı varsa o geçerlidir (`vendor_connections.failover_mode`: `always` her hatada geçer, `never` hiçbirinde, `custom` yalnızca `failover_codes` içindeki kodlarda); arayanın vazgeçmesi (487) hiçbir kuralda sıradaki rotaya geçirmez. Kural, olay geldiği andaki yapılandırmadan okunur.

Her olay birden çok kez gönderilebilir: yinelenen `answered` cevap anını değiştirmez, bitmiş ya da bilinmeyen çağrının olayı yok sayılır. Motor durum deposuna erişemezse 503 döner; SIP çekirdeği bunu "rota yok" sayar. `ended` hiç gelmezse (SIP çekirdeği yeniden başladı ya da çağrı bittiğinde motora ulaşamadı) motor çağrının kaydını durumu düşerken kendisi yazar: aşağıda, "Bitişi bildirilmeyen çağrı".

Zaman damgalarını motor kendi saatiyle, olayı aldığı anda koyar; arayüzde saat taşınmaz.

## Rota sırası ve ağırlıklı dağıtım

Motorun `routes` listesi basamaklardan oluşur. Bir basamak, birbirine eşit duran rotalardır: öncelik politikasında önceliği aynı olanlar, LCR politikasında alış fiyatı **ve** önceliği aynı olanlar. Basamaklar her zaman sırayla denenir (LCR'de ucuz olan, sonra küçük öncelik sayısı); ağırlık bu sırayı değiştirmez, yalnızca bir basamağın içindeki çağrıları paylaştırır.

- Basamak içindeki sıra çağrı başına ağırlıklı kurayla belirlenir: ilk denenecek rota ağırlığıyla orantılı olasılıkla seçilir, ikinci sıra kalanlar arasından aynı yolla, böyle devam eder. Ağırlıkları 70 / 20 / 10 olan üç rota çağrıların yaklaşık %70, %20 ve %10'unu ilk deneme olarak alır; ilki hata verirse çağrı basamağın kalanına, sonra sonraki basamağa geçer.
- **Ağırlık 0**, "yalnızca yedek" demektir: basamağında ağırlığı olan başka rota varken hiçbir çağrıda ilk denenmez, hep basamağın sonunda durur. Bir basamaktaki bütün ağırlıklar 0 ise sıra sabittir (bağlantı kimliğine göre).
- Kura çağrının `call_id` değerinden türetilir: aynı çağrı için tekrar sorulursa (örneğin parola sorulduktan sonra gelen ikinci INVITE) aynı sıra çıkar. Durum tutulmaz; karar süresine etkisi ölçülemeyecek kadar küçüktür.
- Paylar ilk denemeler içindir. Limiti dolan ya da hata veren bir rotanın çağrıları basamaktaki diğerlerine kayar; gerçekleşen dağılım raporlardan izlenir.
- Dağıtım olasılıksaldır: az sayıda çağrıda oranlar sapabilir (laboratuvarda 3'e 1 ağırlıkla 60 çağrı 46'ya 14 bölündü), çağrı sayısı arttıkça ağırlıklara yaklaşır.

Rota simülatörü (`POST /v1/route/simulate`) kura çekmez: rotaları sabit sırada, her biri için `step` (basamak numarası) ve `share_pct` (basamağındaki ilk deneme payı, yüzde) ile döner.

## Güne ve saate göre fiyat ve rota

Bir fiyat (`rates`) ya da rota satırı (`routing_entries`) haftanın günleriyle ve günün saatleriyle sınırlanabilir: `days` (bit kümesi; pazartesi = 1, salı = 2 … pazar = 64; 127 = her gün), `time_from` ve `time_to` (gece yarısından beri dakika; pencere `time_to`'dan hemen önce biter; ikisi eşitse tüm gün). `time_from`, `time_to`'dan büyükse pencere gece yarısını aşar ve başladığı güne sayılır (cuma 22:00–06:00, cumartesi 02:00'yi kapsar). Saatler tarifenin ya da planın `timezone` alanındaki saat dilimiyle okunur (IANA adı; varsayılan UTC).

- **Fiyat seçimi:** çağrının başladığı an için, o anda fiyatı olan en uzun önek geçerlidir. Aynı önekte penceresi o anı kapsayan fiyat, penceresiz fiyattan önce gelir; birden çok pencere kapsıyorsa yürürlüğe en son giren kazanır. Yalnızca pencereli fiyatı olan bir önek, pencere dışında yokmuş gibi davranır ve daha kısa öneke düşülür.
- **Tek kod:** motor (karar ve bakiye ayırma) ile ücretlendirme (rater) fiyatı aynı işlevle seçer (`rating.Tidy`, `rating.Select`); ikisinin farklı fiyat seçmesi mümkün değildir. Uçtan uca testte dört ayrı an için simülatör ve çağrı kaydı karşılaştırılır.
- **Rota:** penceresi çağrı anını kapsamayan rota satırı hiç yokmuş gibi atlanır.
- Çağrı, başladığı andaki fiyatla sonuna kadar ücretlendirilir; pencere sınırını aşan çağrı bölünmez. Yedek rotaya geçen çağrıda her deneme kendi başlangıç anına göre ücretlenir.
- Motor süren bir çağrıyı pencere bitti diye kapatmaz.

- **Resmî tatil** (T-19, ADR-0044): hesabın takvimindeki (`holidays`) bir tarih, gün kümesi sınırlı pencerelerde seçilen gün (varsayılan pazar) sayılır; fiyatlarda da rotalarda da. Tarih tarifenin ya da planın saat dilimiyle okunur; gece yarısını aşan pencerede başladığı tarihe bakılır. Her gün (`days` = 127) olan pencere ve penceresiz satır etkilenmez. Yarım gün tatil yoktur. Motor takvimi görüntüyle yükler (`TimedTariff.Cal`, `Plan.Cal`), rater fiyat ararken tarifenin sahibinin takviminden okur; ikisi de `rating.Window.Covers` ile karar verir.

Sınırlar: CSV ile toplu yüklemede gün kümesi sayı olarak yazılır (1–127); panelde gün seçimi üç hazır kümeyle sınırlıdır (her gün, hafta içi, hafta sonu; API her kümeyi kabul eder); tarifenin saat dilimi panelde yalnızca oluştururken verilir.

## Paket dakikalar

Bir müşteriye, belirli hedef önekleri için her dönem ücretsiz konuşma süresi tanımlanabilir (`bundles`, `/v1/bundles`): önek kümesi, dönem başına saniye, açık/kapalı. Paket bitince çağrılar tarifedeki fiyattan ücretlenir.

- **Hangi paket:** numaranın başına uyan en uzun önekli paket; eşit uzunlukta önce oluşturulan. Bir çağrı tek paketten yer: paketi biten çağrı başka pakete geçmez, bakiyeden öder. Boş önek bütün numaralara uyar.
- **Ne düşülür:** faturalanan saniye (tarifedeki ilk süre ve artım uygulandıktan sonra). Paketin tamamen karşıladığı çağrı hiçbir şey ödemez (bağlantı ücreti dahil); paket çağrının ortasında biterse kalan saniyeler ve bağlantı ücreti ücretlenir. Ücretsiz hedefe (fiyatı ve bağlantı ücreti 0) giden çağrı paketten düşmez. Hedefin tarifede fiyatı olmalıdır; paket, tarifede olmayan ya da engelli hedefi açmaz.
- **Dönem** takvim ayıdır ve müşterinin tarifesinin saat dilimiyle başlar (`tariffs.timezone`). Kullanım `bundle_usage` tablosunda dönem başına tutulur; yeni ayın satırı yoktur, yani kullanım kendiliğinden sıfırdan başlar. Kullanılmayan süre devretmez.
- **Dönem sınırını aşan çağrı, başladığı dönemin paketinden düşer:** tamamı, ne kadar sürerse sürsün. Çağrı bölünmez (fiyat pencerelerindeki kuralın aynısı); başlangıç, denemenin başladığı andır (`start_at`).
- **Ücretlendirme** (`rater`): faturalanan saniye önce paketten düşülür, yalnızca kalanı ücretlenir. Dönemin sayacı çağrı kaydıyla aynı işlemde kilitlenip artırılır; yan yana saklanan iki kayıt aynı saniyeyi alamaz, `(call_id, leg)` tekrarında hiçbir şey ikinci kez düşülmez. Çağrı kaydında `bundle_sec` paketten düşülen saniyedir, `bundle_id` paketi gösterir; `sell_billed_sec` bu saniyeleri içerir, `sell_amount` içermez.
- **Motor** paketi rezervasyonla bilir: çağrı kabul edilirken ayrılan sürenin saniyeleri önce paketten, kalanı bakiyeden ayrılır (aşağıda "Bakiye rezervasyonu"); böylece bakiyesi olmayan ama paketi olan müşteri arayabilir ve eşzamanlı çağrılar aynı kalan süreyi iki kez kullanamaz. Paket ve bakiye birlikte biterse çağrı kapatılır.
- Tutar hesabı motor ve ücretlendirme için tek koddur (`rating.Rate.AmountAfter`, `rating.Period`).
- Rota simülatörü cevabında `bundle_id` ve `bundle_free_sec` (görüntünün gördüğü kalan süre; süren çağrıların ayırdıkları düşülmeden) bulunur.

Sınırlar:
- Paketin hangi çağrıya yazılacağını kesin olarak ücretlendirme belirler (kayıtların saklanma sırasıyla), motor ise çağrı sürerken ayırır. Eşzamanlı çağrılarda ikisi saniyeleri farklı çağrılara yazabilir: paketten düşülen toplam süre paketi aşmaz, ama bir çağrı motorun ayırdığından fazla ya da az ücretlenebilir. Fiyatları farklı hedeflere aynı anda yapılan çağrılarda bu, ön ödemeli müşterinin bakiyesini en çok (paylaşılan saniye × fiyat farkı) kadar eksiye düşürebilir.
- Dönemi biten bir çağrı için motor o dönemin paketinden yeni saniye ayırmaz (uzatmalar bakiyeden ayrılır); ücretlendirme yine çağrının tamamını başladığı dönemin kalanından düşer. Bakiyesi olmayan müşterinin ay sonunu aşan çağrısı bu yüzden paketi bitmeden kapatılabilir.
- Yedek rotaya geçen çağrıda motor paketi çağrının geldiği ana, ücretlendirme denemenin başladığı ana göre döneme yazar; ikisi yalnızca ay tam o saniyelerde değişirse farklıdır.
- Kalan süre faturalama artımından küçükse (ör. 60/60 faturalamada 30 saniye) ve bakiye yoksa çağrı reddedilir: en kısa çağrı bile paketten fazlasını gerektirir.
- Paket yalnızca müşterinin kendi bakiyesinin yerine geçer; sağlayıcıya ödenen tutar ve Enterprise'taki bayi bakiyeleri paket çağrılarında da her zamanki gibi işler. Enterprise'ın kendi ücretlendirdiği çağrı türleri (ör. gelen numara çağrıları) paketten düşmez.
- Paketin süresi dönem ortasında değiştirilirse yeni süre hemen geçerlidir; o dönemin kullanımı korunur. Çağrı sürerken kapatılan ya da silinen paket o çağrıyı artık karşılamaz: çağrı bakiyeden ayırarak sürer ve tamamı tarifeden ücretlenir (motorun paketten ayırdığı ilk saniyeler için para ayrılmamıştır). Silinen paketin kullanım sayaçları da silinir, çağrı kayıtlarındaki `bundle_sec` kalır.
- Taahhüt (asgari harcama), devreden dakika ve paket ücreti (paketin kendisinin faturalanması) yoktur.

## Para birimleri

Hesap defterini kendi para biriminde tutar (`resellers.currency`). Müşteri ve sağlayıcı başka para biriminde olabilir; bakiyesi, ödemeleri ve çağrı tutarları kendi para birimindedir.

- **Tarife, sahibinin para biriminde olur.** Müşterinin satış tarifesi müşterinin, sağlayıcının alış tarifesi sağlayıcının para birimindedir; veritabanı başka türlüsünü kabul etmez (422). Böylece bir fiyat ücretlendirilirken hiç çevrilmez.
- **Kur** (`exchange_rates`, `/v1/exchange-rates`): bir para biriminin 1 biriminin hesabın para birimindeki karşılığı, mikro birimle (1,20 → 1200000). Elle girilir ya da (`FX_SOURCE=ecb` ile) Avrupa Merkez Bankası'nın günlük referans kurlarını izler: aşağıda "Kurların dış kaynaktan alınması". Kuru olmayan para biriminde müşteri ya da sağlayıcı açılamaz, kullanılan kur silinemez.
- **Motor** kuru yalnızca karşılaştırmak için kullanır: LCR sıralaması ve "zararına rota" denetimi alış ve satış fiyatlarının hesabın para birimindeki karşılığıyla yapılır. Simülatör cevabında `sell_price_base` ve rotalarda `buy_price_base` bu karşılıklardır.
- **Çağrı kaydı** tutarları iki biçimde taşır: `sell_amount` / `buy_amount` (müşterinin / sağlayıcının para biriminde) ve `sell_base` / `buy_base` (ücretlendirme anındaki kurla hesabın para biriminde). Raporlardaki gelir, maliyet ve kâr ikincilerden toplanır; kur sonradan değişse de eski kayıtlar değişmez.
- Çevirme tamsayıdır (128 bit ara çarpım, yarım mikro birim yukarı yuvarlanır); kayan nokta kullanılmaz.

### Kurların dış kaynaktan alınması

T-20, ADR-0036. Varsayılan kapalıdır (`FX_SOURCE` boş); açıkken (`FX_SOURCE=ecb`) API süreci Avrupa Merkez Bankası'nın günlük dosyasını (`eurofxref-daily.xml`; ücretsiz, anahtarsız; `FX_URL` başka bir adres verir) okur.

- **Hangi kur değişir:** yalnızca kaynağı `ecb` olan kurlar (`exchange_rates.source`). Elle girilen kur (`manual`; bu değişiklikten önceki bütün kurlar) hiçbir zaman değiştirilmez. Bir kur `POST /v1/exchange-rate-feed/follow` ile kaynağa bağlanır (yoksa eklenir, elle girilmişse çevrilir; değeri son alınan tablodan hemen hesaplanır). Kaynağa bağlı bir kura API'den (genel `PATCH`) dokunmak, değer aynı kalsa bile, onu elle girilmiş yapar: bunu veritabanı tetikleyicisi (`exchange_rate_kept`) sağlar; yalnızca kaynağın yazdığı işlem (`softswitch.fx_feed` işlem ayarı) kaynaklı kalır.
- **Çevirme:** banka 1 euronun kaç birim ettiğini yayınlar. X'in hesabın para birimi B'deki karşılığı (B/euro) ÷ (X/euro)'dur; ondalıklar metin olarak tam okunur (kesir; kayan nokta yok), sonuç en yakın mikro birime yuvarlanır, yarım yukarı (`fx.Table.Rate`). Euro'nun kendisi 1'dir; hesabın para birimi yayında yoksa ya da sonuç bir mikro birimin altında kalıyorsa kur değişmez ve "yayında yok" listesine (`missing`) girer.
- **Zaman:** banka iş günlerinde 16:00 (Orta Avrupa saati) civarında yayınlar. API açılışta (başka bir süreç son on dakikada almadıysa) ve her gün 15:30 UTC'den sonra bir kez alır; on dakikada bir yalnızca vaktinin gelip gelmediğine bakar. Başarısız deneme bir saat sonra yinelenir. Hafta sonu ve tatilde aynı tablo döner: değer değişmez, kurun `published` tarihi ilerlemez. Eski tarihli bir dosya (eski bir ayna) hiçbir şeyi değiştirmez.
- **Birden çok API süreci:** `exchange_rate_fetch` satırındaki `claimed_until` ile yalnızca biri alır; yazma tek işlemdir.
- **Kaynağa ulaşılamazsa** (bağlantı, HTTP durumu, okunamayan dosya): son kurlar kalır; hata `exchange_rate_fetch.error`'da tutulur ve panelde uyarı olarak görünür, `softswitch_fx_fetch_failures_total` artar (alarm `ExchangeRatesNotFetched`). Hata metni adresi taşımaz.
- **Etkisi:** değişen kur denetim kaydına "sistem" adına yazılır ve `store.NotifyConfig` motorun görüntüyü hemen yenilemesini sağlar. Eski çağrı kayıtları değişmez (ADR-0011): kayıt, ücretlendirme anındaki kurla yazılmıştır.
- Ülkeye özgü başka kaynaklar (ör. bir merkez bankası) ADR-0022 uyarınca pazar yeri modülü olabilir; çekirdekte tek kaynak ECB'dir.

Sınırlar: kurlar yalnızca `FX_SOURCE=ecb` ile ve yalnızca kaynağa bağlanan kurlar için kendiliğinden güncellenir; müşterinin para birimi, tarifesi ve bakiyesi varken değiştirilemez (önce tarife kaldırılır); dışa aktarılan CSV tutarları tarafların kendi para birimindedir ve para birimi sütunu taşımaz; panelde para birimi seçimi USD, EUR ve TRY ile sınırlıdır (API her üç harfli kodu kabul eder).

## Arayan numara kuralları

Arayanın numarasına göre çağrı reddedilir, kabul edilir ya da sağlayıcıya gösterilecek numara değiştirilir. Kurallar `cli_rules` tablosundadır (`/v1/cli-rules`); motor bunları görüntüyle birlikte belleğe alır, çağrı başına sorgu yapılmaz.

- **Bakılan numara** (`number_source`): `from` (varsayılan; `src` alanı, From başlığının kullanıcı kısmı) ya da `pai` (`asserted` alanı, `P-Asserted-Identity`'deki numara). Var olan kurallar migration ile `from` olur; davranışları değişmez. İki numaranın kuralları ayrı ayrı uygulanır: From kuralları From'daki numaraya, kimlik kuralları kimlik numarasına. Çağrı ancak ikisi de reddetmezse geçer; birinin `allow` kuralı ötekinin reddini kaldırmaz. Kimlik başlığı olmayan çağrının kimlik numarası yoktur (numarasız sayılır).
- **Kural:** bir önek ve bir işlem: `block` (403, `caller not allowed`), `allow` (olduğu gibi geçer), `rewrite` (geçer; numaranın başından `strip_digits` hane silinir, başına `add_prefix` eklenir; yalnızca `from` kurallarında, çünkü sağlayıcıya gösterilen numara From'dakidir), `anonymous` (aşağıda). Kural bir müşteriye ya da (`client_id` boşsa) hesabın bütün müşterilerine aittir.
- **Eşleşme:** numaranın başındaki `+` atılır; en uzun eşleşen önek geçerlidir, eşit uzunlukta müşterinin kendi kuralı hesabınkinden önce gelir. Hiçbir kural eşleşmezse çağrı geçer.
- **Kara liste:** engellenecek önekler için `block` kuralları. **Beyaz liste:** müşteriye boş önekli bir `block` kuralı ve izinli önekler için `allow` (ya da `rewrite`) kuralları.
- **Numarasız arayan:** başındaki tek `+` atıldıktan sonra boş kalan ya da rakam dışında bir karakter içeren numara numarasızdır: boş, `anonymous`, `Anonymous`, bir ad, `+`, ayraçlı yazım (`90-212-555`), harf taşıyan numara. Numarasız arayan yalnızca boş önekli kurallarla eşleşir; beyaz listesi olan müşteride reddedilir, dönüştürülmez.
- **Numarasızı reddet** (`anonymous`): öneki yoktur (veritabanı önek ve dönüşüm alanlarını boş ister). Kuralın kapsamındaki (müşteri ya da hesabın tümü) ve bakılan numarası numarasız olan çağrı **433** `anonymity disallowed` ile reddedilir ve bu kodla kaydedilir. Önek kurallarından önce uygulanır: aynı numarada müşterinin boş önekli `allow` kuralı bile onu kaldırmaz; numarası olan arayanlara dokunmaz. Aynı kapsam ve numarada boş önekli bir kuralla yan yana durabilir, ama ondan bir tane olur. Bir çağrıyı hem 433 hem 403 gerektiriyorsa (ör. From numarasız, kimlik numarası engelli) 433 verilir.
- **Dönüştürme:** motor yeni numarayı `caller` alanında verir. SIP çekirdeği From başlığının kullanıcı kısmını değiştirir (görünen ad ve alan adı kalır), `P-Asserted-Identity` varsa aynı numarayla yeniden yazar, `Remote-Party-ID` başlığını çıkarır. Arayan kendi numarasını görmeye devam eder: çağrı içindeki istek ve cevaplarda iki biçim arasındaki çeviriyi SIP çekirdeği yapar. Bağlantının kendi dönüşümü yoksa bütün rotalara aynı numara gösterilir; değişiklik her denemenin kendi isteğine uygulanır (`branch_route`), bir sonraki deneme gelen istekten yeniden başlar.
- **Kayıt:** çağrı kaydındaki `src` her zaman From'dan gelen numaradır; engellenen çağrı da 403 ya da 433 koduyla kaydedilir. Kimlik numarası kayda yazılmaz.

### Arayanın gizlilik isteği

`privacy` alanındaki değerlerden biri (`;`, `,` ya da boşlukla ayrılmış, büyük küçük harf fark etmeksizin) `id` ise (RFC 3325 §9.3; `id;critical`, `header;id` da sayılır) arayan kimliğinin gizlenmesini istemiştir. Motor bunu çağrının durumunda tutar ve o çağrının **her rotasında** (ilk kararda ve her `failed` cevabında; müşterinin kayıtlı trunk'ına giden rotada da) `identity: "remove"` verir: `P-Asserted-Identity`, `P-Preferred-Identity` ve `Remote-Party-ID` çıkarılır.

- **Bağlantının `pai_mode` ayarıyla çelişki:** gizlilik isteği kazanır. `pass` (başlık olduğu gibi ya da dönüştürülmüş numarayla gider) ve `caller` (başlık bu rotada gösterilen numarayla yazılır ya da eklenir) yerine `remove` uygulanır; `remove` zaten aynıdır. Sağlayıcılar güven alanının (RFC 3325 trust domain) içinde sayılmaz; kimliği sağlayıcıya iletmenin tek yolu arayanın gizlilik istememesidir.
- `Privacy` başlığının kendisine dokunulmaz, sağlayıcıya olduğu gibi gider. From başlığına da dokunulmaz (gizlilik isteyen arayan genellikle From'a `anonymous` yazar; numarasız From'u kurallar dönüştürmez).
- Arayan numara kuralları gizlilik isteğinden etkilenmez: kimlik numarasına bakan kural, başlık sağlayıcıya gitmeyecek olsa da numarayı görür (karar SIP çekirdeğinin aldığı başlığa göre verilir).
- `id` dışındaki değerler (`header`, `session`, `user`, `none`) bir şeyi değiştirmez.

Sınırlar: bağlantıya özel dönüşüm bu kuralların üstüne uygulanır (aşağıda "Bağlantı ve trunk ayarları"); dönüştürülmüş numara ve kimlik numarası çağrı kaydında tutulmaz; kimlik başlığının hangi kaynaktan geldiğine (güvenilen bir trunk mı) bakılmaz, gelen her `P-Asserted-Identity` kurallara verilir; `P-Preferred-Identity` kurallara verilmez; ayraçlı numaralar (`tel:+1-201-555-0123`) numarasız sayılır.

`make test-routing` içinde: kimlik kurallı müşteri (teknik önek 878) From'u listede olmayan ama kimlik numarası (`tel:+90212…`) listede olan arayanı alır ("paion"), From'u listede olup kimlik numarası listede olmayanı 403 ile reddeder ("paioff"), kimlik başlığı olmayanı 433 ile reddeder ("paianon"); From'a bakan "numarasızı reddet" kurallı müşteri (876) numarasız From'u 433 ile reddeder, numaralıyı alır ("fromanon"); gizlilik isteyen arayanın kimliği `pass` ve `caller` ayarlı bağlantılarda sağlayıcıya gitmez, istemeyeninki gider ("private"; sağlayıcıyı `connlab.sh` oynar ve gelen başlığa bakar).

## Bağlantı ve trunk ayarları

Bir sağlayıcı bağlantısının ve bir müşteri trunk'ının ayrıntılı ayarları. Hepsinin varsayılanı "ayar yok"tur: yalnızca temel alanlarıyla (ad, sunucu, port, taşıma; trunk'ta ad ve adres ya da kullanıcı adı) tanımlanan bir kayıt bu bölümden önceki gibi davranır. Ayarlar görüntüyle belleğe gelir; çağrı başına sorgu yoktur. SIP çekirdeğine yukarıdaki isteğe bağlı `route` alanlarıyla ulaşır; motor bunları rotayı verdiği anda (ilk kararda ve her `failed` cevabında) o anki yapılandırmadan okur, çağrı durumunda saklamaz.

**Sağlayıcı bağlantısı (`vendor_connections`)**

- **Sağlayıcıya giriş** (`auth_username`, parola): sağlayıcı çağrıya 401 ya da 407 ile kimlik sorarsa SIP çekirdeği aynı denemeyi bir kez daha, Digest cevabıyla gönderir (`uac_auth`; alan adı sorunun kendisinden okunur, `CSeq` bir artar ve çağrının sonraki istekleri buna göre düzeltilir). Motor bundan haberdar olmaz: tek denemedir, tek kayıt yazılır. Sağlayıcı yine reddederse (yanlış parola) deneme o kodla başarısız sayılır ve olağan kural işler. Parola yönetim API'sinde ayrı bir işlemle yazılır (`PUT /v1/vendor-connections/{id}/sign-in`), veritabanında `SECRET_KEY` ile şifreli durur (`pkg/totp` biçimi, bağlantının kimliğine bağlı), hiçbir uçtan geri okunmaz, günlüklere ve denetim kaydına yazılmaz; SIP çekirdeğine yalnızca bu arayüzden, `route.auth` içinde gider (arayüz gibi şifresiz HTTP: "Kimlik doğrulama", sınırlar). Motor parolayı okuyabilmek için API ile aynı `SECRET_KEY` değerini ister; yoksa ya da farklıysa parola rotaya konmaz, günlüğe yazılır ve sağlayıcının sorusu denemeyi başarısız kılar.

  Digest ayrıntıları (T-15; OpenSIPS 3.6 `uac_auth`, laboratuvarda sınandı):

  | Sağlayıcının sorusu | Sonuç |
  |---|---|
  | MD5, `qop`'suz | Çağrı geçer (RFC 2617 uyumlu cevap). |
  | `qop="auth"` (401 ya da 407) | Çağrı geçer: cevapta `qop=auth`, `nc=00000001`, `cnonce`. |
  | `qop="auth,auth-int"` | `auth` seçilir, çağrı geçer. |
  | `algorithm=SHA-256`, `SHA-512-256` (RFC 7616, 8760), `-sess` biçimleri | Desteklenir; SHA-256 ve SHA-512-256 `qop=auth` ile sınandı. |
  | Birden çok soru başlığı (ör. önce SHA-256, sonra MD5) | Desteklenen ilk başlık cevaplanır (sağlayıcının sırası). Alan adı ilk başlıktan okunur. |
  | Yalnızca `qop="auth-int"` | **Başarısız:** SIP çekirdeği gövdenin özetini arayandan gelen SDP üzerinden hesaplar, sağlayıcıya ise medya aktarıcısının yeniden yazdığı SDP gider; sağlayıcı reddeder (laboratuvarda 403) ve deneme o kodla başarısız sayılır, olağan kural (sıradaki rota) işler. |
  | Bilinmeyen algoritma (ör. `SHA-1`) | Cevap gönderilmez; deneme sağlayıcının 401/407'siyle başarısız sayılır, sıradaki rota denenir. |

  Sınırlar: soru başına tek cevap verilir (`nc` hep `00000001`; aynı denemede ikinci soru gelirse cevaplanmaz). `cnonce` rastgele değil, `nonce`'tan türetilir (modülün davranışı; `nonce` sağlayıcının taze değeri olduğu sürece cevap tekrarlanmaz). Parola `0x` ile başlayıp ardından algoritmanın özet uzunluğunda (MD5'te 32, SHA-256'da 64) küçük harf onaltılık hanelerle devam ediyorsa modül onu parola değil hazır HA1 özeti sayar; böyle bir parola kullanılmamalı.
- **Numara biçimi** (`strip_digits`, `tech_prefix`, `number_plus`): numaranın başından hane silinir, önek eklenir (ikisi eskiden beri var), `number_plus` doğruysa en başa `+` konur. Motor bunu rotanın `uri`'sinde verir; SIP çekirdeğine ayrı alan gitmez.
- **Arayan numara** (`cli_strip_digits`, `cli_add_prefix`): arayan numara kurallarından çıkan numaranın (kural yoksa gelen numaranın) başından hane silinir, önek eklenir (önek `+` ile başlayabilir); sonuç `route.caller` olur. Numarasız arayana dokunulmaz. Çağrı kaydındaki `src` gelen numaradır.
- **Kimlik başlığı** (`pai_mode`): `pass` (varsayılan; eskisi gibi: arayan numara değiştiyse başlık yeni numarayla yazılır, değişmediyse dokunulmaz), `remove` (`P-Asserted-Identity`, `P-Preferred-Identity` ve `Remote-Party-ID` çıkarılır), `caller` (bunlar çıkarılır ve `P-Asserted-Identity` o rotada gösterilen arayan numarayla yazılır; istekte yoksa eklenir). Arayan `Privacy: id` ile gizlilik istediyse ayar ne olursa olsun `remove` uygulanır (yukarıda "Arayanın gizlilik isteği").
- **Çalma süresi sınırı** (`ring_timeout_sec`, 5–180; 0 = SIP çekirdeğinin 30 saniyesi): karşı uç çaldırıp açmazsa bu süre sonunda deneme 408 ile biter (sağlayıcıya CANCEL gider) ve sıradaki rota denenir. Hiç cevap vermeyen sağlayıcı için bekleme ayrı ve sabittir (5 sn).
- **En uzun çağrı süresi** (`max_duration_sec`, 5–7200; 0 = yok): çağrı bu rotada cevaplandıysa süre dolunca SIP çekirdeği iki tarafa BYE gönderir. Bakiyenin izin verdiği süre daha kısaysa o geçerlidir.
- **Hangi cevaplarda sıradaki rota** (`failover_mode`, `failover_codes`): yukarıda `events` altında.
- **Erişilebilirlik denetimi** (`options_interval_sec`, 10–3600; 0 = kapalı): aşağıda "Sağlayıcıyı yoklama".
- **Kodekler** (`codecs`): sıralı liste. Arayanın teklifinden listede olmayan kodekler çıkarılır, kalanlar listenin sırasına konur (medya aktarıcısının teklif bayraklarıyla: `codec-strip-all`, `codec-offer-…`). Arayan listedekilerden hiçbirini sunmuyorsa çağrı o rotaya hiç gönderilmez: SIP çekirdeği denemeyi 488 ile başarısız bildirir ve sıradaki rota denenir. Liste çağrıyla saklanır ve çağrı içindeki yeni tekliflere de uygulanır.
- **Kodek çevirisi** (`transcode`, varsayılan kapalı; T-09): arayanın trunk'ının listesiyle bu bağlantının listesi hiç örtüşmüyorsa bağlantı yine de rota sayılır ve ses medya aktarıcısında çevrilir (aşağıda "Kodek çevirisi"). Kapalıyken davranış eskisidir.
- **Limitler ve sertifikadaki ad** (`max_calls`, `max_cps`, `tls_name`): eskisi gibi.

**Müşteri trunk'ı (`client_trunks`)**

- **Numara biçimi** (`number_rules`, T-17): kural listesi, her kural `{"strip", "add"}`. Teknik önek silindikten sonra numaranın başladığı en uzun `strip`'li kural uygulanır: o kısım silinir, yerine `add` konur; hiçbir kurala uymayan numaraya dokunulmaz. `strip` boş kural her numaraya uyar, bu yüzden yalnızca başka kural uymadığında uygulanır (`add` her numaranın başına eklenir). `00` ile yazan müşteri için `00` / boş, başında `+` ile yazan için `+` / boş, ulusal yazan için `0` / ülke kodu; bir müşteri birden çoğunu aynı trunk'ta kullanabilir (`00905…` en uzun eşleşme olan `00` kuralını alır, `0` kuralını değil). Liste tabloda en uzun `strip` önde saklanır; motor görüntüyü yüklerken bir kez daha sıralar ve çağrıda ilk eşleşmede durur (yalnızca bellek; en çok 20 kural). Tek kurallı trunk eski iki alanla (`number_strip`, `number_add`) aynı davranır; o alanlar uyumluluk için kalır (`docs/06-api.md`). Tarife, rota ve çağrı kaydı dönüştürülmüş numarayı görür. Senaryo `make test-routing`, "numfmt": tek trunk'tan `00…`, `+…` ve ulusal yazılmış üç çağrı sağlayıcıya aynı numarayla gider.
- **En uzun çağrı süresi** (`max_duration_sec`): kararın `max_duration_sec` değerini sınırlar.
- **Limitler** (`max_calls`, `max_cps`; 0 = yok): trunk'ın kendi eşzamanlı çağrı ve saniyedeki çağrı sınırı; müşterinin limitleri ayrıca geçerlidir. Müşteri limitleriyle aynı Redis betiğinde, aynı düzenle sayılır (`cc:t:<trunk>`, `cps:t:<trunk>:<saniye>`); limiti olmayan trunk'ın yaptığı çağrı için fazladan yazma yapılmaz. Dolunca 503 (`trunk call limit reached`, `trunk cps limit reached`). Sınır trunk'ın yaptığı ve ona **teslim edilen** çağrıları birlikte sayar (aşağıda "Müşterinin trunk'ına çağrı"; ADR-0042): `max_calls = 1` olan trunk'ın aynı anda iki yönden yalnızca bir çağrısı olur. Müşterinin `max_calls`/`max_cps` sınırı da öyledir: müşterinin yaptığı çağrılar ve trunk'larına teslim edilenler `cc:c:<müşteri>` ve `cps:c:<müşteri>:<saniye>` içinde birlikte sayılır.
- **Kodekler** (`codecs`): bu trunk'tan gelen çağrıların teklifi bu listeyle sınırlanır. Bağlantının da listesi varsa sağlayıcıya, bağlantının sırasıyla, ikisinde de olanlar teklif edilir; ortak kodek yoksa o bağlantı rota sayılmaz (hiç rota kalmazsa 503, `no route`) — bağlantı ya da trunk çeviriye izin vermiyorsa. Trunk'a teslim edilen çağrıda liste aynı biçimde trunk'a teklif edilene uygulanır (aşağıda).
- **Kodek çevirisi** (`transcode`, varsayılan kapalı; T-09): bu trunk'ın çağrıları ve bu trunk'a teslim edilen çağrılar, karşı tarafla ortak kodek yoksa çevrilerek taşınır (aşağıda "Kodek çevirisi"). Bağlantının ya da trunk'ın izni yeter.
- **Şifreleme kuralı ve kayıt sayısı** (`security`, `max_contacts`): eskisi gibi.

### Kodek çevirisi

Ortak kodeği olmayan arayan ile sağlayıcı (ya da hedef trunk) arasında çağrı kurmak için ses medya aktarıcısında (rtpengine) bir kodekten ötekine çevrilir (yol haritası B6, T-09; ADR-0041). Varsayılan kapalıdır; kapalıyken her şey eskisi gibidir.

- **Ne zaman:** karar kodek **listelerine** göre verilir: arayanın trunk'ının `codecs` listesi ile bağlantının (teslim edilen çağrıda hedef trunk'ın) listesi ikisi de dolu ve ortak kodek yok, ve ikisinden biri `transcode` ile çeviriye izin veriyor. Ortak kodek varsa çeviri yapılmaz, liste eskisi gibi süzer. Motor arayanın teklifini (SDP) görmez: listesi olmayan trunk "ne sunulursa" sayılır ve onun çağrısı için çeviri kararı verilmez; arayan bağlantının listesindekilerden hiçbirini sunmuyorsa çağrı eskisi gibi 488 ile o rotadan geçmez. Bir müşterinin çağrıları çevrilecekse trunk'ına kodeklerini yazmak gerekir.
- **Sıra:** çevrilecek rota plandaki yerinde kalır (LCR'de fiyatına, öncelikte önceliğine göre); çevrilmeyen rotalar ondan önce denenmez.
- **Arayüz (v1, isteğe bağlı alan; ADR-0002):** motor böyle bir rotayı verirken `route.codecs` alanında karşı tarafın listesini, `route.transcode` alanında arayan tarafının listesini verir. Alanı tanımayan bir SIP çekirdeği bugünkü gibi davranır (arayan listedekileri sunmuyorsa 488).
- **SIP çekirdeği:** `transcode` gelince teklif bayrakları `codec-mask-all` + her `codecs` için `codec-transcode-…` + `codec-offer-telephone-event` olur: arayanın kodekleri teklifte görünmez ama aktarıcı onları arayandan almayı sürdürür, karşı tarafın kodekleri teklife eklenir, aktarıcı ikisi arasında çevirir; arayana giden cevapta arayanın kodeği vardır. Arayan listedekilerden birini zaten sunuyorsa o kodek çevrilmeden geçer. Bayraklar dialogla saklanır: arayanın çağrı içindeki yeni teklifine aynısı, **karşı uçtan** gelen yeni teklife (`$DLG_dir` = `upstream`) ters yönü (`codec-mask-all` + `transcode` listesi için `codec-transcode-…`) uygulanır; yoksa karşı tarafın kodeği arayana teklif edilirdi. Sıradaki denemede çeviri yoksa ters yön silinir.
- **Tuşlar ve faks (ADR-0031):** `telephone-event` iki tarafın teklifinde de kalır, aktarıcı tuş olaylarını iki ses akışı arasında taşır; SIP INFO'ya dönüşüm yoktur. T.38 teklifine kodek bayrakları dokunmaz (aşağıda "Faks (T.38) ve tuşlar"); çevrilen bir çağrının T.38'e geçişi sınanmadı.
- **Eşzamanlı sınır:** aynı anda çevrilen çağrı sayısı `TRANSCODE_MAX` (varsayılan 200; 0 = hiçbir çağrı çevrilmez) ile sınırlıdır. Sınır dolunca o rota, sağlayıcının çağrı limiti dolmuş gibi atlanır: sıradaki rota denenir, kalmazsa 503 (`all routes busy`). Sayım kabulün tek Redis betiğindedir (her rotanın anahtarlarına `cc:x` kümesi eklendi; çevrilmeyen rotada sınır argümanı -1'dir ve kümeye yazılmaz): olağan çağrıya ikinci bir Redis gidişi eklenmez. Yer, çağrı bittiğinde, sıradaki rotaya geçildiğinde ya da kapanışı bildirilmeyen çağrının süresi dolunca bırakılır. Sınır motor başına değil, Redis'teki küme üzerinden bütün motorlar için ortaktır; medya aktarıcısı tektir (tek düğüm). Birden çok aktarıcıya dağıtma (çeviri havuzu) Enterprise'tadır.
- **Sayaçlar:** `softswitch_engine_transcoded_total` (çevrilerek gönderilen denemeler), `…_transcode_full_total` (sınır yüzünden atlanan rotalar; alarm `TranscodingAtLimit`), `…_transcoding_calls`, `…_transcoding_max`; aktarıcının kendi `rtpengine_transcoded_media` ve `rtpengine_transcode_packets_total{chain}` sayaçları gerçekten çevrilen sesi gösterir (`docs/10-izleme.md`). Motor, listelerin dediğine göre sayar: trunk'ının listesine rağmen karşı tarafın kodeğini de sunan bir arayanın çağrısı aktarıcıda çevrilmeden geçer ama sınırda yer tutar.
- **Kodekler:** imajdaki aktarıcı (Debian `rtpengine-daemon` 10.5, kullanıcı alanında) PCMA, PCMU, G.722, G.723, G.729/G.729a (bcg729), Opus, Speex, GSM ve `telephone-event` arasında iki yönde çevirir; iLBC, AMR ve AMR-WB'yi yalnızca çözebilir (bu yöne çeviri olmaz). Lisans ayrıntısı `docs/09-kurulum.md`, "Kodek çevirisi". Laboratuvarda yalnızca PCMA ↔ PCMU sınandı.

Senaryolar (`make test-routing`, "transcode"; `test/sipp/uac-transcode.xml`, sağlayıcı `uas-transcode.xml`, `connlab.sh transcode`; tohum 8737 (çeviren bağlantı, .100) ve 8738 (çevirmeyen, .101), trunk'lar 880 (yalnızca PCMA) ve 879 (yalnızca PCMA, çeviriye izinli); yedek rota C (.20)):

- 880 → 8737: sağlayıcıya yalnızca PCMU ve `telephone-event` ulaşır, arayana PCMA döner; arayan 3 saniye PCMA çalar, sağlayıcı geri yollar; sağlayıcının çağrı içindeki yeni PCMU teklifi arayana PCMA olarak ulaşır ve cevabı sağlayıcıya PCMU olarak döner. 879 → 8738: bağlantı çevirmese de trunk'ın izniyle aynısı. Aktarıcının sayaçlarında iki çağrının sesi iki yönde çevirmenlerden geçmiştir (`PCMA/8000 -> PCMU/8000` ve tersi, her biri ≥ 250 paket).
- 880 → 8738: çeviri izni yok, rota atlanır, C cevaplar (eski davranış).
- Motorun kümesi `TRANSCODE_MAX` kadar yer tutucuyla doldurulunca 880 → 8737'de çeviren rota atlanır, C cevaplar; `…_transcode_full_total` bir artar.
- Motor iki çağrıyı çevrilmiş sayar; çağrılardan sonra `…_transcoding_calls` 0'dır ve durum deposunda bir şey kalmaz.

Sınırlar:

- Çevirinin işlemci bedeli kodeğe göre çok değişir; varsayılan sınır bir başlangıç değeridir, sunucuda ölçülerek ayarlanmalıdır (`docs/09-kurulum.md`).
- Arayanın teklifine bakılmadığı için yukarıdaki iki durum: listesi olmayan trunk'ın çağrısı çevrilmez; listesine rağmen karşı tarafın kodeğini sunan arayan sınırda yer tutar.
- Kullanıcı alanında (Mac laboratuvarı) sınandı; Linux'ta çekirdek aktarımı açıkken çevrilen akış kullanıcı alanında kalır (çekirdek yalnızca çevrilmeyen akışları aktarır).
- Çevrilen çağrının T.38'e geçişi, çağrı içindeki bekletme ve SRTP ile birlikte çeviri sınanmadı.

### Sağlayıcıyı yoklama

`options_interval_sec` verilen her etkin bağlantıya motor, o aralıkla, kendisi bir SIP `OPTIONS` isteği gönderir (bağlantının taşımasıyla: UDP, TCP ya da TLS; TLS'te sertifika, ad denetimindeki kurallarla doğrulanır). İstek SIP çekirdeğinden geçmez; arayüze OpenSIPS'e özgü hiçbir şey girmez.

- **Hüküm:** herhangi bir SIP cevabı (200, 404, 405, 401…) "orada" demektir; yalnızca 503 "hizmet dışı" sayılır. UDP'de istek yarım ve bir saniye sonra yinelenir, bir yoklama en çok 3 saniye bekler. Arka arkaya **iki** yoklama cevapsız kalınca bağlantı "cevap vermiyor" olur (ilk kaçan cevaptan sonra 2 saniye içinde yeniden sorulur); tek bir cevap onu geri getirir.
- **Etkisi:** cevap vermeyen bağlantının rotaları karardan çıkarılır (sertifikası reddedilen bağlantılarla aynı düzenek: bellekte, kilitsiz okunur, çağrı yolunda bekleme yok); başka rota yoksa 503 (`no route`). Hüküm çağrı sırasında değil, arka planda verilir.
- **Görünürlük:** `GET /v1/peers` → `{"v":1,"connections":{"<id>":{"state":"up|down|unknown","since":…,"checked_at":…,"last":{"outcome":…,"sip_code":…,"ms":…}}}}`; ölçü `softswitch_engine_vendors_unreachable`, atlanan rotalar `softswitch_engine_health_skipped_total`, alarm `VendorUnreachable` (`docs/10-izleme.md`); panelde bağlantı listesinde "Erişim" sütunu.
- **Bir kez sorma:** `POST /v1/probe` — `{"v":1,"host":…,"port":…,"transport":…,"tls_name":…}` → `{"v":1,"outcome":…,"sip_code":…,"sip_reason":…,"ms":…,"detail":…}`. `outcome`: `answered`, `no_answer`, `unresolved` (ad bulunamadı), `refused` (dinleyen yok), `certificate` (sertifika kabul edilmedi), `failed`. Hüküm saklanmaz, hiçbir şey değişmez. Yönetim API'si bunu `POST /v1/vendor-connections/{id}/test` olarak sunar (paneldeki "Dene").

Sınırlar:

- Yoklama motorun adresinden ve rastgele bir porttan gider, SIP çekirdeğinin 5060'ından değil. Tek sunucu kurulumunda adres aynıdır; istekleri yalnızca belirli bir kaynak porttan kabul eden ya da motor ayrı bir sunucudayken onun adresini tanımayan sağlayıcı yoklamayı cevaplamaz. Böyle bir bağlantıda yoklama kapalı bırakılmalıdır, yoksa çalışan bir bağlantı devre dışı kalır; "Dene" bunu önceden gösterir.
- Yoklamaya hiç cevap vermeyen sağlayıcılar vardır; bu yüzden varsayılan kapalıdır.
- Birden çok motor düğümü varsa her biri kendi yoklar ve kendi hükmünü verir.
- Hükümler motorun belleğindedir: motor yeniden başlayınca sıfırdan başlar ve ilk iki yoklama bitene kadar (saniyeler) cevap vermeyen bir bağlantı çağrı alabilir; o çağrılar SIP çekirdeğinin kendi zaman aşımıyla sıradaki rotaya geçer.
- Yoklama yalnızca sinyalleşme ucunun cevap verdiğini gösterir; sağlayıcının çağrıyı gerçekten tamamlayabildiğini (kalite, rota) göstermez.

### Sağlayıcıya kayıt (REGISTER)

Bazı sağlayıcılar çağrı kabul etmeden önce switch'in kendilerine kaydolmasını ister (T-14). Kaydı, ADR-0035 gereği, SIP çekirdeği kendi adresinden ve portundan gönderir (OpenSIPS `uac_registrant` modülü); motor yalnızca hangi kayıtların olacağını söyler ve durumlarını okur. Yoklamadan farkı budur: sağlayıcı çağrılarını kaydın geldiği adrese gönderecekse o adres SIP çekirdeğininki olmalıdır.

- **Ayar** (bağlantıda; migration `0026`): `register` (aç/kapa, varsayılan kapalı), `register_expires_sec` (istenen süre, 60–86400, varsayılan 3600; sağlayıcı daha kısasını verebilir, verdiği geçerlidir), `register_domain` (kayıt adresinin alanı; boş = bağlantının sunucusu). Kayıt adı ve parola "Sağlayıcıya giriş"tekilerdir (`auth_username`, `auth_secret`); adı olmayan bağlantı kaydedilmez. Kapalı ya da sağlayıcısı askıda bağlantı da kaydedilmez.
- **Kayıt:** `To`/`From` = `sip:<auth_username>@<register_domain ya da sunucu>`; istek bağlantının sunucusuna, portuna ve taşımasına gider (`sip:<sunucu>:<port>;transport=<taşıma>`). `Contact` = `sip:<auth_username>@<SIP_ADVERTISED_IP ya da SIP_IP>:<SIP_PORT>` (TLS'te `SIP_TLS_PORT` ve `;transport=tls`), istek SIP çekirdeğinin dinlediği soketten çıkar. 401/407'ye Digest ile cevap verilir (çağrıdaki girişle aynı modül, `uac_auth`; laboratuvarda 401 ve MD5 ile sınandı). Kayıt, verilen süre dolmadan `REG_TIMER_SEC` önce yenilenir.
- **Liste (A1'e yeni soru):** `GET /v1/vendor-registrations?version=<elde tutulan>` → `{"v":1,"version":"…","registrations":[{"id":<bağlantı>,"registrar":"sip:…","aor":"sip:…","transport":"udp|tcp|tls","username":"…","password":"…","expires":3600}]}`. `version` elde tutulana eşitse cevap yalnızca `{"v":1,"version":"…","unchanged":true}`'dur: parola yalnızca liste değiştiğinde ağdan geçer. `version` listenin tuzlu özetidir (motorun her açılışında değişir), parolanın düz özeti değildir. Aynı kayıt adresini aynı sağlayıcı sunucusuna kaydedecek iki bağlantı tek kayıttır (küçük kimlikli olan). Uç motorun sırrıyla korunur (`Cache-Control: no-store`).
- **SIP çekirdeği tarafı:** `deploy/images/opensips/registrants.sh` (OpenSIPS imajında, TLS tablosu betiğinin eşi) listeyi `REG_SYNC_SEC` (varsayılan 10) saniyede bir sorar, değiştiyse modülün metin tablosunu (`/run/opensips/regdb/registrant`, `db_text`) yazar ve `reg_reload` ile yükletir. Yeniden yüklemede kayıtlı (REGISTERED) bir kaydın durumu korunur: değişen parola bir sonraki yenilemede kullanılır. Kapatılan kayıt tablodan çıkar; modül kayıt silme (`Expires: 0`) göndermez, sağlayıcıdaki kayıt süresi dolunca düşer.
- **Durum (A2'ye okuma komutu):** motor `REG_STATUS_SEC` (varsayılan 10) saniyede bir SIP çekirdeğine `reg_list` sorar (`pkg/sipctl`, `OpenSIPSRegistrations`; kayıt adresi ve sunucu dizgesiyle bağlantıya eşlenir) ve `GET /v1/peers` cevabına `"registrations":{"<id>":{"state":"registered|pending|failing|unknown","reason":"…","since":…,"checked_at":…,"expires":…}}` ekler. `reason`: `registered`, `pending`, `no_answer` (sağlayıcı cevap vermedi), `credentials` (ad ya da parola kabul edilmedi), `refused` (başka bir hata cevabı), `error` (gönderilemedi), `sip_core` (SIP çekirdeğine sorulamadı; `state` `unknown`). Yenileme ve yeniden deneme sürerken önceki hüküm korunur (çalışan kayıt her yenilemede "bekliyor"a düşmez). Yönetim API'si bunu `GET /v1/vendor-connections/health` cevabında `registrations` olarak verir; panelde bağlantı listesinde "Kayıt" sütunu. Ölçü `softswitch_engine_vendor_registrations{state=…}`, alarm `VendorRegistrationFailing` (`docs/10-izleme.md`).
- **Başarısızlıkta:** modül `REG_RETRY_SEC` (varsayılan 60) sonra yeni bir Call-ID ile yeniden dener; aralık sabittir (aşağıda, sınırlar).

Parolanın düz hâli: veritabanında şifreli (ADR-0019); motorun belleğinde (görüntü, çağrıdaki giriş için zaten öyleydi); motordan SIP çekirdeğine HTTP cevabında, yalnızca liste değiştiğinde; SIP çekirdeğinde modülün tablosunda (`/run/opensips/regdb/registrant`, yalnızca root okur; compose dosyaları `/run/opensips`'i bellekte, tmpfs olarak bağlar) ve OpenSIPS'in paylaşılan belleğinde, kayıt açık kaldıkça. Betik cevabı `curl`'dan `jq`'ya, oradan tabloya aktarır, ara dosyayı hemen siler; hiçbir yere yazdırmaz. Motorun sırrı `curl`'e komut satırında değil, yalnızca root'un okuyabildiği bir başlık dosyasıyla verilir.

Sınırlar:

- Yeniden deneme aralığı sabittir (`REG_RETRY_SEC`): `uac_registrant` artan aralık bilmez. T-14'ün ilk kabul ölçütündeki "artan aralıkla" bu yüzden karşılanmadı; varsayılan 60 saniye, parolası yanlış bir kayıt için saatte 60 deneme demektir. Sağlayıcı denemeleri sayıp adresi engelliyorsa `REG_RETRY_SEC` büyütülür ya da kayıt kapatılır.
- `REG_TIMER_SEC` modülün en küçük değeri olan 10'dan küçük olamaz; sağlayıcının verdiği süre bundan büyük olmalıdır (değilse modül her bakışta yeniden kaydolur ve günlüğe yazar).
- Yalnızca UDP ve TLS: SIP çekirdeği TCP dinlemez, TCP bağlantısının kaydı gönderilemez (`error`).
- Kayıt switch'in adresini tanıtır; sağlayıcıdan gelen çağrılar (DID) bu depoda değildir (Enterprise E16).
- Birden çok SIP çekirdeği düğümü olursa her biri kendi kaydolur; `uac_registrant`'ın küme desteği (`cluster_shtag`) kullanılmıyor.

### Senaryolar

`make test-routing` içinde, sağlayıcıları `test/sipp/connlab.sh` oynar (172.30.0.60–64; her biri aldığı isteğe bakar ve beklediği gibi değilse 488 ile reddeder): çaldırıp açmayan sağlayıcının 5 saniyede bırakılması ve sıradakinin cevaplaması ("ringtime"); bağlantının ve trunk'ın en uzun süresinde çağrının SIP çekirdeğince kapatılması ("connmax", "trunkmax"); 486'nın olağan kuralda çağrıyı bitirmesi, `always` ile sıradakine geçmesi, `never` ile 500'ün çağrıyı bitirmesi, `custom` listesinde olan ve olmayan kod ("moveon"); sağlayıcıya `+`, önek ve silinen haneyle giden numara, `00` ve ulusal biçimde arayan trunk'lar ("numfmt"); bağlantının arayan numara dönüşümü ve kimlik başlığının yazılması, silinmesi, reddeden sağlayıcıdan sonra ikincisinin kendi biçimini görmesi ("shown"); yalnızca bağlantının kodeğinin teklif edilmesi, sıranın değişmesi, trunk'ın listesi, arayanın sunmadığı kodeği isteyen bağlantının atlanması, ortak kodeği olmayan trunk ve bağlantı ("codecs"); ortak kodek yokken bağlantının ya da trunk'ın izniyle sesin iki yönde çevrilmesi, izin yokken ve sınır doluyken rotanın atlanması ("transcode", yukarıda); trunk'ın eşzamanlı çağrı limiti ("trunkone"); parolanın API'den verilmesi, şifreli saklanması, geri okunmaması, 401 ve 407 soran sağlayıcının ikinci istekte (CSeq 2) çağrıyı alması, yanlış parolanın ve parolasız bağlantının sıradaki rotaya geçmesi, `qop=auth` (401 ve 407; `nc`, `cnonce`), SHA-256'nın MD5'e tercih edilmesi, SHA-512-256, yalnızca `auth-int` isteyen ve bilinmeyen algoritma soran sağlayıcıda sıradaki rotaya geçilmesi, parolanın hiçbir günlükte geçmemesi ("signin"; 401'de parolayı SIPp'in `verifyauth`'u denetler, ayrıca her cevabın özeti `connlab.sh` içinde kullanıcı adı ve paroladan yeniden hesaplanır: 407'de parolanın tek denetimi budur); cevap vermeyen bağlantının devre dışı kalması, çağrının doğrudan diğer rotaya gitmesi, sağlayıcı cevap vermeye başlayınca çağrı alması, API'nin durumu göstermesi ("health"); "Dene"nin beş sonucu (cevapsız, cevaplı, dinleyen yok, sertifika, bilinmeyen ad) ve denetim kaydına iz bırakmaması ("try"); sağlayıcıya kayıt: SIP çekirdeğinin kendi adresinden (Via ve Contact 172.30.0.10:5060) ve bağlantıda verilen alanla kaydolması, 401'e doğru parolayla cevap vermesi, sağlayıcının verdiği 20 saniye dolmadan yenilemesi, motorun ve API'nin "registered" göstermesi, kayıtlıyken çağrının geçmesi; yanlış parolada "failing"/"credentials" ve ikinci denemenin sağlayıcıya ulaşması, doğru parolayla yeniden kayıt, kapatınca listeden çıkması, parolanın hiçbir günlükte ve denetim kaydında geçmemesi ("vendorreg"; sağlayıcı 172.30.0.93, `uas-registrar.xml`).

Sınırlar:

- **Kodek çevirisi** yalnızca bağlantı ya da trunk izin verince ve kodek listelerine göre yapılır (yukarıda, "Kodek çevirisi"); izin yoksa ortak kodeği olmayan çağrı o rotadan geçmez.
- Kodek adları sabit bir listedendir (PCMA, PCMU, G729, G722, opus, GSM, iLBC, G723, AMR, AMR-WB, speex); video ve faks (T.38) için ayar yoktur, liste T.38 teklifini düşürmez (aşağıda, "Faks (T.38) ve tuşlar"). Laboratuvarda yalnızca PCMA/PCMU ile sınandı. Liste çağrı içindeki yeni tekliflere (re-INVITE), iki yönde de uygulanır (aşağıda, "Çağrı içi yeni teklif").
- Sağlayıcıya giriş çağrı (INVITE) ve, bağlantıda açılmışsa, kayıt (REGISTER; yukarıda "Sağlayıcıya kayıt") içindir. `qop=auth-int` yalnızca istenirse çalışmaz (yukarıdaki tablo). Şifresiz taşımada Digest cevabı ağda görülür.
- `never` kuralı, sağlayıcıya hiç ulaşılamadığında (bağlantı kurulamadı, 503 SIP çekirdeğince üretildi) da sıradaki rotaya geçirmez; `custom` listesine 503 yazılmadıysa aynı şey olur.
- Sağlayıcının 503 cevabı arayana, SIP'in kuralı gereği, 500 olarak iletilir (eskiden beri); çağrı kaydında 503 durur.
- Çalma süresi ilk ilerleme cevabından (180/183) sonra sayılır; cevaptan önceki bekleme 5 saniyedir ve ayarlanmaz.
- Trunk'ın numara kuralı tektir: hem `00` hem `+` ile yazan bir müşteri için iki ayrı kural tanımlanamaz (teknik önekle ayrılmış iki trunk gerekir).
- Trunk'ın kodek listesi ve en uzun süresi yalnızca trunk'tan *gelen* çağrılar içindir; kayıtlı trunk'a *teslim edilen* çağrıya (uzantı yolu) arayanın trunk'ının listesi uygulanır, hedef trunk'ınki değil.
- Ayarlar olay geldiği andaki yapılandırmadan okunur: çağrı sürerken değiştirilen bir bağlantının yeni kuralı, o çağrının sonraki denemelerine de uygulanır.

## Yabancı kaynaklar

`POST /v1/source` — `{"v":1,"src_ip":…}` → `{"v":1,"known":true|false,"blocked":…,"registered":…}`: adres bir müşteri trunk'ının adreslerinden biriyse, ağlarından birinin içindeyse ya da açık bir sağlayıcı bağlantısına aitse `known` doğrudur (teknik önek ne olursa olsun; çağrı eşleşmesiyle aynı tablo kullanılır). Bir trunk'a geniş bir ağ tanımlamak, o ağdaki bütün adresleri taşkın korumasının dışına çıkarır; ağı gereğinden geniş tutmayın (API IPv4'te `/8`'den, IPv6'da `/16`'dan genişini kabul etmez). SIP çekirdeği, çağrı dışındaki bir isteği (OPTIONS vb.) cevaplamadan önce tanımadığı adres için bunu sorar.

OpenSIPS betiği kenarı şöyle korur:

- Motorun tanıdığı adres beş dakika hatırlanır ve serbestçe geçer.
- Tanınmayan adres 2 saniyede 30 istekle sınırlıdır; aşarsa iki dakika boyunca hiçbir isteği işlenmez ve cevaplanmaz (`pike` aşımı saptar, yasak yerel önbellekte tutulur).
- Tanınmayan adresin çağrısı (INVITE) 401 ile parola sorar (aşağıda "Kullanıcı adı ve parolayla giriş"); diğer istekleri hiç cevaplanmaz, böylece tarayıcılar burada bir SIP sunucusu olduğunu OPTIONS ile öğrenemez.
- Tanınan adresin OPTIONS isteği 200 ile cevaplanır (karşı tarafın canlılık denetimi için).
- Tanınmayan adresin hiçbir isteği (401 sorusu, taşkın, engel dahil) çağrı izine kopyalanmaz; iz yalnızca motorun bir müşteriye ait saydığı çağrılar içindir (`authorize` cevabında `trace`, ADR-0046).
- Bir trunk'ın o an **kayıtlı olduğu adres** (`registered: true`) tanınan sayılmaz, ama yabancı da değildir: OPTIONS isteği 200 ile cevaplanır (santraller karşıyı böyle yoklar; cevap alamayan santral bağlantıyı ölü sayıp çağrı göndermeyi kesebilir). Çağrılarına ve kayıt isteklerine yine parola sorulur, hız sınırı ona da uygulanır. Motor bunu bellekten söyler: kayıtlı adresleri, kendisi sakladıkça ve her yapılandırma yüklemesinde depodan okuyarak bilir.

Sınırlar: koruma kaynak adres başınadır, yani sahte kaynak adresli (spoofed) UDP taşkınını durdurmaz; her sahte adres motora bir kez sorulur (bellekten cevaplanır, Redis'e gidilmez). Bunun önlemi ağ katmanındadır (güvenlik duvarında yalnızca bilinen adreslere izin vermek ya da hız sınırı; örnek kurallar `docs/09-kurulum.md`, "Ağ katmanı koruması"). Yeni tanımlanan bir trunk hemen tanınır; silinen bir trunk'ın adresi beş dakikaya kadar hız sınırından muaf kalır, ama çağrıları yine motor tarafından reddedilir.

## Güvenlik duvarı

Operatörün kaynak adresler hakkındaki kuralları (`firewall_rules`, `/v1/firewall-rules`) görüntüyle birlikte motora gelir ve motor bunları SIP çekirdeğine her soruda bildirir. Kural bir adres ya da ağdır (IPv4 ya da IPv6) ve isteğe bağlı bir bitiş anı taşır; yalnızca sistemi işleten (kök) hesabın kuralları uygulanır.

- **`deny`:** adres yok sayılır. `POST /v1/source` cevabında `blocked: true` gelir ve SIP çekirdeği hiçbir cevap vermeden bırakır. Adres o ana kadar tanınıyorsa (bir müşterinin trunk'ı, önbellekte) ilk çağrısı `authorize` cevabındaki `source_blocked: true` ile 403 alır, sonrakiler yok sayılır. Engellenen çağrı için kayıt yazılmaz. `deny`, `allow`'dan ve trunk tanımından önce gelir.
- **`allow`:** adres tanınmış sayılır: istek sınırına takılmaz ve çağrı dışı istekleri cevaplanır. Çağrı yetkisi vermez; onun için trunk gerekir.
- **Ne kadar sürede uygulanır:** kural eklenince motor bir bildirimle hemen öğrenir; SIP çekirdeği bir adresi en çok bir dakika (engel) ya da beş dakika (tanınmışlık) önbellekte tuttuğu için engel bir dakika içinde, bir iznin ya da bir engelin kalkması en çok bu süreler sonunda görünür. Engeli hemen kaldırmak için aşağıdaki `unban` kullanılır.
- **Süren çağrılar** kesilmez: kural yeni istekler içindir.

Otomatik engeller: SIP çekirdeği taşkın yüzünden bir adresi kapattığında motora bildirir (`POST /v1/ban`: `{"v":1,"src_ip":"…","reason":"flood","seconds":120}`). Motor listeyi bellekte tutar (`GET /v1/bans`; en çok 10.000 adres, motor yeniden başlayınca boşalır) ve `POST /v1/unban` (`{"v":1,"src_ip":"…"}`) ile SIP çekirdeğine engeli unutturur. Yönetim API'si bunları `GET /v1/firewall/bans` ve `POST /v1/firewall/unban` olarak sunar.

Sınırlar: bu, uygulama düzeyinde bir korumadır; paket yine de sunucuya ve SIP çekirdeğine ulaşır, yalnızca cevapsız bırakılır. Büyük hacimli bir taşkına karşı işletim sistemi ya da ağ düzeyinde süzme gerekir: rehber ve örnek kurallar `docs/09-kurulum.md`, "Ağ katmanı koruması" (B5); paneldeki kuralların sunucunun güvenlik duvarına yansıtılması T-27. Kurallar doğrusal taranır; yüzlerce kural için ölçülmüştür (200 ağda adres başına 1 µs'nin altında), on binlercesi için değildir. Sahte kaynak adresli UDP ile başkasının adresi otomatik engele sokulabilir; kalıcı kurallar yalnızca operatörün yazdıklarıdır.

## Kullanıcı adı ve parolayla giriş

Sabit adresi olmayan müşteri, trunk'ına tanımlı kullanıcı adı ve parolayla (SIP digest) tanınır.

- `POST /v1/credentials` — `{"v":1,"username":…}` → `{"v":1,"found":true,"ha1":…}`: SIP çekirdeğinin parolayı denetlemesi için kullanıcının özeti (MD5(kullanıcı:alan:parola)). Bu uç parola özeti verir; arayüzün geri kalanı gibi motorun sırrını ister ("Kimlik doğrulama"). Cevap ağda şifresiz gittiği için motor yine yalnızca SIP çekirdeğinin eriştiği ağda dinlemelidir.
- SIP çekirdeği parolayı doğruladıktan sonra `authorize` isteğine `auth_user` ekler; motor trunk'ı kaynak adrese göre değil bu ada göre bulur. `auth_user` boşsa yalnızca kaynak adres belirler.
- Giriş yapan arayanın adresi "tanınan" sayılmaz (`source_known` yanlış döner): bir sonraki çağrısında yeniden parola sorulur ve yabancılara uygulanan hız sınırı ona da uygulanır.
- Parolayı API üretir (`POST /v1/client-trunks/{id}/password`), yalnızca özetini saklar ve parolayı bir kez gösterir. Özet SIP alanına (realm) bağlıdır: API'ye `SIP_REALM` ile verilen değer ile OpenSIPS betiğindeki alan aynı olmalıdır (varsayılan `softswitch`); alan değişirse verilmiş bütün parolalar geçersiz olur.
- Yanlış kullanıcı adı da yanlış parola gibi yeniden sorulur; cevap hangi adların var olduğunu ele vermez.

Giriş yapan trunk kendini kaydedebilir (aşağıda "Kayıt (REGISTER)") ve adres çevirisi arkasında olabilir (aşağıda "Adres çevirisi (NAT) arkasındaki uçlar").

Sınırlar: Digest'in kendisi MD5'e dayanır ve TLS olmadan parola özeti ağda dinlenebilir; güvenilmeyen ağlarda arayan TLS ile bağlanmalıdır (aşağıda "Şifreleme"). Arayanın kimlik bilgisi (`Authorization` başlığı) sağlayıcıya iletilmez.

## Kayıt (REGISTER)

Kullanıcı adı ve parolayla giriş yapan bir trunk, nerede olduğunu SIP `REGISTER` ile bildirebilir. Kayıt iki işe yarar: adres çevirisi (NAT) arkasındaki bir santrale giden yol açık kalır, ve bir çağrı o trunk'a teslim edilebilir (aşağıda "Müşterinin trunk'ına çağrı"). Çağrı *göndermek* için kayıt gerekmez; giriş yapan trunk eskisi gibi her çağrıda parolasıyla tanınır.

**Kayıtlar nerede durur:** motorda, çağrı durumunun yanında, Redis'te (`reg:<trunk>`: trunk başına bir kayıt kümesi; son kaydıyla birlikte kendiliğinden silinir). SIP çekirdeği kayıt tutmaz: OpenSIPS'in `usrloc` ve `registrar` modülleri kullanılmadı. Gerekçe: kayıtları sistemin geri kalanı (API, panel, çağrı kararı) görebilmeli; iş kuralları (kimin, ne kadar süreyle, kaç adresle kaydolabileceği) motorda olmalı; arayüz SIP sunucusuna özgü olmamalı. Bu yerleşimde kayıtlar motorun ya da SIP çekirdeğinin yeniden başlamasından etkilenmez ve aynı Redis'i paylaşan bütün düğümler aynı kayıtları görür.

Akış: SIP çekirdeği parolayı çağrılardaki gibi denetler (`/v1/credentials`; parolasız ya da yanlış parolalı istek 401 ile yeniden sorulur), sonra isteği olduğu gibi motora sorar:

`POST /v1/register`

```json
{"v": 1, "auth_user": "ornek-pbx", "src_ip": "203.0.113.9", "src_port": 40123, "transport": "udp", "tls_name": "",
 "nat": true, "contacts": "<sip:pbx@10.0.0.5:5060>", "expires": 3600, "call_id": "…", "cseq": 2, "user_agent": "…"}
```

`contacts` isteğin `Contact` başlığıdır (birden çoksa virgülle birleştirilmiş, geldiği gibi; motor ayrıştırır), `expires` `Expires` başlığıdır (istekte yoksa alan da yoktur). `nat`, isteğin adres çevirisinden geçtiğini gösterir (aşağıda). Cevap her zaman 200'dür:

```json
{"v": 1, "allowed": true, "sip_code": 200, "reason": "ok", "contacts": "<sip:pbx@10.0.0.5:5060>;expires=60"}
```

`contacts`, cevaptaki `Contact` başlığıdır: trunk'ın geçerli bütün kayıtları, her biri kalan süresiyle. SIP çekirdeği `sip_code` ve `reason` ile cevap verir, `contacts` varsa `Contact`, `min_expires` varsa `Min-Expires` başlığını ekler. Reddedilen istekte `allowed` yanlıştır ve hiçbir şey saklanmaz.

| Kod | Neden |
|---|---|
| 401 | Parola yok ya da yanlış, kullanıcı adı bilinmiyor (SIP çekirdeği sorar; motora gelmez) |
| 403 | Trunk'ın kuralı: `tls required`, `client certificate required` (çağrılardaki kuralın aynısı); askıdaki hesap (`account inactive`); güvenlik duvarının engellediği adres (`source blocked`, `source_blocked: true`); tek istekte trunk'ın sınırından çok adres (`too many contacts`) |
| 423 | İstenen süre en azın altında (`interval too brief`); `min_expires` en azı söyler |
| 400 | Okunamayan `Contact` (SIP adresi değil, 255 karakterden uzun, bir istekte 10'dan çok adres; `*` yalnızca tek başına ve `Expires: 0` ile) |
| 503 | Durum deposu erişilemez |

Kurallar:

- **Süre.** Trunk'ın istediği süre `REG_MIN_EXPIRES` (varsayılan 60 sn) altındaysa 423; `REG_MAX_EXPIRES` (varsayılan 3600 sn) üstündeyse ona indirilir; hiç söylemezse en çok süre verilir. Kaynağı üzerinden ulaşılan trunk'a (adres çevirisi arkasında, ya da TCP/TLS ile bağlı) en çok `REG_NAT_EXPIRES` (varsayılan 60 sn) verilir; böyle bir trunk için en az süre de bu ikisinin küçüğüdür. Verilen süre cevaptaki `expires` ile bildirilir; cihaz ona uyar ve o süre dolmadan yeniden kaydolur.
- **Kaldırma.** `expires=0` o adresi, `Contact: *` ile `Expires: 0` trunk'ın bütün kayıtlarını siler. Süresi dolan kayıt kendiliğinden düşer. `Contact` taşımayan istek bir sorudur: geçerli kayıtlar cevapta döner.
- **Adres sayısı.** Trunk aynı anda en çok `max_contacts` adresle kayıtlı olabilir (`client_trunks.max_contacts`, varsayılan 1, en çok 10). Sınır doluyken gelen yeni adres reddedilmez: en uzun süredir yenilenmemiş kayıt ona yer açar. Böylece adresi ya da portu değişen bir santral eski kaydının dolmasını beklemez. Aynı `Contact` ile gelen istek kaydı yeniler; kaydın "ilk kayıt" anı korunur.
- **Sıra.** Aynı `Call-ID` ile daha küçük `CSeq` taşıyan (yolda gecikmiş) istek hiçbir şeyi değiştirmez.
- **Taşkın.** Kayıt istekleri de yabancı kaynak korumasından geçer: kayıtlı bir trunk'ın adresi "tanınan" olmaz, 2 saniyede 30 istek sınırı ona da uygulanır, aşan adres iki dakika cevapsız kalır ("Yabancı kaynaklar"). Olağan bir cihaz dakikada iki istek gönderir (soru ve cevabı).

Okuma ve silme (yönetim API'si ve betikler için):

- `GET /v1/registrations` — bütün kayıtlar; `?trunk_id=` (yinelenebilir) ile yalnızca o trunk'lar. Her kayıt: `trunk_id`, `contact`, `src_ip`, `src_port`, `transport`, `nat`, `since`, `until`, `user_agent`.
- `POST /v1/unregister` — `{"v":1,"trunk_id":…,"contact":…}`; `contact` boşsa trunk'ın bütün kayıtları. Trunk engellenmez: yeniden kaydolabilir.

Yönetim API'si bunları `GET /v1/registrations?client_id=`, `GET` ve `DELETE /v1/client-trunks/{id}/registrations` olarak sunar (`docs/06-api.md`).

Sınırlar:

- **Canlı tutma (keepalive) gönderilmez.** Seçilen yol, adres çevirisi arkasındaki cihazı kısa aralıkla yeniden kaydolmaya zorlamaktır (`REG_NAT_EXPIRES`): her kayıt isteği yolu içeriden açık tutar. Gerekçe: kayıtlar SIP çekirdeğinde durmadığı için oradan yoklama (OPTIONS) göndermek ayrı bir düzenek isterdi; kısa süreli kayıt her cihazın uyduğu standart davranıştır. Bedeli: 60 saniyeden kısa sürede eşlemeyi düşüren yönlendiricilerde iki kayıt arasında gelen çağrı ulaşmayabilir (bu durumda `REG_NAT_EXPIRES` düşürülür; her trunk dakikada 60 / süre × 2 istek üretir), ve verilen süreye uymayıp kendi süresinde ısrar eden bir cihazın kaydı arada düşer.
- TLS ile kayıtlı trunk'a çağrı yalnızca kaydın geldiği bağlantıdan gider ve o bağlantı açık kaldığı sürece ulaşır (T-07; aşağıda, "Müşterinin trunk'ına çağrı"). Bağlantı kapanmışsa SIP çekirdeği yenisini açmaz: deneme başarısız sayılır ve trunk'ın sıradaki kaydı denenir. Trunk her kayıtta (en geç `REG_NAT_EXPIRES`) bağlantıyı yeniden açar ya da tazeler; iki kayıt arasında kopan bağlantı bir sonraki kayda kadar çağrı almaz. SIP çekirdeği yalnızca UDP ve TLS dinler; TCP ile kayıt olamaz.
- TLS ile kayıtlı trunk'a şifreli ses (SRTP, SDES) teklif edilir; UDP ile kayıtlıya düz RTP, `Contact`'ı `transport=tls` taşısa da (ADR-0015: anahtar şifresiz sinyalleşmede taşınmaz).
- Kapatılan ya da silinen bir trunk'ın kaydı süresi dolana kadar listede görünür, ama o trunk'a çağrı gönderilmez ve yeniden kaydolamaz.
- `Path` (RFC 3327), `outbound` (RFC 5626), GRUU ve `q` sırası yoktur; birden çok kayıt varsa en son yenilenen önce denenir.
- Kayıtlı adresin "bilinen" olduğu bilgisi motorun belleğindedir ve başka bir motor düğümünün sakladığı kayıt ya da operatörün sildiği kayıt bir sonraki yapılandırma yüklemesiyle (en geç `RELOAD_SEC`) yansır.
- IPv6 kaynaklı kayıt yalnızca birim testleriyle denendi.

## Adres çevirisi (NAT) arkasındaki uçlar

Adres çevirisi arkasındaki bir santral kendini iç ağdaki adresiyle tanıtır (`Via`, `Contact`, SDP), ama paketleri yönlendiricisinin dış adresinden gelir. Ona yazılan her şey paketlerinin geldiği adrese ve porta gitmelidir.

**Ne zaman uygulanır.** Bir istek, kendisi için özel bir adres bildiriyorsa (`Via` ya da `Contact` içinde RFC 1918 / RFC 6598 adresi) **ve** bu adres paketin geldiği adres değilse adres çevirisinden geçmiş sayılır. Açık adres bildiren istekler ve özel adresi gerçekten geldiği adres olan istekler (santralle aynı ağdaki cihaz) olduğu gibi bırakılır; kaynak portu `Via`'dakinden farklı olan ama adresi tutan taşıyıcılar da etkilenmez. Cevaplar yalnızca `Contact`'larına göre değerlendirilir.

- **Cevaplar** isteğin geldiği adrese ve porta döner (`received` / `rport`; istek `rport` taşımasa da: `force_rport`).
- **Çağrı içindeki istekler** (karşı tarafın BYE'ı, yeni INVITE, ACK) o tarafın ilk isteğinin, kayıtlı trunk'ta kaydının geldiği adrese gönderilir. İstek satırı değişmez (cihazın bildirdiği `Contact`), yalnızca gönderildiği yer değişir. Adres çağrıyla birlikte saklanır (dialog değeri: `caller_hop`, `callee_hop`) ve her çağrı içi istekte uygulanır (`route[hop]`).
- **SIP çekirdeğinin kendi ürettiği BYE** (motor çağrıyı kapattığında, süre dolduğunda) da aynı adrese gider: `local_route` içinde aynı kural uygulanır.
- **`fix_nated_contact` kullanılmadı.** Karar ADR-0014'tedir: onun yerine `Contact` olduğu gibi bırakılıyor ve hedef adres (`$du`) yukarıdaki gibi çağrıyla saklanan değerden veriliyor; `Contact`'ı yeniden yazan hiçbir işlev çağrılmıyor. Kullanılanlar: `nat_uac_test`, `force_rport`, `$dlg_val`, `$DLG_dir`, `$du`. Laboratuvar senaryolarının sonunda süreçlerin düşmediği denetlenir.
- **Ses.** Medya aktarıcısı zaten her çağrıda yoldadır. Adres çevirisinden geçmiş tarafın SDP'sindeki adres ulaşılamaz olduğu için aktarıcıya ilk tahmin olarak sinyalleşmenin geldiği adres verilir (`SIP-source-address`); sesin gerçekte geldiği adresi ve portu aktarıcı gelen paketlerden kendisi öğrenir (rtpengine'in varsayılan öğrenme düzeni: sinyalleşmeden sonraki ilk üç saniye geçici, sonrasında kalıcı). Ses bu yüzden cihaz ilk paketini gönderdikten sonra ona ulaşır; hiç ses göndermeyen (yalnızca dinleyen) bir cihaz, yönlendiricisi portu koruyorsa ilk tahminle yine ulaşılır, korumuyorsa ulaşılmaz.

Laboratuvarda çeviri yapan bir yönlendirici yoktur: senaryolardaki santral özel adresler bildirir (10.0.0.5, 10.0.0.6) ama paketleri motorun tanımadığı laboratuvar adreslerinden gelir (172.30.0.70, 172.30.0.71), `Via`'sında `rport` yoktur ve SDP'sinde bildirdiği port sesin geldiği port değildir. Bildirilen adreslere giden hiçbir şey yerine ulaşmaz; bir yönlendiricinin yol açacağı uyuşmazlık budur (`test/sipp/natlab.sh`).

Sınırlar:

- Kendisi için açık adres bildiren (STUN ya da yönlendiricisindeki SIP ALG ile) cihazlar çeviriden geçmemiş sayılır; onlara bildirdikleri adresten ulaşılır.
- Gerçek bir yönlendiriciyle, simetrik çeviriyle ve eşleme süresi dolan bir yolla denenmedi.
- Adres çevirisi arkasındaki **sağlayıcı** ele alınmadı: sağlayıcı ayağına istekler her zaman bağlantının tanımlı adresine gider.
- Çağrı sürerken cihazın dış adresi değişirse (yönlendirici yeniden başladı) çağrı içi istekler eski adrese gider; yeni kayıt yalnızca yeni çağrıları etkiler.
- IPv6 için adres çevirisi beklenmez; kural yalnızca IPv4 özel adreslerine bakar.

## Müşterinin trunk'ına çağrı

Motor, bir çağrıyı sağlayıcıya değil bir müşterinin kayıtlı trunk'ına teslim edebilir. Community sürümünde numara (DID) kavramı yoktur; burada yalnızca **mekanizma** vardır, hangi numaranın hangi müşteriye gideceğine bir uzantı karar verir.

- **Uzantı tarafı.** `engine.Config.Adjust` içinde kararın rotaları `routing.ToTrunk(trunkID, numara)` ile değiştirilir. Çağrı, her çağrı gibi önce arayanın müşterisi, tarifesi ve planı üzerinden kabul edilmiş olmalıdır (`Adjust` yalnızca kabul edilecek çağrılarda çağrılır). Kaynağı hiçbir trunk'a uymayan çağrıyı bir müşteri adına alan uzantı rotayı `Incoming` cevabında (`routing.Inbound.Routes`) da verebilir; motor onu aynı biçimde kayda çevirir (aşağıda "Müşteri olmayan kaynaktan gelen çağrı").
- **Motor.** Trunk rotasını, o trunk'ın o anki kayıtlarına çevirir: kayıt başına bir rota (en son yenilenen önce), `uri` = trunk'ın bildirdiği `Contact`, `next_hop` = kaydın geldiği adres (yalnızca adres çevirisi arkasında ya da bağlantı üzerinden ulaşılan kayıtta), `media` = `rtp`; TLS ile gelen kayıtta `media` verilmez ve arayüzün kuralı (sinyalleşme TLS ise SRTP) geçerli olur (T-07). Kayıt yoksa, trunk kapalıysa ya da müşterisi askıdaysa rota düşer; hiç rota kalmazsa çağrı **480** (`not registered`) ile reddedilir. Bu, çağrı başına bir Redis okumasıdır ve yalnızca trunk rotası olan çağrılarda yapılır; olağan çağrılarda fazladan hiçbir şey yapılmaz.
- **SIP çekirdeği** isteği `uri`'ye yazar, `next_hop` varsa oraya gönderir ve bu adresi çağrıyla saklar (`callee_hop`); çağrı içindeki istekler ve kendi BYE'ı da oraya gider.
- **TLS ile kayıtlı trunk** (T-07). `next_hop` kaydın geldiği bağlantının karşı ucudur (`sip:<adres>:<port>;transport=tls`). SIP çekirdeği isteği o bağlantıdan gönderir (OpenSIPS, açık bağlantıyı karşı ucun adresi ve portuyla bulur) ve yeni bağlantı açmaz: `route[take]` bu denemeye `NO_NEW_CONN` dal bayrağını koyar (çekirdek parametresi `tcp_no_new_conn_bflag`). Bağlantı kapanmışsa `t_relay` hemen başarısız olur, `route[attempt]` motora `failed` 503 bildirir ve motor trunk'ın sıradaki kaydını (yoksa başka rotayı) verir; hiçbiri kalmazsa arayan 503 alır. Adres çevirisi arkasındaki bir santralin bağlantısının karşı ucu yönlendiricinin portudur; oraya açılacak yeni bir bağlantı ya hiçbir yere ya da başka bir yere ulaşırdı. Ses: SRTP (`RTP/SAVP`, SDES anahtarı); arayan ayağı arayanın teklifine uyar, çeviriyi medya aktarıcısı yapar. Bayrak yalnızca çağrının denemelerine konur; çağrı sürerken bağlantı koparsa çağrı içi istekler (BYE) için SIP çekirdeği yeni bağlantı açmayı dener; bu durum sınanmadı.
- **Kayıt ve sayaçlar.** Böyle bir ayağın çağrı kaydında sağlayıcı bağlantısı yoktur (`connection_id` boş), yerine hedef trunk yazılır (`dst_trunk_id`; sağlayıcıya giden ayakta boş, alan JSON'a hiç konmaz); arayanın müşterisi kendi tarifesinden ücretlenir, alış tutarı sıfırdır. Trunk'a giden çağrılar trunk'ın sayaçlarında (`cc:t:<trunk>`, `cps:t:<trunk>:<saniye>`, trunk'ın yaptığı çağrılarla ortak) ve trunk'ın müşterisinin sayaçlarında (`cc:c:<müşteri>`, `cps:c:<müşteri>:<saniye>`) sayılır; canlı görünümün sağlayıcı kırılımında yer almaz, müşteri kırılımında hem arayanın hem hedef müşterinin sayısına girer ve `GET /v1/calls?client_id=` hedef müşteri için de listelenir (satırdaki `client_id` arayandır).
- **Hedef trunk'ın limitleri** (T-06). Hedef trunk'ın `max_calls` ve `max_cps` değerleri, trunk'a giden her rotaya (kayıt başına) sağlayıcı bağlantısının limitleri gibi konur ve kabulde, çağrının tek Redis betiğinde, sağlayıcı rotalarıyla aynı düzenle denetlenir (`cc:t:<trunk>` kümesi, `cps:t:<trunk>:<saniye>` sayacı): ikinci bir Redis gidişi yoktur (T-09'da her rotaya çeviri kümesi `cc:x` ve sınırı eklendi; T-45'te teslim edilen rotası olan çağrının rotalarına hedef müşterinin kümesi ve sayacı, aşağıda). Trunk doluysa onun bütün kayıtları atlanır, sıradaki rota denenir; rota kalmazsa 503 (`all routes busy`). Çağrı aynı trunk'ın bir başka kaydına geçerken (yedek kayıt) yerini yanında götürür ve saniyedeki çağrı sayısına yeniden yazılmaz: aynı trunk'a giden aynı çağrıdır. Yer, çağrı bitince (`ended`, son `failed`) ya da kapanışı hiç bildirilmeyen çağrının süresi dolunca (ADR-0029) bırakılır. Trunk'ın limitleri iki yönü birlikte sayar (ADR-0042, T-45): trunk'ın yaptığı çağrılar da (limiti varsa) aynı `cc:t:<trunk>` kümesinde ve aynı saniye sayacındadır; `max_calls = 1` olan trunk bir çağrı yaparken çağrı almaz, çağrı alırken çağrı yapamaz (`trunk call limit reached`). Trunk'ın kendine dönen çağrısı bir kez sayılır ve yalnızca arayan tarafında denetlenir.
- **Hedef müşterinin limitleri** (T-45, ADR-0042). Trunk'a giden rota, trunk'ın müşterisinin `max_calls`/`max_cps` değerlerini de taşır; çağrı o müşterinin kümesine (`cc:c:<müşteri>`) ve saniye sayacına (`cps:c:<müşteri>:<saniye>`) yazılır ve sınır, müşterinin yaptığı çağrılarla birlikte denetlenir. Dolu müşterinin trunk'ı, dolu trunk gibi atlanır (sıradaki rota, yoksa 503 `all routes busy`); müşterinin dolu sınırı onun kendi çağrısını da reddeder (`call limit reached`, `cps limit reached`). Aynı müşterinin başka trunk'ına ya da aynı trunk'ın başka kaydına geçiş yeni çağrı sayılmaz. Arayan müşterinin kendi trunk'ına yaptığı çağrı (gelen çağrının adı verilen müşteriye teslimi dahil, "Müşteri olmayan kaynaktan gelen çağrı") bir kez sayılır ve yalnızca arayan tarafında denetlenir. Çağrının parası yalnızca arayandan ayrılır. Betik düzeni: kabul ve sıradaki rota betiği rotaları genişlikleriyle alır (`ARGV[4]`); yalnızca sağlayıcı rotası olan çağrıda rota başına 3 anahtar ve 3 sınır, teslim edilen rotası olan çağrıda 5 (hedef müşterinin kümesi ve sayacı; sağlayıcı rotasında müşteri sınırı -1, sayılmaz). Sınandı: birim testleri `pkg/engine/bothways_test.go`; `make test-routing`, "bothways" (`natlab.sh limit_both`, `limit_client`: `max_calls = 1` olan `lab-nat` kendi çağrısı sürerken çağrı almaz, aldığı çağrı sürerken çağrı yapamaz; müşterisinin sınırı 1 iken diğer trunk'ı `lab-nat-2`'nin çağrısı `lab-nat`'a teslimi 503 ile durdurur).
- **Hedef trunk'ın kodekleri** (T-06). Trunk'a teklif edilen ses, hedef trunk'ın `codecs` listesiyle sınırlanır (rotanın `codecs` alanı; kural "Bağlantı ve trunk ayarları", **Kodekler**): hedefin sırasıyla, arayanın trunk'ının listesi de varsa ikisinde de olanlar. Ortak kodek yoksa trunk rota sayılmaz ve kayıtlarına bakılmaz; başka rota kalmazsa 503 (`no route`) — hedef trunk ya da arayanın trunk'ı çeviriye izin vermiyorsa: veriyorsa rota çevrilerek kalır ("Kodek çevirisi"; birim testiyle sınandı, gerçek çağrıyla sınanmadı). Arayan listedekilerden hiçbirini sunmuyorsa SIP çekirdeği denemeyi 488 ile başarısız bildirir (sağlayıcıdaki gibi).

Laboratuvardaki kanca: motor `LAB_DELIVER="önek=kullanıcı adı,…"` ile başlatılırsa o önekle başlayan numaralara giden çağrıları o kullanıcı adıyla giriş yapan trunk'a teslim eder (`cmd/route-engine/lab.go`). Tam olarak yukarıdaki uzantı yolunu kullanır; bir ürün özelliği değildir, yalnızca laboratuvarın compose dosyasında tanımlıdır ve üretimde verilmemelidir.

Sınırlar:

- Çağrı ya arayanın bir müşteri trunk'ından gelmeli ya da bir uzantı onu bir müşteri adına almış olmalıdır (aşağıda "Müşteri olmayan kaynaktan gelen çağrı"; bir numara sağlayıcısından gelen DID çağrısı böyle bağlanır, Enterprise E16).
- Yükseltme anında süren ve bir trunk'a teslim edilmiş çağrılar hedef müşterinin sayacına yazılmamıştır; bitene kadar o müşterinin sınırında görünmezler (ADR-0042, "Geçiş anı").
- Birden çok kayıt sırayla denenir (bir öncekinin hata vermesi ya da zaman aşımı üzerine); aynı anda çaldırma yoktur.
- TLS ile kayıtlı trunk'a giden ses yalnızca SDP'de anlaşıldı (`RTP/SAVP`, `a=crypto`); laboratuvarda şifreli sesin aktığı ölçülmedi (SIPp SRTP göndermez; sağlayıcı tarafındaki SRTP için de öyle).

## Şifreleme (TLS ve SRTP)

Şifreleme SIP çekirdeğinin ve medya aktarıcısının işidir; motorla arayüz değişmez. Motor yalnızca rotanın adresini verir (`route.uri`), sağlayıcı bağlantısının taşıması `tls` ise adres `;transport=tls` taşır.

- **Arayan tarafı, sinyalleşme:** SIP çekirdeği 5060'ta UDP'nin yanında 5061'de TLS dinler. TLS ile bağlanan arayan, UDP ile gelen gibi tanınır (kaynak adresi ya da kullanıcı adı ve parola).
- **Trunk kuralı** (`client_trunks.security`): `any` (varsayılan) her yoldan gelen çağrıyı alır; `tls` yalnızca TLS ile geleni; `cert` yalnızca TLS ile gelen ve adı `tls_cert_name` olan doğrulanmış bir istemci sertifikası sunanı. Kuralı motor uygular (yukarıda 403); rota simülatörü kuralı yok sayar. İstemci sertifikası yalnızca `TLS_CLIENT_CA` (güvenilen istemci otoriteleri dosyası) verilmişse istenir; verilmemişse `cert` kurallı trunk hiç çağrı alamaz. `TLS_CLIENT_CA` verildiğinde sertifika sunmayan arayan bağlanmaya devam eder, ama bu otoritelerin imzalamadığı bir sertifika sunan arayan TLS ile hiç bağlanamaz.
- **Sağlayıcı tarafı, sinyalleşme:** taşıması `tls` olan bağlantıya TLS ile gidilir ve sağlayıcının sertifikası güvenilen kök sertifikalara göre doğrulanır (`TLS_CA_LIST` dosyası; verilmemişse `TLS_CA_DIR` dizini, varsayılanı sistemin genel kökleri); doğrulanamayan sağlayıcıya çağrı gönderilmez (503, sıradaki rota). **Sertifikadaki adı motor denetler** (SIP çekirdeğinin TLS modülünde ad denetimi yoktur; yalnızca imzayı doğrular): aşağıda "Sağlayıcı sertifikasında ad denetimi".
- **Sertifikanın değişmesi:** SIP çekirdeği sertifika, anahtar ve otorite dosyalarını izler; değişen dosyalar yeniden başlatılmadan yüklenir (laboratuvarda 3, üretimde 30 saniyede bir bakılır). Anahtarıyla uyuşmayan sertifika yüklenmez, eskisi kullanılmaya devam eder. Süren çağrılar ve açık bağlantılar etkilenmez; yeni bağlantılar yeni sertifikayı görür. TLS ayarları bu yüzden yapılandırma dosyasında değil, `deploy/images/opensips/tls-table.sh` betiğinin yazdığı ve `tls_reload` ile yeniden okunan bir tabloda durur.
- **Ses:** sağlayıcı ayağı, o sağlayıcıya TLS ile gidiliyorsa SRTP (`RTP/SAVP`, SDES anahtarı) ile, aksi halde düz RTP ile teklif edilir. Arayan ayağı arayanın teklif ettiği gibi cevaplanır: düz RTP, SDES anahtarlı SRTP ya da DTLS-SRTP (`UDP/TLS/RTP/SAVP`, anahtar medya yolunda anlaşılır). Sağlayıcıya DTLS-SRTP teklif edilmez. İki taraf farklıysa çeviriyi medya aktarıcısı yapar. Anahtarın şifresiz sinyalleşmede taşınmaması için SRTP yalnızca TLS ile birlikte seçilir. Teklifsiz INVITE'ta teklifi sağlayıcı 200'de yapar ve sağlayıcı ayağı onun teklifine uyar; switch bu kez arayana teklif eder: arayanın sinyalleşmesi TLS ise SRTP, değilse düz RTP (ADR-0043; aşağıda, "Teklifsiz INVITE").
- **Erişilemeyen sağlayıcı:** bağlantı kurulamayan (ret, el sıkışma hatası) sağlayıcı hiç cevap vermediği için `failed` olayı 503 koduyla SIP çekirdeğince üretilir ve motorun verdiği sıradaki rota hemen denenir.

Laboratuvarda sertifikaları `deploy/compose/certs.sh` üretir (yerel bir kök ve onun "güvenilen kökler dizini" biçimi; SIP çekirdeği, TLS sağlayıcı ve arayan için birer sertifika; depoya girmez). Sağlayıcılar üretimdeki varsayılan yolla, yani kökler dizinine göre doğrulanır. Senaryolar (`make test-routing`): TLS ile bağlanan arayan; SRTP arayan → düz sağlayıcı; düz arayan → TLS/SRTP sağlayıcı; iki yanı da şifreli çağrı; erişilemeyen TLS sağlayıcıdan sonrakine geçiş; TLS isteyen trunk'ın UDP çağrısını reddetmesi ve TLS çağrısını alması; sertifika isteyen trunk'ın sertifikasız ve yanlış adlı arayanı reddetmesi, doğru adlıyı alması; DTLS-SRTP teklif eden arayan → düz sağlayıcı; teklifsiz INVITE'ta TLS sağlayıcının şifreli teklifi → arayana düz RTP, sağlayıcıya şifreli cevap; sertifikanın çağrı sürerken değiştirilmesi (uyuşmayan sertifikanın yüklenmemesi, yenisinin sunulması, SIP çekirdeğinin yeniden başlamaması, süren TLS çağrısının düşmemesi). Laboratuvar sağlayıcıları yanlış türde teklifi 488 ile reddeder.

Sınırlar:
- Üretim sertifikasının alınması ve yenilenmesi kurulum paketindedir (`docs/09-kurulum.md`, "SIP sertifikası"); gerçek bir sertifika otoritesiyle denenmedi.
- Sağlayıcı sertifikasındaki ad çağrının kendi bağlantısında değil, motorun ayrı bağlantısında denetlenir (aşağıda sınırlarıyla). Genel köklerle (sistemin kendi dizini) gerçek bir sağlayıcıya bağlanılmadı; aynı düzenek laboratuvarın kendi köküyle denendi.
- İstemci sertifikasında yalnızca ortak ad (CN) karşılaştırılır; alternatif adlar (SAN) ve iptal listeleri (CRL) kullanılmaz.
- Kullanıcı adı ve parolayla tanınan TLS zorunlu bir trunk, şifresiz gelen çağrıyı parolayı sorduktan sonra reddeder; yanlış ayarlı cihazın parola özeti o ana kadar ağda görünmüş olur.
- DTLS-SRTP'de senaryo yalnızca anlaşmayı (SDP) denetler: SIPp el sıkışmayı yapmaz ve ses göndermez, bu yüzden DTLS el sıkışmasının tamamlandığı ve şifreli sesin aktığı (SDES için de) ölçülmedi. Tarayıcı (WebRTC) istemcisiyle denenmedi; WebRTC ayrıca ICE ve WebSocket ister, bunlar yok.
- SIP çekirdeğinin kendisinin başlattığı isteklerin (motorun kapattığı çağrıdaki BYE) TLS ile bağlı bir arayana ulaştığı sınanmadı.
- Yük testi yalnızca UDP ve düz RTP ile yapıldı; TLS el sıkışması ve SRTP çevirisinin kapasiteye etkisi ölçülmedi.
- Laboratuvardaki TLS sağlayıcının önünde bir TLS sonlandırıcı (socat) durur: SIPp'in kendi TLS dinleyicisi el sıkışmayı saniyelerce sürdürüyor ve hatalı bir bağlantıda kapanıyor.

### Sağlayıcı sertifikasında ad denetimi

Motor, taşıması `tls` olan her etkin sağlayıcı bağlantısına kendisi TLS ile bağlanır ve sertifikaya bakar: güvenilen bir otorite imzalamış olmalı (`TLS_CA_LIST`; boşsa sistemin kökleri — SIP çekirdeğiyle aynı ayar), süresi geçmemiş olmalı ve **bağlantının adına düzenlenmiş olmalı**. Beklenen ad bağlantının `tls_name` alanıdır; boşsa sunucu alanındaki ad ya da adres (adres için sertifikada IP alternatif adı gerekir). Alternatif adlar ve joker adlar (`*.ornek.com`) desteklenir; yalnızca "ortak ad" (CN) taşıyan eski tip sertifikalar reddedilir.

- Sertifikası reddedilen bağlantıya **hiç çağrı gönderilmez**: motor o rotayı listeden çıkarır, çağrı diğer rotalara gider; başka rota yoksa 503 (`no route`). Sonraki bakışta sertifika düzelmişse bağlantı kendiliğinden geri alınır.
- Bakış aralığı bir dakikadır (`PEER_CHECK_SEC`); yeni eklenen ya da değiştirilen bağlantıya beklemeden bakılır.
- Erişilemeyen sağlayıcı hakkında hüküm verilmez (önceki hüküm kalır): o durumu SIP çekirdeği çağrı sırasında görür ve sıradaki rotaya geçer.
- Durum `GET /v1/stats` cevabındaki `peers` alanında (bakılan bağlantı sayısı, reddedilenler ve nedenleri), motorun günlüğünde ve `softswitch_engine_peers_refused` ölçüsünde görünür (alarm: `VendorCertificateRefused`).
- `PEER_VERIFY=off` denetimi kapatır.

Sınırlar: denetim çağrının kendi TLS bağlantısında değil, motorun ayrı açtığı bağlantıda yapılır. Yolu ele geçirip motorun bağlantısı ile SIP çekirdeğinin bağlantısını ayırt edebilen ve ikincisine başka (güvenilen bir otoritenin imzaladığı) sertifika sunan biri bu denetimi aşar; bunu kapatmak SIP çekirdeğinin kendi bağlantısında ad denetimi gerektirir ve bu OpenSIPS sürümünde yoktur. Yeni bir bağlantı, ilk bakış tamamlanana kadar (saniyeler) denetimsiz çağrı alabilir. Birden çok motor düğümü varsa her biri kendi bakar. İptal listeleri (CRL/OCSP) denetlenmez.

## IPv6

SIP çekirdeği `SIP_IP6` verilince IPv6 adresinde de aynı portlarla dinler (UDP ve TLS; `docs/09-kurulum.md`), medya aktarıcısı `RTP_INTERFACE` içindeki ikinci adresle IPv6'da da ses taşır. Arayüzde değişen bir şey yoktur (sürüm 1, yeni alan yok); T-28, ADR-0047.

- **Kaynak:** `authorize`, `source`, `register`, `ban` ve `unban` içindeki `src_ip` IPv6 olabilir; motor onu kısa biçime çevirir. Trunk adresleri ve ağları IPv6 olabilir, eşleşme kuralı IPv4'teki gibidir (en dar ağ; yukarıda "Kaynağın trunk ile eşleşmesi"). Sağlayıcının IPv6 adresi de her yazımda tanınan kaynaktır.
- **Rota:** motor sağlayıcı adresini URI'de köşeli parantez içinde verir (`sip:…@[2001:db8::20]:5060;transport=udp`; RFC 3261 §19.1.1, RFC 5118 §4). IPv6 ile kayıtlı trunk'ın rotası kaydın `Contact`'ıdır (`sip:pbx@[2001:db8::72]:5077`); NAT arkasındaki IPv6 trunk'ın `next_hop`'u da köşeli parantezlidir. OPTIONS yoklaması (`/v1/probe`, `options_interval_sec`) IPv6 sağlayıcıya da gider.
- **Medyanın adres ailesi:** SIP çekirdeği aktarıcının teklifteki adresini, isteğin gerçekten gittiği yerin yazılışına göre seçer: köşeli parantezli adres IPv6, noktalı adres IPv4. Arayan tarafı kendi ailesinde kalır (cevap). Böylece IPv4 arayan IPv6 sağlayıcıya (ve tersi) aktarıcı üzerinden bağlanır. Teklifsiz INVITE'ta (aşağıda "Teklifsiz INVITE") ilk teklif arayana, INVITE'ın geldiği ailede gider. Sağlayıcı adla tanımlıysa aile seçilmez, aktarıcı arayanın ailesini korur: yalnızca IPv6'da olan bir sağlayıcıya bağlantıda ad değil adres yazın.
- **Laboratuvar:** ayrı bir IPv6 ağı (`fd30::/64`; SIP çekirdeği `fd30::10`, aktarıcı `fd30::11`, motor `fd30::40`, arayan `fd30::21`, sağlayıcı V6 `fd30::20`). Senaryolar `make test-routing`, "ipv6" (`test/sipp/v6lab.sh`): IPv6 → IPv6, IPv6 → IPv4, IPv4 → IPv6 ses iki yönde (aktarıcının her taraftan aldığı paket sayısı ve arayana dönen paketler), teklifsiz INVITE aileler arasında, IPv6 ağ ve daha dar ağ eşleşmesi, yabancı IPv6 adresi, IPv6 ile kayıt ve kayda teslim edilen çağrı, motorun IPv6 sağlayıcıya yoklaması.
- **Sınırlar:** NAT arkasındaki IPv6 uç laboratuvarda denenmedi (IPv6'da adres çevirisi beklenmez; birim testi var). Gerçek sunucuda IPv6 denenmedi. SIP çekirdeğinin yönetim soketi, motor ve aktarıcının denetim soketi IPv4'te kalır (sunucunun içi).

## Motorun genişleme noktaları

Community motoru rota sırasını yalnızca fiyat, öncelik ve ağırlıkla belirler. Başka sürümler motoru kendi ikilisinde `engine.Serve` ile çalıştırırken şu uçlara bağlanabilir (`pkg/engine`):

- `Config.Adjust(CallInfo, Decision) Decision`: motorun kabul etmek üzere olduğu her çağrıda, limitler ve bakiye ayrılmadan önce çağrılır. Rotaların sırasını değiştirebilir, bazılarını çıkarabilir ya da çağrıyı reddedebilir (kodu ve nedeniyle; kod verilmezse 403, rota kalmazsa 503). Çağrı yolundadır: yalnızca bellekten karar vermeli, beklememelidir.
- `routing.ToTrunk(trunkID, numara)`: `Adjust` içinde bir kararın rotalarına konabilen, sağlayıcıya değil bir müşterinin kayıtlı trunk'ına giden rota (yukarıda "Müşterinin trunk'ına çağrı"). Çekirdeğin kendi yönlendirmesi böyle bir rota üretmez.
- `Config.Incoming(CallInfo, *routing.Snapshot) (routing.Inbound, bool)`: kaynağı hiçbir trunk'a uymayan çağrıyı bir müşteri adına almak için (aşağıda "Müşteri olmayan kaynaktan gelen çağrı"; ADR-0032).
- `Config.OnLeg(Leg)`: her deneme bittiğinde ve reddedilen her çağrıda çağrılır: çağrı kaydı, karşı ucun ilk ilerleme bildirimine kadar geçen süre (PDD), konuşma süresi, satış fiyatı, başka deneme gelip gelmeyeceği (`Final`) ve denemenin hiç gönderilmediği (`NotSent`: SIP çekirdeği `cause: "media"` bildirdi; kayıttaki bağlantı seçilen rotadır ama o sağlayıcıya hiç sorulmadı, kalitesine sayılmamalıdır; ADR-0039). Olaylar birden çok kez gelebildiği için aynı deneme de birden çok kez bildirilebilir; engellememelidir.

## Müşteri olmayan kaynaktan gelen çağrı

Kaynağı hiçbir trunk'a uymayan çağrı `403 unknown source` ile reddedilir ve kaydı yazılmaz. Bir uzantı böyle bir çağrıyı bir müşteri adına alabilir (ADR-0032; T-04): Enterprise'ın, numara sağlayıcısının bağlantısından gelen DID çağrısını numaranın sahibine götürmesi bunun üzerine kurulur (E16). Çekirdekte numara kavramı yoktur; yalnızca bu uç vardır.

- **Uç.** `engine.Config.Incoming func(CallInfo, *routing.Snapshot) (routing.Inbound, bool)`. Motor onu yalnızca kaynağı hiçbir trunk'a uymayan, giriş yapmamış (`auth_user` boş) çağrıda sorar; `CallInfo.Dst` gelen numaradır. Uç `false` döner ya da görüntüde olmayan bir müşteri adlandırırsa çağrı bugünkü gibi `unknown source` olur. Uç verilmemişse davranış bit bit aynıdır.
- **`routing.Inbound`.** `ClientID` (zorunlu): çağrının adına alındığı müşteri. İsteğe bağlı: `Number` (numara, tarifelerin yazıldığı biçimde; boşsa geldiği gibi), `Rate` (müşterinin bu çağrı için ödeyeceği fiyat; boşsa müşterinin tarifesindeki fiyat), `Routes` (çağrının gideceği rotalar; boşsa müşterinin planı; örneğin `routing.ToTrunk`).
- **Karar.** Çağrı, o müşterinin kendi kuralı olmayan bir trunk'tan gelmiş gibi verilir (`Snapshot.AuthorizeFor`): hesap açık olmalı; arayan numara kuralları, tarife (ya da `Rate`), paket dakikalar, bakiye ve plan (ya da `Routes`) müşterinindir. Hiçbir trunk'ın kuralı, limiti, kodeği ya da azami süresi uygulanmaz. Karar `Inbound: true` ve `TrunkID: 0` taşır; `Adjust` onu her çağrı gibi görür (rotaları değiştirebilir, `routing.ToTrunk` koyabilir, reddedebilir).
- **Limit ve rezervasyon.** Müşterinin eşzamanlı çağrı ve saniyedeki çağrı sınırları ve bakiye rezervasyonu, müşterinin kendi çağrılarıyla aynı tek Redis betiğinde, aynı sayaçlarla uygulanır; ikinci bir Redis gidişi yoktur. Rezervasyon kararın fiyatıyla (`Rate` verildiyse onunla) yapılır.
- **Kayıt.** Çağrı kaydı müşteriyi adlandırır, `trunk_id` boştur, `src_ip` gelen adrestir; reddedilen çağrının kaydı da yazılır (trunk'tan gelen çağrıdaki gibi). Rater kaydı her kayıt gibi müşterinin tarifesinden ücretlendirir; `Rate` ile başka bir fiyat verilmişse kaydı aynı fiyatla ücretlendirmek uzantının işidir (`api.OnCDRPrice`).
- **Çağrı yolu kuralı.** Uç çağrı yolundadır: yalnızca kendisine verilen görüntüden ve kendi belleğinden karar verir, G/Ç yapmaz, beklemez, görüntüyü değiştirmez. Uzantı kendi verisini (ör. numara → müşteri) görüntü yüklenirken, yükleyiciyi sararak hazırlar (`cmd/route-engine/lab.go`'daki `labDeliver` gibi). Görüntü yenilendiğinde uç yenisini alır.
- **Güvenlik duvarı önce gelir.** `deny` hükmü uçtan önce verilir: engellenen adres uca hiç sorulmaz, `source_blocked` ile reddedilir, kaydı yazılmaz.
- **Kaynak tanınmışlığı.** Uç adresi tanınmış yapmaz: aldığı çağrının cevabında `source_known` yanlıştır ve `POST /v1/source` değişmedi. Uç çağrı başına (numaraya göre) karar verir; adres başına bir güvence vermez. Bu yüzden SIP çekirdeğinin böyle bir çağrıyı motora sorması için adresin zaten tanınıyor olması gerekir: numara sağlayıcısı bir **sağlayıcı bağlantısı** olarak tanımlanır (bağlantının adresi tanınır; bkz. "Yabancı kaynaklar") ya da adres güvenlik duvarında `allow` ile tanınır. Tanınmayan adresin çağrısı eskisi gibi 401 ile parola sorar ve motora hiç gelmez.

Laboratuvardaki kanca: motor `LAB_INBOUND="adres=kullanıcı adı,…"` ile başlatılırsa o adresten gelen ve hiçbir trunk'a uymayan çağrıyı, o kullanıcı adıyla giriş yapan trunk'ın müşterisi adına alır (`cmd/route-engine/lab.go`). Laboratuvarda `172.30.0.91=lab-nat`; .91 ve .92, rotası olmayan bir sağlayıcının (`Lab carrier (inbound)`) bağlantılarıdır. Bir ürün özelliği değildir, yalnızca laboratuvarın compose dosyasında tanımlıdır ve üretimde verilmemelidir.

Senaryo (`make test-routing`, 6d): .91'den gelen çağrı cevaplanır, `Lab SIPp NAT` adına, trunk'sız, sağlayıcı C üzerinden kaydedilir ve ücretlenir; .92'den gelen çağrı 403 (`unknown source`) alır, kaydı yazılmaz; .91 güvenlik duvarında engellenince çağrısı 403 alır, uca sorulmaz, kaydı yazılmaz.

Sınırlar: numara sağlayıcısından gelip müşterinin trunk'ına teslim edilen çağrının (DID) gerçek çağrılı senaryosu Enterprise E16 ile gelecek; çekirdekte bu yol birim testiyle (`TestInboundDeliveredToTrunk`) sınandı.

## Çağrıyı kapatma (motordan SIP çekirdeğine)

Arayüzün ters yöndeki tek komutu: motor, ayrılmış süresi biten ve uzatılamayan çağrıyı SIP çekirdeğine kapattırır. Motor yalnızca "şu `call_id`'li çağrıyı bitir" der; bunun SIP sunucusuna nasıl söyleneceği `pkg/sipctl` içindeki sürücüdedir. OpenSIPS sürücüsü yönetim arayüzüne (`mi_datagram`, UDP üzerinden JSON-RPC, `dlg_end_dlg`) konuşur; adresi `OPENSIPS_MI` ile verilir. SIP çekirdeği çağrıyı kapatınca her zamanki `ended` olayını gönderir. Bu soketin kimlik doğrulaması yoktur ve motorun sırrı onu kapsamaz: yalnızca motorun eriştiği adreste açılmalıdır ("Kimlik doğrulama", sınırlar).

`OPENSIPS_MI` verilmezse motor çağrı kapatamaz; geriye yalnızca `max_duration_sec` zamanlayıcısı kalır ve eşzamanlı çağrılar bakiyeyi aşabilir.

Tek çağrının parası bittiğinde `max_duration_sec` paranın bittiği ana eşittir; motor çağrıyı bundan bir saniye önce kapatır, ama OpenSIPS'in dialog zamanlayıcısı tam saniyelerle sayar ve süreyi bir saniyeye kadar erken doldurabilir. Bu yüzden böyle bir çağrıyı ara sıra (laboratuvarda on çağrıda bir kadar) motor değil SIP çekirdeğinin zamanlayıcısı kapatır (`opensips_expired_dialogs` artar, `cut` artmaz). İkisi de çağrıyı ayrılan tutarın içinde bitirir; ürün hatası değildir (T-29). Motorun kapatması asıl, paranın çağrı sürerken başka yere gittiği durumda gereklidir; orada SIP çekirdeğinin sınırı daha uzaktır.

## Canlı görünüm

Yönetim API'sinin canlı ekranlar için okuduğu uçlar (SIP çekirdeği bunları kullanmaz; API'ye motorun adresi `ENGINE_URL`, sırrı `ENGINE_TOKEN` ile verilir):

- `GET /v1/live` — son 60 tam saniye. `calls`: süren çağrı sayısı. `total`: pencere toplamı. `seconds`: saniye saniye aynı sayaçlar ve o saniyedeki süren çağrı sayısı. `clients`, `connections`, `countries`: kırılımlar; her birinde `calls` (şu an süren) ve limitler. `countries[].prefixes` / `connections`: ülkenin önek ve sağlayıcı bağlantısı dağılımı. `sip_codes`: denemelerin son cevap kodları.
- Sayaçlar: `attempts` (gelen çağrı; bağlantıda biten deneme), `finished`, `answered`, `failed_network` (karşı ucun değil ağın bitirdiği: 402, 403, 404, 480, 484, 486, 487 ve 6xx dışındaki hatalar), `code_503`, `duration_sec` (cevaplanan çağrıların süresi), `pdd_ms` ve `pdd_samples`. Oranları (ASR = answered / finished, NER = 1 − failed_network / finished, ACD = duration_sec / answered, PDD = pdd_ms / pdd_samples) okuyan hesaplar. Gönderilemeyen deneme (`cause: "media"`) bağlantının satırında sayılmaz; müşteri, ülke ve toplam satırlarında 503 ile biten çağrıdır, `sip_codes` içinde denemeden önce reddedilen çağrı gibi sayılır.
- Ülke, aranan numaranın ülke kodundan bulunur (`pkg/numplan`); numaranın uluslararası biçimde geldiği varsayılır.
- `GET /v1/calls?client_id=&offset=&limit=` — süren çağrılar.
- `POST /v1/hangup` — `{"v":1,"call_id":…}`; cevaplanmış çağrıyı SIP çekirdeğine kapattırır (404 bilinmeyen çağrı, 409 henüz cevaplanmamış, 501 kapatma kanalı yok).

Süren çağrı sayıları Redis'ten okunur ve yeniden başlatmadan etkilenmez. Sayaçlar motorun belleğindedir: yeniden başlatınca sıfırdan başlar, birden çok motor düğümünde her düğüm yalnızca kendi payını görür (Faz 4).

## Bakiye rezervasyonu

Süren çağrılar müşterinin bakiyesini paylaşır; hiçbir çağrı ayrılmamış parayı harcayamaz.

- **Kabulde** çağrı için `RESERVE_SEC` (varsayılan 60) saniyelik konuşmanın tutarı ayrılır. Boşta kalan para bir adıma yetmiyorsa yettiği kadar süre ayrılır; hiç yetmiyorsa çağrı 402 ile reddedilir. Boşta kalan = görüntüdeki bakiye + kredi limiti − ayrılmış tutarlar.
- **Paket dakikalar** aynı düzenle, para yerine saniye olarak ayrılır: her paketin her dönemi için ayrı bir defter tutulur (boşta kalan = paketin süresi − görüntüdeki kullanım − ayrılmış saniyeler). Kabulde ve her uzatmada adımın faturalanan saniyeleri önce paketten ayrılır, paketin yetmediği kısım için para ayrılır; ikisi de yetmezse çağrı yettikleri son saniyeden 1 saniye önce kapatılır. Çağrı bitince ayrılan saniye kullanılana indirilir; `rater` sayacı artırınca motora bildirir ve saniyeler, bu andan sonra yüklenen ilk görüntüyle bırakılır (para için olanın eşi; bildirim gelmezse 24 saat sonra). Paketi olmayan çağrıda fazladan Redis işlemi yapılmaz.
- **Konuşma sürerken** ayrılan sürenin bitmesine 10 saniye kala motor bir adım daha ayırır. Para yetmezse kalan paranın yettiği son saniyeye kadar uzatır ve çağrıyı o andan 1 saniye önce kapatır; böylece çağrı ayrılan tutardan fazla faturalanmaz.
- **Çağrı bitince** ayrılan tutar, çağrının gerçek ücretine indirilir ve ayrılmış kalmaya devam eder. `rater` ücreti bakiyeden düşüp bunu Redis'e bildirir; motor, bu andan sonra yüklenen ilk görüntüyle birlikte tutarı bırakır. Böylece ücret ne iki kez sayılır ne de arada kaybolur. Bildirim hiç gelmezse tutar 24 saat sonra kendiliğinden bırakılır. Cevaplanmayan çağrı ayırdığının tamamını hemen geri verir.
- Motor ücreti geçici tutarı hesaplamak için kendisi de hesaplar; kesin tutarı her zaman `rater` belirler.


- **Karar** bellekteki görüntüden (snapshot) verilir; çağrı yolunda SQL yoktur. Yönetim API'si her başarılı yazma isteğinden sonra PostgreSQL üzerinden bildirim gönderir (`NOTIFY softswitch_config`); motor bildirimi alınca görüntüyü hemen yeniden yükler, böylece panelde yapılan değişiklik ve yüklenen para bir saniyeden kısa sürede geçerli olur. Art arda gelen bildirimler tek yüklemede birleştirilir. Görüntü ayrıca `RELOAD_SEC` (varsayılan 10 sn) aralığıyla yenilenir: bu, ücreti düşülen çağrıların bakiyeye yansımasını ve API dışından (doğrudan SQL ile) yapılan değişiklikleri kapsar. `POST /v1/reload` da hemen yeniler. Her yüklemede fiyatların yalnızca değişeni okunur: `rates` üzerindeki tetikleyici yazılan her fiyatın tarife ve önekini `rate_changes`'e not eder, yükleyici önceki yüklemeden sonra biten işlemlerin notlarını okur; geçerliliği başlayan ya da biten fiyatın tarifesi bütünüyle okunur; bütün fiyatlar her `RELOAD_FULL_SEC` (300 sn) baştan okunur (ADR-0048, T-30).
- **Limitler, rezervasyon ve çağrı durumu** Redis'tedir; çağrı başına tek betik çağrısı. Eşzamanlı çağrılar, bitmesi gereken ana göre puanlanmış kümelerde sayılır: kapanışı hiç bildirilmeyen çağrı kendiliğinden düşer. Sağlayıcı bağlantısının limiti doluysa o rota atlanır.
- **CDR**: her deneme ayrı bir bacaktır (`leg` 1, 2, 3…). Motor bacağı önce kendi diskindeki günlüğe, sonra NATS JetStream'e (`CDR` akışı, `cdr.v1` konusu) yazar (aşağıda, "Çağrı kayıtlarının günlüğü"); `rater` servisi ücretlendirip PostgreSQL'e kaydeder, bakiyeleri günceller ve kaydı ClickHouse'a kopyalar. İleti kimliği ve `(call_id, leg)` tekilliği yinelenen teslimatı zararsız kılar. Tanınan bir müşterinin reddedilen çağrısı da sağlayıcısız tek bacak olarak kaydedilir; bilinmeyen kaynaktan gelen çağrı kaydedilmez. Bacağın alanları: `call_id`, `leg`, `client_id`, `trunk_id` (arayanın trunk'ı), `connection_id` (sağlayıcı bağlantısı; reddedilen ya da trunk'a teslim edilen bacakta boş), `dst_trunk_id` (teslim edilen müşteri trunk'ı; yalnızca o bacakta bulunur), `src`, `src_ip`, `dst`, `start_at`, `answer_at`, `end_at`, `sip_code`, `pdd_ms`. `rater` bilmediği alanı taşıyan kaydı reddeder; bu yüzden yükseltmede `rater` motordan önce yükseltilir (`deploy/prod/upgrade.sh`). `dst_trunk_id` sağlayıcıya giden bacakta JSON'a hiç konmadığından, alanı bilmeyen eski bir `rater` olağan kayıtları almaya devam eder.
- `GET /v1/stats` karar süresini (p50, p99, en yüksek), uzatılan ve motorun kapattığı çağrı sayılarını verir; `make load` raporu karar süresini 10 ms bütçesiyle karşılaştırır. `reservations.extended` (ve `softswitch_engine_reservations_extended_total`) yalnızca tam adım uzatmalarını sayar; paranın yettiği kadarlık son, kısmi uzatma sayılmaz, o çağrı kapatılınca `reservations.cut` artar.
- Uçtan uca kanıt: `make test-routing`, "reserve" (T-03): varsayılan `RESERVE_SEC` ile 130 saniyelik çağrıda iki uzatma ve ücrete inen tutar, ikinci adımda parası biten müşterinin 89. saniyede kapatılan çağrısı.

## CDR'lerin ClickHouse'a akışı

PostgreSQL ücretlendirmenin asıl kaydıdır: kayıt, bakiye düşümü ve tekillik aynı işlemde orada yapılır. Arama ve raporlar için her kayıt ClickHouse'a da kopyalanır.

- Kayıt saklanırken aynı işlemde `cdr_export_queue` tablosuna bir satır yazılır; yani kayıt saklandıysa kuyruktadır, saklanmadıysa değildir.
- `rater`, `CLICKHOUSE_URL` verilmişse kuyruğu 5.000'lik gruplar hâlinde ClickHouse'a yazar ve satırları ancak ClickHouse kabul ettikten sonra kuyruktan siler. Bir kayıt birden çok kez gidebilir; tablo (`ReplacingMergeTree`) yinelenenleri birleştirir, kesin sayı isteyen sorgu `FINAL` ile okur.
- Aktarım ilk açıldığında o ana kadarki bütün kayıtlar kuyruğa alınır. `CLICKHOUSE_URL` hiç verilmemişse kuyruğa hiçbir şey yazılmaz.
- Yönetim API'sine `CLICKHOUSE_URL` verilirse `GET /v1/reports/summary` ClickHouse'tan cevaplanır (kayıtlar oraya bir iki saniye gecikmeyle ulaşır); ClickHouse cevap vermezse aynı rapor PostgreSQL'den verilir ve günlüğe yazılır. İki kaynağın aynı sonucu verdiğini `make test-reports` denetler.
- ClickHouse şeması `db/migrations/clickhouse/` altındadır ve `rater` başlarken uygulanır. Satırlar JSON olarak gider; ClickHouse'ta karşılığı olmayan sütunlar (ör. Enterprise'ın eklediği) atlanır.

## Çağrı kayıtlarının günlüğü

ADR-0029 (S-4'ü kapattı). Motor her kaydı NATS kabul edene kadar kendi diskinde tutar (`pkg/engine/journal.go`, `sink.go`); NATS kesikken motor yeniden başlasa ya da çökse de kayıt kaybolmaz. Arayüz değişmedi.

- **Çağrı yolunda disk yok.** Olayı işleyen kod kaydı bellekteki kuyruğa koyar (200.000 kayıt), o kadar. Arka plandaki tek yazıcı kuyruktakileri toplu hâlde (en çok 2.048 kayıt) günlüğe ekler ve her toplu yazımdan sonra bir kez `fsync` eder; gönderenler kaydı oradan okuyup NATS'a yazar ve NATS kabul edince günlüğe "tamam" der. Kayıt, olay ile toplu yazımın `fsync`'i arasındaki birkaç milisaniye bellekte durur; motor tam bu arada çökerse o kayıtlar kaybolur (tek kalan pencere).
- **Biçim:** dizin `CDR_JOURNAL_DIR`; içinde sırayla numaralı `cdr-<sıra>.log` parçaları ve bir `lock` dosyası (aynı dizini iki süreç açamaz; ikincisi başlamaz). Her kayıt: 4 bayt uzunluk, 4 bayt CRC-32C, kaydın JSON'u (NATS'a giden iletinin aynısı). Yazılan parça 1 MiB'a ulaşınca ya da bir dakika dolunca kapanır; kapanmış ve bütün kayıtları NATS'ta olan parça silinir.
- **Açılışta** dizindeki parçalar baştan okunur. Son kaydı yarım kalmış (yazılırken çökülmüş) ya da sağlama toplamı tutmayan parça o kayıttan sonrası kesilerek bırakılır (sayaç `…_journal_torn_total`); o kayıt hiçbir zaman "yazıldı" sayılmamıştı. Kalan kayıtların hepsi yeniden gönderilir: aralarında NATS'ın zaten aldıkları da vardır (bir parça içindeki bütün kayıtlar onaylanana kadar parça silinmez; yani en çok son dakikanın ya da son 1 MiB'ın kayıtları). Tekrar zararsızdır: NATS ileti kimliğiyle (`call_id:leg`, 10 dakikalık pencere), rater `(call_id, leg)` tekilliğiyle eler; bakiye ve paket bir kez düşülür.
- **NATS kesikken** motor başlar ve çağrı alır (akış NATS gelince kurulur); kayıtlar diskte birikir, NATS gelince gönderilir.
- **Günlük dolarsa** (`CDR_JOURNAL_MAX_MB`, varsayılan 1024) ya da disk yazmayı reddederse kayıtlar bellekteki kuyrukta bekler ve yazıcı artan aralıkla yeniden dener; bu sırada eski davranış geçerlidir (motor durursa bellektekiler kaybolur; kuyruk da dolarsa çağrı sonu olayı bekler). Alarm: `CallRecordJournalFilling`, `CallRecordJournalFailing`.
- **Kapanışta** (SIGTERM) motor önce dinleyiciyi kapatır, süren istekleri bitirir, sonra bellekte kalan kayıtları günlüğe yazar.
- `CDR_JOURNAL_DIR` boşsa günlük yoktur (eski davranış; motor uyarı yazar). Kurulum ve laboratuvar bir Docker birimi (`engine-journal`) verir (`docs/09-kurulum.md`).

## Bitişi bildirilmeyen çağrı

ADR-0029. SIP çekirdeği yeniden başlarsa süren çağrıları unutur; çağrı biterken motora ulaşamazsa `ended` kaybolur. İkisinde de motor çağrının bittiğini duymaz. Eskiden çağrının durumu süresi dolunca kayıtsız düşerdi.

- **Ne zaman:** çağrının durumu, kabulündeki azami süresi (`max_duration_sec` kararı) + kurulum payı (300 sn) dolunca "süresi doldu" sayılır; o andan sonra çağrı hiçbir yolda sürüyor olamaz. Sayaçlardan da aynı anda düşer.
- **Nasıl:** her çağrı kabulde `expiry` kümesine bitmesi gereken anla (unix saniyesi) yazılır (kabul betiğinin içinde; çağrı başına Redis çağrısı eklenmez) ve `Finish` ile çıkar. Çağrı durumu bu andan sonra 24 saat daha saklanır (`expiryGrace`). Motorun zamanlayıcısı (250 ms'de bir, rezervasyon zamanlayıcısıyla birlikte) süresi dolanları 30 saniyelik kiralamayla alır (birden çok motor aynı çağrıyı iki kez işlemez; motor ölürse çağrı kiralama bitince geri gelir).
- **Kayıt:** ne olduğu bilinen kadarı yazılır, bilinmeyen ücretlenmez. Cevaplanmış deneme 200 koduyla, bitişi cevap anı olarak (konuşma süresi 0, ücret yok) yazılır; cevaplanmamış deneme 408 koduyla, bitişi durumun düştüğü an olarak. Ayrılan tutarın tamamı bırakılır; çağrı sayaçlardan ve kümelerden çıkar. Genişleme noktası `OnLeg` bu kaydı da duyar; canlı görünüm (son 60 saniye) saymaz. Sayaç `softswitch_engine_cdr_expired_total`, alarm `CallsExpiredUnreported`; her biri motorun günlüğüne de yazılır.
- Böyle bir kayıt raporda `answer_at = end_at` ve 200 koduyla (ya da cevapsız 408 ile) tanınır. Sağlayıcı bu çağrının gerçek süresini faturalayabilir; switch'in kaydında süre yoktur, uzlaşma elle yapılır.
- **Neden süresiz (ADR-0040):** ayrılan ya da verilen süreye göre ücretlemek gerçek süreyle ilgisizdir; azami süre bakiyenin yettiği süre (en çok 7200 sn) olduğundan 30 saniyelik bir çağrı iki saate kadar ücretlenebilirdi. Bilinmeyen süre için ücret alınmaz.
- **Sınır:** kayıt ancak durum düşünce yazılır: çağrı o ana kadar (en çok azami süre + 5 dakika) sayılı ve parası ayrılmış kalır; motor bu arada SIP çekirdeğine kapatma (`dlg_end_dlg`) gönderiyorsa (para bittiyse) çağrı bulunamadığı için her seferinde başarısız olur. Bütün motorlar 24 saatten uzun kapalı kalırsa o arada süresi dolan çağrıların durumu kaydı yazılmadan düşer. Güncellemeden önce başlamış (kümede olmayan) çağrılar eski davranışla düşer.

Senaryolar `make test-journal` içindedir (`test/sipp/journal.sh`; NATS'ı durdurur, motoru ve SIP çekirdeğini öldürür, yaklaşık 6 dakika sürer: tek başına çalıştırılır): (1) SIP çekirdeği cevaplanmış bir çağrının ortasında öldürülüp başlatılır; (2) NATS durur, üç çağrının kaydı motorun diskinde bekler; (3) motor, başka bir cevaplanmış çağrı 5 saniyelik sınırıyla biterken öldürülür ve NATS yokken yeniden başlatılır; diskteki kayıtları bulur; (4) yeniden başlayan motordan bir çağrı daha; (5) NATS gelir: dört kaydın hepsi bir kez saklanır, günlük boşalır, tekrar gönderilenler sayılır; (6) iki kayıp çağrı, durumları düşünce 200 koduyla, `answer_at = end_at`, süre ve ücret 0 olarak kaydedilir, durum deposunda bir şey kalmaz; düşülen bakiye saklanan kayıtların tutarı kadardır. Çağrılar laboratuvar müşterisinin 885 önekli trunk'ından (azami 5 sn) ve 905551112233'ten (sağlayıcı 172.30.0.20) yapılır; yeni adres kullanılmaz.

## Bilinen sınırlar

- Rezervasyon yalnızca müşterinin kendi bakiyesi ve paket dakikaları içindir; sağlayıcı bakiyesi ve Enterprise'taki bayi bakiyeleri ayrılmaz.
- `max_duration_sec` paketin ve bakiyenin o an boşta olan kısmına göre hesaplanır; paketteki saniyeler başka çağrılara giderse motor çağrıyı daha erken kapatır.
- Süren bir çağrı, `max_duration_sec` verildiği andaki paraya göre hesaplandığı için sonradan yüklenen parayla bu sınırın ötesine uzamaz.
- Motor ya da Redis ayrılan süre bitmeden 10 saniyeden uzun süre erişilemez kalırsa uzatma ve kapatma yapılamaz; çağrı `max_duration_sec` dolana kadar sürebilir ve ayrılmamış para harcanabilir.
- Bitişi motora hiç bildirilmeyen çağrı (SIP çekirdeği yeniden başladı ya da `ended` motora ulaşamadı), durumu düşene kadar (azami süre + 5 dakika) sayılı ve parası ayrılmış kalır; sonra konuşma süresi olmadan, ücretsiz kaydedilir (yukarıda, "Bitişi bildirilmeyen çağrı").
- Birden çok motor düğümü aynı Redis'i paylaşabilir, ama her biri kendi görüntüsünün bakiyesini kullanır; düğümler arasında bir yenileme aralığı kadar fark olabilir (Faz 4).
- Tutarlar Redis betiklerinde 2^53 mikro birime (yaklaşık 9 milyar para birimi) kadar tamdır.
- Redis anahtarları tek düğüm varsayar (betik birden çok anahtara dokunur); Redis kümesi Faz 4'te ele alınacak.
- Bildirim "bir şey değişti" der, neyin değiştiğini söylemez: motor her seferinde görüntünün tamamını yükler. Çok büyük tarifelerde (milyonlarca fiyat) bunun süresi ölçülmedi; gerekirse yalnızca değişeni yükleme Faz 4'te ele alınacak.
- Özet raporu ClickHouse'tan okunur; çağrı kayıtları listesi ve tekil kayıt hâlâ PostgreSQL'den okunur. PostgreSQL'deki eski kayıtlar silinmez.
- Teklifsiz INVITE'ta (teklif 200'de, cevap ACK'te) cevap taşımayan ACK çağrıyı iki taraftan kapattırır; aktarıcı 200'deki teklifi almazsa çağrı cevaplandıktan hemen sonra kapatılır ve 503 olarak kaydedilir; PRACK'te taşınan teklif ve cevap işlenmez (aşağıda, "Teklifsiz INVITE"; ADR-0043). Arayanın vazgeçmesinin (CANCEL) senaryoları aşağıda, "Arayanın vazgeçmesi".
- Cevaplanan çağrıyı ACK'lemeyen arayanın çağrısı en çok 32 saniye açık, sayılı ve parası ayrılmış kalır ve bu süre ücretlenir (sağlayıcı da 200'ü verdiği için öder; aşağıda, "Cevabı onaylamayan arayan").
- Sesi kesilen çağrı `RTP_TIMEOUT` (60) saniye sonra kapanır ve bu sessiz süre de ücretlenir; bekletilen çağrıda bu süre `RTP_HOLD_TIMEOUT` (3600) saniyedir (aşağıda, "Ölü çağrı").
- Medya aktarıcısı cevap vermezse ya da portu kalmazsa yeni çağrı 503 ile reddedilir, sıradaki rota denenmez (ADR-0030); aktarıcı ilk fark edildiğinde o anda teklif yapan çağrılar ~5 saniye bekler ve kayıt, hiç ulaşılmamış sağlayıcı bağlantısının adıyla yazılır (aşağıda, "Medya aktarıcısı teklifi almazsa").
- Çağrı içindeki aktarma isteği (REFER) her zaman 403 ile reddedilir; switch aktarma taşımaz (ADR-0028). Erken medyası sessiz (183 + SDP, paket yok) ve 60 saniyeden uzun çalan çağrıyı medya aktarıcısı çalarken kapattırır, bağlantının `ring_timeout_sec` değeri daha büyük olsa da (aşağıda, "Erken medya, PRACK ve aktarma"; ADR-0038).

## Kayıt, adres çevirisi ve trunk'a çağrı senaryoları

`make test-routing` içinde ("register", "nat", "media", "trunk", "tlstrunk", "operator"):

- Adres çevirisi arkasından doğru parolayla kayıt: kabul, cevabın `Via`'da yazmayan adrese dönmesi, kısa sürenin bildirilmesi; kaydın motorda ve yönetim API'sinde görünmesi (nereden, ne zamandan beri, ne zamana kadar); başka hesabın anahtarına gösterilmemesi; kayıtlı adresin OPTIONS isteğinin cevaplanması.
- Yanlış parolanın yeniden sorulması; TLS isteyen trunk'ın UDP ile kaydının reddi ve TLS ile kabulü; çok kısa sürenin 423 ve `Min-Expires` ile reddi; bunlardan hiçbirinin saklanmaması.
- Kendi adresini bildiren trunk'ın istediği süreyi alması; ikinci adresin ilkinin yerine geçmesi (sınır 1); trunk'ın kaydını geri alması (`Expires: 0`); süresi dolan kaydın düşmesi.
- Adres çevirisi arkasındaki arayanın çağrısı: kurulması, arayanın kapatması; motorun kapattığı çağrıda (`POST /v1/hangup`) SIP çekirdeğinin BYE'ının arayana ulaşması.
- Ses, gerçek RTP ile: arayan SDP'sinde bildirmediği bir porttan 100 paket gönderir, sağlayıcı her birini geri yollar. Aktarıcının sorgusunda arayan tarafının hedefinin öğrenilen gerçek adres ve port olduğu, iki yönde paket sayıldığı ve geri dönen paketlerin arayanın kabına ulaştığı (gelen datagram sayısıyla) denetlenir.
- Kayıtlı trunk'a çağrı: kaydın geldiği adrese ulaşması, trunk'ın bildirdiği adrese yazılmış olması, şifresiz ses ve aktarıcı adresi taşıması; çağrıyı arayanın, trunk'ın ve motorun kapatması (üçünde de BYE iki tarafa ulaşır); iki taraf da adres çevirisi arkasındayken trunk'ın ve arayanın kapatması; kayıtların sağlayıcısız cevaplanmış ayak olarak yazılması ve arayanın tarifesinden ücretlenmesi.
- TLS ile kayıtlı trunk'a çağrı ("tlstrunk", T-07; `natlab.sh tls_trunk`, `gone_tls`; `uas-trunk-tls.xml`; laboratuvar kancası 8987 → `lab-nat-tls`, 8986 → `lab-nat-mix`, ikisi de 172.30.0.70'ten). SIPp bir bağlantıyı iki süreç arasında taşıyamadığı için kayıt ve çağrı tek SIPp sürecindedir: arayan, santral senaryosunun `Call-ID`'siyle arar ve SIPp gelen INVITE'ı kaydın senaryosuna bağlar. Denetlenen: INVITE'ın kaydın bağlantısından gelmesi (santral kendi portunda da dinlediği için çağrı sürerken santralin SIP çekirdeğine bağlantıları sayılır: yalnızca kendi açtığı, 5061'e giden), kayıtlı adrese yazılmış olması, aktarıcı adresi, `RTP/SAVP` ve `a=crypto`; düz RTP teklif eden arayanın düz RTP cevap alması. Bağlantı kapandıktan sonra (kaydı yapan süreç biter): UDP ile de kayıtlı `lab-nat-mix`'te TLS denemesi hemen 503 ile başarısız olur ve çağrı UDP kaydına düz RTP ile gider (iki ayak); başka kaydı olmayan `lab-nat-tls`'te arayan 503 alır ve SIP çekirdeği, o adreste dinleyen biri olsa da yeni bağlantı açmaz. Her ayak hedef trunk'ı taşır, durum deposunda bir şey kalmaz.
- Operatörün kaydı API'den silmesi: hemen düşmesi, denetim kaydına adresle yazılması, trunk'a çağrının 480 alması, adresin yeniden yabancı sayılması.
- Kayıt isteği taşkını: 3 saniyede 600 istek gönderen adresin sonraki (doğru parolalı) isteğinin cevapsız kalması.

## Arayanın vazgeçmesi (CANCEL)

Arayüzde yeni bir şey yoktur: vazgeçen arayanın denemesi `failed` olayıyla, `sip_code` 487 ile bildirilir ve 487 hiçbir kuralda (bağlantının `failover_mode` ayarı `always` olsa da) sıradaki rotaya geçirmez. Motor çağrıyı bırakır: sayaçlardan düşer, ayrılan tutarın tamamını geri verir, denemeyi 487 ile kaydeder.

- **Sağlayıcı çalarken:** SIP çekirdeği CANCEL'ı 200 ile cevaplar ve sağlayıcıya iletir; sağlayıcının 487'si arayana gider.
- **Motor henüz karar vermemişken:** CANCEL 200 ile cevaplanır; karar gelince SIP çekirdeği çağrının iptal edildiğini görür, motora verilen ilk rota için `failed 487` bildirir, arayana 487 verir ve sağlayıcıya hiçbir şey göndermez. Kayıtta rota, motorun verdiği bağlantıyla görünür.
- **Cevaptan sonra:** CANCEL'ın etkisi yoktur; çağrı sürer ve BYE ile kapanır.

Senaryolar (`make test-routing`, "cancel"): çalan sağlayıcıya CANCEL'ın ulaşması, arayanın 487 alması ve yedek rota varken (olağan kural ve `always`) sıradakinin denenmemesi; cevaptan sonra gelen CANCEL'da çağrının sürmesi ve BYE ile kapanması; motorun kararı 0,8 saniye gecikirken (laboratuvar kancası `LAB_DECIDE_DELAY`, `cmd/route-engine/lab.go`; üretimde verilmez) vazgeçen arayan. Her durumda durum deposunda (çağrı, eşzamanlı çağrı kümeleri, ayrılan tutar) hiçbir şey kalmaz ve tek kayıt yazılır.

Sınırlar: CANCEL ile 200 OK'in aynı anda yola çıktığı gerçek yarış SIPp ile tekrarlanabilir biçimde kurulamıyor; iki yanı ayrı ayrı sınandı (çalarken vazgeçme ve cevaptan sonra vazgeçme). Yarışı kaybeden ve 200'ü ACK'lemeyen bir arayanın çağrısını SIP çekirdeği 32 saniye sonra kapatır (aşağıda).

## Cevabı onaylamayan arayan (ACK)

Arayüzde yeni bir şey yoktur (ADR-0026). Sağlayıcının 200'ü arayana iletildikten sonra arayan 32 saniye (64×T1; RFC 3261 §13.3.1.4, §17.1.1.2) içinde ACK göndermezse SIP çekirdeği çağrıyı iki taraftan BYE ile kapatır. Motor her zamanki `ended` olayını alır (her BYE için bir kez; tekrarı zararsızdır); kayıt 200 koduyla ve gerçek süreyle (cevaptan BYE'a, yaklaşık 32 saniye) yazılır, sayaçlar düşer ve ayrılan tutar gerçek ücrete iner. Tipik durum: CANCEL'ı 200 ile yarışan ve vazgeçtiğini sanan bir cihaz.

- **Nasıl:** dialog modülünün zamanlayıcısı. Cevap gelince (`onreply_route[media]`) çağrının azami süresi saklanır (`$dlg_val(limit)`) ve zamanlayıcı 32 saniyeye indirilir; ACK gelince (çağrı içi istek) zamanlayıcı azami sürenin kalanına (`limit` − cevaptan beri geçen süre) geri alınır. Dolarsa `create_dialog("B")` iki tarafa BYE gönderir ve `local_route` `ended` bildirir; azami süre dolduğundaki kapatmayla aynı yoldur. 32 saniye betikte sabittir (şablon değeri değildir); laboratuvar da aynı değerle çalışır.
- Azami süresi 32 saniye ya da daha kısa olan çağrıya dokunulmaz: zamanlayıcı zaten o süreyle biter.
- Sağlayıcının yeniden gönderdiği 200'ler zamanlayıcıyı yeniden kurmaz.
- ACK'in geldiği saniye tam sayıya yuvarlanır: geç gelen ACK'ten sonra çağrı azami süresini en çok bir saniyenin kesri kadar aşabilir (dialog zamanlayıcısı zaten saniye adımlıdır).
- Dialog modülünün kendi "yarış" bayrağı (`create_dialog("E")`, `race_condition_timeout`) yalnızca 200 ile CANCEL'ın yarıştığı durumu kapsar; ACK'i hiç gelmeyen her çağrı için kullanılmadı.

Senaryolar (`make test-routing`, "ackwait"; `test/sipp/uac-noack.xml`, `uac-lateack.xml`, sağlayıcı `uas-ackwait.xml`, `connlab.sh ack_wait`; tohum 8720 ve 8721, sağlayıcılar 172.30.0.65 ve .66): 200'ü hiç ACK'lemeyen arayan 32 saniye sonra BYE alır, sağlayıcı da BYE alır; kayıt 200 ve 31–34 saniye; durum deposunda bir şey kalmaz. Aynı anda ACK'ini 3 saniye geç gönderen arayanın çağrısı 32 saniyeyi geçer ve arayanın BYE'ıyla biter (kayıt 35–38 saniye).

## Ölü çağrı (medya zaman aşımı)

Arayüzde yeni bir şey yoktur (ADR-0027). Sesi kesilen bir çağrıyı (iki taraftan da hiç RTP gelmeyen; ör. kapanışını bildiremeden düşen bir cihaz, kopan bir ağ) medya aktarıcısı saptar: `RTP_TIMEOUT` (varsayılan 60) saniye boyunca çağrının hiçbir akışından paket görmezse SIP çekirdeğine "bu çağrıyı bitir" der, SIP çekirdeği iki tarafa BYE gönderir ve motor her zamanki `ended` olayını alır (her BYE için bir kez). Kayıt 200 koduyla ve gerçek süreyle (cevaptan BYE'a; sessiz geçen süre dahil) yazılır, sayaçlar düşer, ayrılan tutar gerçek ücrete iner.

- **Nasıl:** rtpengine'in kendi zaman aşımı (`timeout`) ve zaman aşımında çağrıyı XML-RPC ile bildirmesi (`b2b-url`, `xmlrpc-format = 1`: `teardown` yöntemi, parametresi Call-ID). SIP çekirdeği bunu yönetim arayüzünün XML-RPC kapısında (`mi_xmlrpc_ng`, ölçü dinleyicisiyle aynı yer: `SIP_METRICS`, yol `/RPC2`) karşılar; `teardown` komutu rtpengine modülünündür ve `dlg_end_dlg`'nin sarmalayıcısıdır. BYE'lar ve `ended` olayı motorun kapattığı ya da azami süresi dolan çağrıdakiyle aynı yoldan gider (`local_route`). Betikte karar yoktur; motor da bu yolda değildir. Aktarıcının bu kapıya adresi `RTP_TEARDOWN_URL` ile verilir.
- **Bekletme:** bir taraf göndermeyecek olduğunu bildirdiyse (`a=sendonly`, `a=recvonly`, `a=inactive`) aktarıcı o çağrıya uzun süreyi uygular: `RTP_HOLD_TIMEOUT` (varsayılan 3600 saniye, rtpengine'in `silent-timeout` ayarı). Bekletilen ve hiçbir tarafın ses göndermediği çağrı bu yüzden bir dakikada kapanmaz; bir saat sessiz kalırsa kapanır. Bekletmede müzik çalan (tek yönde ses akan) çağrı zaten "ses var" sayılır.
- **Ne sayılır:** zaman, akışın son paketinden (ya da paket hiç gelmediyse teklif/cevabın aktarıcıya yazıldığı andan) ölçülür; aktarıcı her saniye bakar. Tek yönde akan ses çağrıyı canlı tutar.
- **Oturum zamanlayıcıları (RFC 4028 §7–10):** switch kendisi yenileme istemez ve oturum zamanlayıcısı uygulamaz; `Supported: timer`, `Session-Expires`, `Min-SE` ve cevaptaki `Require: timer` olduğu gibi iki yönde geçer, yenileyen (`refresher`) tarafın re-INVITE ya da UPDATE'i diğer çağrı içi istekler gibi iletilir ("Çağrı içi yeni teklif"). Yenilemesi kesilen çağrıyı taraflar kendileri BYE ile kapatır; kapatamadan düşen tarafın çağrısını sessizlik kapatır.
- Aktarıcının oturumu, SIP çekirdeğinin kendi kapattığı çağrıda da (motorun kapatması, azami süre, ACK beklemesi) BYE'larla birlikte silinir (`local_route`, iki BYE için bir kez: `$dlg_val(media_gone)`). Önceden bu oturumlar aktarıcının zaman aşımına bırakılıyordu.

Senaryolar (`make test-routing`, "deadcall"; `test/sipp/uac-media.xml`, sağlayıcı `uas-media.xml` (`-rtp_echo`), `connlab.sh media_timeout`; tohum 8725–8728, sağlayıcılar 172.30.0.73–.76, sessiz çalan çağrı için 8733 ve .86 (`uac-early.xml` "silent" planı, `uas-early.xml` "late" planı); ses `run.sh`'nin yazdığı 3 ve 70 saniyelik yakalama dosyalarıyla). Laboratuvar üretim süreleriyle çalışır (60 / 3600 sn; laboratuvarın sessiz başka çağrıları 40 saniyeye kadar sürdüğü için kısaltılmadı); beş çağrı aynı anda koşar, bölüm yaklaşık 75 saniye sürer:

- 3 saniye sesten sonra susan çağrıya iki taraf da BYE alır; kayıt 200 ve 62–66 saniye, aktarıcının zaman aşımı sayacı artar.
- 70 saniye boyunca ses akan çağrı kapatılmaz, arayan 65. saniyede kendisi kapatır.
- 3 saniye sesten sonra `sendonly` ve `inactive` ile bekletilen, sonra iki tarafın da hiç göndermediği iki çağrı kapatılmaz; arayanlar 68,5. saniyede kapatır.
- SDP'li 183 ile çalan, hiçbir taraftan ses gelmeyen ve sağlayıcının 70. saniyede açacağı çağrı (bağlantının çalma süresi 90 sn) 183'ten 60–66 saniye sonra bırakılır: sağlayıcıya CANCEL, sağlayıcının 487'si arayana ulaşır, kayıt 487 (ADR-0038).
- Dört sesli çağrıda arayanın oturum zamanlayıcısı başlıkları sağlayıcıya, sağlayıcının cevaptaki başlıkları arayana değişmeden ulaşır (değişen başlıkla sağlayıcı 488 verir, arayan senaryosu düşer). Her çağrı tek kayıt, durum deposunda bir şey kalmaz.

Sınırlar:

- Aktarıcı yalnızca iki tarafı da bilinen (cevaplanmış ya da erken medyası cevaplanmış) akışa kısa süreyi uygular; cevapsız teklif `offer-timeout`'a (3600 sn) kalır. Erken medyalı (183 + SDP) olup hiçbir taraftan paket gelmeden 60 saniyeden uzun çalan çağrı (yalnızca bağlantının `ring_timeout_sec` değeri 60'tan büyükse olabilir) **çalarken kapatılır**: aşağıda "Erken medya, PRACK ve aktarma". **KARAR (ADR-0038).**
- Aktarıcının isteği SIP çekirdeğine ulaşmazsa (ör. o sırada yeniden başlıyorsa) oturum aktarıcıda yine silinir; SIP tarafı `max_duration_sec` ya da rezervasyonun bitişiyle kapanır. Bu durum sınanmadı.
- `/RPC2` kapısında bütün yönetim komutları kimlik doğrulamasız açıktır (MI soketi gibi): yalnızca özel ağda ya da 127.0.0.1'de dinlemelidir (ARCHITECTURE S-14).

## Çağrı içi yeni teklif (re-INVITE)

Arayüzde yeni bir şey yoktur: motor çağrı içindeki yeni tekliflerden haberdar edilmez (RFC 3261 §14; RFC 3264 §8). Çağrının süresi, ayrılan tutarı ve kaydı değişmez; SIP çekirdeğinin dialog zamanlayıcısı da yeniden kurulmaz, azami süre cevaptan sayılmaya devam eder.

- **Akış (iki yönde aynı):** istek `topology_hiding_match` ile dialoga bağlanır, `route[hop]` NAT arkasındaki tarafa gerçek adresini verir (`caller_hop`, `callee_hop`), SDP medya aktarıcısından geçer (`rtpengine_offer`: `replace-origin replace-session-connection`, dialogda saklanan kodek bayrakları, teklif sahibi NAT arkasındaysa `SIP-source-address`); cevaptaki SDP `onreply_route[reinvite]` içinde `rtpengine_answer` ile yazılır. İki tarafa giden SDP'de aktarıcının adresi vardır.
- **Kodek listesi** (bağlantının ya da trunk'ın `codecs` ayarı) dialogla saklanır ve yeni teklife, hangi taraftan gelirse gelsin, uygulanır: listede olmayan kodek çıkar, `telephone-event` kalır. Çevrilen çağrıda karşı uçtan gelen teklife ters yönün bayrakları uygulanır ("Kodek çevirisi"). T.38 teklifine (`m=image … udptl t38`) dokunmaz ("Faks (T.38) ve tuşlar").
- **Taşıma ve ICE:** yeni teklif için `RTP/AVP`/`RTP/SAVP` ya da `ICE=remove` bayrağı verilmez; rtpengine her bacağın ilk görüşmesinde kararlaştırılan taşımayı korur ve ilk teklifte ICE'si çıkarılan (`ICE=remove`) çağrının yeni teklifindeki ICE satırlarını da çıkarır (ICE laboratuvarda çağrıyla denetlendi; SRTP'nin korunması aktarıcının denetim arayüzüne doğrudan gönderilen tekliflerle doğrulandı, SIP çağrısıyla sınanmadı).
- **Bekletme ve geri alma:** `a=sendonly`, `a=recvonly`, `a=inactive`, `a=sendrecv` olduğu gibi geçer; SIP çekirdeği bekletmeyi ayrıca yorumlamaz. T.38 ve oturum yenileme (RFC 4028) de olduğu gibi geçer (ADR-0027, ADR-0031; aşağıda, "Faks (T.38) ve tuşlar").
- **Ret:** yeni teklifi reddeden cevap (ör. 488) karşı tarafa iletilir ve çağrı sürer; retin ACK'i atlama atlamadır, SIP çekirdeğinin işlem katmanı karşılar. Ardından gelen yeni teklif yine geçer.

Senaryolar (`make test-routing`, "reinvite"; `test/sipp/uac-reinvite.xml`, sağlayıcı `uas-reinvite.xml`, `connlab.sh reoffer`/`reoffer_short`, `natlab.sh reoffer_caller`/`reoffer_trunk`; tohum 8722–8724, sağlayıcılar 172.30.0.67–.69):

- Tek çağrıda (8722, bağlantı yalnızca PCMA'yı geçirir): arayan çağrıyı bekletir (`sendonly`, ICE satırlarıyla; sağlayıcıya yalnızca PCMA ve tuş sinyali, ICE'siz ve aktarıcının adresiyle ulaşır, `recvonly` cevabı arayana döner), sonraki teklifini sağlayıcı 488 ile reddeder, arayan geri alır (`sendrecv`); sağlayıcı PCMU ve PCMA teklif ederek çağrıyı `inactive` yapar (arayana yalnızca PCMA gider), sonraki teklifini arayan 488 ile reddeder; çağrı sürer ve arayanın BYE'ıyla biter.
- En çok 5 saniyelik çağrı (8724) ikinci saniyede bekletilir; SIP çekirdeği yine cevaptan 5 saniye sonra kapatır (kayıt 5–6 saniye).
- Adres çevirisi arkasındaki arayana (`lab-nat-2`, 8723) sağlayıcının yeni teklifi, INVITE'ın geldiği adrese ulaşır; kayıtlı santrale (`lab-nat`, trunk'a teslim edilen 8988… çağrısı) arayanın yeni teklifi, REGISTER'ın geldiği adrese ulaşır. `route[hop]` etkisiz bırakılınca ikisi de başarısız olur (geliştirirken denetlendi).
- Her çağrı için motor yalnızca bir `answered` olayı alır, `failed` almaz; tek kayıt yazılır, süre konuşulan süredir ve durum deposunda (çağrı, eşzamanlı çağrı kümeleri, ayrılan tutar) hiçbir şey kalmaz.

Sınırlar:

- **Teklifsiz yeni INVITE** (teklif 200'de, cevap ACK'te) aşağıda, "Teklifsiz INVITE".
- Reddedilen bir teklif için aktarıcıya bir şey söylenmez; aktarıcı, teklifle değişen bilgiyi (ör. teklif sahibinin yeni ses adresi) tutabilir. Senaryolarda ses akmadığı için reddin sesle etkisi sınanmadı.
- TLS + SRTP bacaklı bir SIP çağrısında yeni teklif (yalnızca aktarıcıyla doğrudan denendi, yukarıda) ve aynı anda iki yönden gelen teklif (491, RFC 3261 §14.1) laboratuvarda sınanmadı; iletiler olduğu gibi geçer.

## Teklifsiz INVITE

ADR-0043 (TASKS: T-43). Arayüzde yeni bir şey yoktur. SDP taşımayan INVITE'ta teklifi karşı taraf 200'de yapar, cevap ACK'te gelir (RFC 3261 §13.2.1, §14.1; RFC 3264 §5–6). İlk INVITE'ta da çağrı içinde de teklif ve cevap medya aktarıcısından geçer; iki tarafın gördüğü SDP'de aktarıcının adresi vardır.

- **İlk INVITE:** SIP çekirdeği INVITE'ı SDP'siz iletir (`route[offer]` bir şey yapmaz) ve çağrının teklifsiz olduğunu dialogda saklar (`offerless`). Sağlayıcının SDP'li 200'ü (ve SDP'li geçici cevabı, 180/183) `onreply_route[media]` içinde `rtpengine_answer` yerine `rtpengine_offer` ile yazılır (`route[late_offer]`): `replace-origin replace-session-connection ICE=remove`, bağlantının kodek listesi ters yöndeki gibi (çevrilen çağrıda arayanın kodekleri, "Kodek çevirisi"), sağlayıcı NAT arkasındaysa (`Contact`'a göre) `SIP-source-address`. Arayanın ACK'indeki cevap `route[ack]` içinde `rtpengine_answer` ile yazılır (arayan NAT arkasındaysa `SIP-source-address`). Motor her zamanki `answered` olayını alır; ACK beklemesi (ADR-0026) değişmez.
- **Şifreleme:** teklif eden taraf kendi profilini seçer: TLS sağlayıcı şifreli (`RTP/SAVP`, anahtarlı) teklif ederse ona şifreli ve anahtarlı cevap gider. Switch'in teklif ettiği taraf olan arayana, arayanın sinyalleşmesi TLS ise `RTP/SAVP`, değilse `RTP/AVP` (anahtarsız) teklif edilir: sağlayıcı ayağındaki kuralın aynısı (ADR-0015). Aktarıcı aradaki şifrelemeyi çevirir.
- **Çağrı içinde:** teklifsiz yeni INVITE olduğu gibi iletilir; 200'deki teklif `onreply_route[reinvite]` içinde aktarıcıdan geçer, kodek bayrakları teklifin gittiği yöne göre ("Çağrı içi yeni teklif"), profil ve `ICE=remove` verilmeden (her ayak ilk görüşmedekini korur); ACK'teki cevap ilk INVITE'taki gibi. Hangi yön olduğu dialogda saklanır (`reoffer`): cevap rotası işlemin AVP'lerini görmez.
- **ACK cevap taşımazsa** (200'de teklif yapılmışken; protokol hatası, RFC 3261 §13.2.2.4'e göre arayan çağrıyı BYE ile bitirmeliydi): ACK iletilir, SIP çekirdeği dialog zamanlayıcısını bir saniyeye indirir; iki taraf BYE alır, motor `ended` alır. Kayıt 200 ve gerçek süreyle (1–2 saniye) yazılır, ücretlenir. Çağrı içi teklifsiz INVITE'ta da aynıdır.
- **Aktarıcı 200'deki teklifi almazsa** (ilk INVITE; ADR-0030'un karşılığı): 200 arayana SDP'siz gider (sağlayıcının adresi görünmez), iki taraf bir saniye sonra BYE alır, motora `answered` yerine `ended` 503 ve `cause: "media"` gider (`route[late_refused]`): kayıt 503, ücret yok, bağlantının canlı sayaçlarına yazılmaz, sıradaki rota denenmez; `opensips_media_refused` çağrı başına bir artar. Aktarıcı beklenirken (ilk fark edilişte ~5 sn) gelen 200 yeniden iletimleri de SDP'siz geçer. SDP'li geçici cevaptaki teklifi alamazsa o cevap arayana iletilmez. Çağrı içi teklifsiz INVITE'ta, SDP'li yeni teklifte olduğu gibi, cevap değiştirilmeden iletilir.

Senaryolar (`make test-routing`, "late" ve "nomedia"; `test/sipp/uac-late.xml`, `uac-relate.xml`, sağlayıcı `uas-late.xml` (`-rtp_echo`), TLS sağlayıcıda `uas-srtp.xml`'in teklifsiz dalı; `connlab.sh late`; tohum 8741–8744, sağlayıcılar 172.30.0.102–.104 ve .94):

- Teklifsiz ilk INVITE (8741, bağlantı yalnızca PCMA'yı geçirir; sağlayıcı PCMU, PCMA ve tuş sinyali teklif eder): arayana aktarıcının adresiyle, düz RTP, anahtarsız, yalnızca PCMA gider; sağlayıcıya ACK'teki cevap aktarıcının adresiyle ve PCMA ile gider. 3 saniyelik ses iki yönde aktarıcıdan geçer (aktarıcının çağrı sırasındaki `query` cevabında iki tarafın RTP'si, PCMA, 100'den çok paket).
- TLS sağlayıcı (172.30.0.24) teklifsiz INVITE'a şifreli ve anahtarlı teklif yapar: UDP'deki arayana düz RTP ve anahtarsız teklif gider, sağlayıcıya şifreli ve anahtarlı cevap gider (yanlışsa sağlayıcı BYE'a 488 verir, arayan senaryosu düşer).
- Çağrı içinde iki yönde teklifsiz INVITE (8742): arayanınkine sağlayıcının 200'deki teklifi (PCMU, PCMA) arayana aktarıcıdan ve yalnızca PCMA ile, sağlayıcınınkine arayanın 200'deki teklifi sağlayıcıya aktarıcıdan ulaşır; iki ACK'teki cevap da aktarıcının adresini taşır (cevapların `CSeq`'i denetlenir); ardından ses iki yönde geçer.
- Cevapsız ACK (8743): iki taraf da BYE alır; kayıt 200 ve 1–2 saniye.
- Aktarıcı durmuşken (8744, "nomedia" bölümünde): arayana SDP'siz 200 ve iki tarafa BYE; motor `ended` alır, `answered` ve `failed` almaz; kayıt 503, ücret yok, bağlantı canlı görünümde suçlanmaz, sayaç bir artar.
- Her çağrı için tek kayıt, durum deposunda bir şey kalmaz. Eski betikle dört senaryo da düşer (geliştirirken denetlendi).

Sınırlar:

- SDP'li geçici cevapta (183) yapılan teklif aktarıcıdan geçer, ama bu teklifin PRACK'te gelen cevabı işlenmez (T-42'nin sınırı: PRACK ve UPDATE'teki SDP); geçici cevaptaki teklif ve PRACK sınanmadı.
- Aktarıcı ilk 200'ü işlerken (yalnızca cevap vermeyi kestiğinde: ~5 sn) gelen 200 yeniden iletimi SDP'siz geçer; aktarıcı bu sürede geç de olsa cevap verirse arayan bu 200'ü cevapsız ACK'leyebilir ve çağrı kapatılır. `drop()` 2xx'i durdurmadığı için yeniden iletim tutulamıyor.
- Aktarıcı reddinde sağlayıcı çağrıyı cevaplamıştır; kısa süreyi faturalayabilir, switch'in kaydında alış tutarı yoktur.
- Sağlayıcının teklifinde bağlantının listesindeki kodeklerden hiçbiri yoksa arayana kodeksiz teklif gidebilir (sınanmadı); SDP'li ilk INVITE'taki gibi rota atlanmaz, çünkü teklif ancak cevaplandıktan sonra görülür. Çevrilen (transcode) bağlantıda teklifsiz INVITE sınanmadı.
- Çağrı içi teklifsiz INVITE'ta aktarıcı teklifi almazsa ve reddedilen teklifler için SDP'li yeni teklifteki sınırlar geçerlidir ("Çağrı içi yeni teklif").

## Faks (T.38) ve tuşlar (DTMF)

Arayüzde yeni bir şey yoktur (ADR-0031); SIP çekirdeğinin betiği de bu iş için değişmedi. Switch faksı ve tuşları yorumlamaz, birinden ötekine çevirmez.

- **T.38'e geçiş:** çağrı içindeki yeni teklif (`m=image <port> udptl t38` ve `a=T38…` satırları; ITU-T T.38 ek D, RFC 3264 §8) herhangi bir re-INVITE gibi medya aktarıcısından geçer ("Çağrı içi yeni teklif"): aktarıcı yalnızca adresi ve portu kendisininkiyle değiştirir, biçimi (`udptl t38`) ve özniteliklerini olduğu gibi bırakır, `a=sendrecv` ekler. Cevap da aynı yoldan döner; sese geri dönüş (`m=audio`) yeni bir teklifle olur. İki yönde de aynıdır.
- **Kodek listesi:** bağlantının ya da trunk'ın `codecs` ayarından gelen bayraklar (`codec-strip-all`, `codec-offer-…`) T.38 teklifine de verilir, ama aktarıcı bunları RTP olmayan akışa uygulamaz: faks teklifi listesi olan bağlantıda da düşmez. Aktarıcı bu durumda günlüğe "List of codecs empty. Restoring original list of codecs" uyarısını yazar (listesiz bağlantıda da); zararsızdır. Sese dönüş teklifine liste her zamanki gibi uygulanır.
- **Aktarıcı:** rtpengine T.38 akışını yalnızca aktarır; T.38 ↔ G.711 ağ geçidi (`T.38=decode`/`force` bayrakları, spandsp) kullanılmaz, ses-faks çevirisi yoktur. Ses ile faks arasında geçiş her zaman uçların kendi yeni teklifleriyle olur.
- **Tuşlar:** RFC 4733 `telephone-event` SDP'de teklif edildiği gibi kalır (kodek listesi onu çıkarmaz); SIP INFO (`application/dtmf-relay` ya da benzeri; RFC 6086) çağrı içi istek olarak gövdesi ve `Content-Type`'ı değişmeden iki yönde iletilir. İkisi arasında dönüşüm yapılmaz: yalnızca INFO bilen bir uçla yalnızca `telephone-event` bilen bir uç arasında tuşlar ulaşmaz.
- **Ölü çağrı:** UDPTL paketleri aktarıcı için medyadır: faks çağrısı, paket aktığı sürece `RTP_TIMEOUT` ile kapatılmaz; T.38'e geçip hiçbir taraftan paket gelmeyen çağrı da sesli çağrı gibi 60 saniyede kapatılır ("Ölü çağrı").
- Video için ayar yoktur (ADR-0031); `m=video` satırlı çağrı sınanmadı, kodek listesi olan bağlantıda video kodeklerine ne olacağı da.

Senaryolar (`make test-routing`, "fax"; `test/sipp/uac-fax.xml`, sağlayıcı `uas-fax.xml`, `connlab.sh fax`; tohum 8729 ve 8730, sağlayıcılar 172.30.0.77 ve .78; faks paketleri `run.sh`'nin yazdığı 72 saniyelik UDPTL yakalama dosyasından). İki çağrı aynı anda koşar, bölüm yaklaşık 75 saniye sürer:

- Tek çağrıda (8729, bağlantı yalnızca PCMA'yı geçirir): arayan T.38'e geçer (sağlayıcıya `m=image … udptl t38`, öznitelikleri tam, ses satırı ve kodek satırı olmadan, aktarıcının adresiyle ulaşır; cevap arayana aynı biçimde döner); arayan INFO ile bir tuş gönderir, sağlayıcı bir tuşla karşılık verir (ikisi de değişmeden ulaşır, cevapların `CSeq`'i denetlenir); sağlayıcı PCMU ve PCMA teklif ederek sese döner (arayana yalnızca PCMA ve tuş sinyali gider), sonra kendisi T.38'e geçer, arayan da sese döner. Çağrı sürer ve arayanın BYE'ıyla biter.
- Cevaptan yarım saniye sonra T.38'e geçen ve ardından 70 saniye boyunca yalnızca arayandan UDPTL paketi akan (hiç ses akmayan) çağrı (8730) aktarıcının zaman aşımıyla kapatılmaz; arayan kendisi kapatır (kayıt 70–73 saniye). Geliştirirken karşı deneme yapıldı: aynı çağrı paket gönderilmeden 60. saniyede iki taraftan kapatıldı.
- Motor her çağrı için yalnızca bir `answered` olayı alır, `failed` almaz; tek kayıt, durum deposunda bir şey kalmaz.

Sınırlar:

- Yalnızca T.38 teklif eden **ilk** INVITE (`m=image` tek satır), kodek listesi olan bir bağlantıya gönderilemez: listedeki kodeklerden hiçbiri teklifte olmadığı için rota atlanır (488; "Bağlantı ve trunk ayarları"). Listesiz bağlantıda geçer. Sınanmadı; faksa başlangıçta geçen uçlar seyrektir.
- SRTP bacaklı (TLS sağlayıcı) çağrıda T.38'e geçiş ve RTP ile T.38'i aynı anda teklif eden iki satırlı SDP sınanmadı.
- Faks paketlerinin içeriği ve gerçek bir faks makinesiyle aktarım denenmedi; senaryo yalnızca paketlerin akışını ve SDP'nin değişmeden geçmesini kanıtlar.

## Erken medya, PRACK ve aktarma (REFER)

Arayüzde yeni bir şey yoktur (ADR-0028). Erken medya ve güvenilir geçici cevaplar (100rel/PRACK) olduğu gibi geçer; çağrı içindeki aktarma isteği reddedilir.

- **Erken medya (RFC 3960):** sağlayıcının SDP'li 183'ü (ya da 180'i) `onreply_route[media]` içinde her cevap gibi medya aktarıcısından geçer (`route[answer]`: `rtpengine_answer`); arayana giden SDP aktarıcının adresini taşır, sağlayıcınınkini değil. Çağrı cevaplanınca 200'deki SDP aynı yoldan yeniden yazılır. Motor her denemenin ilk 180/183'ünü `ringing` olarak alır; SDP'li olup olmaması bir şey değiştirmez. Denemenin hangi dalda bildirildiği dialogda tutulur (`$dlg_val(rung)`, dalın sırası `$T_branch_idx`; cevap rotası işlemin AVP'lerini görmez): aynı dalın sonraki 18x'leri bildirilmez, yeni deneme yeni ve daha büyük sıralı bir dal olduğundan yeniden bildirir, bırakılmış bir denemenin geç gelen 18x'i hiçbir şey bildirmez. Aynı denemenin iki 18x'i iki işçide aynı anda işlenirse ikisi de gidebilir; motor ilkini tutar (her olay birden çok gelebilir). Kimlik bilgisiyle yeniden gönderilen deneme (401/407) yeni bir daldır; bildirimi motor yine ilk ilerlemeye göre tutar (T-46). Senaryo: `make test-routing`, "progress" (`run.sh` 7q2; `uas-progress.xml`, `connlab.sh progress`; tohum 8745, sağlayıcılar 172.30.0.105 ve .106): ilk sağlayıcı 180, 183, 180 ve 503, ikincisi 180, 183 ve 200 gönderir; motor iki `ringing`, bir `failed`, bir `answered` alır (düzeltmeden önceki betikle her 18x ayrı bildirildi: aynı çağrıda altı `ringing` sayıldı).
- **100rel ve PRACK (RFC 3262):** `Supported: 100rel`, 183'teki `Require: 100rel` ve `RSeq`, PRACK'teki `RAck` değişmeden geçer; SIP çekirdeği güvenilir cevabı kendisi onaylamaz, yeniden iletmez. PRACK erken dialogdaki bir çağrı içi istektir: `topology_hiding_match` ile dialoga bağlanır ve sağlayıcıya iletilir, cevabı (200) arayana döner. PRACK'te ya da cevabında SDP olursa (erken teklif/cevap) betik ona dokunmaz; bu akış sınanmadı (aşağıda, sınırlar).
- **Aktarma (REFER, RFC 3515):** çağrı içindeki REFER, hangi taraftan gelirse gelsin, SIP çekirdeğinde `403 Forbidden` alır ve karşı tarafa iletilmez; çağrı sürer. Class 4 trafiğinde aktarma beklenmez ve aktarma, kimsenin yetkilendirmediği yeni bir çağrı olurdu. Bu sabit bir kuraldır, karar değildir: motor sorulmaz, olay gönderilmez. Her ret SIP çekirdeğinin `opensips_refer_refused` sayacını artırır (`docs/10-izleme.md`; alarm `TransfersRefused`). Çağrı dışı REFER, çağrı dışı her yöntem gibi 405 alır (ARCHITECTURE §5.6).

Senaryolar (`make test-routing`, "early"; `test/sipp/uac-early.xml`, sağlayıcı `uas-early.xml`, `connlab.sh early`; tohum 8731 ve 8732, sağlayıcılar 172.30.0.79 ve .85). İki çağrı aynı anda koşar:

- 8731: arayan `Supported: 100rel` ile arar; sağlayıcının `Require: 100rel`, `RSeq: 1` ve kendi adresli SDP'siyle gönderdiği 183 arayana ulaşır, SDP'de aktarıcının adresi vardır (sağlayıcının adresi yoktur). Arayanın PRACK'i sağlayıcıya `RAck: 1 1 INVITE` ile, PRACK'in 200'ü arayana `CSeq: 2 PRACK` ile ulaşır. Cevaptan sonra önce arayan, sonra sağlayıcı REFER gönderir: ikisi de 403 alır (`CSeq` denetlenir), hiçbiri karşı tarafa ulaşmaz (ulaşırsa karşı taraf senaryosu beklenmeyen istek diye düşer), çağrı sürer ve arayanın BYE'ıyla biter. SIP çekirdeğinin sayacı iki artar.
- 8732: `Require: 100rel` taşımayan, SDP'li 183; arayana aktarıcının adresiyle ulaşır, arayan bir şey onaylamaz.
- Motor iki `ringing` ve iki `answered` alır, `failed` almaz; her çağrı tek kayıt (200, konuşulan süre), durum deposunda bir şey kalmaz.
- Geliştirirken karşı deneme yapıldı: REFER'i reddetmeyen eski betikle 8731 başarısız olur (REFER karşı tarafa ulaşır).

Sınırlar:

- **Sessiz erken medya ve uzun çalma (KARAR, ADR-0038).** SDP'li 183, aktarıcı oturumunun iki tarafını da bildirir; bu andan sonra hiçbir taraftan paket gelmezse aktarıcının zaman aşımı (`RTP_TIMEOUT`, 60 sn) çağrı çalarken de işler: aktarıcı `teardown` der, SIP çekirdeği henüz doğrulanmamış dialogu bitirir (sağlayıcıya CANCEL), sağlayıcının 487'si arayana gider ve motor `failed 487` alır (sıradaki rotaya geçilmez); kayıt 487. Bağlantının `ring_timeout_sec` değeri 60'tan büyükse (en çok 180) bu, sessiz erken medyalı çağrının çalma süresini 60 saniyeye indirir. Laboratuvarda sınandı (8733, `ring_timeout_sec` 90, sağlayıcı 172.30.0.86; `connlab.sh early_long <çağrı kimliği öneki> 70000`: 183'ten 60 saniye sonra CANCEL, arayana 487). SDP'siz 180 ile çalan çağrı etkilenmez (aynı bağlantıda 90. saniyede 408; aktarıcı yalnızca teklifi bildiği için `offer-timeout`'a kalır). Erken medyası gerçekten akan (ör. sağlayıcının çaldırma sesi) çağrı da etkilenmez. Karar (ADR-0038, 2026-11-23): böyle kalır; ADR-0027 geçerlidir, sessiz erken medyalı çağrının çalması en çok `RTP_TIMEOUT` kadardır. Panelde bağlantının "Çalma süresi sınırı" alanının açıklaması bunu söyler (`docs/07-panel.md`). Uzun çalması gereken bir sağlayıcı erken medyada ses (çaldırma sesi) göndermelidir; `RTP_TIMEOUT`'u büyütmek ölü çağrının kapanmasını da geciktirir. Senaryo `make test-routing`, "deadcall" içindedir (aşağıda değil, "Ölü çağrı" bölümünün senaryolarında: aynı zaman aşımıyla birlikte koşar).
- PRACK'te ya da 200'ünde SDP (RFC 3262 §5, erken teklif/cevap), UPDATE ile erken dialogda yeni teklif (RFC 3311), birden çok güvenilir geçici cevap (artan `RSeq`) ve birden çok erken dialog (çatallanma) sınanmadı. Betik PRACK ve UPDATE'teki SDP'yi aktarıcıdan geçirmez: böyle bir teklifte tarafın gerçek ses adresi karşıya gider.
- Sağlayıcı 100rel isterken arayan desteklemiyorsa (`Supported: 100rel` yoksa) switch araya girmez; RFC 3262 §4'e göre sağlayıcının 183'ü güvenilir göndermemesi gerekir. Sınanmadı.
- Aktarmayı karşı tarafa bildirmeden kendi yapan uçlar (yeni INVITE ile, ya da `Replaces`, RFC 3891) ayrı çağrılar olarak görülür ve her biri ayrı yetkilendirilir.

## Medya aktarıcısı teklifi almazsa

ADR-0030 (S-5'i kapattı), ADR-0039. Her çağrının sesi aktarıcıdan geçer (topoloji gizleme, NAT, SRTP); aktarıcı teklifi değiştiremezse çağrı kurulmaz. Var olan `ended` olayı kullanılır; isteğe bağlı `cause: "media"` alanı denemenin hiç gönderilmediğini söyler (sürüm 1).

- **Ne zaman:** sağlayıcıya gidecek her denemenin teklifi `branch_route[out]` içinde aktarıcıya verilir (`route[offer]`: `rtpengine_offer`). Aktarıcı cevap vermezse (durmuş, ulaşılamıyor; modülün varsayılanıyla 1 saniye beklenir, 5 kez denenir) ya da hata ile cevap verirse (portu kalmadı: "Ran out of ports"; başka her hata da) `route[offer]` yanlış döner ve deneme gönderilmez (`drop`).
- **Ne olur:** `route[media_refused]` sayacı artırır (`opensips_media_refused`), motora `ended` olayını `sip_code` 503 ve `cause: "media"` ile bildirir ve arayana `503 Service Unavailable` gider. `failed` gönderilmez: sorun sağlayıcıda değil switch'te olduğu için sıradaki rota denenmez; bağlantının `failover_mode` ayarı `always` olsa da. Motor çağrıyı bırakır (sayaçlar, ayrılan tutarın tamamı), denemeyi 503 ile kaydeder (cevap yok, ücret yok). Kayıt, motorun seçtiği bağlantıyı taşır (hangi rotanın seçildiği görünsün diye), ama deneme o bağlantıya yazılmaz: canlı görünümde bağlantının `attempts`, `failed_network`, `code_503` sayaçları ve NER'i değişmez, `OnLeg` denemeyi `NotSent` ile bildirir (Enterprise'ın kalite tabanlı yönlendirmesi saymaz). Aynı kural ikinci ve sonraki denemelerde de geçerlidir (`failure_route[next]`, sağlayıcının 401/407 sorusundan sonraki yeniden gönderim dahil): o an aktarıcı teklifi almazsa çağrı 503 ile biter, önceki denemelerin kaydı olduğu gibi kalır (bu yol sınanmadı: senaryoda aktarıcı ilk denemeden önce durur).
- **Aktarıcıyı kenara koyma:** cevap vermeyen aktarıcıyı modül devre dışı bırakır; bu sırada teklifler beklemeden reddedilir. Modül aktarıcıyı arka planda her 10 saniyede yeniden yoklar (`rtpengine_disable_tout` 10; varsayılan 60) ve cevap verince çağrılar kendiliğinden yeniden kurulur. Laboratuvarda aktarıcı yeniden başladıktan yaklaşık 11 saniye sonra çağrılar geçti.
- **Görünürlük:** `opensips_media_refused` ve alarmlar `CallsRefusedNoMedia` (kritik, her ret), `MediaPortsLow` (aktarıcının portlarının %10'undan azı boş; uyarı). Durmuş aktarıcıyı ayrıca `ServiceDown` (rtpengine) gösterir (`docs/10-izleme.md`).

Senaryolar (`make test-routing`, "nomedia"; `run.sh` 7r; tohum 8734: ulaşılmayan sağlayıcı 172.30.0.87, `failover_mode` `always`, yedek C). Aktarıcı bütün çağrıların ortak parçası olduğu için bölüm tek başına koşar; ne olursa olsun aktarıcı portlarını geri alır, yeniden başlatılır ve bir çağrı içinden geçene kadar beklenir (`relay_back`, `trap`).

- Port kalmadı: aktarıcının kabında başka bir süreç aralığın (30000–39999) bütün portlarını aktarıcının adresinde tutar (en çok bir dakika); aktarıcı "Ran out of ports" der, arayan 503 alır.
- Aktarıcı durduruldu (`docker compose stop`): ilk arayan 503'ü aktarıcının beklenmesinden sonra (~5 saniye) alır, sonraki hemen alır.
- Üç çağrının her biri tek kayıt (bağlantı 8734'ün, 503), sağlayıcıya hiçbir şey gitmez, motor üç `ended` alır ve hiç `failed` almaz, ücret yok, durum deposunda bir şey kalmaz, sayaç üç artar. Ardından aktarıcı hâlâ durmuşken teklifsiz bir INVITE (8744): sağlayıcı cevaplar, arayana SDP'siz 200 ve iki tarafa BYE gider; motor dördüncü `ended`'i alır, `answered` almaz; kayıt 503, ücretsiz; sayaç dört artmış olur (çağrı başına bir). Motorun canlı görünümünde 8734'ün bağlantısı deneme, hata ve 503 saymaz (`cause: "media"`). Aktarıcı geri gelince çağrılar yeniden geçer.

Sınırlar:

- **Raporlar bağlantıyı suçlar.** Kayıtta "gönderilmedi" bilgisi yoktur (ADR-0039 kayıt alanı eklemedi): geçmiş raporlar bu denemeleri seçilen bağlantının 503'ü olarak sayar; motorun canlı görünümü ve `OnLeg` saymaz. Yeni motor eski SIP çekirdeğiyle (alanı göndermeyen) çalışırken de bağlantı suçlanır; ikisi de güncel olunca kalkar.
- **İlk fark edilişte bekleme.** Aktarıcı cevap vermeyi kestiği anda teklif yapan her SIP çekirdeği işçisi ~5 saniye bekler (modülün varsayılanı: 1 saniye × 5); bu sırada o işçi başka istek işleyemez. Süreler bilinçli olarak varsayılanda bırakıldı: kısaltmak, yük altında yavaş cevap veren aktarıcıyı da kenara koydurup bütün çağrıları reddettirebilir.
- **Teklifsiz INVITE.** Teklif sağlayıcının 200'ündeyse (ADR-0043) 200'den sonra 503 verilemez: 200 SDP'siz iletilir, çağrı bir saniye sonra iki taraftan kapatılır, motora `ended` 503 ve `cause: "media"` gider; yukarıda, "Teklifsiz INVITE". Senaryoda aktarıcı durmuşken bir çağrı (8744, sağlayıcı 172.30.0.94).
- **Kurulmuş çağrı.** ADR-0030 yalnızca yeni çağrıyı kapsar. Çağrı içi yeni teklifte (re-INVITE) ya da bir cevapta (180/183/200, `route[answer]`) aktarıcı cevap vermezse bugünkü davranış sürer: SDP değiştirilmeden iletilir, ses taraflar arasında doğrudan kalabilir (topoloji gizleme sesi kapsamaz) ya da hiç akmaz; çağrı kapatılmaz, sayılmaz. Sınanmadı.
- Aktarıcı biten çağrının portlarını hemen geri verir (`delete-delay = 0`); varsayılan 30 saniyelik bekletmeyle laboratuvarın 10.000 portu 200 CPS'te tükeniyordu. Tek adresin portları (~2.500 eşzamanlı çağrı bu aralıkta) hedef kapasiteye yetmez: `docs/09-kurulum.md`, medya havuzu (Enterprise).
- Gerçek port tükenmesi (aktarıcının kendi oturumlarıyla dolması) yerine portlar dışarıdan tutularak sınandı; aktarıcının `rtpengine_ports_free` ölçüsü dışarıdan tutulan portları görmez (bu durumda `MediaPortsLow` uyarmaz, `CallsRefusedNoMedia` uyarır).
