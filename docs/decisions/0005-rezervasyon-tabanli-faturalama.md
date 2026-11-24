# ADR-0005: Rezervasyon tabanlı faturalama ve motorun çağrıyı kapatması

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

Ön ödemeli müşterinin eşzamanlı çağrıları aynı bakiyeyi paylaşır. Çağrı bitince ücretlendirmek bakiyenin eksiye düşmesine izin verir.

## Seçenekler

1. Yalnızca kabulde bakiye denetimi ve azami süre: basit; eşzamanlı çağrılar bakiyeyi aşar.
2. Çağrının tamamı için baştan ayırma: güvenli; bakiyeyi gereksiz kilitler, eşzamanlı çağrı sayısını düşürür.
3. Adım adım ayırma: kabulde bir adım, bitmeden uzatma, para yetmezse motor çağrıyı kapatır.

## Karar

Üçüncü seçenek. Adım `RESERVE_SEC` (60 sn); bitişe 10 sn kala uzatılır. Defter Redis'tedir (`hold:`/`held:`/`heldsum:`), işlemler Lua betiğiyle atomiktir. Çağrı bitince ayrılan tutar gerçek ücrete indirilir ve rater düşümü bildirene kadar (en geç 24 saat) ayrılmış kalır. Paket dakikalar aynı düzenle, para yerine saniye olarak ayrılır.

## Sonuçları

- Kesin tutarı her zaman rater belirler; motorun hesabı geçicidir, ama ikisi aynı kodu kullanır (ADR-0010).
- Betikler fiyat hesaplamaz: Go tahmin eder, betik uygular; betik yetersiz derse Go kalanla yeniden dener.
- Motor çağrıyı SIP çekirdeğinin yönetim arayüzünden kapatır (`pkg/sipctl`); `OPENSIPS_MI` yoksa yalnızca `max_duration_sec` kalır.
- Motor ya da Redis 10 saniyeden uzun kesilirse süren çağrılar korumasız kalır (TASKS: T-33).
- Rezervasyon yalnızca müşterinin kendi bakiyesi ve paketi içindir; sağlayıcı ve bayi bakiyeleri ayrılmaz.
