# ADR-0022: Ülkeye ve sağlayıcıya özgü entegrasyonlar pazar yeri modülüdür

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Ürün birden çok ülkede kullanılacak. Kartla ödeme ve e-fatura gibi entegrasyonlar ülkeye ve sağlayıcıya göre değişir.

## Seçenekler

1. Enterprise çekirdeğine gömmek: basit; her pazar için gereksiz kod ve bakım.
2. Ürün içi pazar yerinde, ayrı ayrı açılan modüller.

## Karar

İkinci seçenek (2026-11-22): kartla ödeme, e-fatura ve benzeri ülkeye ya da sağlayıcıya özgü entegrasyonlar birer modüldür ve ürün içi pazar yerinde tutulur; Enterprise çekirdeğine gömülmez.

## Sonuçları

- Ödeme ve e-fatura entegrasyonu Enterprise çekirdeğine yazılmaz; Community'ye de yazılmaz.
- Modül modeli ayrı bir kararla belirlendi: ADR-0037 (eski AÇIK SORU S-8).
