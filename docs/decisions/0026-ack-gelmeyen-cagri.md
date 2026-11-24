# ADR-0026: Cevaplanan çağrı 32 saniyede ACK'lenmezse kapatılır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-17

## Bağlam

Cevaplanan (200) bir çağrıyı ACK'lemeyen arayan çağrıyı `max_duration_sec` dolana kadar (en çok 2 saat) açık, sayılı ve parası ayrılmış tutuyordu; sağlayıcıya da ödeniyordu (T-01'de görüldü).

## Seçenekler

1. Hiçbir şey yapmamak (SIP'e göre arayanın hatası).
2. ACK beklemesini sınırlamak: RFC 3261'deki 64×T1 = 32 sn sonra iki tarafa BYE.
3. Oturum zamanlayıcılarına bırakmak.

## Karar

İkinci seçenek: 32 saniye içinde ACK gelmeyen cevaplanmış çağrı iki taraftan BYE ile kapatılır; motor her zamanki `ended` olayını alır, kayıt 200 kodu ve gerçek süreyle yazılır.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- Arayüz değişmez; uygulama SIP çekirdeğindedir.
- Senaryo: 200'ü ACK'lemeyen arayan (TASKS: T-41).
