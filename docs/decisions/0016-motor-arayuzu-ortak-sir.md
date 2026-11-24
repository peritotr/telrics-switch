# ADR-0016: Motorun arayüzü tek ortak sırla korunur

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Motorun HTTP arayüzü parola özeti verir, çağrı kapatır, engel kaldırır. Başta özel ağ varsayımıyla korumasızdı.

## Seçenekler

1. Yalnızca ağ yalıtımı: yanlış ayarda tamamen açık.
2. Karşılıklı TLS: güçlü; OpenSIPS `rest_client` ve kurulum karmaşıklığı.
3. Ortak sır (`ENGINE_TOKEN`), `Authorization: Bearer`, sabit süreli karşılaştırma.

## Karar

Üçüncü seçenek. `/v1/` altındaki bütün uçlar korunur; yeni uç kendiliğinden korunur. `/metrics` ve `/healthz` bilinçli olarak açıktır. Sır boşsa motor herkese cevap verir ve uyarı yazar (yükseltme sırasında çağrı kesilmesin diye).

## Sonuçları

- Betikte her `rest_post` öncesinde `route(engine_token)` çağrılır; unutulursa o soru 401 alır ve çağrılar 503 olur.
- Sır günlüğe, hata iletisine, panele yazılmaz.
- Sır ağda şifresiz gider; tek sırdır (arayan başına ayrım, salt okunur sır, kesintisiz değişim yok).
- SIP çekirdeğinin yönetim soketi, rtpengine denetim soketi, Redis ve NATS hâlâ kimlik doğrulamasızdır. AÇIK SORU: bunların ve arayüzde TLS'in ele alınışı (S-14).
