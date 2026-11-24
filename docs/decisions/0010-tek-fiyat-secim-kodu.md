# ADR-0010: Fiyat seçimi ve tutar hesabı tek koddur; çağrı başladığı andaki fiyatla ücretlenir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Fiyatlar güne ve saate göre değişebilir. Motor rezervasyon için, rater kesin ücret için fiyat seçer; ikisi farklı seçerse bakiye ile CDR ayrışır.

## Seçenekler

1. Seçimi SQL'de yapmak (rater) ve Go'da yapmak (motor): iki uygulama, kaçınılmaz ayrışma.
2. Tek Go işlevi; ikisi de onu çağırır.

## Karar

`rating.Tidy` + `rating.Select` fiyatı seçer, `rating.Rate.AmountAfter` tutarı hesaplar, `rating.Period` dönemi verir. Çağrı, başladığı andaki fiyatla sonuna kadar ücretlenir; pencere ya da dönem sınırını aşan çağrı bölünmez.

## Sonuçları

- Bu mantık SQL'e, betiğe ya da ikinci bir Go işlevine kopyalanmaz.
- Görüntüde tek fiyatlı önekler `Snapshot.Tariffs`, pencereli olanlar `Snapshot.Timed` içindedir; fiyat ararken `Snapshot.rate` kullanılır.
- Motor süren çağrıyı pencere bitti diye kapatmaz.
- Yedek rotaya geçen çağrıda her deneme kendi başlangıç anına göre ücretlenir.
- Resmî tatil takvimi yoktur (TASKS: T-19).

> Not (2026-11-23): resmî tatil takvimi ADR-0044 ile eklendi; tatil günü de aynı tek kodla (`rating.Window.Covers`, `rating.Select`) seçilir.
