# ADR-0023: Panel: React + TypeScript + Vite; tasarım paketine uyulur

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

Yönetim arayüzü Türkçe, tutarlı ve Enterprise ekranlarıyla genişletilebilir olmalıdır. Tasarım ayrı bir tasarım paketinden gelir.

## Seçenekler

1. Sunucu taraflı şablonlar: az bağımlılık; zengin tablolar ve canlı ekranlar zor.
2. React + TypeScript + Vite, tek sayfa uygulaması; `createPanel(...extensions)` ile genişleme.

## Karar

İkinci seçenek. Tasarım paketi bu depoda değildir. Pakette olmayan ekran, durum ya da metin için sorulmaz: ortak bileşenlerle (`web/src/ui/kit.tsx`) bütünlüğe uygun karar verilir ve `docs/07-panel.md` içindeki sapmalar tablosuna yazılır (karar, 2026-11-21).

## Sonuçları

- Arayüz Türkçedir; `text-transform: uppercase` kullanılmaz.
- Oturum belirteci yalnızca bellekte tutulur; tarayıcı deposuna yazılmaz.
- Listeler sunucu taraflı sayfalanır; büyük tablolar istemciye toplu çekilmez.
- Tutarlar `web/src/lib/format.ts` ile biçimlenir; kayan noktayla hesap yapılmaz.
- Yeni menü grubu `web/src/panel.tsx` içindeki `groups` dizisine de eklenir, yoksa görünmez.
