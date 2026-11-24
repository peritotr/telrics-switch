# ADR-0021: Kurulum: Docker Compose ile tek sunucu; çağrı yolu sunucunun ağında

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Rakiplerin güçlü yanı kolay kurulumdur. Küçük kurulumda bütün bileşenler tek sunucuda çalışabilmelidir.

## Seçenekler

1. Paket (deb/rpm) + systemd: yerel performans; dağıtım başına bakım.
2. Kubernetes: ölçeklenir; tek sunucu için ağır, RTP port aralıklarıyla zahmetli.
3. Docker Compose: tek dosya, laboratuvarla ortak imajlar.

## Karar

Üçüncü seçenek. `deploy/prod/` kurulum, yükseltme, yedek ve geri yükleme betiklerini içerir. Üretimde SIP ve medya sunucunun ağını kullanır (`docker-compose.host.yml`); duman testi özel ağda çalışır (`docker-compose.bridge.yml`). Yapılandırma dosyaları şablondur ve ortam değişkenlerinden doldurulur.

## Sonuçları

- Adres ya da alan (realm) yapılandırma dosyasına sabit yazılmaz; iki yer tutucu yan yana konmaz.
- Kurulum sırlarını kendisi üretir (`deploy/prod/.env`); yükseltme eksik sırrı ekler ve rater ile api'yi motordan önce kaldırır.
- Sunucu ağı yerleşimi Mac'te çalışmaz ve gerçek Linux sunucuda henüz denenmedi (TASKS: T-34).
- Çok düğümlü kurulum tasarlanmadı (ARCHITECTURE S-7).
