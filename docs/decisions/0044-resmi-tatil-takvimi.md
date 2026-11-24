# ADR-0044: Resmî tatil, hesabın takviminde başka bir gün sayılır; pencere kodu tatili kendisi bilir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı görev:** T-19

## Bağlam

Fiyat ve rota pencereleri (`days`, `time_from`, `time_to`; ADR-0010) haftanın gününe bakar. Resmî tatilde operatörler çoğunlukla pazar fiyatını uygular; takvim olmadığı için tatil günü kendi haftanın günü sayılıyordu (ADR-0010: "Resmî tatil takvimi yoktur"). Karar verilmesi gerekenler: takvim kimin, tatilin hangi saat dilimiyle okunduğu, rota pencerelerinin de tatile uyup uymadığı, yarım gün tatil, takvimin çağrı yoluna nasıl ulaştığı.

## Seçenekler

1. **Takvim kimin:** (a) hesabın tek takvimi; (b) tarife başına takvim; (c) ikisi birden.
2. **Saat dilimi:** (a) tarifenin ya da planın saat dilimi (pencereyle aynı); (b) hesabın ayrı bir saat dilimi; (c) UTC.
3. **Rota pencereleri:** (a) onlar da tatile uyar; (b) yalnızca fiyatlar.
4. **Kod:** (a) tatili `rating.Window.Covers` bilir (takvim ona verilir); (b) motor ve rater tatil gününü önce hesaplayıp pencereye başka bir an verir.

## Karar

- **1a.** `holidays` tablosu (`reseller_id`, `day`, `counts_as`, `name`; şema `0027_holidays.sql`), hesap başına bir takvim; bir tarih bir kez. Bir tarife ya da plan, **sahibi olan hesabın** takvimini kullanır. Community tek hesaplıdır, yani tek takvim vardır. Tarife başına takvim gerekirse sonra genişleme noktası olarak eklenir.
- **`counts_as`**: tatilin sayıldığı gün (pazartesi 1 … pazar 7), varsayılan pazar. Tatil yalnızca gün kümesi sınırlı pencereleri etkiler; `days` = 127 (her gün) olan pencere ve penceresiz fiyat/rota tatilden etkilenmez.
- **2a.** Bir anın tatil olup olmadığı, penceresi okunan tarifenin ya da planın saat diliminde, o andaki tarihe bakılarak belirlenir. Gece yarısını aşan pencere başladığı güne sayıldığından, böyle bir pencerede başladığı tarihin (bir önceki günün) tatil olup olmadığına bakılır: tatil gecesinin sabahı tatil gecesine, tatilden önceki gecenin sabahı o geceye aittir.
- **3a.** Rota pencereleri de aynı kodla, planın saat dilimi ve planın sahibinin takvimiyle tatile uyar. Fiyat ile rotanın aynı ana farklı gün demesi beklenmedik olurdu.
- **4a.** `rating.Window.Covers(local, cal)` ve `rating.Select(list, local, cal)` takvimi (`rating.Calendar`) alır; motor ile rater tatil gününü aynı işlevle bulur (ADR-0010'un tek kod kuralı). `nil` takvim "tatil yok" demektir.
- **Yarım gün tatil (arife) yoktur:** tatil bütün tarihi kapsar. Gerekirse saatli pencere ve ayrı bir fiyatla değil, ileride ayrı bir kararla eklenir.
- **Çağrı yoluna ulaşma:** motor takvimleri görüntü yüklenirken okur (`store.LoadSnapshot`; tablo küçüktür, hepsi tutulur, simülatör her tarihi sorabilir) ve takvimi tarifenin pencereli fiyatlarının yanına (`TimedTariff.Cal`) ve plana (`Plan.Cal`) koyar. Çağrı yolunda SQL yoktur; takvimsiz tarifede maliyet bir uzunluk denetimidir. Tatil API'nin genel CRUD'uyla (`/v1/holidays`) yazılır; her yazma `NotifyConfig` gönderir, motor görüntüyü hemen yeniler. Rater fiyatı ararken, pencereli bir fiyat varsa, tarifenin sahibinin takviminden çağrı tarihini ve bir önceki günü okur.

## Sonuçları

- Tatil eklemek ya da silmek, o tarihte başlayan çağrıların fiyatını ve rotasını değiştirir; geçmiş kayıtlar yeniden ücretlenmez (rater kaydı geldiği anda ücretler).
- Enterprise'da her bayi kendi takvimini tutar; üst hesabın takvimini devralma yoktur.
- ADR-0010 değişmez; yalnızca "Resmî tatil takvimi yoktur" sonucu bu kayıtla geçersiz olur.
