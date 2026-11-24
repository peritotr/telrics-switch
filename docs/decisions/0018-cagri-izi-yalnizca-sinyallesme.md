# ADR-0018: Çağrı izi: kendi HEP toplayıcımız, yalnızca sinyalleşme; ses kaydı yok

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Operatör sorun giderirken çağrının SIP akışını görmek ve pcap indirmek ister. Ses (RTP) kaydı da rakiplerde bulunur.

## Seçenekler

1. Homer kurmak: hazır; ayrı ürün, ayrı veritabanı, ayrı kimlik.
2. Kendi toplayıcımız (`trace-collector`): HEP → ClickHouse `sip_messages`; panel ve pcap aynı veriden.
3. RTP kaydı: istenen özellik; her çağrıda kopyalama CPS'i düşürür.

## Karar

İkinci seçenek; RTP kaydı yapılmaz (karar, 2026-11-22: "CPS'ten feragat demek; ileride ayrı bir modül olabilir"). `Authorization` başlıkları maskelenir. Saklama `TRACE_KEEP_DAYS` (7 gün). `HEP_TARGET` verilirse kopya ayrıca dış toplayıcıya da gider.

## Sonuçları

- Toplayıcı çağrı yolunda değildir: yetişemediğini sayarak atar, göndereni bekletmez.
- pcap saklanan iletilerden üretilir (uydurma Ethernet/IP başlıkları); gerçek paket yakalama değildir.
- ~~Yalnızca sağlayıcıya gönderilen çağrılar izlenir; reddedilen çağrıların ve kimlik sorularının izi yoktur (TASKS: T-26).~~ Bu madde ADR-0046 ile değişti: motorun bir müşteriye ait saydığı reddedilen çağrı da izlenir; yabancının istekleri izlenmez.
- RTP kaydı çekirdeğe yazılmaz.
