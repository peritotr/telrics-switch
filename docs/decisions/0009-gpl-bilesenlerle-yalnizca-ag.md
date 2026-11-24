# ADR-0009: GPL bileşenlerle yalnızca ağ üzerinden konuşulur

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

OpenSIPS GPLv2, rtpengine GPLv3 lisanslıdır. Enterprise kodu kapalı kalmalıdır.

## Seçenekler

1. OpenSIPS modülü ya da rtpengine yaması yazmak: GPL olur, açılmak zorundadır.
2. Ayrı süreçler; HTTP, MI ve ng protokolüyle konuşmak.

## Karar

İkinci seçenek. Kendi servislerimiz bu bileşenlerle yalnızca ağ üzerinden konuşur; kodları bağlanmaz, kopyalanmaz, onlara modül yazılmaz.

## Sonuçları

- Enterprise'a özgü hiçbir mantık OpenSIPS betiğine ya da bu bileşenlerin içine konmaz.
- OpenSIPS'te eksik olan bir yetenek (ör. sağlayıcı sertifikasında ad denetimi) modül yazılarak değil, motorda çözülür (ADR-0015).
