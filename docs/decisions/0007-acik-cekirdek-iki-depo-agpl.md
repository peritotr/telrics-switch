# ADR-0007: Açık çekirdek: iki depo, AGPLv3 + CLA, ayrım yetenekle

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

Ücretsiz bir Community sürümü ve ücretli bir Enterprise sürümü isteniyor. Açık kaynak kodda sayısal limit zorlanamaz: satırı silen yeniden derler.

## Seçenekler

1. Tek depo, özellik bayrakları: basit; kapalı kod açık depoda duramaz.
2. Apache 2.0: geniş benimsenme; rakip kodu kapatıp satabilir.
3. BSL / kaynak-erişilebilir: güçlü koruma; açık kaynak sayılmaz.
4. AGPLv3 + katkı sözleşmesi (CLA), iki depo; ayrım sayıyla değil yetenekle (tek düğüm ↔ kümeleme, tek hesap ↔ bayi hiyerarşisi).

## Karar

Dördüncü seçenek. `core` deposu Community'nin tamamıdır (AGPL-3.0-only; telif sahibi Telrics Limited, `NOTICE`). `enterprise` deposu özeldir, `core`'u Go modülü olarak içe aktarır.

## Sonuçları

- `core`, `enterprise`'ı asla içe aktarmaz.
- Her kaynak dosyası `SPDX-License-Identifier: AGPL-3.0-only` başlığı taşır.
- Dışarıdan katkı, CLA imzalanmadan kabul edilmez; yoksa kod Enterprise ile birlikte lisanslanamaz. AÇIK SORU: CLA metni yazılmadı (S-16).
- Community tek başına kullanılabilir bir ürün olmalıdır: yönlendirme, faturalama, limitler, CDR, API ve panel Community'dedir.
- Enterprise şeması `ent_` önekli ayrı migration dosyalarıyla `core` şemasının üzerine uygulanır.
