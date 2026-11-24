# ADR-0013: Kayıt defteri (registrar) motordadır, Redis'te durur

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23

## Bağlam

Kullanıcı adıyla tanınan trunk'ların REGISTER ile yerini bildirmesi gerekir. OpenSIPS'in `usrloc`/`registrar` modülleri hazırdır, ama kayıtları SIP çekirdeğinin içinde tutar.

## Seçenekler

1. `usrloc` + `registrar`: hazır, NAT yoklaması dahil; kayıtlar API'den ve karar motorundan görünmez, kurallar betiğe girer, arayüze OpenSIPS kavramı sızar.
2. Kayıtları motor tutar: SIP çekirdeği parolayı denetler ve ham `Contact`/`Expires` ile motora sorar (`POST /v1/register`).

## Karar

İkinci seçenek. Kayıtlar Redis'te `reg:<trunk>` altında durur. Süre sınırları `REG_MIN_EXPIRES` / `REG_MAX_EXPIRES` / `REG_NAT_EXPIRES`; trunk başına adres sayısı `client_trunks.max_contacts` (dolunca en eski kayıt yer açar). Canlı tutma gönderilmez: NAT arkasındaki ya da TCP/TLS ile bağlı trunk kısa süreyle (60 sn) yeniden kaydolmaya zorlanır.

## Sonuçları

- Kayıtlar motorun ve SIP çekirdeğinin yeniden başlamasından etkilenmez; aynı Redis'i paylaşan düğümler aynı kayıtları görür.
- Trunk kuralı (TLS, sertifika), güvenlik duvarı ve hesap durumu çağrılardaki kodla uygulanır (`Trunk.Admits`).
- `Path`, `outbound`, GRUU ve `q` sırası yoktur.
- 60 saniyeden kısa sürede eşlemeyi düşüren yönlendiricilerde çağrı ulaşmayabilir. AÇIK SORU: canlı tutma (OPTIONS) eklenecek mi (S-13)?
- Kayıtlı trunk'a çağrı Community'de yalnızca mekanizmadır (`routing.ToTrunk`); hangi numaranın kime gideceği Enterprise işidir.
