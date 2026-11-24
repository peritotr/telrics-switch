# Mimari

Bu belge sistemin bugünkü yapısını anlatır: bileşenler, aralarındaki arayüzler, çağrı akışları, eşzamanlılık modeli, hata ve zaman aşımı stratejisi. Amaç, yalnızca bu belgeyi, `docs/decisions/` altındaki karar kayıtlarını ve `docs/TASKS.md` dosyasını okuyan birinin doğru yere doğru kodu yazabilmesidir.

- Kapasite hesabı ve donanım planı: `docs/02-mimari.md`.
- Arayüzlerin alan alan tanımı: `docs/08-cagri-arayuzu.md` (motor ↔ SIP çekirdeği), `docs/06-api.md` (yönetim API'si).
- Bu belgeyle kod çelişirse kod doğrudur; çelişkiyi bildir ve belgeyi düzelt.
- Karar verilmemiş noktalar **AÇIK SORU** diye işaretlidir; hepsi sondaki listede toplanır. AÇIK SORU'ya dokunan bir işi kendi varsayımınla bitirme, sor.

## 1. Kapsam ve temel ilke

Ürün bir **Class 4** SIP anahtarıdır: müşteri trunk'ından gelen çağrıyı tanır, yetkilendirir, fiyatlandırır, bir sağlayıcıya yönlendirir, ücretlendirir. Hedef 20.000 eşzamanlı çağrı ve 1.000 CPS'tir (bugüne kadar yalnızca Mac üzerinde Docker'da 200 CPS / 2.000 çağrı ölçüldü).

**SIP yığınını biz yazmıyoruz** (ADR-0001). SIP ayrıştırma, işlem (transaction) ve dialog katmanı OpenSIPS'tir; ses aktarımı rtpengine'dir. Bizim kodumuz bunların çevresindeki Go servisleridir. Bu yüzden "SIP yığını" ve "transaction/dialog katmanı" başlıkları altında anlatılan şey bir OpenSIPS **betiğidir** (`sip/opensips/opensips.cfg`), Go kodu değil. Go tarafında SIP iletisi üreten tek yer motorun sağlayıcıları yoklamak için gönderdiği `OPTIONS` isteğidir (`pkg/engine/health.go`); sağlayıcıya kayıt (REGISTER) ise SIP çekirdeğinden, OpenSIPS'in `uac_registrant` modülüyle gider (ADR-0035).

İkinci temel ilke: **iş mantığı SIP betiğinde durmaz** (ADR-0002). Betik sorar, cevabı uygular, olanı bildirir. Kimin arayabileceği, nereye gideceği, ne kadar süreceği, kaydın kabul edilip edilmeyeceği motorun kararıdır.

## 2. Bileşenler

```
 Müşteri trunk'ları ──SIP/UDP,TCP,TLS──▶ ┌──────────────┐ ──SIP──▶ Sağlayıcılar
                                         │ SIP çekirdeği│
                     ┌──── HTTP+JSON ───▶│  (OpenSIPS)  │◀── MI (UDP, JSON-RPC) ──┐
                     │    (v1, Bearer)   └──┬────────┬──┘                         │
                     │                      │ng      │HEP                         │
              ┌──────┴───────┐       ┌──────▼────┐ ┌─▼───────────────┐            │
              │ route-engine │       │ rtpengine │ │ trace-collector │            │
              │    (Go)      │───────┼───────────┼─┼─────────────────┼────────────┘
              └─┬────┬─────┬─┘       └───────────┘ └──────┬──────────┘
        Redis ◀─┘    │     └─▶ NATS JetStream ──▶ rater ──┼─▶ ClickHouse
   (limit, rezervasyon,   (CDR olayları)         (Go)     │   (CDR kopyası, sip_messages)
    çağrı durumu, kayıt)                           │      │
                     │ görüntü (LISTEN/NOTIFY)     ▼      │
                     └────────────────────────▶ PostgreSQL ◀── api (Go, REST) ◀── panel (React)
                                               (yapılandırma, bakiye, CDR asıl kaydı)
```

| Bileşen | Kod | Görev | Durum tutar mı |
|---|---|---|---|
| **SIP çekirdeği** | `sip/opensips/opensips.cfg`, `deploy/images/opensips/` | SIP ayrıştırma, işlem ve dialog, topoloji gizleme, digest sorusu, rota denemesi ve sıradakine geçiş, NAT, TLS, rtpengine'e teklif/cevap, kenar koruması, sağlayıcılara kayıt (`uac_registrant`; listeyi `registrants.sh` motordan alır) | İşlem ve dialog (bellekte); yerel önbellek (`known:`, `ban:`); sağlayıcı kayıt tablosu (`/run/opensips/regdb`, tmpfs, parolalarla) |
| **route-engine** (motor) | `cmd/route-engine`, `pkg/engine`, `pkg/routing`, `pkg/rating` | Çağrı kararı, limitler, bakiye ve paket rezervasyonu, kayıt (registrar), sağlayıcı sağlık ve sertifika denetimi, sağlayıcılara kayıt listesi ve durumu, canlı sayaçlar, çağrı kaydı (CDR) üretimi | Bellekte yalnızca görüntü ve sayaçlar; kalıcı durum Redis'te |
| **rtpengine** | `media/rtpengine/rtpengine.conf` | RTP/SRTP aktarımı, SDP yeniden yazımı, kodek süzme, NAT'ta kaynak öğrenme | Oturumlar (bellekte) |
| **rater** | `cmd/rater`, `pkg/rater`, `pkg/api` (`IngestCDRs`) | CDR'yi ücretlendirir, bakiyeden ve paketten düşer, PostgreSQL'e yazar, ClickHouse'a kopyalar, eski kayıtları arşivler | Hayır (PostgreSQL) |
| **api** | `cmd/api`, `pkg/api`, `pkg/store` | REST yönetim API'si, kimlik ve roller, denetim kaydı, raporlar, e-posta kuyruğu, zamanlanmış işler | Hayır (PostgreSQL) |
| **panel** | `web/` | Yönetim arayüzü (React + TypeScript + Vite) | Oturum belirteci yalnızca bellekte |
| **trace-collector** | `cmd/trace-collector`, `pkg/trace` | HEP ile gelen sinyalleşme kopyasını ClickHouse'a yazar | Hayır |
| PostgreSQL | `db/migrations/postgres/` | Yapılandırma, bakiye defteri, CDR'nin asıl kaydı, denetim kaydı | — |
| Redis | — | Eşzamanlı çağrı ve CPS sayaçları, rezervasyon defterleri, süren çağrıların durumu, kayıtlar (`reg:<trunk>`) | — |
| NATS JetStream | — | `CDR` akışı, `cdr.v1` konusu | — |
| ClickHouse | `db/migrations/clickhouse/` | CDR kopyası (arama, raporlar), `sip_messages` | — |

Go paketleri ve sorumlulukları:

| Paket | Sorumluluk | Çağrı yolunda mı |
|---|---|---|
| `pkg/routing` | Görüntü (`Snapshot`) veri yapıları ve saf karar: trunk eşleme, önek eşleme, LCR/öncelik/ağırlık, arayan numara kuralları, güvenlik duvarı hükmü. Giriş/çıkış yapmaz | Evet |
| `pkg/rating` | Fiyat seçimi (`Tidy`, `Select`), tutar (`Rate.AmountAfter`), kur çevirme (`Convert`), dönem (`Period`). Motor ve rater için **tek** kod | Evet |
| `pkg/engine` | HTTP uçları, Redis betikleri, rezervasyon zamanlayıcısı, kayıt, sağlık/sertifika denetimi, canlı sayaçlar, CDR'nin NATS'a yazımı | Evet |
| `pkg/fx` | Yayımlanan referans kurlarının okunması (Avrupa Merkez Bankası dosyası) ve hesabın para birimine tam sayı aritmetiğiyle çaprazlanması (T-20, ADR-0036). Kurları API süreci yazar (`pkg/api/fx.go`) | Hayır |
| `pkg/sipctl` | Motorun SIP çekirdeğine verdiği komutların sürücüsü (çağrı kapatma, engel kaldırma, sağlayıcı kayıtlarının durumu `reg_list`). OpenSIPS'e özgü tek Go kodu | Hayır (arka plan) |
| `pkg/api` | Kaynaklar (genel CRUD), özel işlemler, görüntünün PostgreSQL'den yüklenmesi (`LoadSnapshot`), CDR'nin saklanması (`IngestCDRs`), genişleme noktaları | Hayır |
| `pkg/rater` | NATS tüketicisi, ClickHouse aktarımı, arşiv | Hayır |
| `pkg/store` | Bağlantı havuzu, şema adımları (`MigrateFS`), `NotifyConfig` | Hayır |
| `pkg/metrics`, `pkg/trace`, `pkg/sysinfo`, `pkg/mail`, `pkg/totp`, `pkg/numplan`, `pkg/clickhouse` | Yardımcılar | `metrics` evet (tek atomik toplama) |

## 3. Arayüzler

| # | Kimden → kime | Taşıma | Sözleşme | Kimlik |
|---|---|---|---|---|
| A1 | SIP çekirdeği → motor | HTTP + JSON, `"v": 1` | `docs/08-cagri-arayuzu.md`: `/v1/authorize`, `/v1/events`, `/v1/source`, `/v1/credentials`, `/v1/register`, `/v1/ban`; ayrıca SIP çekirdeğinin yanındaki `registrants.sh` için `GET /v1/vendor-registrations` (sağlayıcılara kayıt listesi, parolalarla) | `Authorization: Bearer <ENGINE_TOKEN>` |
| A2 | Motor → SIP çekirdeği | OpenSIPS MI (`mi_datagram`, UDP, JSON-RPC), `pkg/sipctl` üzerinden | Yalnızca iki emir ve bir okuma: çağrıyı bitir (`dlg_end_dlg`), engeli unut (`pike_rm` + `cache_remove`); sağlayıcılara kayıtların durumunu oku (`reg_list`, ADR-0035) | Yok (özel ağ varsayımı) |
| A3 | SIP çekirdeği → rtpengine | ng protokolü (UDP) | `rtpengine_offer` / `_answer` / `_delete` bayrakları (kodek çevirisinde `codec-mask-all`, `codec-transcode-…`; §9) | Yok |
| A4 | Motor → Redis | Redis protokolü, Lua betikleri | Anahtar düzeni §6.3 | Yok |
| A5 | Motor → NATS | JetStream yayını, `cdr.v1` | `engine.CDR` JSON'u; `api.CDR` bilinmeyen alanı reddeder | Yok |
| A6 | Motor ← PostgreSQL | `LoadSnapshot` + `LISTEN softswitch_config` | Görüntü yüklenir; fiyatların yalnızca değişeni (`rate_changes`, ADR-0048) | Parola |
| A7 | api → motor | HTTP + JSON (A1 ile aynı dinleyici): `/v1/live`, `/v1/calls`, `/v1/hangup`, `/v1/registrations`, `/v1/unregister`, `/v1/bans`, `/v1/unban`, `/v1/peers`, `/v1/probe`, `/v1/stats`, `/v1/reload` | `docs/08-cagri-arayuzu.md` | Bearer |
| A8 | Panel / dış istemci → api | REST + JSON, `GET /v1/openapi.json` | `docs/06-api.md` | API anahtarı ya da `tss_` oturum belirteci |
| A9 | SIP çekirdeği → trace-collector | HEP v3 (UDP) | `pkg/trace` | Yok |
| A10 | Enterprise → core | Go içe aktarma | `pkg/api` genişleme noktaları, `engine.Config` (ADR-0008) | — |
| A11 | rtpengine → SIP çekirdeği | XML-RPC (HTTP), `SIP_METRICS` dinleyicisinde `/RPC2` (`mi_xmlrpc_ng`) | Yalnızca `teardown <Call-ID>` (rtpengine modülünün komutu, `dlg_end_dlg` sarmalayıcısı): sesi kesilen çağrıyı bitir (ADR-0027) | Yok (özel ağ varsayımı; aynı kapıda bütün MI komutları açıktır, S-14) |

Arayüz kuralları (hepsi bağlayıcıdır):

1. **A1 sürümlüdür ve SIP sunucusundan bağımsızdır.** OpenSIPS'e özgü hiçbir kavram (AVP adı, bayrak, modül adı, `$du` gibi değişken) gövdeye girmez. Yeni ihtiyaç isteğe bağlı alanla karşılanır; mevcut bir alanın anlamını değiştiren ya da zorunlu alan ekleyen değişiklik `v`'yi artırır. Motor bilinmeyen alanı reddetmez, yok sayar: isteğe bağlı alan eklemek bu yüzden yükseltme sırasını bağlamaz. Örnek: `events` içindeki `cause` (ADR-0039; v1'de kaldı), `route` içindeki `transcode` (ADR-0041).
2. A1'de cevap her zaman HTTP 200'dür, karar gövdededir. 200 dışı cevap "motor cevap veremedi" demektir (§7).
3. Motor zaman damgalarını kendi saatiyle koyar; arayüzde saat taşınmaz.
4. Her olay birden çok kez gelebilir; motor tekrarı zararsız işlemek zorundadır.
5. Betikte her `rest_post` öncesinde `route(engine_token)` çağrılır (`rest_append_hf` yalnızca bir sonraki isteğe gider).
6. A2 tek yönlü ve dar tutulur: motor "şu çağrıyı bitir" der (ve sağlayıcı kayıtlarının durumunu sorar); nasıl söyleneceği ve cevabın nasıl okunacağı `pkg/sipctl` sürücüsünün işidir.
7. Yönetim API'sinde yeni uç `s.Handle` ya da `s.HandlePublic` ile kaydedilir (OpenAPI oradan üretilir); doğrudan `s.mux` kullanılmaz.

## 4. SIP çekirdeği: işlem ve dialog katmanı

Betiğin yapısı (`sip/opensips/opensips.cfg`):

| Blok | Ne zaman çalışır | Ne yapar |
|---|---|---|
| `route` | Her istek | Max-Forwards; NAT sınaması ve `force_rport`; çağrı içi isteklerin ayrılması (çağrı içi REFER'e 403, §5.6); kenar koruması; yönteme göre dallanma |
| `route[signin]` | `Authorization` taşıyan INVITE/REGISTER | Özeti motordan alır (`/v1/credentials`), parolayı denetler |
| `route[register]` | REGISTER | Motora sorar (`/v1/register`), cevabı yazar |
| `route[authorized]` | `authorize` cevabı gelince (eşzamansız devam) | Kararı uygular: ret ya da dialog kurulumu, arayan numara, ilk rota |
| `route[take]` | Her rota için | Motorun verdiği rotayı işlem değişkenlerine alır (hedef, kimlik, kodek, süreler) |
| `route[attempt]` | Her deneme | İsteği gönderir; gönderilemezse (503) ya da kodek yoksa (488) `failed` bildirip sıradaki rotayı alır; en çok 16 tur |
| `branch_route[out]` | Her denemenin kendi isteğinde | Yalnızca o denemeye ait değişiklikler: SDP teklifi, arayan numara, kimlik başlığı |
| `onreply_route[media]` | Sağlayıcı cevabı | SDP cevabı; ilk 180/183'te `ringing`, 200'de `answered` olayı ve ACK beklemesi (dialog zamanlayıcısı 32 sn) |
| `failure_route[next]` | Deneme kesin hata verince | 401/407'ye bir kez digest cevabı; `failed` olayı; motor rota verdiyse yeni deneme, vermediyse medya oturumunu siler |
| `route[hop]`, `local_route` | Çağrı içi istekler; SIP çekirdeğinin kendi BYE'ı (motor, azami süre, ACK beklemesi, aktarıcının `teardown`'ı) | NAT arkasındaki tarafa gerçek adresinden ulaşır; kendi BYE'ında aktarıcı oturumunu siler ve `ended` olayını gönderir |

Kullanılan modüller ve rolleri: `tm` (işlem durumu, yeniden iletim, zamanlayıcılar), `dialog` (çağrı durumu, azami süre, dialog değerleri), `topology_hiding` (Via/Record-Route/Contact gizleme; `rr` yalnızca `uac` istediği için yüklüdür, Record-Route eklenmez), `uac` (From değişimi, sağlayıcıya digest), `auth` (arayana digest sorusu), `pike` (taşkın), `cachedb_local` (kaynak önbelleği), `rest_client` (motor), `rtpengine`, `tls_mgm` + `db_text` (sertifikalar), `nathelper` (yalnızca `nat_uac_test`), `mi_datagram` (A2), `mi_xmlrpc_ng` (A11: aktarıcının `teardown`'ı), `prometheus`, `statistics` (betiğin kendi sayaçları: `refer_refused`, §5.6; `media_refused`, §9); HEP ile iz kopyası (A9) için gereken satırları konteyner açılırken `deploy/images/opensips/run.sh` ekler (`TRACE_TARGET`, `HEP_TARGET`).

**Bu OpenSIPS sürümünde (3.6.10) yasak olan:** `async()` öncesinde isteği değiştiren her komut (`consume_credentials()`, `fix_nated_contact()`, `remove_hf()`, `append_hf()`, `subst*()` …). İsteği değiştirmek gerekiyorsa bunu devam rotasında (`route[authorized]`) yap. Betik `proxy_challenge()` ve `fix_nated_contact()` kullanmıyor (ADR-0014).

Dialogda saklanan değerler (`$dlg_val`): `caller_hop`, `callee_hop` (NAT arkasındaki tarafın gerçek adresi), `codecs` (çağrı içi yeni tekliflere uygulanacak kodek bayrakları), `limit` ve `ack_wait` (cevaptan sonra ACK beklenirken çağrının azami süresi; §5.4), `media_gone` (SIP çekirdeğinin kendi BYE'larında aktarıcı oturumu silindi; §5.3), `offerless` (ilk INVITE teklifsizdi; arayana teklif edilecek profil), `offerless_af` (o teklifin adres ailesi: INVITE'ın geldiği aile; ADR-0047), `late` (200'de yapılan teklifin cevabı ACK'te bekleniyor), `late200` ve `gone` (teklifsiz INVITE'ın 200'ü aktarıcıda işleniyor ya da aktarıcı onu almadı), `reoffer` (son çağrı içi INVITE teklif taşıyor muydu, taşımıyorsa 200'deki teklif hangi yöne gidecek; §5.2, ADR-0043), `rung` (motora `ringing` bildirilen son denemenin dal sırası; her denemenin yalnızca ilk 18x'i bildirilir, T-46). Cevap rotasının ihtiyaç duyduğu bilgi AVP'de değil dialogda tutulur: cevap rotası işlemin AVP'lerini görmez (`tm` `onreply_avp_mode` varsayılanı 0). Çağrı durumu için başka kalıcı yer yoktur; betik veritabanı kullanmaz.

## 5. Çağrı akışları

Gösterimde `M` = motor, `R` = rtpengine. Arayüz alanları için `docs/08-cagri-arayuzu.md`.

### 5.1 INVITE (çağrı kurulumu)

```
Arayan                SIP çekirdeği                 M            R        Sağlayıcı
  │ INVITE ───────────────▶│                         │            │           │
  │                        │ kenar: known:/ban: önbelleği, pike                │
  │                        │ (tanınmıyorsa) POST /v1/source ─▶│                │
  │ ◀── 401 (tanınmayan kaynak; digest sorusu)        │                        │
  │ INVITE + Authorization ▶│ POST /v1/credentials ──▶│                        │
  │                        │ async POST /v1/authorize ▶│ görüntüden karar      │
  │                        │                         │ + Redis: limit, rezervasyon
  │                        │◀── allowed, route, max_duration_sec               │
  │ ◀── 4xx/5xx (ret)      │ ya da: dialog kur, topoloji gizle                 │
  │                        │ offer ───────────────────────────▶│               │
  │                        │ INVITE ──────────────────────────────────────────▶│
  │ ◀── 180/183 ───────────│◀──────────────────────────────────────── 180/183 │
  │                        │ POST /v1/events ringing ─▶│ (PDD ölçümü)          │
  │                        │◀──────────────────────────────────────── 200 OK  │
  │                        │ answer ──────────────────────────▶│               │
  │                        │ POST /v1/events answered ▶│                       │
  │ ◀── 200 OK ────────────│                         │                         │
  │ ACK ──────────────────▶│ ACK ─────────────────────────────────────────────▶│
```

Motorun karar sırası (`pkg/engine/engine.go`, `pkg/routing/engine.go`):

1. Güvenlik duvarı hükmü (`deny` → `source_blocked`).
2. Trunk eşleme: `auth_user` doluysa ada göre, değilse kaynak adrese göre (en dar ağ, eşitlikte en uzun teknik önek). Trunk bulunmazsa `unknown source`; ancak `auth_user` boşsa ve `Config.Incoming` verilmişse uç sorulur, çağrıyı bir müşteri adına alırsa karar o müşterinin trunk'sız çağrısı olarak sürer (ADR-0032; adımlar 3–6 müşterinin kurallarıyla, trunk kuralı olmadan; `source_known` yanlış kalır).
3. Trunk kuralı (`security`: `any`/`tls`/`cert`), hesap durumu.
4. Trunk'ın numara biçimi; arayan numara kuralları (`cli_rules`): From'daki numaraya ve (`asserted` alanı) `P-Asserted-Identity`'deki numaraya kendi kuralları ayrı ayrı uygulanır; numarasız arayan "numarasızı reddet" kuralıyla 433 alır. `privacy`'de `id` varsa çağrının her rotasında kimlik başlığı çıkarılır (`identity: "remove"`, bağlantının `pai_mode` ayarından önce gelir).
5. Satış fiyatı (çağrının başladığı ana göre `rating.Select`), paket.
6. Yönlendirme planı → rotalar: basamaklar (öncelik; LCR'de fiyat), basamak içinde `PickFor(call_id)` ile ağırlıklı sıra; zaman penceresi dışındaki, sertifikası reddedilen, yoklamaya cevap vermeyen ve ortak kodeği olmayan rotalar çıkarılır.
7. `Config.Adjust` (varsa; Enterprise).
8. Redis'te tek betikle kabul: müşteri/trunk/sağlayıcı limitleri, CPS, bakiye ve paket rezervasyonu, çağrı durumunun yazılması.
9. Cevap: ilk rota ve `max_duration_sec`.

**Sıradaki rotaya geçiş.** Deneme kesin hata verirse ya da zaman aşımına uğrarsa betik `failed` olayını gönderir; motor ya sıradaki rotayı ya da boş cevap verir. Geçip geçmemeye motor karar verir (varsayılan: 404, 480, 484, 486, 487 ve 6xx çağrıyı bitirir; bağlantının `failover_mode` ayarı bunu değiştirir). Her deneme ayrı bir CDR bacağıdır (`leg` 1, 2, 3…).

**Teklifsiz INVITE** (SDP'siz; ADR-0043): teklif sağlayıcının 200'ündedir ve aktarıcıdan cevapta geçer, arayanın cevabı ACK'te (§5.2).

**Aktarıcı teklifi almazsa** (`offer` adımı; cevap yok ya da port kalmadı) deneme sağlayıcıya gönderilmez: betik motora `ended` olayını 503 ve `cause: "media"` ile bildirir, arayana 503 verir; `failed` gönderilmediği için sıradaki rota denenmez (ADR-0030). Motor kaydı seçilen bağlantıyla yazar, ama denemeyi o bağlantının sayaçlarına ve kalitesine yazmaz (`OnLeg`: `NotSent`; ADR-0039, §8.2, §9).

### 5.2 re-INVITE (çağrı içi yeni teklif)

```
Taraf A            SIP çekirdeği                     R            Taraf B
  │ INVITE (to-tag) ─▶│ topology_hiding_match, route(hop)          │
  │                   │ offer (dialogdaki kodek bayraklarıyla) ─▶│  │
  │                   │ INVITE ────────────────────────────────────▶│
  │                   │◀─────────────────────────────────── 200 OK │
  │                   │ answer ─────────────────────────────────▶│  │
  │ ◀── 200 OK ───────│                                            │
```

- **Motor haberdar edilmez.** Çağrının süresi, rezervasyonu ve kaydı re-INVITE'tan etkilenmez; dialog zamanlayıcısı (azami süre) da yeniden kurulmaz: süre cevaptan sayılmaya devam eder.
- İki yönde de aynı yol: `route[hop]` (NAT arkasındaki taraf gerçek adresinden), `rtpengine_offer` (dialogdaki kodek bayraklarıyla; NAT arkasındaki teklif sahibi için `SIP-source-address`), cevapta `onreply_route[reinvite]` → `route(answer)`. Taşıma (RTP/SAVP) ve ICE için bayrak verilmez: rtpengine her bacağın ilk görüşmesinde kararlaştırılanı korur (ICE satırları yeni teklifte de çıkar; SRTP bacağı SRTP kalır, aktarıcıyla doğrudan doğrulandı).
- Kodek listesi dialogda saklandığı için yeni teklife, hangi taraftan gelirse gelsin, uygulanır.
- Bekletme (hold: `sendonly`/`inactive`), T.38 ve oturum yenileme için özel işlem yoktur; yön satırları olduğu gibi geçer. T.38'e geçen teklif (`m=image … udptl t38`) aktarıcıdan biçimi ve öznitelikleriyle geçer; kodek listesinin bayrakları RTP olmayan akışa uygulanmaz (ADR-0031). Çağrı içi SIP INFO (tuşlar) değişmeden iletilir. Senaryolar: `make test-routing`, "fax" (TASKS: T-40).
- Reddedilen yeni teklif (ör. 488) çağrıyı düşürmez (RFC 3261 §14.1); ret ACK'i atlama atlamadır, `tm` karşılar.
- **Teklifsiz INVITE** (teklif 200'de, cevap ACK'te; RFC 3261 §13.2.1, §14.1, RFC 3264 §5–6; ADR-0043): ilk INVITE'ta 200'deki (ve SDP'li geçici cevaptaki) teklif `onreply_route[media]`, çağrı içinde `onreply_route[reinvite]` içinde `rtpengine_offer` ile aktarıcıdan geçer (kodek bayrakları teklifin gittiği yöne göre, NAT bayrağı cevabın `Contact`'ına göre); ACK'teki cevap `route[ack]` içinde `rtpengine_answer` ile. İlk teklifte arayana giden profil, arayanın sinyalleşmesi TLS ise `RTP/SAVP`, değilse `RTP/AVP`'dir (switch'in teklif ettiği taraf kuralı, ADR-0015); sağlayıcı ayağı kendi teklifine uyar. Cevap taşımayan ACK'te çağrı iki taraftan BYE ile kapatılır; aktarıcı 200'deki teklifi almazsa 200 SDP'siz iletilir, çağrı kapatılır ve motora `ended 503` (`cause: "media"`) gider (§9). Senaryolar: `make test-routing`, "late" (TASKS: T-43).
- Senaryolar: `make test-routing`, "reinvite" (TASKS: T-02; `docs/08`, "Çağrı içi yeni teklif").
- **Oturum zamanlayıcıları (ADR-0027, eski S-1):** RFC 4028 başlıkları (`Supported`/`Require: timer`, `Session-Expires`, `Min-SE`) uçtan uca değişmeden geçer; SIP çekirdeği kendisi yenileme istemez, bacak başına oturum zamanlayıcısı uygulamaz. Ölü çağrıyı medya aktarıcısı saptar (§5.3, §9): 60 saniye hiç ses gelmezse çağrı iki taraftan kapatılır. Senaryolar: `make test-routing`, "deadcall" (TASKS: T-38).

### 5.3 BYE

Üç kaynak vardır; üçünde de motor aynı `ended` olayını alır ve olay birden çok kez gelebilir.

| Kaynak | Akış |
|---|---|
| Taraflardan biri | BYE → `topology_hiding_match` → `route(hop)` → `rtpengine_delete` → `POST /v1/events ended` → karşı tarafa iletilir |
| Motor (rezervasyon bitti, `POST /v1/hangup`) | Motor → `pkg/sipctl` → MI `dlg_end_dlg` → SIP çekirdeği iki tarafa BYE üretir → `local_route`: `route(hop)` + `ended` |
| SIP çekirdeği (`max_duration_sec` doldu ya da cevaba 32 sn içinde ACK gelmedi) | Dialog zaman aşımı → iki tarafa BYE → `local_route` |
| Medya aktarıcısı (`RTP_TIMEOUT` boyunca hiç ses yok; ADR-0027) | rtpengine → XML-RPC `teardown <Call-ID>` (A11) → SIP çekirdeği `dlg_end_dlg` → iki tarafa BYE → `local_route` |

SIP çekirdeğinin kendi BYE'larında (`local_route`) aktarıcının oturumu da bir kez silinir (`$dlg_val(media_gone)`); yoksa oturum aktarıcının zaman aşımına kalır ve aktarıcı bir dakika sonra biten çağrı için `teardown` gönderirdi (zararsız ama gürültü).

`ended` üzerine motor: çağrıyı Redis'ten düşer, ayrılan tutarı çağrının gerçek ücretine indirir (bırakmaz; §6.4), CDR bacağını NATS'a yazar.

### 5.4 CANCEL

```
Arayan             SIP çekirdeği                    M             Sağlayıcı
  │ CANCEL ──────────▶│ t_check_trans → t_relay                       │
  │ ◀── 200 (CANCEL)  │ CANCEL ──────────────────────────────────────▶│
  │                   │◀────────────────────────────────────── 487    │
  │                   │ failure_route: t_was_cancelled → kod 487      │
  │                   │ POST /v1/events failed 487 ─▶│ çağrı biter    │
  │ ◀── 487 ──────────│ rtpengine_delete                              │
```

- 487 hiçbir kuralda sıradaki rotaya geçirmez (bağlantı `always` olsa da).
- Eşleşen işlemi olmayan CANCEL cevapsız bırakılır.
- `authorize` cevabı beklenirken gelen CANCEL: `tm` CANCEL'ı 200 ile cevaplar. Motorun kararı gelince `route[authorized]` çağrının iptal edildiğini görür (`t_was_cancelled`), motora verdiği rota için `failed 487` bildirir (motor çağrıyı kabul etmiş, sayacı ve rezervasyonu tutuyordur), arayana 487 verir ve sağlayıcıya hiçbir şey göndermez. Aynı denetim `route[attempt]` içinde de vardır: gönderilemeyen bir deneme, arayan arada vazgeçtiyse 503 değil 487 sayılır. (Bu düzeltmeden önce deneme 503 ile bildiriliyordu: kayıt yanlış kodla yazılıyor, ağ hatası sayılıyor ve yedek rotası olan çağrıda motor sıradaki rotayı veriyordu.)
- Cevaptan (200) sonra gelen CANCEL'ın etkisi yoktur (RFC 3261 §9.1): çağrı sürer, arayan ACK ve BYE göndermek zorundadır.
- Senaryolar: `make test-routing`, "cancel" (TASKS: T-01).
- **ACK gelmeyen cevap (ADR-0026, eski S-17):** cevaplanan bir çağrıyı ACK'lemeyen arayanın (örneğin 200 ile CANCEL'ı yarışan ve vazgeçtiğini sanan bir cihaz) çağrısını SIP çekirdeği 32 saniye (64×T1) sonra iki taraftan BYE ile kapatır; motor `ended` alır, kayıt 200 ve gerçek süreyle yazılır. Uygulama dialog zamanlayıcısıyla: `onreply_route[media]` 200'de zamanlayıcıyı 32 saniyeye indirir ve azami süreyi `$dlg_val(limit)` içinde saklar; ACK gelince zamanlayıcı azami sürenin kalanına geri alınır. Azami süresi 32 saniye ya da kısa olan çağrıya dokunulmaz. Senaryolar: `make test-routing`, "ackwait" (TASKS: T-41; `docs/08`, "Cevabı onaylamayan arayan").

```
Arayan             SIP çekirdeği                    M             Sağlayıcı
  │                   │◀──────────────────────────────────── 200 OK   │
  │ ◀── 200 OK ───────│ zamanlayıcı: 32 sn; answered ─▶│              │
  │   (ACK yok)       │ … 32 sn …                                     │
  │ ◀── BYE ──────────│ dialog zaman aşımı ("B") ─────────── BYE ────▶│
  │                   │ local_route: ended ───────────▶│ çağrı biter  │
```

### 5.5 REGISTER

```
Trunk               SIP çekirdeği                     M (Redis: reg:<trunk>)
  │ REGISTER ────────▶│ kenar koruması (kayıtlı adres "tanınan" olmaz)
  │ ◀── 401 ──────────│
  │ REGISTER + Auth ─▶│ POST /v1/credentials ──────▶│
  │                   │ POST /v1/register ─────────▶│ trunk kuralı, güvenlik duvarı,
  │                   │   (Contact ve Expires ham)  │ süre sınırları, max_contacts
  │ ◀── 200 + Contact │◀── sip_code, contacts, min_expires
```

- Kayıt defteri **motordadır**, Redis'te durur; `usrloc`/`registrar` kullanılmaz (ADR-0013).
- `Contact` başlığını motor ayrıştırır; betik ham metni taşır.
- Canlı tutma gönderilmez: NAT arkasındaki ya da TCP/TLS ile bağlı trunk'a en çok `REG_NAT_EXPIRES` (60 sn) verilir.
- Kayıtlı trunk'a çağrı yalnızca bir uzantı `Adjust` içinde (ya da `Incoming` cevabının rotalarında) `routing.ToTrunk` koyarsa yapılır; motor rotayı kayda çevirir (`next_hop`, `media`), kayıt yoksa 480. Hedef trunk'ın `max_calls`/`max_cps` değerleri ve trunk'ın müşterisinin `max_calls`/`max_cps` değerleri kayıt başına rotaya konur ve kabul betiğinde sağlayıcı rotaları gibi denetlenir (trunk: `cc:t:<trunk>`, `cps:t:<trunk>:<saniye>`, trunk'ın kendi yaptığı çağrılarla ortak; müşteri: `cc:c:<müşteri>`, `cps:c:<müşteri>:<saniye>`, müşterinin yaptığı çağrılarla ortak; biri doluysa sıradaki rota, yoksa 503; ADR-0042, T-45); kodek listesi rotanın `codecs` alanına girer, arayanın trunk'ıyla ortak kodek yoksa trunk rota sayılmaz (T-06). TLS ile kayıtlı trunk'a çağrı yalnızca kaydın geldiği bağlantıdan gider (`next_hop` `;transport=tls`; SIP çekirdeği yeni bağlantı açmaz: `NO_NEW_CONN` dal bayrağı); bağlantı kapanmışsa deneme hemen 503 ile başarısız sayılır ve sıradaki kayıt denenir (T-07). Bir çağrı her sayaçta bir kez sayılır: aynı trunk'ın başka kaydına ya da aynı müşterinin başka trunk'ına geçiş yeni çağrı değildir, müşterinin kendi trunk'ına yaptığı çağrı yalnızca arayan tarafında denetlenir. **KARAR (ADR-0042)**.

Ters yön, switch'in sağlayıcıya kaydı (T-14, ADR-0035):

```
M                      registrants.sh (SIP çekirdeği kabı)      SIP çekirdeği (uac_registrant)      Sağlayıcı
│◀── GET /v1/vendor-registrations?version= ──│                          │                              │
│── liste (değiştiyse, parolalarla) ────────▶│ tabloyu yazar ──────────▶│ reg_reload                   │
│                                            │                          │── REGISTER ─────────────────▶│
│                                            │                          │◀── 401 ──────────────────────│
│                                            │                          │── REGISTER + Authorization ─▶│
│                                            │                          │◀── 200 (Contact;expires) ────│
│── reg_list (MI, REG_STATUS_SEC) ──────────────────────────────────────▶│ durum → GET /v1/peers, ölçü │
```

- Kayıt SIP çekirdeğinin adresinden gider; motor SIP iletisi göndermez. Betik (OpenSIPS betiği) kayda hiç dokunmaz.
- Yenileme süre dolmadan `REG_TIMER_SEC` önce, başarısızlıkta yeniden deneme `REG_RETRY_SEC` sonra (sabit aralık).

### 5.6 Diğer yöntemler

| Yöntem | Davranış |
|---|---|
| OPTIONS (tanınan ya da kayıtlı adresten) | 200 |
| OPTIONS ve diğerleri (yabancıdan) | Cevapsız |
| ACK | Çağrı içiyse dialogla, değilse eşleşen işlemle iletilir |
| Çağrı içi PRACK, UPDATE, INFO, NOTIFY vb. | Dialogla eşleşirse olduğu gibi iletilir (erken dialogdaki PRACK da) |
| Çağrı içi REFER (iki yönden) | 403; karşı tarafa iletilmez, çağrı sürer; sayılır (`opensips_refer_refused`) |
| Çağrı dışı diğer her şey (SUBSCRIBE, MESSAGE, REFER…) | 405 |

**KARAR (ADR-0028, eski S-3), uygulandı (T-42):** erken medya (SDP'li 183) ve 100rel/PRACK olduğu gibi geçer: 183'teki SDP `onreply_route[media]` içinde her cevap gibi aktarıcıdan geçer (arayan aktarıcının adresini görür), `Require: 100rel`, `RSeq`, `RAck` değişmez, PRACK erken dialogda `topology_hiding_match` ile eşleşip iletilir. Çağrı içindeki REFER sabit bir kuralla reddedilir (karar değildir; motor sorulmaz). PRACK/UPDATE'teki SDP aktarıcıdan geçmez (sınanmadı). Sessiz erken medyalı çağrı, bağlantının çalma süresi daha uzun olsa da medya zaman aşımında (60 sn) kapanır: **KARAR (ADR-0038)**, §8.1. Ayrıntı ve senaryolar: `docs/08`, "Erken medya, PRACK ve aktarma".

## 6. Motor

### 6.1 Görüntü (snapshot)

- Yapılandırmanın tamamı (müşteriler, trunk'lar, tarifeler, planlar, kurallar, kurlar, paketler, güvenlik duvarı) PostgreSQL'den tek seferde yüklenir ve değişmez bir yapı olarak tutulur: `atomic.Pointer[routing.Snapshot]`.
- Yenileme: `NOTIFY softswitch_config` gelince hemen (art arda bildirimler birleştirilir), ayrıca `RELOAD_SEC` (10 sn) aralığıyla, ayrıca `POST /v1/reload`.
- Fiyatlar (`rates`) yalnızca değişen kısmıyla okunur (**KARAR ADR-0048**, T-30): veritabanındaki tetikleyici her yazılan fiyatın tarife ve önekini işlem kimliğiyle `rate_changes`'e not eder; yükleyici önceki yüklemenin veritabanı anlık durumunun görmediği notları okur, o önekleri tarifenin haritasının kopyasına yeniden okur, değişmeyen tarifelerin haritalarını yeni görüntüye paylaşarak geçirir. Geçerliliği başlayan/biten fiyatın tarifesi ve 10.000 satırdan büyük deyimin tarifesi bütünüyle okunur. Görüntünün geri kalanı her yüklemede tamdır; her `RELOAD_FULL_SEC` (300 sn) fiyatlar da baştan okunur. 5 milyon fiyatta tam yükleme ~8 sn ve 2,7 GiB ayırır; değişiklik yokken 10 ms, önek değişikliğinde ~0,25 sn.
- Görüntüyü yerinde değiştirme; yenisini kur ve işaretçiyi değiştir. Testte görüntüyü kopyalayıp alan değiştirme (kopya eski dizinleri taşır); baştan kur.
- API dışından veri değiştiren her kod `store.NotifyConfig` çağırır.

### 6.2 Çağrı yolunun bütçesi

- Çağrı başına: bellekten karar + **tek** Redis betik çağrısı. SQL yok, dosya yok, ağ üzerinden başka çağrı yok. İstisna: trunk rotası olan çağrıda bir Redis okuması daha (kayıtlar).
- Karar süresi p99 < 10 ms. Çağrı yoluna dokunan her değişiklikten sonra `make load CPS=200 CC=2000` ve `go test -bench Authorize ./pkg/routing`.
- `Adjust` ve `Incoming` çağrı yolundadır: yalnızca bellek, bekleme yok; `Incoming` kendisine verilen görüntüden karar verir ve onu değiştirmez.

### 6.3 Redis'teki durum

| Anahtar ailesi | İçerik |
|---|---|
| Çağrı durumu | `call_id` başına: müşteri, trunk, kalan rotalar, deneme sayısı, cevap anı, ayrılan tutar; bitmesi gereken andan sonra 24 saat daha saklanır |
| Süresi dolacak çağrılar | `expiry`: bitmesi gereken ana göre puanlı; geçen çağrının kaydını motor yazar (ADR-0029) |
| Eşzamanlı çağrı kümeleri | Müşteri (`cc:c:<müşteri>`: müşterinin yaptığı çağrılar ve trunk'larına teslim edilenler), trunk (`cc:t:<trunk>`: trunk'ın yaptığı çağrılar, limiti varsa, ve ona teslim edilenler, her zaman; iki yön tek sayaçta, saniye sayacı `cps:t:<trunk>:<saniye>` de ortak; **KARAR (ADR-0042)**; eski `cc:s:` kümelerini motor açılışta `cc:t:`'ye katar), bağlantı başına; çevrilen çağrılar (`cc:x`, sınırı `TRANSCODE_MAX`; ADR-0041); üyeler bitmesi gereken ana göre puanlı (kapanışı bildirilmeyen çağrı kendiliğinden düşer) |
| CPS sayaçları | `…:<saniye>` |
| Rezervasyon defterleri | `hold:` / `held:` / `heldsum:` + defter adı; defter müşteri kimliği ya da `engine.BundleLedger(paket, dönem)`; `holders` kümesi |
| Kayıtlar | `reg:<trunk>` |

Kabul çağrı başına tek betiktir (`admitScript`): sistem, ülke, müşteri ve trunk sayaçları, rezervasyon defterleri ve aday rotalar (sağlayıcı bağlantıları ve teslim edilecek trunk kayıtları; her biri küme, CPS sayacı, çeviri kümesi ve üç limit — çevrilmeyen rotanın çeviri limiti -1) aynı çağrıda; anahtarlar konumludur, argümanlar sondan sayılır (düzen `pkg/engine/calls.go`, betiğin üstündeki yorumda). Redis betikleri fiyat hesaplamaz; Go hesaplar, betik atomik olarak uygular. Anahtarlar tek Redis düğümü varsayar (TASKS: T-32).

### 6.4 Rezervasyon

Ayrıntı: `docs/08-cagri-arayuzu.md`, "Bakiye rezervasyonu". Özet: kabulde `RESERVE_SEC` (60) saniyelik tutar ayrılır; bitmesine 10 saniye kala bir adım daha; para yetmezse motor çağrıyı yettiği son saniyeden 1 saniye önce kapatır; çağrı bitince ayrılan tutar gerçek ücrete indirilir ve rater düşümü bildirene kadar (en geç 24 saat) ayrılmış kalır. Kesin tutarı her zaman rater belirler.

## 7. Eşzamanlılık modeli

| Bileşen | Model | Paylaşılan durum ve koruması |
|---|---|---|
| SIP çekirdeği | Çok süreçli (işçi süreçler), paylaşılan bellek; işlem ve dialog kilitlerini OpenSIPS yönetir | `authorize` sorusu `async()` ile sorulur: işçi beklemez, cevap gelince `route[authorized]` çalışır. Diğer sorular (`source`, `credentials`, `register`, `events`, `ban`) eşzamanlıdır ve işçiyi en çok `curl_timeout` kadar tutar |
| Motor | İstek başına goroutine (`net/http`) | Görüntü, sertifika ve sağlık hükümleri: `atomic.Pointer` (kilitsiz okuma). Limit/rezervasyon/çağrı durumu: Redis Lua betiği (atomik). Canlı sayaçlar, engel listesi, kayıtlı adres dizini: kendi kilitleri (`sync.Mutex`/`RWMutex`); ölçüler: tek atomik toplama |
| Motorun arka plan işleri | Ayrı goroutine'ler: görüntü yenileme, `LISTEN`, rezervasyon uzatma/kapatma ve süresi dolan çağrı zamanlayıcısı, sertifika denetimi, sağlayıcı yoklama, sağlayıcı kayıtlarının durumunu okuma, CDR günlüğünün yazıcısı, okuyucusu ve NATS'a gönderenleri | Çağrı yolunu hiçbir zaman bekletmezler; sonuçlarını atomik işaretçiyle yayımlarlar. Çağrı yolu CDR'yi yalnızca bellekteki kuyruğa koyar; diske tek yazıcı yazar, günlüğün kilidi altında dosya işlemi yapılmaz |
| rater | Tek dayanıklı tüketici (`rater`), toplu çekme, açık onay (`AckWait` 30 sn) | Tekillik veritabanında: `(call_id, leg)`; paket sayacı satır kilidiyle |
| api | İstek başına goroutine; her yazma tek işlemde (kayıt + denetim kaydı + varsa e-posta kuyruğu) | Zamanlanmış işler (uyarı taraması, rapor zamanlayıcısı) süreçte tek goroutine |
| trace-collector | Alıcı + sınırlı kuyruk + toplu yazıcı | Kuyruk dolarsa **atar ve sayar**; göndereni bekletmez |

Kurallar:

- Çağrı yolunda kilit altında giriş/çıkış yapılmaz.
- Aynı çağrı için olaylar farklı işçilerden, sırasız ve yinelenerek gelebilir; doğruluk Redis betiğinin atomikliğine ve olayların tekrar işlenebilirliğine dayanır, motorun belleğindeki sıraya değil.
- Birden çok motor düğümü aynı Redis'i paylaşabilir, ama her biri kendi görüntüsünü, canlı sayaçlarını ve sağlık hükümlerini tutar (TASKS: T-31).
- `metrics` ile bir seri süreç başına bir kez kaydedilir (ikinci kayıt panik verir).

## 8. Hata ve zaman aşımı stratejisi

İlke: **çağrı yolundaki her bağımlılığın tanımlı bir zaman aşımı ve tanımlı bir "ulaşılamadı" davranışı vardır; hiçbir çağrı belirsiz süre beklemez ve hiçbir hata sessizce "izin ver" anlamına gelmez** (tek bilinçli istisna: `ENGINE_TOKEN` boşken motorun herkese cevap vermesi).

### 8.1 Zamanlayıcılar

| Zamanlayıcı | Değer | Nerede | Dolunca |
|---|---|---|---|
| Sağlayıcıdan ilk cevap (`fr_timeout`) | 5 sn | `tm` | Deneme 408 ile başarısız; sıradaki rota |
| Kesin cevap (`fr_inv_timeout`) | 30 sn; bağlantının `ring_timeout_sec` değeri varsa o | `tm` | Sağlayıcıya CANCEL, deneme 408 |
| Çağrının azami süresi | `max_duration_sec` (en çok 7200 sn) | `dialog` | İki tarafa BYE |
| Cevaptan sonra ACK beklemesi | 32 sn (64×T1; betikte sabit), azami süre daha kısaysa o | `dialog` (200'de kurulur, ACK'te azami sürenin kalanına döner) | İki tarafa BYE; kayıt 200 ve gerçek süre (ADR-0026) |
| Sessiz çağrı (medya zaman aşımı) | `RTP_TIMEOUT`, 60 sn; bekletmede (bir taraf `sendonly`/`recvonly`/`inactive`) `RTP_HOLD_TIMEOUT`, 3600 sn | rtpengine (`timeout`, `silent-timeout`) | Aktarıcı oturumu siler, SIP çekirdeğine `teardown`; iki tarafa BYE; kayıt 200 ve gerçek süre (ADR-0027). Erken medyalı (SDP'li 183) çalan çağrıda da işler: sağlayıcıya CANCEL, arayana 487, kayıt 487; bağlantının `ring_timeout_sec` değeri 60'tan büyük olsa da (**KARAR, ADR-0038**; panelde ayarın açıklamasında yazılı) |
| Oturum zamanlayıcısı (RFC 4028) | Uygulanmaz; başlıklar uçtan uca geçer | — | Taraflar kendileri kapatır (ADR-0027) |
| Motora bağlanma / toplam | 1 sn / 2 sn | `rest_client` | §8.2 |
| Medya aktarıcısının cevabı | 1 sn, 5 deneme (modülün varsayılanı) | `rtpengine` modülü | Aktarıcı kenara konur, çağrı 503 (§8.2); 10 sn'de bir arka planda yeniden yoklanır (`rtpengine_disable_tout`) |
| Rezervasyon adımı / uzatma payı | 60 sn / bitişe 10 sn kala | Motor | Para yoksa çağrı kapatılır |
| Düşümü bildirilmeyen rezervasyon | 24 saat | Redis | Tutar bırakılır |
| Tanınan kaynak önbelleği | 300 sn | SIP çekirdeği | Yeniden sorulur |
| Engel önbelleği (taşkın / güvenlik duvarı) | 120 sn / 60 sn | SIP çekirdeği | Engel kalkar |
| Taşkın eşiği | 2 sn'de 30 istek (tanınmayan kaynak) | `pike` | Engel |
| Görüntü yenileme | 10 sn (+ bildirim); fiyatların tamamı her `RELOAD_FULL_SEC` (300 sn) | Motor | — |
| Sağlayıcı yoklaması | Bağlantı başına 10–3600 sn; yoklama en çok 3 sn; iki kayıp → "cevap vermiyor" | Motor | Rotalar karardan çıkar |
| Sağlayıcıya kayıt | Süre dolmadan `REG_TIMER_SEC` (30) önce yenilenir; başarısızsa `REG_RETRY_SEC` (60) sonra yeniden (sabit aralık) | SIP çekirdeği (`uac_registrant`) | Durum "failing", ölçü ve alarm; çağrılar etkilenmez |
| Sertifika denetimi | 60 sn | Motor | Reddedilen bağlantının rotaları çıkar |
| Kayıt süresi | 60–3600 sn; NAT/TCP/TLS'te en çok 60 sn | Motor | Kayıt düşer |
| Oturum belirteci / giriş kilidi | 12 saat / 5 hatada 15 dk | api | — |

### 8.2 Bağımlılık ulaşılamazsa

| Durum | Davranış |
|---|---|
| Motor cevap vermiyor ya da 200 dışı (`authorize`) | Çağrı 503 ile reddedilir (kapalı başarısızlık) |
| Motor cevap vermiyor (`source`) | İstek cevapsız bırakılır |
| Motor cevap vermiyor (`events failed`) | "Rota yok" sayılır; çağrı biter |
| Motor cevap vermiyor (`events answered`/`ended`) ya da SIP çekirdeği yeniden başladı | Çağrı sürer/biter; motor durumu kümedeki bitiş puanıyla düşer. Düşerken motor kaydı kendisi yazar: cevaplanmışsa 200 ve bitiş = cevap anı (süre ve ücret 0), değilse 408; ayrılan tutar bırakılır (ADR-0029, içerik ADR-0040: ayrılan süreye göre ücretlemek 30 saniyelik çağrıyı iki saate kadar ücretleyebilirdi; sağlayıcının faturasıyla elle uzlaşılır; `docs/08`, "Bitişi bildirilmeyen çağrı"). O ana kadar (azami süre + 300 sn) çağrı sayılı ve parası ayrılmış kalır |
| Sır uyuşmuyor (401) | Yukarıdakiyle aynı; ölçü ve alarm: `EngineRefusingCallers` |
| Redis ulaşılamaz | Motor 503 döner → çağrı reddedilir; süren çağrılar uzatılamaz ve kapatılamaz, `max_duration_sec`'e kadar sürebilir (yol haritası D3; ADR-0029'un ve T-33'ün kapsamı dışında kaldı) |
| PostgreSQL ulaşılamaz | Motor son görüntüyle çalışmaya devam eder; api ve rater durur; CDR'ler NATS'ta bekler |
| NATS ulaşılamaz | Motor kayıtları kendi diskindeki günlüğe yazar (`CDR_JOURNAL_DIR`, en çok `CDR_JOURNAL_MAX_MB`) ve NATS kabul edene kadar artan aralıkla (50 ms → 5 sn) yeniden dener; NATS kesikken motor yeniden başlar ya da çökerse günlükteki kayıtları açılışta yeniden gönderir (ADR-0029; `pkg/engine/journal.go`, `sink.go`; `docs/08`, "Çağrı kayıtlarının günlüğü"). Çağrı yolu diske yazmaz: kaydı bellekteki kuyruğa (200.000) koyar, arka plandaki yazıcı toplu yazar ve `fsync` eder. Motor NATS yokken de başlar. Günlük dolarsa ya da disk yazmazsa kayıtlar bellekte bekler (o arada motor durursa kaybolur; kuyruk da dolarsa yalnızca çağrı sonu olayı bekler); alarm verir |
| rater yavaş ya da kapalı | Çağrılar etkilenmez; ayrılan tutarlar bırakılmadığı için boşta bakiye olduğundan düşük görünür (güvenli taraf) |
| ClickHouse ulaşılamaz | CDR'ler `cdr_export_queue`'da bekler; rapor PostgreSQL'den verilir; iz iletileri atılır ve sayılır |
| rtpengine cevap vermiyor ya da port kalmadı | Yeni çağrı: deneme gönderilmez, arayana 503, motora `ended 503` ve `cause: "media"` (sıradaki rota denenmez, kayıt 503 ve seçilen bağlantıyla, ücret yok; bağlantının canlı sayaçlarına ve `OnLeg` ile kalitesine yazılmaz, ADR-0039); sayılır (`opensips_media_refused`) ve alarm verir (`CallsRefusedNoMedia`). Cevap vermeyen aktarıcı ilk fark edilişte ~5 sn beklenir (1 sn × 5), sonra kenara konur ve 10 sn'de bir arka planda yoklanır; o arada çağrılar beklemeden reddedilir (ADR-0030, eski S-5; `docs/08`, "Medya aktarıcısı teklifi almazsa"). Teklifsiz INVITE'ın 200'ündeki teklif (ADR-0043): 200 SDP'siz iletilir, iki tarafa bir saniye sonra BYE, motora `ended 503` ve `cause: "media"` (`answered` değil; ücret yok), sayılır. Kurulmuş çağrının yeni teklifi ya da cevabı (teklifsiz çağrı içi INVITE'ın 200'ü dahil): SDP değiştirilmeden iletilir (sınır; sınanmadı) |
| Sağlayıcıya hiç ulaşılamıyor (bağlantı reddi, TLS hatası) | `route[attempt]` 503 ile `failed` bildirir, sıradaki rota hemen denenir |
| MI soketi yok (`OPENSIPS_MI` boş) | Motor çağrı kapatamaz; yalnızca `max_duration_sec` kalır |

### 8.3 Tekrar işlenebilirlik

- `events`: yinelenen `answered` cevap anını değiştirmez; bitmiş ya da bilinmeyen çağrının olayı yok sayılır.
- CDR: NATS ileti kimliği + `(call_id, leg)` tekilliği; kayıt, bakiye düşümü, paket düşümü ve `cdr_export_queue` satırı tek işlemde.
- ClickHouse: `ReplacingMergeTree`; kesin sayı isteyen sorgu `FINAL` kullanır.
- REGISTER: aynı `Call-ID` ile daha küçük `CSeq` hiçbir şeyi değiştirmez.
- Zamanlanmış rapor ve düşük bakiye e-postası: ileti, onu doğuran durum değişikliğiyle aynı işlemde kuyruğa yazılır.

### 8.4 Hata kodları

Arayana dönen kodların tablosu `docs/08-cagri-arayuzu.md` içindedir (403, 402, 480, 503, 423…). Yeni bir ret nedeni eklerken: mevcut bir kodu ve yeni bir `reason` metnini kullan; kodun anlamını değiştirme; nedeni o tabloya ve senaryoya ekle.

## 9. Medya

- Medya her çağrıda rtpengine'den geçer (topoloji gizleme ve NAT için). Kernel aktarımı yalnızca Linux'ta; Mac'te kullanıcı alanı.
- Teklif bayrakları betikte kurulur: her zaman `replace-origin replace-session-connection ICE=remove`; kodek listesi varsa `codec-strip-all` + `codec-offer-…`; NAT arkasındaki taraf için `SIP-source-address`.
- SRTP: sağlayıcı ayağı yalnızca sinyalleşme TLS ise SRTP (SDES) ile teklif edilir; arayan ayağı arayanın teklifine uyar (RTP, SDES-SRTP, DTLS-SRTP). Kayıtlı trunk'a da aynı kural: TLS ile kayıtlıya (istek o bağlantıdan gider) SRTP, UDP ile kayıtlıya düz RTP (motor `media: "rtp"` der; T-07). Betik sinyalleşmenin TLS olup olmadığına isteğin gerçekten gittiği yere (`$du`, yoksa `$ru`) bakar.
- **Kodek çevirisi (ADR-0041, T-09):** varsayılan kapalı. Arayanın trunk'ının ve bağlantının (ya da hedef trunk'ın) kodek listeleri dolu ve ortak kodek yoksa rota atlanır; ikisinden biri `transcode` ile izin veriyorsa rota kalır, motor `route.transcode` (arayan tarafının listesi) ile bildirir ve rtpengine çevirir (`codec-mask-all` + `codec-transcode-…`; karşı uçtan gelen yeni teklife ters yön). Karar listelere göredir, SDP'ye değil. Aynı anda çevrilen çağrılar `TRANSCODE_MAX` (200) ile sınırlıdır; sınırda rota atlanır (kabul betiğinde, §6.3). Tek düğüm: çeviri aynı aktarıcıda yapılır; ayrı çeviri havuzu Enterprise'tadır. `telephone-event` iki tarafta kalır, T.38'e dokunulmaz (ADR-0031). İmajdaki kodekler `docs/09-kurulum.md`.
- **IPv6 (ADR-0047, T-28):** SIP çekirdeği `SIP_IP6` ile IPv6'da da dinler, aktarıcının iki ailede adresi olur. Teklifteki aktarıcı adresinin ailesini betik, isteğin gittiği yerin yazılışından seçer (köşeli parantezli adres IPv6, noktalı IPv4, ad: aktarıcıya bırakılır); bu bir karar değil, TLS → SRTP gibi mekanik bir eşlemedir. Motor IPv6 kaynak adresini girişte kısa biçime çevirir (OpenSIPS tam ve büyük harfle yazar); arayüz değişmedi.
- **Aktarıcı yoksa çağrı yok (ADR-0030):** yeni çağrının teklifini aktarıcı almazsa (cevap yok, port kalmadı ya da başka hata) deneme gönderilmez ve çağrı 503 ile reddedilir; sıradaki rota denenmez, çünkü sorun switch'tedir (`branch_route[out]` → `route[media_refused]`; §8.2). Biten çağrının portları hemen geri verilir (`rtpengine.conf`: `delete-delay = 0`; varsayılan 30 sn, yük testinde 200 CPS'te aralığı tüketiyordu). Ses hiçbir zaman aktarıcısız, taraflar arasında doğrudan kurulmaz; bu yalnızca yeni çağrı için geçerlidir, kurulmuş çağrının yeni teklifi aktarıcı yoksa değiştirilmeden geçer. Teklifsiz INVITE'ta teklif sağlayıcının 200'ündedir: aktarıcı onu almazsa 200 SDP'siz iletilip çağrı hemen kapatılır (`route[late_refused]`, ADR-0043).
- Ses kaydı yoktur ve yapılmayacaktır (ADR-0018).
- **Ölü çağrı (ADR-0027):** aktarıcı bir çağrının hiçbir akışından `RTP_TIMEOUT` (60) saniye paket görmezse oturumu siler ve SIP çekirdeğine `teardown` gönderir (A11, `b2b-url`, `xmlrpc-format = 1`); SIP çekirdeği iki tarafa BYE gönderir, motor `ended` alır. Bir taraf göndermeyeceğini bildirmişse (bekletme) süre `RTP_HOLD_TIMEOUT` (3600) saniyedir; tek yönde akan ses çağrıyı canlı tutar. Değerler şablon yer tutucusudur (`media/rtpengine/rtpengine.conf`, `deploy/images/rtpengine/run.sh`). Kapanan oturumlar aktarıcının ölçüsündedir: `rtpengine_closed_sessions_total{reason="timeout"}`.
- **Erken medya (ADR-0028):** SDP'li 183 her cevap gibi aktarıcıdan geçer (`rtpengine_answer`); aktarıcının oturumu bu andan itibaren iki tarafı da bilir ve zaman aşımı (yukarıda) çalarken de işler: hiçbir taraftan paket gelmeyen erken medyalı çağrı 60 saniyede kapanır, bağlantının çalma süresi daha uzun olsa da (**KARAR, ADR-0038**; senaryo: `make test-routing`, "deadcall"). SDP'siz 180'de aktarıcı yalnızca teklifi bilir ve çalan çağrıya dokunmaz.
- Faks (T.38), video, DTMF dönüşümü (RFC 2833 ↔ SIP INFO) için ayar yoktur. **KARAR (ADR-0031, eski S-6).** T.38 akışı yalnızca aktarılır (rtpengine'in T.38 ağ geçidi kullanılmaz); UDPTL paketleri medya sayılır, faks çağrısı paket aktığı sürece ölü çağrı diye kapatılmaz. `telephone-event` teklifte kalır, SIP INFO uçtan uca geçer; ikisi arasında çeviri yoktur (`docs/08`, "Faks (T.38) ve tuşlar"; TASKS: T-40).

## 10. CDR ve ücretlendirme hattı

```
Motor ──cdr.v1──▶ NATS JetStream ──▶ rater ──tek işlem──▶ PostgreSQL: cdrs + bakiye + bundle_usage + cdr_export_queue
                                                 └──toplu──▶ ClickHouse: cdrs (ReplacingMergeTree)
                                       rater ──bildirim──▶ Redis: düşüm yapıldı (rezervasyon bırakılır)
```

- Her deneme ayrı bacaktır. Tanınan müşterinin reddedilen çağrısı sağlayıcısız tek bacak olarak kaydedilir; bilinmeyen kaynağın çağrısı kaydedilmez. Bitişi bildirilmeyen çağrının kaydını, durumu düşerken motor yazar (ücretsiz; ADR-0029, ADR-0040).
- Motor kaydı NATS'a göndermeden önce kendi diskindeki günlüğe yazar ve NATS kabul edince siler; açılışta günlükte kalanları yeniden gönderir. Tekrar zararsızdır (ileti kimliği `call_id:leg`, rater'da `(call_id, leg)` tekilliği). Tek kalan pencere: olay ile toplu yazımın `fsync`'i arasındaki birkaç milisaniye.
- PostgreSQL asıl kayıttır. ClickHouse kopyadır; eski kayıtlar PostgreSQL'den ancak ClickHouse'ta kimliğiyle doğrulandıktan sonra silinir (`CDR_KEEP_DAYS`). Silen her geçiş arşiv ufkunu (`cdr_archive.horizon`) ileri taşır; API ufuktan önceki kayıtları (tekil kayıt, liste, CSV) ClickHouse'tan okur (T-24).
- Para `int64` mikro birimdir (1/1.000.000); hiçbir yerde kayan nokta yoktur (ADR-0004). Kur çevirme 128 bit ara çarpımla, yarım birim yukarı.
- Fiyatı motor da rater da `rating.Tidy` + `rating.Select` ile seçer; tutarı `rating.Rate.AmountAfter` hesaplar. Bu mantığı SQL'e ya da ikinci bir yere kopyalama.
- Çağrı kaydına alan eklemek dört yeri birlikte değiştirir: `engine.CDR`, `api.CDR`, `cdrs` tablosu (migration), ClickHouse şeması; listenin ClickHouse satırı (`pkg/api/cdrlist.go`, `cdrCols`) PostgreSQL'inkiyle aynı alanı vermeli (`make test-reports`). Yükseltmede rater motordan önce yükseltilir. Yalnızca bazı bacaklarda anlamı olan alan motorda `omitempty` yazılır (ör. `dst_trunk_id`, T-05): böylece alanı bilmeyen eski rater olağan kayıtları yükseltme sırasında da alır.
- Rapor sorguları PostgreSQL ve ClickHouse için ayrı yazılıdır ve aynı alanları vermek zorundadır (`make test-reports`).

## 11. Yönetim API'si ve panel

- Kaynakların çoğu genel CRUD ile tanımlıdır (`pkg/api/crud.go`: `Resource`); özel işlemler ayrı dosyalardadır.
- Her yazma: tek işlem → kayıt + `audit_log` satırı (`Server.Audit`) → `NOTIFY softswitch_config`. Hiçbir şeyi değiştirmeyen çağrı `AuditNothing` kullanır. Sırlar denetim kaydında alan adına göre maskelenir.
- Kimlik: API anahtarı ya da e-posta + parola (bcrypt) ile alınan 12 saatlik oturum; isteğe bağlı iki adımlı doğrulama (TOTP); hesap onu herkese zorunlu kılabilir, kurmamış kullanıcının oturumu yalnızca kuruluma açılır (ADR-0045). Roller: `admin`, `readonly`.
- Görünürlük: Community tek hesaplıdır; her kayıt kök hesaba aittir. Enterprise `SetScope` ile "kendi alt ağacı" kuralını koyar.
- Listeler sunucu taraflı sayfalanır.
- Panel: `web/src/panel.tsx` (menü ve `groups`), `web/src/screens/`, ortak bileşenler `web/src/ui/kit.tsx`. Enterprise ekranları özel depoda yazılır ve `createPanel(...extensions)` ile eklenir; Community panelinde Enterprise'a ait hiçbir iz görünmez.

## 12. Açık çekirdek sınırı

- Bu depo Community'dir (AGPL-3.0-only). Enterprise ayrı, özel depodadır ve bu depoyu içe aktarır; tersi asla olmaz.
- Enterprise özelliği (bayi hiyerarşisi, fatura, DID, kümeleme, medya havuzu, kalite tabanlı yönlendirme, dolandırıcılık tespiti, lisans) buraya yazılmaz; yalnızca genişleme noktası ve tek hesaplı/tek düğümlü varsayılanı eklenir.
- Genişleme noktaları (ADR-0008) iki depo arasındaki sözleşmedir; imzalarını değiştiren her değişiklikten sonra Enterprise deposu da derlenir ve test edilir:
  - **API** (`pkg/api`): `Extension`, `AddResource`, `Handle`, `HandlePublic` (kimlik doğrulamasını uzantının yaptığı uçlar), `HashPassword`/`CheckPassword`, `WriteErr`, `QueueMail`/`MailEnabled`/`NewResetToken`/`Amount` (e-posta), `OnAlertMail` (düşük bakiye e-postasının gidip gitmeyeceği), `Audit`/`AuditEntry`/`AuditNothing`/`AuditSnapshot`/`AuditDiff` (denetim kaydı), `ClientIP`, `SetScope`, `AddParty`, `OnCDRPrice`, `OnCDRStored`, `AllowCDRField`, `SetSnapshotLoader`, `SetEdition`. route-engine ve rater da aynı `api.Server` üzerinden çalışır (`LoadSnapshot`, `IngestCDRs`, `RootCaller`); uzantının kuralları böylece üç serviste de geçerlidir.
  - **Motor** (`engine.Config`): `Adjust` (kabul edilecek çağrının rotalarını sıralar, çıkarır ya da çağrıyı reddeder; çağrı yolunda, yalnızca bellek; `routing.ToTrunk` ile çağrı müşterinin kayıtlı trunk'ına verilebilir), `OnLeg` (her denemenin ve reddin sonucu; `Leg.NotSent`: SIP çekirdeğinin gönderemediği deneme, sağlayıcıya sayılmaz), `Incoming` (kaynağı hiçbir trunk'a uymayan çağrıyı bir müşteri adına alır; ADR-0032), `Peers` (`engine.PeerCheckFromEnv`), `MaxCalls`.
  - **Panel:** `createPanel(...extensions)` (`web/src/lib/session.tsx`, `PanelExtension`).
- OpenSIPS ve rtpengine GPL'dir: yalnızca ağ üzerinden konuşulur; kodları bağlanmaz, kopyalanmaz, onlara modül yazılmaz.

## 13. Dağıtım

- Laboratuvar: `deploy/compose/` (`make lab-up`). Üretim: `deploy/prod/`, Docker Compose ile tek sunucu (`docs/09-kurulum.md`).
- `opensips.cfg` ve `rtpengine.conf` şablondur: çift alt çizgi arasındaki adlar konteyner açılırken ortam değişkenlerinden doldurulur. Adres ya da alan sabit yazılmaz.
- Şema adımları danışma kilidiyle sırayla uygulanır (`store.MigrateFS`); ClickHouse dosyaları tek ifade içerir ve yeniden çalıştırılabilir olmalıdır.
- Çok düğümlü kurulum (aktif/yedek SIP, dialog çoğaltma, Redis/PostgreSQL/NATS kümeleri) **tasarlanmadı**; Enterprise işidir ve gerçek sunucu bekler. **AÇIK SORU (S-7).**

## 14. AÇIK SORU listesi

Üstü çizili sorular 2026-11-23'te karara bağlandı; karar kaydı yanında. Metindeki eski "AÇIK SORU" işaretleri "KARAR" olarak değişti; uygulanana kadar o bölümdeki "bugün" anlatımı geçerlidir.

| No | Soru | Nerede |
|---|---|---|
| ~~S-1~~ | ~~Oturum zamanlayıcıları (RFC 4028): çekirdek bacak başına uygulayacak mı?~~ — karar: ADR-0027 | §5.2 |
| ~~S-2~~ | ~~`authorize` beklenirken gelen CANCEL'da motor durumunun kapanışı~~ — kapandı (T-01): durum temizleniyordu, ama deneme 503 ile bildiriliyordu; betik düzeltildi | §5.4 |
| ~~S-3~~ | ~~REFER, PRACK/100rel ve erken medya politikası~~ — karar: ADR-0028 | §5.6 |
| ~~S-4~~ | ~~NATS ulaşılamazken motorun bellekteki CDR kuyruğu motor çökerse kaybolur: kapatılacak mı, nasıl?~~ — karar: ADR-0029; uygulandı (T-33) | §8.2, §10 |
| ~~S-5~~ | ~~rtpengine cevap vermezse çağrı reddedilmeli mi?~~ — karar: ADR-0030; uygulandı (T-39) | §8.2 |
| ~~S-6~~ | ~~Faks (T.38), DTMF dönüşümü, video~~ — karar: ADR-0031 | §9 |
| S-7 | Yüksek erişilebilirlik tasarımı: dialog çoğaltma, sanal IP, Redis kümesinde anahtar düzeni, düğümler arası bakiye görüntüsü | §13 |
| ~~S-8~~ | ~~Pazar yeri modül modeli: derlenmiş ve lisansla açılan mı, sonradan kurulan mı?~~ — karar: ADR-0037 | ADR-0022 |
| ~~S-9~~ | ~~Trunk ağlarında `/8` (IPv4) ve `/16` (IPv6) genişlik sınırı kalsın mı?~~ — karar: ADR-0034 | ADR-0020 |
| S-10 | Hedef pazarlarda ABD/Kanada var mı (STIR/SHAKEN gerekir)? | TASKS: T-13 |
| S-11 | Numara taşınabilirliği hangi pazarlar için ve hangi sorgu sağlayıcısıyla? | TASKS: T-12 |
| ~~S-12~~ | ~~Gelen (müşteri olmayan kaynaktan) çağrı için genişleme noktasının biçimi~~ — karar: ADR-0032 | TASKS: T-04 |
| ~~S-13~~ | ~~Kayıtlı trunk'a canlı tutma (OPTIONS) gönderilecek mi, yoksa kısa süreli kayıt kalıcı çözüm mü?~~ — karar: ADR-0033 | ADR-0013 |
| S-14 | Motor arayüzünde TLS ve arayan başına ayrı sır; MI soketinin ve MI'ın XML-RPC kapısının (A11) korunması | ADR-0016 |
| S-15 | SSO için kimlik sağlayıcı türü (OIDC, SAML ya da ikisi) | TASKS: T-22 |
| S-16 | Katkı sözleşmesi (CLA) metni | ADR-0007 |
| ~~S-17~~ | ~~Cevaplanan çağrıyı ACK'lemeyen arayan: çağrı ne zaman kapatılmalı?~~ — karar: ADR-0026 | §5.4 |
| ~~S-18~~ | ~~Erken medyası sessiz (SDP'li 183, hiç paket yok) ve 60 saniyeden uzun çalan çağrı: medya aktarıcısının zaman aşımı mı, bağlantının `ring_timeout_sec` değeri mi hükmeder?~~ — karar: ADR-0038 (zaman aşımı hükmeder) | §8.1, §9 |
| ~~S-19~~ | ~~Trunk'ın eşzamanlı çağrı sınırı (`max_calls`) iki yönde ayrı mı sayılmalı, yoksa toplam mı?~~ — karar: ADR-0042 (toplam, tek sayaç); uygulandı (T-45) | §6.3; `docs/08`, "Müşterinin trunk'ına çağrı" |
| ~~S-20~~ | ~~Trunk'a teslim edilen çağrıya hedef trunk'ın **müşterisinin** limitleri (`clients.max_calls`/`max_cps`) uygulanmalı mı?~~ — karar: ADR-0042 (uygulanır; müşterinin sınırı da iki yönü birlikte sayar); uygulandı (T-45) | §5.5 |
