# ADR-0027: Ölü çağrıyı medya zaman aşımı kapatır; switch oturum yenilemesi istemez

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-1

## Bağlam

Oturum zamanlayıcıları (RFC 4028) uçtan uca geçiyor; switch kendisi yenileme istemiyor. Kapanışı bildirilmeyen çağrıyı yalnızca `max_duration_sec` ve rezervasyonun bitişi temizliyordu.

## Seçenekler

1. Switch bacak başına oturum zamanlayıcısı uygular (B2BUA davranışı; proxy çekirdekte zor).
2. Başlıklar uçtan uca geçmeye devam eder; ölü çağrıyı medya aktarıcısı saptar: belirli süre ses gelmezse çağrı kapatılır.

## Karar

İkinci seçenek: rtpengine 60 saniye boyunca bir çağrıdan hiç ses görmezse çağrı iki taraftan kapatılır (SIP çekirdeği BYE gönderir, motor `ended` alır).

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- Bekletmede (hold) ses akmayan çağrılar için süre ayrıca ele alınır (bekletmede zaman aşımı daha uzun ya da kapalı); uygulamada belgelenir.
- Oturum zamanlayıcısı başlıkları olduğu gibi geçmeye devam eder.
- Görev: T-38.
