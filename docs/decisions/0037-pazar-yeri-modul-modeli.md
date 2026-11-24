# ADR-0037: Pazar yeri modülleri ikiliye derlenir ve lisansla açılır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-8

## Bağlam

ADR-0022 ülkeye ve sağlayıcıya özgü entegrasyonları pazar yeri modülü yapmıştı, ama modül modeli kararlaştırılmamıştı.

## Seçenekler

1. Çalışma anında yüklenen eklentiler (Go'da güvenli ve taşınabilir değil).
2. Ayrı süreç olarak kurulan modüller.
3. Enterprise ikilisine derlenen, lisansla açılan modüller.

## Karar

Üçüncü seçenek (2026-11-23). Modüller Enterprise ikilisine derlenir ve lisansla açılır; yalnızca çekirdeğin var olan genişleme noktalarını kullanırlar. İş Enterprise deposundadır; çekirdeğe bir şey eklenmez.

## Sonuçları

- Yeni modül yeni sürüm demektir; modül lisansla açılır.
- ADR-0022'deki AÇIK SORU kapandı.
