# ADR-0035: Sağlayıcıya kayıt SIP çekirdeğinden gider; yoklama motorda kalır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** T-18

## Bağlam

Motorun gönderdiği OPTIONS yoklaması SIP çekirdeğinin adresinden ve 5060'tan değil, motorun adresinden gider. Sağlayıcıya kayıt (REGISTER) da motordan gitseydi sağlayıcı çağrıları yanlış adrese gönderebilirdi.

## Seçenekler

1. Yoklamayı ve kaydı SIP çekirdeğine yaptırmak.
2. Yoklamayı motorda bırakmak (sınırı belgelendi), kaydı SIP çekirdeğine yaptırmak.

## Karar

İkinci seçenek: sağlayıcıya kayıt OpenSIPS'in `uac_registrant` modülüyle switch'in kendi adresinden gider. Kayıt listesi, TLS tablosundaki gibi, motorun ya da API'nin ürettiği bir metin tablodan okunur; SIP sunucusuna özgü bilgi arayüze girmez.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- Kayıt durumu panelde ve ölçüde görünür.
- Görev: T-14.
