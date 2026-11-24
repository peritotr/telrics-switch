# Katkı

Katkınız için teşekkürler. Lütfen bir değişiklik göndermeden önce bu sayfayı okuyun.

> **English summary.** Contributions are welcome, but no contribution can be merged until its author has signed the Contributor License Agreement (CLA). The CLA document will be published by the maintainers; until then, pull requests can be discussed but not merged. Code, comments and commit messages are in English; documentation under `docs/` and the panel UI are in Turkish. Please read `docs/ARCHITECTURE.md` and the decision records in `docs/decisions/` before proposing a change; the invariants and code standards below are binding. Report security issues as described in `SECURITY.md`, not as public issues.

## Katkı sözleşmesi (CLA)

Bu depodaki kod AGPL-3.0-only ile yayımlanır ve aynı kod ticari lisanslı Enterprise sürümünün de temelidir. Bir katkının iki lisans altında da dağıtılabilmesi için katkı sahibinin bu hakkı açıkça tanıması gerekir; bu, katkı sözleşmesiyle (Contributor License Agreement, CLA) yapılır.

- CLA belgesi proje sahipleri tarafından ayrıca yayımlanacaktır. Bu sayfa CLA'nın kendisi değildir ve onun yerine geçmez.
- **İmzalanmış CLA'sı olmayan katkı birleştirilmez.** CLA yayımlanana kadar çekme istekleri (pull request) tartışılabilir, ancak birleştirilemez.
- Başkasına ait kodu (ör. işvereninizin hakları olan kodu) katkı olarak göndermeden önce gerekli izni aldığınızdan emin olun.

## Başlamadan önce

- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md), [`docs/decisions/`](docs/decisions/README.md) ve [`docs/TASKS.md`](docs/TASKS.md) dosyalarını okuyun. Ayrıntı: [`docs/08-cagri-arayuzu.md`](docs/08-cagri-arayuzu.md) (motor ↔ SIP çekirdeği arayüzü), [`docs/06-api.md`](docs/06-api.md), [`docs/07-panel.md`](docs/07-panel.md), [`docs/09-kurulum.md`](docs/09-kurulum.md), [`docs/10-izleme.md`](docs/10-izleme.md).
- **Kararlar bağlayıcıdır.** Bir karar kaydına (ADR), `docs/ARCHITECTURE.md` içindeki bir kurala ya da aşağıdaki değişmez kurallara aykırı düşen bir değişiklik gerekiyorsa önce bir hata kaydı (issue) açıp tartışın. Karar değişirse eskisini "Yerini aldı" diye işaretleyen yeni bir ADR yazılır ve mimari belgesi aynı değişiklikte güncellenir.
- Belgelerde **AÇIK SORU** diye işaretlenen noktalara dayanan işler için önce karar gerekir.
- Enterprise sürümüne ait özellikler (bayi hiyerarşisi, fatura, DID, kümeleme, medya havuzu, kalite tabanlı yönlendirme, dolandırıcılık tespiti, lisans doğrulama) bu depoya yazılmaz; yalnızca genişleme noktaları ve tek hesaplı/tek düğümlü varsayılanlar buradadır ([`docs/04-acik-cekirdek-modeli.md`](docs/04-acik-cekirdek-modeli.md); genişleme noktalarının listesi `docs/ARCHITECTURE.md` §12). Panelde Enterprise özelliği hiçbir biçimde görünmez (kilitli ekran, soluk menü ya da etiket yok).

## Değişmez kurallar

