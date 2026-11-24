# ADR-0030: Medya aktarıcısı cevap vermezse çağrı 503 ile reddedilir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-5

## Bağlam

rtpengine cevap vermediğinde ya da port kalmadığında teklif değiştirilemiyor ve çağrı yine kuruluyordu: ses taraflar arasında doğrudan kalabiliyor (topoloji gizleme bozulur) ya da hiç akmıyordu.

## Seçenekler

1. Bugünkü gibi kurmak.
2. Çağrıyı reddetmek.

## Karar

İkinci seçenek: teklif değiştirilemezse çağrı arayana 503 ile reddedilir ve motora bildirilir; olay sayılır ve alarm verir.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- Sorun switch'in kendisinde olduğu için sıradaki rota denenmez.
- Görev: T-39.
