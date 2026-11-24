# ADR-0048: Görüntü yüklenirken fiyatların yalnızca değişeni okunur; değişikliği veritabanındaki tetikleyici not eder

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-24
- **Kapattığı görev:** T-30
- **İlgili:** ADR-0003 (çağrı yolunda SQL yok; bu kayıt onun kararını değiştirmez, "yükleme her seferinde tamdır" sonucunu değiştirir)

## Bağlam

Motor görüntüyü her bildirimde ve en geç `RELOAD_SEC` (10 sn) aralığıyla yeniden yükler; aralıklı yükleme bakiyeler için gereklidir (ADR-0005). Yükleme tamdı. T-30'da ölçüldü (laboratuvar PostgreSQL'i, Mac; ayrı veritabanında 5 milyon geçerli fiyat, bunların yirmide biri için hafta sonu fiyatı, onda biri kadar geçmiş fiyat; `make bench-snapshot`):

| | Süre | Bellek |
|---|---|---|
| Tam yükleme (5 milyon fiyat) | 7,5–9,8 sn (sunucuda sorgu 1,2 sn; kalan aktarım, çözme, harita kurma ve çöp toplama) | Görüntü 1.308 MiB; yükleme boyunca 2.707 MiB ayrılır; eski görüntü tutulurken (motorun her yenilemesi) en yüksek 3,8 GiB |
| Yük altında, 1.000 karar/sn, her 10 sn'de tam yükleme | Yükleme 7,7–10,3 sn: sürenin %80'i yükleme | Yığın en çok 6,4 GiB; karar p99 22,6–23,4 ms (bütçe 10 ms), yükleme sırasında 25–32 ms, en yüksek 236 ms |

Öneri hedef (2 sn) dört katı aşılıyor; aralık 10 sn olduğu için motor neredeyse sürekli yüklüyor ve karar süresi bütçeyi aşıyor. Sorunun kaynağı fiyatlardır: görüntünün geri kalanı (müşteriler, trunk'lar, planlar, bağlantılar, kurallar, paketler) aynı ölçekte küçük kalır ve tam okunması sürer.

## Seçenekler

1. **Tam yükleme, daha hızlı kod** (ara harita yok, paralel sorgu, çöp toplama ayarı): aktarım ve çözme tek başına ~2,4 sn; hedefe inmez, bellek aynı kalır.
2. **Bildirim "ne değişti" bilgisini taşır** (`NOTIFY` yükü): yalnızca API'nin yazdığını bilir; doğrudan SQL ile, rater'dan ya da Enterprise'tan gelen yazmayı ve zamanı gelen fiyatı (`effective_from`/`effective_to`) bilmez; bildirim kaybolursa (bağlantı koptu) değişiklik bir sonraki tam yüklemeye kadar görünmez.
3. **Tarife başına sürüm:** değişen tarife bütünüyle okunur. Denendi: 5 milyonluk tarifede tek fiyat değişince yine 8,7 sn.
4. **Fiyat düzeyinde değişiklik notu, veritabanı tetikleyicisiyle** ve önceki görüntüden alınan değişmemiş kısımlar.

## Karar

Dördüncü seçenek.

- `rates` üzerindeki deyim düzeyindeki tetikleyiciler (`0031_rate_changes.sql`) yazılan her satırın tarifesini ve önekini, yazan işlemin kimliğiyle (`xid8`) `rate_changes`'e yazar; ekleme, güncelleme (eski ve yeni önek), silme ve `truncate` (bütün tarifeler). 10.000 satırdan büyük deyim yalnızca tarifeyi yazar (önek boş: tarife bütünüyle okunur). Notlar bir saat tutulur, her yazmada eskileri silinir.
- Yükleme (`store.LoadSnapshot`, `pkg/store/rates.go`) önce veritabanının anlık durumunu (`pg_current_snapshot()`) ve saatini alır, sonra önceki yüklemenin anlık durumunun **görmediği** işlemlerin notlarını okur (`pg_visible_in_snapshot`): işlemlerin bitiş sırası ne olursa olsun kaçan değişiklik olmaz; arada biten bir yazma iki kez okunur, hiç okunmaması olmaz. Notlu önekler yeniden okunur ve tarifenin önceki haritasının **kopyasına** yazılır (eski görüntü kullanımdadır, yerinde değiştirilmez). Önekli notu olmayan, ya da bir fiyatının geçerliliği başlayan ya da biten (tarife başına sonraki an saklanır; `rates (tariff_id, effective_from)` dizini) ya da önceki yüklemede olmayan tarife bütünüyle okunur. Değişmeyen tarifelerin haritaları yeni görüntüye olduğu gibi geçer (paylaşılır, hiçbir görüntü değiştirmez).
- **Yedek tam yükleme:** her `RELOAD_FULL_SEC` (varsayılan 300 sn; en çok 1.800, notların saklanma süresinin yarısı) bütün fiyatlar baştan okunur. Önceki yükleme yarım saatten eskiyse de tam okunur. `RELOAD_FULL_SEC=0` her yüklemeyi tam yapar (eski davranış). Yükleme durursa (API sürecinde ara sıra çalışan simülatör) saklanan fiyatlar iki aralık sonra bırakılır.
- Arayüzler değişmez: `store.LoadSnapshot`, `api.Server.LoadSnapshot`, `SetSnapshotLoader` aynı imzayla çalışır; Enterprise yükleyicisi çekirdek yükleyiciyi çağırdığı için kendiliğinden yararlanır. `store.NotifyConfig` yük taşımaz. Bildirim ve `RELOAD_SEC` yenilemesi ADR-0003'teki gibi kalır.

## Sonuçları

- Ölçüm (aynı veri; ayrıntı `docs/TASKS.md` T-30): değişiklik yokken yükleme 8–15 ms; tek fiyat ya da 1.000 fiyat değişince 0,23–0,24 sn (5 milyonluk haritanın kopyası ~0,2–0,3 sn ve geçici olarak +1,1 GiB); 10.000'den büyük deyim ya da zamanı gelen fiyat tarifeyi bütünüyle okur (5 milyonda ~8 sn, eskisi gibi). Yük altında (1.000 karar/sn, her 10 sn'de 1.000 fiyat değişikliği): yükleme 0,4–1,1 sn, karar p99 5,1–5,8 ms, yığın en çok 4,5–4,9 GiB. Yükleme süren ~1 sn içinde karar p99 29–45 ms'ye çıkar (kopya ve çöp toplama): eskisinden kısa ve seyrek, ama bütçenin üstünde.
- Kalan açık: büyük bir tarifede her değişiklik haritayı kopyalar. Kopyasız bir yapı (değişenleri ayrı katmanda tutmak) `routing.Snapshot`'ın tarife alanlarının biçimini, dolayısıyla çağrı yolunun aramasını değiştirir; gerekirse ayrı karar ister.
- Bir saatten uzun açık kalan bir işlemin notu, yazıldığı anla saklandığından, işlem bitmeden silinebilir; yedek tam yükleme (en geç `RELOAD_FULL_SEC`) onu da yakalar.
- Tetikleyiciyi atlayan yazma (ör. `session_replication_role = replica` ile yükleme) da yalnızca yedek tam yüklemede görünür.
- Görüntü yükleyen her süreç (motor, API'nin simülatörü) büyük tarifeyi bellekte saklar; API sürecinde simülatör kullanılmazsa iki aralık sonra bırakılır.
- Rotalar (`routing_entries`) ve diğer tablolar her yüklemede tam okunur; büyürlerse aynı yol kullanılabilir.
