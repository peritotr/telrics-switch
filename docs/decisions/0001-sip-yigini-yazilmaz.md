# ADR-0001: SIP yığını yazılmaz: OpenSIPS ve rtpengine

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

Hedef 20.000 eşzamanlı çağrı ve 1.000 CPS. SIP ayrıştırma, işlem (transaction) ve dialog katmanı, yeniden iletimler, TLS ve RTP aktarımı projenin en riskli ve en az ayırt edici kısmıdır.

## Seçenekler

1. Sıfırdan Go ile B2BUA ve RTP aktarıcı yazmak: tam denetim; yıllar süren olgunlaşma, operatör uyumu riski.
2. OpenSIPS + rtpengine: kanıtlanmış veri düzlemi, topoloji gizleme dialog modülünde hazır, kernel RTP aktarımı; betik dili sınırlı, GPL.
3. Kamailio + rtpengine: eşdeğer; topoloji gizleme ve B2B desteği OpenSIPS'te daha hazır.
4. FreeSWITCH/Asterisk: gerçek B2BUA; Class 4 ölçeğinde çağrı başına maliyeti yüksek.

## Karar

OpenSIPS (3.6 serisi) SIP çekirdeği, rtpengine medya aktarıcısı olarak kullanılır. Kendi kodumuz bunların çevresindeki Go servisleridir.

## Sonuçları

- Go tarafında SIP iletisi üretilmez ve ayrıştırılmaz; tek istisna motorun sağlayıcı yoklaması için gönderdiği OPTIONS (ADR-0024) ve REGISTER'ın `Contact` başlığının motorda ayrıştırılmasıdır (ADR-0013).
- Proxy tabanlı çekirdeğin sınırları vardır: bacak başına bağımsız oturum zamanlayıcısı, re-INVITE yalıtımı, aktarma. İhtiyaç çıkarsa SIP çekirdeği değiştirilebilir olmalıdır; bunu ADR-0002 sağlar.
- OpenSIPS'in bu sürümünde (3.6.10) `async()` öncesinde isteği değiştiren komut kullanılmaz; değişiklik devam rotasında yapılır (`docs/ARCHITECTURE.md` §4). Betik `proxy_challenge` ve `fix_nated_contact` kullanmaz.
- arm64 paketi olmadığı için imaj kaynaktan derlenir.
