# ADR-0024: Bağlantı ayarları Temel ve Gelişmiş olarak ayrılır; davranışı olmayan ayar olmaz

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23

## Bağlam

Rakip ürünlerde bağlantı başına ayrıntılı bir yapılandırma alanı vardır. Kullanıcıların hepsi profesyonel değildir (istek, 2026-11-23).

## Seçenekler

1. Bütün ayarları tek formda göstermek: eksiksiz; yeni kullanıcıyı boğar.
2. Ayrı bir "uzman" ekranı: temiz; ayar gözden kaçar.
3. Aynı formda temel alanlar ve kapalı gelen "Gelişmiş ayarlar" bölümü.

## Karar

Üçüncü seçenek. Kurallar: (1) her ayarın gerçek bir davranışı ve `make test-routing` içinde gerçek çağrılı bir senaryosu vardır; kanıtlanamayan ayar forma girmez; (2) varsayılan "ayar yok"tur ve yalnızca temel alanlarla tanımlanan kayıt eskisi gibi davranır; (3) kapalı bölüm, içinde varsayılandan farklı ayar varsa bunu gösterir; (4) her alanın sade bir açıklama satırı vardır. Sağlayıcı yoklamasını (OPTIONS) motor kendisi gönderir; SIP çekirdeğinden geçmez.

## Sonuçları

- Ayarlar SIP çekirdeğine `route` içindeki isteğe bağlı alanlarla ulaşır (`auth`, `caller`, `identity`, `ring_timeout_sec`, `max_duration_sec`, `codecs`); arayüze OpenSIPS kavramı girmez.
- Ayarlar olay geldiği andaki yapılandırmadan okunur, çağrı durumunda saklanmaz.
- Yoklama motorun adresinden gider; kaynak adrese ya da porta göre süzen sağlayıcıda kapalı bırakılmalıdır. Varsayılan kapalıdır.
- ~~Kodek çevirisi yoktur: liste yalnızca süzer ve sıralar (TASKS: T-09).~~ Değişti: bağlantı ya da trunk izin verirse ortak kodeği olmayan çağrı çevrilerek taşınır (ADR-0041, 2026-11-23). Liste izin yokken yalnızca süzer ve sıralar.
- Switch sağlayıcıya kayıt (REGISTER) olmaz (TASKS: T-14).
