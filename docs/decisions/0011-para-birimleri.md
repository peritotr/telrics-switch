# ADR-0011: Tarife sahibinin para birimindedir; kur yalnızca karşılaştırma ve rapor içindir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Müşteriler ve sağlayıcılar farklı para birimlerinde çalışır. Kur ücretlendirme anında çevrilirse her fiyat hesabına yuvarlama ve kur riski girer.

## Seçenekler

1. Her şeyi hesabın para birimine çevirip saklamak: basit rapor; müşteriye kendi para biriminde kesin tutar verilemez.
2. Tarife, sahibinin (müşteri ya da sağlayıcı) para birimindedir; ücretlendirmede çevirme yoktur. Kur yalnızca LCR karşılaştırması ve raporlar için kullanılır.

## Karar

İkinci seçenek. Veritabanı tetikleyicileri taraf ile tarifesinin para birimini eşit tutar. `exchange_rates` hesabın para birimine mikro birimle kur tutar. Çağrı kaydı tutarı iki biçimde taşır: tarafın para biriminde (`sell_amount`, `buy_amount`) ve ücretlendirme anındaki kurla hesabın para biriminde (`sell_base`, `buy_base`).

## Sonuçları

- Raporlar `…_base` alanlarından toplanır; kur sonradan değişse de eski kayıtlar değişmez.
- Kuru olmayan para biriminde taraf açılamaz; kullanılan kur silinemez.
- Kurlar elle girilir; dış kaynaktan güncelleme yoktur (TASKS: T-20).
