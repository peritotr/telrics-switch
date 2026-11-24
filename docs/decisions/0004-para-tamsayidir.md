# ADR-0004: Para tamsayıdır: int64 mikro birim

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

Ücretlendirme hataları doğrudan para kaybıdır; kayan nokta yuvarlama farkları milyonlarca kayıtta birikir ve bakiye ile CDR toplamını ayırır.

## Seçenekler

1. `float64`: kolay; yuvarlama farkı.
2. Ondalık kütüphanesi: doğru; çağrı yolunda yavaş, Redis betiklerinde yok.
3. `int64`, en küçük birim 1/1.000.000.

## Karar

Bütün tutarlar, fiyatlar ve kurlar `int64` mikro birimdir. Kur çevirme 128 bit ara çarpımla yapılır, yarım mikro birim yukarı yuvarlanır (`rating.Convert`). Panelde biçimlendirme `web/src/lib/format.ts` ile yapılır.

## Sonuçları

- Go, SQL, Lua ve TypeScript'te para için kayan nokta kullanılmaz; kod incelemesinde ret nedenidir.
- Redis betiklerinde tutarlar 2^53 mikro birime kadar tamdır (Lua sayıları).
- 10.000 çağrılık testte bakiye toplamı CDR toplamına kuruşu kuruşuna eşittir; bu denetim korunur.
