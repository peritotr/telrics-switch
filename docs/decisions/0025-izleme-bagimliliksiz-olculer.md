# ADR-0025: İzleme: bağımlılıksız ölçü paketi, Prometheus biçimi

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Her servisin sayaçları ve alarmları olmalıdır. Çağrı yolundaki sayaç artırımı ölçülebilir maliyet getirmemelidir.

## Seçenekler

1. Prometheus istemci kütüphanesi: standart; bağımlılık ve çağrı yolunda etiket aramaları.
2. Kendi küçük paketimiz (`pkg/metrics`): sayaç tek atomik toplama, `NewGauge` yalnızca okunurken çalışır.

## Karar

İkinci seçenek. Motor `/metrics`'i kendi dinleyicisinde, API ve rater `METRICS_LISTEN`'de, OpenSIPS 8888'de, rtpengine 2225'te verir. Kurallar `deploy/prod/monitoring/alerts.yml` içindedir.

## Sonuçları

- Aynı seriyi iki kez kaydetmek panik verir: süreç başına bir kez çağrılan yerde kaydedilir.
- Yeni bir hata sayacı eklenince alarm kuralı ve `docs/10-izleme.md` içindeki tablo da eklenir.
- Ölçü uçları kimlik doğrulamasızdır (ADR-0016).
- Grafana panosu ve gerçek trafikle eşik ayarı yapılmadı.
