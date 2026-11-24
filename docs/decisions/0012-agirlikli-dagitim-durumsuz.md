# ADR-0012: Ağırlıklı dağıtım durumsuzdur: sıra call_id'den türetilir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Aynı basamaktaki rotalar çağrıları yüzdeyle paylaşmalıdır. Sayaçla (round-robin) dağıtım, çağrı yolunda paylaşılan durum ve birden çok motor düğümünde eşgüdüm ister.

## Seçenekler

1. Redis'te sayaç: kesin oran; çağrı başına ek yazma, düğümler arası çekişme.
2. Bellekte sayaç: hızlı; düğümler arası tutarsız, yeniden başlatmada sıfırlanır.
3. Çağrı başına, `call_id`'den türetilen ağırlıklı kura (`routing.PickFor`).

## Karar

Üçüncü seçenek. Basamak = aynı öncelik (LCR'de ayrıca aynı fiyat). Ağırlık 0 yalnızca yedektir.

## Sonuçları

- Aynı çağrı için yeniden sorulduğunda (digest sorusundan sonraki ikinci INVITE) aynı sıra çıkar.
- Dağılım olasılıksaldır; az çağrıda oranlar sapar.
- Simülatör kura çekmez (`Pick` sıfır): sabit sıra ve pay yüzdesi döner.
