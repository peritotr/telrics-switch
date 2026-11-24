# ADR-0036: Kurlar isteğe bağlı olarak Avrupa Merkez Bankası'ndan alınır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** T-20

## Bağlam

Kurlar elle giriliyor (ADR-0011).

## Seçenekler

1. Elle girişi korumak.
2. Avrupa Merkez Bankası'nın günlük referans kurları (ücretsiz, anahtar gerektirmez).
3. Ücretli bir kur servisi.

## Karar

İkinci seçenek; varsayılan kapalıdır. Açıkken günde bir kez alınır, hesabın para birimine çevrilerek mikro birim tamsayıyla yazılır; elle girilen kur korunur.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- Eski çağrı kayıtları değişmez (ADR-0011).
- Kaynağa ulaşılamazsa son kur kalır ve uyarı verilir.
- Görev: T-20.

## Uygulama notu (T-20, 2026-11-23)

- Açma: `FX_SOURCE=ecb` (API süreci; `FX_URL` aynı dosyanın başka adresi). Açılışta ve her gün 15:30 UTC'den sonra bir kez alınır; başarısız deneme bir saat sonra yinelenir. Birden çok API sürecinde yalnızca biri alır (`exchange_rate_fetch.claimed_until`).
- "Elle girilen kur korunur": `exchange_rates.source` (`manual` / `ecb`); migration (`0028`) var olan bütün kurları `manual` bırakır. Kaynak yalnızca `source = 'ecb'` olan kurları yazar; böyle bir kura API'den dokunmak onu `manual` yapar (tetikleyici). Kaynağa bağlamak için `POST /v1/exchange-rate-feed/follow`.
- Çevirme: çapraz kur euro üzerinden, yayınlanan ondalıklar kesir olarak tam okunur, sonuç en yakın mikro birime yarım yukarı yuvarlanır (`pkg/fx`). Kayan nokta yok.
- Uyarı: hata `exchange_rate_fetch.error` ve paneldeki kur kartında; `softswitch_fx_fetch_failures_total`, `softswitch_fx_fetch_age_seconds`, alarm `ExchangeRatesNotFetched`.
- Ayrıntı: `docs/08-cagri-arayuzu.md`, "Kurların dış kaynaktan alınması".
