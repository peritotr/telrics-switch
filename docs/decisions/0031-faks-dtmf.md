# ADR-0031: T.38 yeni teklifi olduğu gibi geçer; DTMF dönüşümü yok

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-6

## Bağlam

Faks (T.38), DTMF dönüşümü ve video için politika yoktu.

## Seçenekler

1. Hiçbirini ele almamak.
2. T.38 re-INVITE'ını geçirmek ve sınamak, DTMF dönüşümünü sonraya bırakmak.
3. DTMF dönüşümünü de yapmak.

## Karar

İkinci seçenek: T.38'e geçen yeni teklif medya aktarıcısından olduğu gibi geçer ve senaryoyla kanıtlanır. `telephone-event` ↔ SIP INFO dönüşümü yapılmaz; video için ayar yoktur.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- Görev: T-40.
