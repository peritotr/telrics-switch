# Yönetim API'si

Arayüz bu API üzerine kurulacak. Çalıştırma: `BOOTSTRAP_API_KEY=<anahtar> make api-run` (127.0.0.1:58080). İlk çalıştırmada kök hesap ve bu anahtar oluşturulur.

Bu belge Community API'sini anlatır. Bayi, DID ve fatura uçları Enterprise sürümündedir ve orada belgelenir; Enterprise ikilisi aşağıdakilerin tamamını da sunar.

## Kurallar

- Kişiler e-posta ve parolayla girer: `POST /v1/login` bir oturum belirteci verir (`tss_…`, 12 saat geçerli) ve bu belirteç anahtar gibi `Authorization: Bearer` ile gönderilir. Programlar API anahtarı kullanır. İkisinde de haklar rolden gelir (`admin` ya da `readonly`).
- Kimlik doğrulama: `Authorization: Bearer <anahtar>`. Her anahtar bir hesaba aittir ve yalnızca o hesabın kayıtlarını görür. Başkasına ait kayıt `404` döner. Community'de tek hesap (kök) vardır.
- Tüm tutarlar **tamsayı mikro birimdir**: `1.000000` para birimi = `1000000`. Ondalık yalnızca CSV tarife dosyasında kabul edilir.
- Bakiyeler doğrudan değiştirilemez; yalnızca ödeme ve çağrı işlemleriyle değişir.
- Müşteri bakiyesi kullanılabilir tutardır. Sağlayıcı bakiyesi sağlayıcıya olan borçtur.
- Hatalar: `{"error": "..."}`; bir programın ayırt etmesi gereken birkaç retle ayrıca sabit bir `code` (şimdilik `two_step_required`, `own_two_step_off`; "İki adımlı doğrulama"). `400` geçersiz istek, `401` anahtar yok/geçersiz, `403` yetki yok, `404` bulunamadı, `409` çakışma, `422` doğrulama hatası.
- Listeleme: `?limit=` (en çok 1000), `?offset=` ve alan adıyla eşitlik filtresi (ör. `?status=active`). Ayrıca: `?q=` ad/e-posta/vergi no içinde arama (müşteri, sağlayıcı, tarife, plan); `rates.prefix`, `cdrs.dst` ve `cdrs.src` "ile başlayan" eşleşir; `cdrs` için `?from=`/`?to=` (RFC 3339); `rates` için `?validity=current|future|expired`. Eşleşen kayıt sayısı `X-Total-Count` başlığında döner (`cdrs` hariç; büyük tabloda sayım yapılmaz).
- Anahtarlar `tsw_live_` ile başlar ve 32 harf/rakam taşır. `BOOTSTRAP_API_KEY` de bu biçimde olmalıdır (`tsw_live_` ya da `tsw_test_` + en az 16 harf/rakam); panel başka biçimi kabul etmez.

## Kaynaklar (GET liste, GET tekil, POST, PATCH, DELETE)