- **Çağrı yolunda SQL sorgusu yok.** route-engine bellekteki görüntüden karar verir; çağrı başına yalnızca Redis'e, tek bir betikle gider (ADR-0003).
- **Karar süresi bütçesi: p99 < 10 ms.** Çağrı yoluna dokunan her değişiklikte benchmark çalıştırın (`go test -bench Authorize ./pkg/routing`, `make load CPS=200 CC=2000`).
- **Para tamsayıdır.** Tutarlar en küçük birimde (1/1.000.000) `int64`; Go, SQL, Lua ve TypeScript'te para için kayan nokta yok (ADR-0004). Fiyat seçimi ve tutar yalnızca `pkg/rating` ile (ADR-0010).
- **CDR kaybolmaz.** Olaylar kuyruğa en-az-bir-kez yazılır; tüketiciler tekrar işlemeye dayanıklıdır (ADR-0006, ADR-0029).
- **SIP çekirdeği ile route-engine arasındaki arayüz sürümlüdür** ve SIP sunucusundan bağımsızdır; OpenSIPS'e özgü kavram arayüze sızmaz (ADR-0002).
- **Her SIP özelliği bir SIPp senaryosuyla gelir.** Senaryosu olmayan SIP davranışı tamamlanmış sayılmaz.
- **Şema yalnızca migration dosyasıyla değişir;** her migration var olan satırları eski davranışında bırakır. ClickHouse dosyası tek ifadedir ve yeniden çalıştırılabilir.
- **`core`, `enterprise`'ı asla içe aktarmaz.** OpenSIPS ve rtpengine GPL'dir: onlarla yalnızca ağ üzerinden konuşulur, kodları bağlanmaz ya da kopyalanmaz (ADR-0009).

## Kod standartları

- **Go:** `gofmt` ve `go vet` temiz; hata yutulmaz, bağlamıyla sarılıp döndürülür (`fmt.Errorf("…: %w", err)`); panik yalnızca programcı hatasında (ör. aynı ölçünün iki kez kaydı). Dışa açık her ad ve açık olmayan her karar için "neden"i söyleyen kısa yorum.
- **Çağrı yolu** (`pkg/routing`, `pkg/rating`, `pkg/engine` içindeki karar ve olay kodu): giriş/çıkış yalnızca tek Redis betiği; kilit altında bekleme yok; paylaşılan durum `atomic.Pointer` ile değiştirilir, yerinde değiştirilmez.
- **OpenSIPS betiği ince kalır:** karar içermez; her `rest_post` öncesinde `route(engine_token)`; adres ve alan sabit yazılmaz (betik şablondur, `docs/09-kurulum.md`). Bu OpenSIPS sürümünde (3.6.10) `async()` öncesinde isteği değiştiren komut (`consume_credentials`, `fix_nated_contact`, `remove_hf`, `append_hf` …) kullanılmaz; isteği devam rotasında değiştirin (`docs/ARCHITECTURE.md` §4).
- **Testler:** saf mantık birim testiyle, API uçtan uca testle (`make test-api`), SIP davranışı SIPp senaryosuyla (`make test-routing`). Zamana bağlı test sabit tarih kullanırken başka testin "şimdi" penceresine düşmemeli.
- **API:** yeni uç `s.Handle` ya da `s.HandlePublic` ile, özeti ve gövde türleriyle kaydedilir (OpenAPI oradan üretilir; `docs/06-api.md`, "Yeni bir ucun tanımı"). Yazan her işlem denetim kaydını aynı işlemde yazar (`Server.Audit`; hiçbir şeyi değiştirmeyen çağrıda `AuditNothing`).
- **TypeScript:** `npx tsc --noEmit -p .` temiz; ortak bileşenler `web/src/ui/kit.tsx`. Arayüz Türkçedir, `text-transform: uppercase` kullanılmaz; tutarlar `web/src/lib/format.ts` ile biçimlenir; oturum anahtarı yalnızca bellekte tutulur (tarayıcı deposuna yazılmaz); listeler sunucu taraflı sayfalanır. Tasarımda olmayan ekran ve durumlar ortak bileşenlerle yazılır ve `docs/07-panel.md` içindeki sapmalar tablosuna eklenir.
- **Sırlar** (parola, belirteç, anahtar) günlüğe, hata iletisine, denetim kaydına, test çıktısına ve depoya yazılmaz; laboratuvar değerleri tohum dosyalarından okunur.
- Yeni her kaynak dosyası `SPDX-License-Identifier: AGPL-3.0-only` başlığı taşır.
- **Dil:** kod, yorumlar ve commit iletileri İngilizce; `docs/` ve arayüz Türkçe.
- Bir değişiklik, ilgili belgeler güncellenmeden bitmiş sayılmaz: dokunulan alanın belgesi, `docs/03-yol-haritasi.md` ve `docs/05-ozellik-katalogu.md`.

