# ADR-0003: Çağrı yolunda SQL yok: bellekte görüntü, bildirimle yenileme

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

1.000 CPS'te çağrı başına veritabanı sorgusu hem gecikme hem de tek hata noktası olur. Tarifeler milyon önek düzeyine çıkabilir.

## Seçenekler

1. Çağrı başına PostgreSQL sorgusu (önbellekli): basit; kuyruklanma ve gecikme sıçramaları.
2. Yapılandırmayı Redis'e kopyalamak: hızlı; iki doğruluk kaynağı, karmaşık eşleme.
3. Motor yapılandırmanın tamamını belleğe alır (görüntü), çağrı başına yalnızca sayaçlar ve rezervasyon için Redis'e gider.

## Karar

Üçüncü seçenek. Görüntü değişmez bir yapıdır, `atomic.Pointer` ile değiştirilir. API her yazmadan sonra `NOTIFY softswitch_config` gönderir; motor hemen, ayrıca `RELOAD_SEC` (10 sn) aralığıyla yeniden yükler. Karar süresi bütçesi p99 < 10 ms'dir.

## Sonuçları

- Çağrı yoluna SQL, dosya ya da başka ağ çağrısı eklenmez; çağrı başına tek Redis betik çağrısı vardır (trunk'a teslimde bir okuma daha).
- Çağrı yoluna dokunan her değişiklik yük testi ve benchmark ile ölçülür.
- API dışından veri değiştiren kod `store.NotifyConfig` çağırmak zorundadır.
- ~~Yükleme her seferinde tamdır; milyonlarca fiyatlı tarifede süresi ölçülmedi (TASKS: T-30).~~ Ölçüldü (5 milyon fiyatta ~8 sn); fiyatlar artık yalnızca değişen kısmıyla okunur, aralıklı tam yükleme yedektir: ADR-0048. Bu kaydın kararı değişmedi.
- Bakiye görüntüde bir yenileme aralığı kadar eski olabilir; aradaki fark rezervasyonla kapatılır (ADR-0005).
- Görüntü yerinde değiştirilmez; testlerde de baştan kurulur.
