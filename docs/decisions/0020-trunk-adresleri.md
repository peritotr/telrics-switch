# ADR-0020: Trunk kaynakları adres ve ağ listesidir; en dar ağ kazanır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Bir müşteri çoğu zaman birden çok adresten ya da bir ağdan arar. Tek `ip` sütunu yetmedi.

## Seçenekler

1. Trunk başına tek adres, çok trunk: basit; yönetimi zor.
2. `client_trunks.addresses inet[]`: tek adresler ve ağlar, IPv4 ve IPv6.

## Karar

İkinci seçenek. Eşleşmede en dar ağ, eşitlikte en uzun teknik önek kazanır. Aynı (adres ya da ağ, teknik önek) çiftinin iki açık trunk'ta olmamasını `client_trunk_sources` tablosu sağlar; onu yalnızca tetikleyici yazar. API IPv4'te `/8`'den, IPv6'da `/16`'dan geniş ağı reddeder.

## Sonuçları

- Eski `ip` sütunu yoktur (API'de kırıcı değişiklik).
- `on conflict` ile trunk eklenemez; `not exists` kullanılır.
- Geniş ağ tanımlamak o ağdaki bütün adresleri taşkın korumasının dışına çıkarır.
- AÇIK SORU: `/8` ve `/16` sınırı uygulayanın kendi seçimiydi; onaylanmadı (S-9).
- IPv6 yalnızca birim ve API testleriyle denendi (TASKS: T-28).
