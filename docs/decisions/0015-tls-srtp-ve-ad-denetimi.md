# ADR-0015: SRTP yalnızca TLS ile; sağlayıcı sertifikasındaki adı motor denetler

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Sinyalleşme ve ses şifrelenebilmelidir. SDES anahtarı SDP'de taşınır; şifresiz sinyalleşmede anahtar açıkta kalır. OpenSIPS'in TLS modülü karşı sertifikanın adını denetlemez.

## Seçenekler

1. SRTP'yi sinyalleşmeden bağımsız seçtirmek: esnek; anahtarı açıkta bırakan yanlış ayara izin verir.
2. Sağlayıcı ayağı, sinyalleşme TLS ise SRTP; arayan ayağı arayanın teklifine uyar.
3. Ad denetimi için OpenSIPS modülü yazmak: GPL (ADR-0009).
4. Ad denetimini motor yapar: her TLS bağlantısına kendisi bağlanır, sertifikayı doğrular, reddedilenin rotalarını karardan çıkarır.

## Karar

İkinci ve dördüncü seçenekler. Trunk kuralı `client_trunks.security` (`any`/`tls`/`cert`) motor tarafından uygulanır. Sertifikalar `tls_mgm` + `db_text` tablosunda durur ve yeniden başlatmadan yüklenir (`db_mode 1` şart).

## Sonuçları

- Ad denetimi çağrının kendi bağlantısında değil, motorun ayrı bağlantısında yapılır; bu ikisini ayırt edebilen bir saldırgan denetimi aşar. Bilinen ve kabul edilen sınır.
- Sağlayıcıya DTLS-SRTP teklif edilmez; WebRTC (ICE, WebSocket) yoktur.
- İstemci sertifikasında yalnızca CN karşılaştırılır; SAN ve iptal listeleri yoktur.
- Kayıtlı trunk'a da aynı kural uygulanır: TLS ile kayıtlı olana (istek kaydın bağlantısından gider) SRTP, UDP ile kayıtlıya düz RTP. Bu kural T-07'de (2026-11-23) uygulandı; o güne kadar kayıtlı trunk'a her zaman düz RTP gidiyordu.
