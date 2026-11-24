# Güvenlik politikası

> **English summary.** Please do not report security vulnerabilities through public issues, pull requests or discussions. Report them privately through GitHub's private vulnerability reporting (the repository's **Security** tab → **Report a vulnerability**). We will acknowledge the report, investigate, and agree a disclosure date with you once a fix is available.

## Açık bildirme

Güvenlik açıklarını **herkese açık hata kaydı, çekme isteği ya da tartışma olarak bildirmeyin.** Bunun yerine özel olarak bildirin:

- GitHub'ın özel güvenlik bildirimi: deponun **Security** sekmesinde **Report a vulnerability** düğmesi. Bildirim yalnızca proje yöneticilerine görünür.

Bildiriminizde mümkünse şunlar olsun:

- Etkilenen bileşen ve sürüm (commit),
- Açığın türü ve etkisi,
- Yeniden üretme adımları (yapılandırma, SIP iletileri, SIPp senaryosu vb.),
- Bildiğiniz bir geçici önlem.

Gerçek sistemlerden alınmış sır, parola, müşteri verisi ya da çağrı kaydı göndermeyin; laboratuvar verisiyle yeniden üretin.

## Süreç

1. Bildirimi aldığımızı onaylarız.
2. Açığı doğrular, etkisini değerlendiririz.
3. Düzeltmeyi hazırlar ve sizinle bir açıklama tarihi üzerinde anlaşırız.
4. Düzeltme yayımlandıktan sonra, isterseniz adınızla birlikte, açığı duyururuz.

## Kapsam

Bu depo (Community sürümü). OpenSIPS ve rtpengine ayrı projelerdir; onlardaki açıkları kendi projelerine bildirin. Kurulumun kendisinin güvenliği için `docs/09-kurulum.md` ("Ağ katmanı koruması", "Bilinen sınırlar") ve `docs/08-cagri-arayuzu.md` ("Kimlik doğrulama") bölümlerine bakın: SIP çekirdeğinin yönetim soketi, Redis ve NATS gibi bileşenler kimlik doğrulamasızdır ve özel ağ varsayımına dayanır.