| Yol | İçerik |
|---|---|
| `/v1/resellers` | Salt okunur; kök hesap |
| `/v1/clients` | Müşteriler: faturalama türü, kredi limiti, tarife, yönlendirme planı, limitler |
| `/v1/client-trunks` | Müşteri bağlantıları: kaynak adresler ya da kullanıcı adı, teknik önek; `security` (`any`, `tls`, `cert`) ve `cert` için `tls_cert_name`. `addresses`, adresle tanınan trunk'ın (`auth_type: "ip"`) kaynaklarının listesidir: tek adresler ve ağlar (CIDR), IPv4 ve IPv6, ör. `["203.0.113.10", "198.51.100.0/24", "2001:db8:12::/48"]`. En az bir öğe gerekir; liste her yazmada bütünüyle değişir. Sunucu listeyi düzenli saklar ve öyle döndürür: sıralı, yinelenmeyen, ağ uzunluğundan sonraki bitleri sıfırlanmış (`10.0.0.9/24` → `10.0.0.0/24`). Okunamayan adres, boş liste ve IPv4'te `/8`'den, IPv6'da `/16`'dan geniş ağ `422`; aynı adres ya da ağ aynı teknik önekle açık başka bir trunk'ta varsa `409` (kapalı trunk adres tutmaz; açılırken denetlenir). Kullanıcı adıyla tanınan trunk'ta liste kullanılmaz. Eski tekil `ip` alanı kaldırıldı (şema 0017 var olan adresi listeye taşır). `max_contacts` (1–10, varsayılan 1): kullanıcı adıyla tanınan trunk'ın aynı anda kayıtlı olabileceği adres sayısı. Ayrıntılı ayarlar (isteğe bağlı; `docs/08-cagri-arayuzu.md`, "Bağlantı ve trunk ayarları"): `number_rules`: müşterinin numara yazımı, kural listesi, ör. `[{"strip": "00", "add": ""}, {"strip": "+", "add": ""}, {"strip": "0", "add": "90"}]`; numaraya en uzun eşleşen `strip` uygulanır. `strip` en çok 8 rakam (başında `+` olabilir), `add` en çok 8 rakam; ikisi birden boş olamaz, aynı `strip` iki kez olamaz, en çok 20 kural; liste her yazmada bütünüyle değişir ve en uzun `strip` önde döner; aykırısı `422`. Eski `number_strip` ve `number_add` alanları kalır (şema 0025 var olan değerleri tek kurallı listeye taşıdı): yalnız onları yazan istemci listeyi tek kurala çevirir (ikisi boşsa liste boşalır); okurken liste tek kurallıysa o kuralı, değilse boş gösterir. Birden çok kurallı trunk'ı eski alanlarla boşaltmak için `number_rules: []` yazılmalı (iki boş değer değişiklik sayılmaz). İkisi aynı istekte gelirse liste geçerlidir; `max_duration_sec` (0 ya da 5–7200); `max_calls`, `max_cps` (0 = yok); `codecs` (bağlantıdaki listeyle aynı adlar); `transcode` (bool, varsayılan `false`: ortak kodek yoksa sesin çevrilmesine izin; `docs/08`, "Kodek çevirisi") |
| `/v1/vendors` | Sağlayıcılar |
| `/v1/vendor-connections` | Sağlayıcı SIP bağlantıları: adres, önek ekleme/silme, limitler; `tls` taşımasında `tls_name` (sertifikanın düzenlenmiş olması gereken ad; boş = sunucu). Ayrıntılı ayarlar (hepsi isteğe bağlı; varsayılanları eski davranıştır, anlamları `docs/08-cagri-arayuzu.md`, "Bağlantı ve trunk ayarları"): `number_plus` (bool), `cli_strip_digits` (0–32), `cli_add_prefix` (rakam, başında `+` olabilir), `pai_mode` (`pass` / `remove` / `caller`), `ring_timeout_sec` (0 ya da 5–180), `max_duration_sec` (0 ya da 5–7200), `failover_mode` (`standard` / `always` / `never` / `custom`), `failover_codes` (300–699 arası kodlar), `options_interval_sec` (0 ya da 10–3600), `codecs` (sıralı liste: `PCMA`, `PCMU`, `G729`, `G722`, `opus`, `GSM`, `iLBC`, `G723`, `AMR`, `AMR-WB`, `speex`), `transcode` (bool, varsayılan `false`: arayanın trunk'ıyla ortak kodek yoksa ses çevrilerek bu bağlantıya gönderilir; `docs/08`, "Kodek çevirisi"), `register` (bool, varsayılan `false`: switch sağlayıcıya `auth_username` ve giriş parolasıyla kaydolur; `docs/08`, "Sağlayıcıya kayıt"), `register_expires_sec` (60–86400, varsayılan 3600: istenen kayıt süresi), `register_domain` (kayıt adresinin alanı, harf, rakam, `.` ve `-`; boş = `host`). Sınır dışı değer `422`. Salt okunur: `auth_username` ve `auth_set` (parola var mı); ikisi aşağıdaki `sign-in` işlemiyle yazılır, parolanın kendisi (`auth_secret`) hiçbir cevapta yer almaz |
| `/v1/tariffs`, `/v1/rates` | Satış/alış tarifeleri ve fiyatları. Tarifede `timezone` (IANA adı); fiyatta isteğe bağlı gün ve saat penceresi: `days`, `time_from`, `time_to` (`docs/08-cagri-arayuzu.md`, "Güne ve saate göre fiyat ve rota"). Aynı önek, başlangıç ve pencere için tek fiyat olur |
| `/v1/holidays` | Hesabın resmî tatilleri (T-19, ADR-0044): `day` (`YYYY-AA-GG`; hesapta bir kez), `counts_as` (tatilin sayıldığı gün, pazartesi 1 … pazar 7; varsayılan 7), `name`. Gün kümesi sınırlı fiyat ve rota pencereleri o tarihi bu gün sayar; tarih tarifenin ya da planın saat dilimiyle okunur. Liste tarihe göre sıralı, `from`/`to` (RFC 3339) ile süzülür. Genel CRUD: denetim kaydı ve motorun görüntüyü yenilemesi kendiliğinden |
| `/v1/routing-plans`, `/v1/routing-entries` | Yönlendirme planları ve önek → bağlantı satırları. Satırda `priority` (küçük olan önce) ve `weight` (0–1.000.000; aynı basamaktaki rotalar arasındaki pay, 0 = yalnızca yedek; satırda isteğe bağlı `days`, `time_from`, `time_to` penceresi, planda `timezone`; `docs/08-cagri-arayuzu.md`, "Rota sırası ve ağırlıklı dağıtım") |
| `/v1/firewall-rules` | Güvenlik duvarı kuralları: `cidr` (adres ya da ağ), `action` (`deny`, `allow`), `note`, `enabled`, `expires_at`. Yalnızca sistemi işleten hesabınkiler uygulanır (`docs/08-cagri-arayuzu.md`, "Güvenlik duvarı") |
| `/v1/report-schedules` | Zamanlanmış raporlar: `name`, `period` (`daily`, `weekly`, `monthly`), `group_by` (özet raporundaki kırılımlar), `recipients` (virgülle ayrılmış adresler), `timezone`, `send_hour` (0–23), `enabled`; `sent_through` son gönderilen dönemin sonudur (salt okunur) |
| `/v1/exchange-rates` | Kurlar: `currency` (üç büyük harf), `rate` (1 birimin hesabın para birimindeki karşılığı, mikro birim). Salt okunur: `source` (`manual` elle girilmiş, `ecb` Avrupa Merkez Bankası'nı izliyor; bu alan genel uçlardan yazılamaz, `400`) ve `published` (izlenen kurun yayın günü). İzlenen bir kura `PATCH` onu elle girilmiş yapar (değer aynı olsa bile). Ayrıntı: `docs/08-cagri-arayuzu.md`, "Para birimleri" ve "Kurların dış kaynaktan alınması" |
| `/v1/cli-rules` | Arayan numara kuralları: `client_id` (boş = tüm müşteriler), `number_source` (`from`, varsayılan; `pai`: `P-Asserted-Identity`'deki numara), `prefix`, `action` (`block`, `allow`, `rewrite`, `anonymous`: numarasız arayanı 433 ile reddet; önek ve dönüşüm alanı boş olmalı), `rewrite` için (yalnızca `from`) `strip_digits` ve `add_prefix`, `note`, `enabled`. Aynı kapsamda bir numara ve önek için tek kural olur; `anonymous` boş önekli kuralla yan yana durabilir. Ayrıntı: `docs/08-cagri-arayuzu.md`, "Arayan numara kuralları" |
| `/v1/bundles` | Paket dakikalar: `client_id`, `name`, `prefixes` (hedef önekleri dizisi; boş önek bütün numaralar), `seconds` (takvim ayı başına faturalanan saniye), `enabled`. `?client_id=` ile süzülür. Ayrıntı: `docs/08-cagri-arayuzu.md`, "Paket dakikalar" |
| `/v1/cdrs` | Salt okunur liste. `bundle_sec` paketten karşılanan saniye, `bundle_id` paketi (paket silindiyse boş). `trunk_id` arayanın trunk'ı; `dst_trunk_id` çağrının teslim edildiği müşteri trunk'ı (sağlayıcıya giden bacakta ve alandan önceki kayıtlarda boş; `connection_id` ve `vendor_id` o bacakta boştur; trunk sonradan silinse de kayıt kimliği saklar). Süzgeç olarak da kullanılır (`?dst_trunk_id=`). PostgreSQL'den arşivlenmiş kayıtlar (`CDR_KEEP_DAYS`) API'de `CLICKHOUSE_URL` varsa yine görünür: `GET /v1/cdrs/{id}` PostgreSQL'de bulamadığı kaydı ClickHouse'tan verir; liste arşiv ufkunun (`cdr_archive.horizon`) öncesine uzanırsa ufuktan sonrasını PostgreSQL'den, öncesini ClickHouse'tan aynı sıra ve sayfalamayla verir. Satırın biçimi aynıdır; yalnızca ClickHouse'ta olmayan uzantı sütunları (Enterprise: `did_id`, fatura kimlikleri) arşivlenen satırda `null` gelir ve böyle bir süzgeç arşivden satır döndürmez. ClickHouse okunamazsa PostgreSQL'deki kayıtlar döner |
| `/v1/audit-log` | Denetim kaydı; salt okunur, en yenisi önce (aşağıda "Denetim kaydı") |

## OpenAPI tanımı

`GET /v1/openapi.json` (kimlik doğrulamalı) API'nin OpenAPI 3.0 tanımını verir. Tanım elle yazılmaz: çalışan sunucu onu kayıtlı yollarından ve kaynak tablosundan üretir, alan türlerini veritabanından okur. Bu yüzden koddan sapamaz ve başka sürümlerin eklediği kaynak ve uçlar da kendiliğinden içindedir. Genel kaynak uçları (liste, tekil, oluştur, değiştir, sil) süzgeçleri ve şemalarıyla tam tanımlıdır. Özel işlemlerin (ödeme, CSV yükleme, simülatör, raporlar…) özeti, sorgu parametreleri, istek gövdesi ve her cevap kodunun gövdesi de tanımdadır (T-21): şemalar, işleyicinin okuduğu ve yazdığı Go türlerinden yansıtmayla (reflection) üretilir. Her ret aynı biçimdedir (`Error`: `{"error": "...", "code"?}`, `4XX`).

```bash
curl -H "Authorization: Bearer $KEY" http://127.0.0.1:58080/v1/openapi.json > openapi.json
```

### Yeni bir ucun tanımı

Uç `s.Handle` ya da `s.HandlePublic` ile kaydedilir (doğrudan `s.mux` ile kaydedilen tanıma girmez) ve ne aldığını, ne verdiğini kayıt satırında söyler; seçenekler çalışmayı değiştirmez, yalnızca tanımı besler (`pkg/api/schema.go`):

```go
s.Handle("POST /v1/firewall/unban", true, s.firewallUnban,
	api.Summary("Lift such a ban before its time is up."),
	api.Takes(unbanRequest{}), api.ReturnsNothing())
```

| Seçenek | Anlamı |
|---|---|
| `Summary(metin)` | Tek satırlık özet (zorunlu) |
| `Takes(v)` | JSON istek gövdesi; `v` işleyicinin çözdüğü türün sıfır değeri |
| `TakesText(tür, açıklama)` | JSON olmayan istek gövdesi (ör. `text/csv`) |
| `TakesNothing()` | Gövdesiz `POST`/`PUT`/`PATCH` |
| `Returns(kod, v...)` | JSON cevap; birden çok değer "bunlardan biri" (`oneOf`); birden çok kodla cevap veren uç her kod için bir kez yazar |
| `ReturnsNothing()` | 204, gövdesiz |
| `ReturnsText(tür, açıklama)` | JSON olmayan cevap (CSV, pcap) |
| `ReturnsRow(kod, kaynak)` / `ReturnsRows(kaynak)` | Bir kaynağın satırı ya da listesi (kaynağın şemasıyla) |
| `ReturnsTableRows(tablo, ek)` | Bir tablonun bütün sütunları, `ek` türünün alanları eklenmiş satırlar |
| `Query(ad, v, zorunlu, açıklama)` | Sorgu parametresi |

Türden şema kuralları: `json` etiketindeki ad; cevapta `omitempty`/`omitzero` olmayan alan her zaman vardır (`required`), istekte hiçbir alan zorunlu sayılmaz, işleyicinin reddettiği eksik alan `openapi:"required"` etiketiyle işaretlenir; işaretçi `nullable`; `int64` (para dahil) `integer`/`int64`; `time.Time` `date-time`; `json.RawMessage` herhangi bir değer, ama bir kaynağın satırını olduğu gibi geçiren alan `openapi:"ref:<kaynak>"` ile o kaynağın şemasını alır; `json.Number` `number`; gömülü yapının alanları üstteki yapıya katılır. Adlı yapılar cevaplarda `components/schemas` altında tür adıyla (ilk harfi büyük) paylaşılır; iki pakette aynı ad varsa paket adı öne eklenir. İstek gövdeleri bileşen olmadan yerinde yazılır.

Denetim: `go test ./pkg/api` içindeki `TestEveryOperationDescribed` genel olmayan her uçta özet, cevap ve (`POST`/`PUT`/`PATCH` için) istek tanımı ister; tanımsız yeni uç testi düşürür. `make test-api` her özel işlemin gerçek isteğini ve cevabını (retler dahil) tanımdaki şemayla karşılaştırır: tanımda olmayan alan, tanımda zorunlu olup gelmeyen alan ya da yanlış tür testi düşürür. Motorun cevabını geçiren uçların türleri (`engineviews.go`) motorunkilerle `TestEngineViewsMatch` içinde karşılaştırılır. `make test-openapi` belgeyi test veritabanına karşı üretir ve Redocly ile denetler (`redocly.yaml`: önerilen kuralların hepsi hata düzeyinde; kapatılan kural yok).

Enterprise ve başka uzantılar aynı seçenekleri kendi kayıtlarında kullanır; seçeneksiz kaydedilmiş bir uç tanımda yalnızca yolu ve "200 OK" ile görünür.

## İşlemler

| İstek | Açıklama |
|---|---|
| `GET /v1/me` | Çağıranın hesap kaydı |
| `GET /v1/session` | `{edition, key: {id, name, role}, account}`; kullanıcı oturumunda ayrıca `user: {id, name, email, role, two_step, two_step_setup_required}`. Panel oturumu bununla açar; `two_step_setup_required` doğruysa yalnızca kurulum ekranını gösterir. `account.require_two_step` hesabın ayarıdır |
| `POST /v1/login` | `{"email", "password"}` → `{"token", "expires_at"}`. Hatalı giriş `401`; art arda beş hatalı denemeden sonra hesap 15 dakika kilitlenir (`429`). Kullanıcıda iki adımlı doğrulama açıksa belirteç yerine `{"two_step": true, "challenge", "expires_at"}` döner: `challenge` (`tsc_…`, 5 dakika geçerli) oturum değildir, yalnızca kodu sunma hakkıdır. Hesap iki adımı zorunlu kılmışsa ve kullanıcı kurmamışsa cevapta ayrıca `"two_step_setup_required": true` vardır: oturum yalnızca kuruluma açıktır |
| `POST /v1/login/verify` | Girişin ikinci adımı: `{"challenge", "code"}` → `{"token", "expires_at"}`. `code` uygulamadaki 6 haneli kod ya da bir yedek koddur. Hatalı kod `401` ve hatalı parola gibi kilide sayılır; süresi dolmuş ya da kullanılmış `challenge` `410`; kilitli hesap `429` |
| `POST /v1/logout` | Oturumu sonlandırır |
| `POST /v1/password/forgot` | `{"email"}`; adres bir kullanıcıya aitse 30 dakika geçerli, tek kullanımlık sıfırlama bağlantısı e-postayla gider. Adres kayıtlı olsun olmasın cevap aynıdır (`204`); dakikada birden fazla bağlantı gönderilmez. E-posta kapalıysa `503` |
| `POST /v1/password/reset` | `{"token", "password"}`; geçersiz ya da kullanılmış bağlantı `410`. Yeni parola kullanıcının oturumlarını kapatır ve kilidi kaldırır |
| `POST /v1/me/password` | `{"current", "new"}`; kullanıcının diğer oturumları kapanır |
| `GET /v1/me/two-step` | Oturumdaki kullanıcının iki adımlı doğrulaması → `{"enabled", "recovery_codes_left"}`. `me/two-step` uçları yalnızca kullanıcı oturumuyla çalışır; API anahtarıyla `400` |
| `POST /v1/me/two-step/setup` | Kurulumu başlatır → `{"secret", "uri"}` (`secret` base32, `uri` uygulamaların kare koddan okuduğu `otpauth://` adresi). Kod onaylanana kadar hiçbir şey açılmaz; yeniden istenirse bekleyen sır değişir. Zaten açıksa `409` |
| `POST /v1/me/two-step/confirm` | `{"code"}`; doğruysa iki adımlı doğrulama açılır → `{"recovery_codes": [10 kod]}`. Kodlar yalnızca bu cevapta görünür. Hatalı kod `422` |
| `POST /v1/me/two-step/recovery-codes` | `{"code"}` (uygulamadaki kod; yedek kod kabul edilmez) → yeni 10 yedek kod; eskileri geçersiz olur. Hatalı kod `403` ve kilide sayılır |
| `POST /v1/me/two-step/disable` | `{"password", "code"}` (`code` uygulama kodu ya da yedek kod); iki adımlı doğrulamayı kapatır, sırrı ve yedek kodları siler. Hatalı parola ya da kod `403` ve kilide sayılır |
| `GET /v1/account/two-step` | Hesabın iki adım zorunluluğu (T-23, ADR-0045) → `{"required", "users_without"}`; `users_without`: hesabın ikinci adımı kurmamış etkin kullanıcı sayısı. Salt okunur roller de okur |
| `PUT /v1/account/two-step` | `{"required": true}` ya da `{"required": false}` → aynı cevap. Yönetici ister. Oturumla açan kişinin kendi ikinci adımı açık olmalı (yoksa `409`, `code: "own_two_step_off"`); API anahtarı koşulsuz açıp kapatır. Denetim kaydına `account` kaynağında `require_two_step_on` / `require_two_step_off` olarak yazılır; değeri değiştirmeyen çağrı kayıt bırakmaz |
| `POST /v1/users/{id}/two-step/reset` | Yönetici, başka bir kullanıcının iki adımlı doğrulamasını kaldırır (kayıp telefon); kullanıcı yalnızca parolayla girer ve yeniden kurabilir. Kişi kendisininkini bu yolla kaldıramaz (`422`) |
| `GET /v1/users` | Kullanıcılar (parola özeti dönmez); `locked` kilitli hesabı, `two_step` iki adımlı doğrulaması açık olanı gösterir |
| `POST /v1/users` | `{"email", "name", "role", "password"}`; parola en az 10 karakter, e-posta benzersiz (`409`) |
| `PATCH /v1/users/{id}` | `name`, `email`, `role`, `status` (`active`/`disabled`), `password`, `unlock`. Yeni parola ya da devre dışı bırakma kullanıcının oturumlarını kapatır; kişi kendi rolünü ve durumunu değiştiremez |
| `DELETE /v1/users/{id}` | Kişi kendini silemez |
| `GET /v1/api-keys` | Anahtarlar (değer ve özet dönmez); `current` çağıranın anahtarını işaretler |
| `POST /v1/api-keys` | Yeni anahtar; anahtar yalnızca bu yanıtta görünür |
| `DELETE /v1/api-keys/{id}` | Anahtarı iptal eder; kullanılan anahtar iptal edilemez (`409`) |
| `POST /v1/tariffs/{id}/rates/import` | CSV tarife yükleme. Başlık: `prefix,price_per_min[,connect_fee,initial_sec,increment_sec,grace_sec,blocked,days,time_from,time_to]` (`price`, `initial`, `increment`, `grace` kısaltmaları da geçerli). İsteğe bağlı pencere (T-19): `days` API'deki gün kümesi (1–127; boş = her gün), `time_from` ve `time_to` tarifenin saat dilimiyle `SS:DD` (ikisi birlikte ve farklı; bitişte `24:00` gece yarısıdır); aynı önek farklı pencerelerle birden çok satırda olabilir, aynı önek ve pencere iki kez olamaz. Pencere sütunu olmayan dosya eskisi gibi yüklenir. Pencere hataları: `days_invalid`, `time_invalid`, `time_pair`. Parametreler: `effective_from`, `mode=close` (varsayılan; aynı önekteki geçerli fiyatların, pencereli olanlar dahil, bitişini bu tarihe çeker) ya da `mode=keep` (geçerli fiyatı olan önekleri reddeder), `dry_run=1` (yalnızca doğrular). Hatalı satırlar atlanır; yanıt `read`, `valid`, `invalid`, `errors[{line, prefix, value, code}]`, `closed`, `new_prefixes`, `rates` taşır. Hiç geçerli satır yoksa `422` |
| `GET /v1/exchange-rate-feed` | Kurların dış kaynaktan alınması (T-20) → `{"enabled", "source", "published", "fetched_at", "failed_at", "error", "missing", "currencies"}`. `enabled` `FX_SOURCE=ecb` ile açıktır; `published` son alınan tablonun günü, `fetched_at` son başarılı alma, `error` son deneme başarısızsa nedeni (adres içermez; başarılı almada boşalır), `missing` kaynağa bağlı olup tabloda bulunmayan kurlar (`"JPY/USD"`: para birimi/hesabın para birimi; son değerleri kalır), `currencies` yayındaki para birimleri (euro dahil). Salt okunur roller de okur |
| `POST /v1/exchange-rate-feed/fetch` | Tabloyu şimdi alır ve kaynağa bağlı kurları yeniden yazar → `{"published", "changed", "missing"}`. Kapalıysa `409`; başka bir süreç o anda alıyorsa `409`; kaynağa ulaşılamaz ya da dosya okunamazsa `502` (son kurlar kalır, hata durumda görünür). Yönetici ister; denetim kaydına `fetch` olarak, değişen her kur ayrıca "sistem" adına `update` olarak yazılır |
| `POST /v1/exchange-rate-feed/follow` | `{"currency"}`: çağıranın hesabında o para biriminin kurunu kaynağa bağlar; kur yoksa eklenir, elle girilmişse çevrilir, değeri son alınan tablodan hemen hesaplanır → kurun satırı. Kapalıysa ya da henüz tablo alınmadıysa `409`; kod üç büyük harf değilse ya da tablodan hesaplanamıyorsa `422`. Elle girilmiş yapmak için genel `PATCH /v1/exchange-rates/{id}` |
| `POST /v1/payments` | `{party_type, party_id, amount, kind, reference, note}`; `kind`: payment, adjustment, refund |
| `GET /v1/ledger?party_type=&party_id=` | Hesap hareketleri |
| `POST /v1/cdrs` | Ham çağrı kayıtlarını ücretlendirir (kayıt `src_ip`, `pdd_ms` ve `dst_trunk_id` taşıyabilir); `(call_id, leg)` ile tekrara dayanıklı. Bilinmeyen alan `400` döner; uzantı kendi alanını `AllowCDRField` ile tanıtır, alan `OnCDRStored` kancasına `CDR.Extra` içinde gelir (rater da aynı yoldan geçer) |
| `POST /v1/route/simulate` | `{src_ip, dst, src, asserted, at}`; `at` (RFC 3339) çağrının başlayacağı andır, verilmezse şimdi; `src` (arayan numara) ve `asserted` (`P-Asserted-Identity`'deki numara) isteğe bağlıdır, ikisi de verilmezse arayan numara kuralları uygulanmaz (biri verilirse öteki numarasız sayılır), verilir ve bir kural numarayı değiştirirse cevapta `caller` bulunur; yönlendirme kararını ve rota sırasını döner; her rotada `step` (basamak) ve `share_pct` (basamağındaki ilk deneme payı) bulunur. Çağrı bir paketten karşılanacaksa `bundle_id` ve `bundle_free_sec` (pakette kalan saniye) döner |
| `POST /v1/report-schedules/{id}/send` | Zamanlanmış raporu son tamamlanan dönemi için hemen sıraya alır → `{"period_start", "period_end", "recipients"}`. Posta sunucusu yoksa `409` |
| `GET /v1/firewall/bans`, `POST /v1/firewall/unban` | Taşkın yüzünden otomatik engellenen adresler (motordan) ve bir engelin kaldırılması (`{"ip"}`); yalnızca sistemi işleten hesap |
| `GET /v1/trace?call_id=` | Bir çağrının saklanan SIP iletileri, bütün rota denemeleriyle, geçtikleri sırayla → `{call_id, messages[{at, direction, transport, src_ip, src_port, dst_ip, dst_port, line, raw}], truncated}`. `at` mikrosaniyeli UTC; `direction` `in` (SIP çekirdeğinin aldığı) ya da `out`; `line` iletinin ilk satırı; `raw` ham metin (parola özeti `***`). En çok 2.000 ileti (`truncated`). İzi olmayan çağrı boş liste döner. Çağrının kaydı çağıranın müşterilerinden birine ait değilse `404`; kaydı olmayan (süren) çağrıyı yalnızca bütün müşterileri gören hesap görür. ClickHouse yoksa ya da okunamıyorsa `503`. Ayrıntı: `docs/10-izleme.md`, "Çağrı izi" |
| `GET /v1/trace/pcap?call_id=` | Aynı iletiler, Wireshark'ın açtığı yakalama dosyası olarak (`application/vnd.tcpdump.pcap`). Başlıklar üretilmiştir, TLS iletileri çözülmüş hâlleriyle yer alır: hattan alınmış görüntü değildir. İz yoksa `404` |
| `GET /v1/system` | Sunucunun durumu (yalnızca sistemi işleten hesap): `machine` (işlemci binde, yük, bellek, ağ; son on dakikanın örnekleri; diskler) ve `services` (her servis cevap veriyor mu, kaç milisaniyede). Linux dışında `machine.supported` false olur, yalnızca diskler dolar |
| `GET /v1/reports/summary?group_by=&from=&to=` | `group_by`: client, vendor, prefix, day. Satırlar denemeleri (çağrı bacaklarını) sayar. Gelir, maliyet ve kâr hesabın para birimindedir. Kalite alanları: `asr_pct`, `acd_sec`, `ner_pct` (ağın bitirmediği denemelerin oranı; canlı izlemedeki tanım: 402, 403, 404, 480, 484, 486, 487 ve 6xx dışındaki hatalar ağ hatasıdır), `pdd_ms` (ilerleme bildiren denemelerde ortalama çevirme sonrası gecikme), toplam alabilmek için ham sayılar `failed_network`, `pdd_samples`, `pdd_sum_ms` |
| `PUT /v1/vendor-connections/{id}/sign-in` | `{"username", "password"}`: switch'in sağlayıcıya giriş yaparken (sağlayıcı 401/407 ile sorduğunda) kullanacağı ad ve parola. Parola `SECRET_KEY` ile şifrelenip saklanır ve hiçbir uçtan geri okunmaz; cevap `{"auth_username", "auth_set": true}`. Yönetici ister; denetim kaydına `sign_in_set` olarak, yalnızca kullanıcı adıyla yazılır. Boş ya da 128 karakterden uzun değer `422` |
| `DELETE /v1/vendor-connections/{id}/sign-in` | Giriş adını ve parolayı kaldırır (`sign_in_cleared`) |
| `POST /v1/vendor-connections/{id}/test` | Bağlantıyı dener: motor sağlayıcıya şimdi tek bir SIP `OPTIONS` gönderir. Cevap `{"outcome", "sip_code", "sip_reason", "ms", "detail"}`; `outcome`: `answered` (kodu ve süresiyle), `no_answer`, `unresolved` (sunucu adı bulunamadı), `refused` (dinleyen yok), `certificate` (sertifika kabul edilmedi), `failed`. Hiçbir şeyi değiştirmez ve denetim kaydına yazılmaz. Yönetici ister; motor erişilemezse `503` |
| `GET /v1/vendor-connections/health?vendor_id=` | Motorun yokladığı (`options_interval_sec` verilmiş, açık) bağlantıların durumu ve switch'in sağlayıcıya kayıtlarının durumu: `{"connections": {"<id>": {"state": "up" / "down" / "unknown", "since", "checked_at", "last": {…}}}, "registrations": {"<id>": {"state": "registered" / "pending" / "failing" / "unknown", "reason", "since", "checked_at", "expires"}}}` (`reason` değerleri `docs/08-cagri-arayuzu.md`, "Sağlayıcıya kayıt"). Yoklanmayan ya da kaydolmayan bağlantı ilgili listede yer almaz; yalnızca çağıranın görebildiği bağlantılar döner |
| `POST /v1/client-trunks/{id}/password` | Kullanıcı adıyla tanınan trunk'a yeni rastgele parola verir → `{"username", "password", "realm"}`. Parola yalnızca bu cevapta görünür; eskisi hemen geçersiz olur. IP ile tanınan trunk için `409` |
| `GET /v1/registrations?client_id=` | Müşterinin trunk'larının şu anki kayıtları (REGISTER) → `{"registrations": [{trunk_id, contact, src_ip, src_port, transport, nat, since, until, user_agent}]}`. `contact` cihazın bildirdiği adres, `src_ip`/`src_port` kaydın gerçekten geldiği yer, `nat` cihaza o adresten ulaşıldığını söyler; `since` ilk kayıt, `until` geçerlilik sonu. Kayıtları motor tutar; motor ayarlı değilse ya da ulaşılamıyorsa `503`. `client_id` zorunludur (`400`) |
| `GET /v1/client-trunks/{id}/registrations` | Tek trunk'ın kayıtları, aynı biçimde. Adresle tanınan trunk'ta liste boştur |
| `DELETE /v1/client-trunks/{id}/registrations` | Trunk'ın kayıtlarını siler; `?contact=` verilirse yalnızca o adresi. Kalan kayıtlar döner. Öyle bir kayıt yoksa `404`. Trunk engellenmez, cihaz yeniden kaydolabilir. Yönetici ister; denetim kaydına `unregister` işlemi olarak, silinen adreslerle yazılır |
| `GET /v1/clients/{id}/bundles` | Müşterinin paketleri; her satırda paketin alanlarına ek olarak `period` (içinde bulunulan dönem, `YYYY-AA`, tarifenin saat dilimiyle) ve `used_sec` (bu dönemde paketten düşülen saniye). Süren çağrıların ayırdığı saniyeler buna dahil değildir |
| `GET /v1/balance-alerts?state=open\|cleared&client_id=` | Düşük bakiye uyarıları (salt okunur). Eşik müşterinin `low_balance` alanıdır (mikro birim; `null` = uyarı yok). API süreci her 10 saniyede bütün müşterilere bakar: bakiye eşiğe ya da altına inmişse uyarı açar, üstüne çıkmışsa ya da eşik kaldırılmışsa kapatır |
| `GET /v1/audit-log/export?…` | Denetim kaydı listesiyle aynı süzgeçler (`limit`/`offset` dışında); eşleşen kayıtların tümü, en yenisi önce, CSV olarak (T-23). Sütunlar: `id, at, actor_type, actor_id, actor_name, ip, resource, row_id, action, label, changes, detail`; `changes` ve `detail` JSON metnidir, sırlar listedeki gibi `"***"`. Sunucu taraflı akış: satırlar veritabanından okundukça gider (her 1.000 satırda gönderilir), toplanmaz; kayıt sayısına sınır yok (saklama süresi sınırlar). Formül gibi başlayan ad ve etiketlerin (`=`, `+`, `-`, `@`) başına `'` eklenir; tablo programı onları formül saymasın diye. Listeyi okuyan herkes (salt okunur roller dahil) indirebilir |
| `GET /v1/cdrs/export?…` | Çağrı kayıtları listesiyle aynı süzgeçler; eşleşen kayıtların tümü CSV olarak (adlar dolu, tutarlar ondalık). 200.000 kayıttan fazlası 422 döner. Son sütunlar `bundle_sec` ve `dst_trunk` (teslim edilen müşteri trunk'ının adı; sağlayıcıya giden bacakta boş). Arşivlenen kayıtlar listedeki kuralla dosyanın sonunda ClickHouse'tan gelir, aynı biçimde; sınır iki kaynağın toplamına uygulanır |
| `GET /v1/live` | Son 60 saniyenin trafiği: `live` (route-engine'in cevabı; alanlar `docs/08-cagri-arayuzu.md` içinde), `clients` ve `connections` (kimlik → ad) |
| `GET /v1/live/calls?client_id=&offset=&limit=` | Süren çağrılar: `total`, `calls`, adlar |
| `POST /v1/live/calls/hangup` | `{"call_id": …}`; cevaplanmış çağrıyı kapatır (yönetici anahtarı) |

Canlı uçlar veriyi route-engine'den okur; adresi API'ye `ENGINE_URL`, motorun beklediği sır `ENGINE_TOKEN` ile verilir. Adres verilmezse ya da motor API'nin sırrını kabul etmezse 503 döner (sır hiçbir cevapta yer almaz). Canlı trafik hesaplara bölünmediği için yalnızca bütün müşterileri gören hesap erişebilir; diğerleri 403 alır.

Tam bir örnek akış için: `pkg/api/api_test.go`.

## Denetim kaydı

Yönetim API'si üzerinden yapılan her değişiklik `audit_log` tablosuna, değişikliği yapan veritabanı işleminin içinde yazılır: kayıtsız değişiklik ya da değişikliksiz kayıt olmaz. Okuma: `GET /v1/audit-log` (ve `/{id}`); salt okunur roller de okur.

| Alan | Anlamı |
|---|---|
| `at` | Zaman (UTC) |
| `reseller_id` | İşlemi yapanın hesabı. Görünürlük buna göredir: her hesap yalnızca kendi kullanıcılarının ve anahtarlarının yaptıklarını görür (Enterprise'da üst hesap alt hesaplarınınkini de) |
| `actor_type`, `actor_id`, `actor_name` | Kim: `user` (ad yerine e-posta), `key` (anahtarın adı) ya da `system`. Ad kayıt anındaki değerdir; kullanıcı ya da anahtar sonradan silinse de kalır |
| `ip` | İsteğin geldiği adres. API bir ters vekilin arkasındaysa `TRUST_PROXY=1` verilmelidir; yoksa vekilin adresi yazılır |
| `resource`, `row_id`, `label` | Ne: API yolundaki kaynak adı (`clients`, `rates`, `users`…), satırın kimliği ve insanların ona verdiği ad (ad, e-posta, önek, adres) |
| `action` | `create`, `update`, `delete` ya da adlı işlem (aşağıda) |
| `changes` | Alan başına `{"old", "new"}`. Güncellemede yalnızca değişen alanlar; oluşturmada yalnızca `new`, silmede yalnızca `old` (silinen satırın son hali). Hiçbir şeyi değiştirmeyen güncelleme kayıt bırakmaz |
| `detail` | Adlı işlemin ayrıntısı (tutar, referans, neden…) |

Süzgeçler: `from`, `to` (RFC 3339), `actor_type`, `actor_id`, `resource`, `action`, `row_id`, `q` (ad, etiket, adres ve değişen değerlerin içinde arar), `limit`, `offset`; eşleşen kayıt sayısı `X-Total-Count` başlığındadır.

Dışa aktarım: `GET /v1/audit-log/export` aynı süzgeçlerle eşleşen kayıtların tümünü CSV olarak akıtır (yukarıdaki uç tablosu). Sırlar tabloya hiç girmediği için dosyada da yoktur; dosya listedeki değerlerin aynısını taşır. Syslog/SIEM'e aktarım ayrı görevdir (T-47).

Adlı işlemler: `payment` / `adjustment` / `refund` (bakiye; kaynak `clients` ya da `vendors`), `rates_import` (kaynak `tariffs`; deneme yüklemesi kayıt bırakmaz), `password_issued` (trunk parolası üretildi; parolanın kendisi yazılmaz), `hangup` (kaynak `live-calls`, etiket Call-ID), `unban` (kaynak `firewall`), `send` (zamanlanmış rapor elle gönderildi), `submit` (`POST /v1/cdrs`), ve kullanıcılar için `login`, `login_failed` (`detail.reason`: `password`, `code`, `disabled`; `detail.locked`: bu deneme hesabı kilitledi mi), `password_changed`, `password_reset_requested`, `password_reset`, `two_step_on`, `two_step_off`, `two_step_reset`, `recovery_codes_renewed` (girişte `detail.two_step_setup_required`: hesap ikinci adımı zorunlu kılmış, kullanıcı kurmamış), hesap için `require_two_step_on` / `require_two_step_off` (kaynak `account`). API anahtarı oluşturma ve iptali `api-keys` kaynağında `create` / `delete` olarak yazılır.

Kurallar:

- **Sırlar kayda girmez.** Parolalar ve özetleri, API anahtarı değerleri, oturum belirteçleri, motorun sırrı, iki adımlı doğrulama sırları ve kodları hiç yazılmaz. Bu bir liste değil, yazan tek işlevin kuralıdır: kaynağın gizlediği sütunlar (`Resource.Hide`) ve adı sır gibi olan her alan (`pass`, `secret`, `token`, `hash`, `ha1`, `otp`, `key`…) `"***"` olarak yazılır; alanın değiştiği görülür, değeri görülmez.
- **Kayıt değiştirilemez.** API'de onu yazan, düzelten ya da silen uç yoktur (`405`).
- **Saklama süresi:** `AUDIT_RETENTION_DAYS` (varsayılan 365 gün; `0` = hep sakla). API süreci saatte bir, süresi dolan kayıtları siler.
- **Hiçbir yazma ucu kaydın dışında kalamaz.** Genel oluşturma/güncelleme/silme, bir uzantının eklediği kaynaklar dahil, kendiliğinden kaydedilir. Kendi kaydını yazmayan bir yazma ucu (`Handle(…, true, …)`) başarıyla bittiğinde uç adından türetilen yalın bir kayıt düşülür (kaynak = yolun ilk parçası, işlem = son parçası). Uzantılar ayrıntılı kayıt için `Server.Audit` çağırır; hiçbir şeyi değiştirmeyen çağrı (önizleme) `AuditNothing` ile bunu söyler.
- Bilinmeyen bir e-posta adresiyle yapılan giriş denemesi kaydedilmez: ait olacağı bir hesap yoktur ve kimliği doğrulanmamış biri kaydı doldurabilirdi. Bilinen kullanıcıdaki hatalı denemeler kaydedilir ve kilitle sınırlıdır.
- Motorda gerçekleşen iki işlemin (`hangup`, `unban`) ve `submit`'in kaydı işlem bittikten sonra yazılır; bunlarda ortak bir veritabanı işlemi yoktur.

## İki adımlı doğrulama

Panel kullanıcıları girişte parolaya ek olarak zamana dayalı tek kullanımlık kod (TOTP, RFC 6238: SHA-1, 6 hane, 30 saniye) kullanabilir; yaygın doğrulama uygulamalarıyla çalışır. Saat kayması için bir önceki ve bir sonraki adımın kodu da kabul edilir. Bir kod bir kez geçerlidir: kabul edilen adım saklanır, o ve öncekiler yeniden kabul edilmez (kurulumu onaylayan kod dahil). Yedek kodlar (10 adet, her biri bir kez) kodun yerine geçer. Hatalı kodlar hatalı parolalarla aynı sayaca yazılır (beş hata, 15 dakika kilit). API anahtarları etkilenmez.

Sır veritabanında `SECRET_KEY` (64 onaltılık hane) verilmişse AES-256-GCM ile, kullanıcının kimliğine bağlanarak şifreli durur; verilmemişse açık durur ve API açılışta bunu günlüğe yazar. Anahtar sonradan verilirse eski satırlar okunmaya devam eder; anahtar kaybolursa şifreli sırlar okunamaz ve yöneticinin o kullanıcıların iki adımlı doğrulamasını sıfırlaması gerekir. Yedek kodların yalnızca özeti saklanır. Kod üretimi ve sırrın saklanması `pkg/totp` paketindedir; Enterprise'ın müşteri portalı da aynı paketi kullanır.

Parola sıfırlama (e-postayla ya da yöneticinin belirlediği yeni parola) iki adımlı doğrulamayı kaldırmaz.

**Zorunlu kılma (T-23, ADR-0045).** Hesabın yöneticisi `PUT /v1/account/two-step` ile ikinci adımı hesabın bütün panel kullanıcıları için zorunlu kılabilir (`resellers.require_two_step`, varsayılan kapalı). Açıkken ikinci adımı kurmamış kullanıcı parolayla girer ve olağan oturum alır, ama oturum yalnızca `GET /v1/session`, `GET /v1/me`, `GET /v1/me/two-step`, `POST /v1/me/two-step/setup`, `POST /v1/me/two-step/confirm` ve `POST /v1/logout` uçlarına ulaşır; diğer her uç (uzantılarınki dahil) `403` ve `{"error", "code": "two_step_required"}` döner. Kısıt her istekte okunur: ayar açılınca açık oturumlar bir sonraki isteklerinde kısıtlanır, kurulum onaylanınca aynı oturum hemen açılır; ayar açıkken yöneticinin sıfırladığı kullanıcı da kuruluma döner. API anahtarları etkilenmez. Oturumla ayarı açan kişinin kendi ikinci adımı açık olmalıdır (`409`, `own_two_step_off`); kimse kilitlenmez, çünkü kurulum kısıtlanan herkese açıktır. Enterprise'da her bayi hesabının ayarı kendisinindir; müşteri portalı bu kuralın dışındadır.

## E-posta

API süreci e-postayı iki yoldan biriyle gönderir; ikisi de aynı kuyruğu kullanır:

| Ortam değişkeni | Anlamı |
|---|---|
| `RESEND_API_KEY` | Resend API anahtarı; gönderim Resend'in HTTP API'siyle yapılır |
| `SMTP_URL` | SMTP sunucusu: `smtp://kullanıcı:parola@sunucu:587` (sunucu sunuyorsa TLS başlatılır) ya da `smtps://…:465` |
| `MAIL_FROM` | Gönderen adres, ör. `Telrics <no-reply@example.com>`; Resend'de doğrulanmış bir alan adına ait olmalı |
| `PANEL_URL` | Panelin adresi; e-postalardaki bağlantılar bununla kurulur |

`RESEND_API_KEY` ve `SMTP_URL` birlikte verilemez; ikisi de yoksa e-posta kapalıdır ve hiçbir şey kuyruğa yazılmaz.

İleti, ona yol açan veritabanı işlemiyle birlikte `mail_outbox` tablosuna yazılır ve API süreci tarafından gönderilir. Gönderilemeyen ileti aralık iki katına çıkarak (1 dk, 2 dk, 4 dk…) sekiz kez denenir; son hata `last_error` sütununda durur.

Gönderilen iletiler: parola sıfırlama bağlantısı ve düşük bakiye uyarısı (müşterinin e-posta adresine, uyarı açıldığında bir kez). İletiler düz metin ve Türkçedir.

Laboratuvarda iletiler dışarı çıkmaz: posta yakalayıcı (Mailpit) hepsini tutar, http://127.0.0.1:58025 adresinden okunur. `make test-mail` akışı uçtan uca dener.
