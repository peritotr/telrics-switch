# ADR-0034: Trunk ağlarında /8 (IPv4) ve /16 (IPv6) genişlik sınırı kalır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-9

## Bağlam

B3'ü uygulayan, trunk'a tanımlanabilecek ağın genişliğini kendi seçimiyle sınırlamıştı; onaylanmamıştı.

## Seçenekler

1. Sınırı kaldırmak.
2. Korumak.

## Karar

Korunur: geniş ağ o ağdaki bütün adresleri taşkın korumasının dışına çıkarır.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- ADR-0020'deki AÇIK SORU kapandı.