## Komutlar

| Komut | Ne yapar |
|---|---|
| `make lab-up` / `make lab-down` | Laboratuvar ortamı (OpenSIPS, rtpengine, SIPp, PostgreSQL 127.0.0.1:56432, Redis 127.0.0.1:56379, ClickHouse 127.0.0.1:58123, posta yakalayıcı 127.0.0.1:58025, NATS, route-engine 127.0.0.1:58090, rater, trace-collector); önce laboratuvar sertifikalarını üretir. Go kodunu değiştirince `make lab-up` yeniden derler |
| `make lab-seed` | Laboratuvar müşterisi, sağlayıcıları ve rotaları (`test/seed/lab-routing.sql`) |
| `make test` | Birim testleri (`go test ./...`; laboratuvar gerekmez) |
| `make test-engine` | route-engine testleri (laboratuvar Redis'inde 15 numaralı veritabanını her seferinde siler) |
| `make test-api` | API uçtan uca testi (laboratuvar PostgreSQL'inde `softswitch_test` veritabanını her seferinde siler) |
| `make test-openapi` | API tanımının Redocly denetimi (`npx` gerekir) |
| `make test-routing` | Gerçek çağrılarla yönlendirme senaryoları (SIPp) |
| `make test-journal` | NATS kesikken motor ölünce çağrı kaydının kaybolmadığının senaryosu (tek başına çalıştırın) |
| `make test-reports` | Özet raporu ve çağrı listesini ClickHouse ile PostgreSQL'den alıp karşılaştırır (`make api-run` gerekir) |
| `make test-archive` | Eski çağrı kayıtlarının arşivlenmesi ve ClickHouse'tan okunması |
| `make test-mail` | Parola sıfırlama e-postası, laboratuvar posta yakalayıcısı üzerinden (`make api-run` ve `make seed-user` gerekir) |
| `make test-nft` | Örnek ağ katmanı kurallarının denetimi, atılan bir konteynerde |
| `make test-call` | OpenSIPS, route-engine ve rtpengine üzerinden tek çağrı |
| `make load CPS=<n> CC=<n>` | Yük testi; rapor ve PASS/FAIL (karar süresi p99 bütçesi dahil) |
| `make bench-snapshot` | 5 milyon fiyatlı görüntünün yükleme süresi ve belleği |
| `BOOTSTRAP_API_KEY=... make api-run` | API'yi 127.0.0.1:58080'de çalıştırır |
| `make seed-user` / `make seed` | Laboratuvar panel kullanıcısı / panel için örnek veri (`test/seed/dev.env`) |
| `make panel-install` / `make panel-dev` / `make panel-build` | Paneli 127.0.0.1:58081'de çalıştırır (API çağrılarını 58080'e iletir) / `web/dist` üretir (`WEB_DIR=web/dist` ile API sunar) |
| `make prod-smoke` | Tek sunucu kurulumunu (`deploy/prod`) özel bir Docker ağında uçtan uca dener: kurulum, çağrı, çökme, yedek ve geri yükleme, yükseltme |

Notlar: OpenSIPS'in arm64 paketi olmadığı için imaj kaynaktan derlenir (ilk kurulum birkaç dakika sürer). SIPp'in yerleşik `uac`/`uas` senaryoları Record-Route'a uymaz; `test/sipp/` altındakileri kullanın. `test/sipp/run.sh` çalışırken dosyayı düzenlemeyin.

## Çekme isteği göndermeden önce

```sh
gofmt -l . && go vet ./... && go build ./... && go test ./...
(cd web && npx tsc --noEmit -p .)
```

SIP davranışına dokunduysanız laboratuvarı kaldırıp (`make lab-up`) `make test-routing` çalıştırın; API'ye dokunduysanız `make test-api`; çağrı yoluna dokunduysanız `make load CPS=200 CC=2000 DURATION=30`.

## Güvenlik

Güvenlik açıklarını herkese açık hata kaydı olarak bildirmeyin; [`SECURITY.md`](SECURITY.md) içindeki yolu izleyin.
