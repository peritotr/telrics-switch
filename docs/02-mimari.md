# Mimari ve Kapasite Planı — 20.000 Eşzamanlı Çağrı

## Varsayımlar (onay bekliyor)

- Ürün **Class 4**: toptan/perakende SIP transit, müşteri → yönlendirme → sağlayıcı. Abone kaydı, PBX özellikleri, IVR kapsam dışı.
- Medya varsayılan olarak sistem üzerinden geçer (topoloji gizleme ve faturalama doğruluğu için).
- Transcoding istisnadır; ayrı havuzda ve ayrı lisanslanır/ücretlenir.
- Yalnızca SIP (H.323 yok).

## Yük hesabı

| Büyüklük | Hesap | Sonuç |
|---|---|---|
| Cevaplanan CPS (toptan, ortalama süre 180 sn) | 20.000 / 180 | ~110 |
| Deneme CPS (cevaplanma oranı %40) | 110 / 0,4 | ~280 |
| Deneme CPS (çağrı merkezi trafiği: 30 sn, %20) | 20.000 / 30 / 0,2 | ~3.300 |
| **Tasarım hedefi** | | **1.000 CPS sürekli, 3.000 CPS tepe** |
| RTP paket hızı (20 ms, çağrı başına 2 yön) | 20.000 × 100 | 2 M paket/sn giriş + 2 M çıkış |
| Bant genişliği (G.711, yön başına ~80 kbit/sn) | 20.000 × 160 kbit/sn | ~3,2 Gbit/sn (her iki tarafta) |
| CDR hacmi (280 CPS sürekli olsa) | 280 × 86.400 | ~24 M kayıt/gün üst sınır |

Trafik profili (toptan mı, çağrı merkezi mi) CPS hedefini 10 kat değiştirir; ilk netleştirilecek konu budur.

## Bileşenler

```
 Müşteriler ──SIP──▶ ┌──────────────┐        ┌───────────────┐ ──SIP──▶ Sağlayıcılar
                     │ SIP çekirdek │◀──────▶│ route-engine  │
                     │  (OpenSIPS)  │  HTTP  │  (Go)         │
                     └──────┬───────┘        └──┬─────────┬──┘
                            │ ng protokolü      │         │
                     ┌──────▼───────┐       ┌───▼───┐ ┌───▼──────┐
 RTP ───────────────▶│  rtpengine   │       │ Redis │ │PostgreSQL│
                     │  havuzu      │       └───────┘ └──────────┘
                     └──────────────┘
   CDR olayları ──▶ NATS JetStream ──▶ rater ──▶ ClickHouse (CDR) + PostgreSQL (bakiye)
   Yönetim: api (Go, REST) ──▶ arayüz (karar bekliyor)          SIP izleme: HEP ──▶ trace-collector ──▶ ClickHouse
```

| Bileşen | Teknoloji | Görev | 20.000 çağrı için adet |
|---|---|---|---|
| SIP çekirdek | OpenSIPS (güncel LTS) | IP/digest kimlik doğrulama, dialog takibi, topology hiding, hata durumunda sıradaki sağlayıcıya geçiş, rtpengine seçimi | 3 aktif (N+1), her biri ~10.000 dialog |
| Medya | rtpengine, kernel forwarding | RTP aktarımı, SRTP | 5 aktif + 1 yedek, düğüm başına ~5.000 çağrı (ölçümle doğrulanacak), 10 GbE |
| Transcoding | rtpengine (ayrı havuz) | G.729/Opus ↔ G.711 | İhtiyaca göre |
| route-engine | Go | Çağrı başına karar: müşteri tanıma, limitler, numara dönüşümü, LCR, bakiye rezervasyonu, azami süre | 3 (durumsuz) |
| rater | Go | CDR ücretlendirme, bakiye kesinleştirme | 2 |
| api (+ arayüz) | Go; arayüz teknolojisi kararlaştırılmadı | Müşteri, sağlayıcı, tarife, yönlendirme planı, rapor | 2 |
| Redis | Redis (küme) | Eşzamanlı çağrı ve CPS sayaçları, bakiye rezervasyonu, rtpengine çağrı durumu | 3 |
| PostgreSQL | Patroni ile HA | Yapılandırma, tarifeler, bakiye defteri | 3 |
| ClickHouse | | CDR ve istatistik | 2-3 |
| Olay kuyruğu | NATS JetStream | CDR olayları, yapılandırma değişiklik bildirimi | 3 |

Küçük kurulumda (≤2.000 çağrı) tüm bileşenler tek sunucuda çalışabilmeli; Sippy'nin kurulum kolaylığı avantajını korumanın yolu bu.

## Temel kararlar ve gerekçeleri

1. **SIP yığını yazılmayacak.** OpenSIPS + rtpengine kanıtlanmış veri düzlemidir. Sıfırdan B2BUA yazmak projenin en riskli ve en az ayırt edici işi olurdu. OpenSIPS seçildi çünkü topology hiding dialog modülüne gömülü, B2B modülleri ve küme (clusterer) desteği hazır. Kamailio eşdeğer bir alternatiftir.
2. **Tüm iş mantığı route-engine'de.** OpenSIPS betiği ince tutulur: her INVITE için route-engine'e sorar, dönen sıralı sağlayıcı listesini uygular. Böylece ileride SIP çekirdeği (örneğin gerçek bir B2BUA ile) değiştirilebilir.
3. **Çağrı yolunda veritabanı sorgusu yok.** route-engine tarifeleri ve yönlendirme planlarını bellekte tutar (PostgreSQL'den yükler, değişiklik bildirimiyle yeniler). Çağrı başına yalnızca Redis'e gider. Hedef: karar süresi p99 < 10 ms.
   - **Görüntünün boyutu (T-30, ADR-0048; Mac'te ölçüldü):** 5 milyon fiyat motorun belleğinde ~1,3 GiB tutar; tam yükleme ~8 sn sürer ve 2,7 GiB ayırır, eski görüntü kullanımdayken yığın 3,8 GiB'a, yük altında çöp toplamanın gecikmesiyle 6,4 GiB'a çıkar. Fiyatlar yalnızca değiştikleri yerde okunduğundan olağan yenileme 10 ms, büyük tarifede önek değişikliği ~0,25 sn (harita kopyası; geçici +1,1 GiB). Motor sunucusunun belleği bu yüzden en büyük tarifenin fiyat sayısına göre seçilir: milyon fiyat başına ~0,3 GiB kalıcı, tam yüklemede ~3 katı geçici; API süreci de simülatör kullanıldığında aynı görüntüyü tutar.
4. **Faturalama rezervasyon tabanlı.** Çağrı kurulurken bakiyeden süre rezerve edilir, azami çağrı süresi dialog zaman aşımı olarak verilir, çağrı bitince kesinleştirilir. Para hesabında kayan nokta kullanılmaz (tamsayı, en küçük birim 1/1.000.000).
5. **CDR çağrı yolundan ayrık.** OpenSIPS olayı kuyruğa yazar; ClickHouse ya da rater yavaşlasa da çağrılar etkilenmez.

## Açık riskler

- **rtpengine düğüm kapasitesi belirsiz.** Yayınlanan raporlar 3.000 ile 16.000 arasında değişiyor. Faz 0'da gerçek donanımda ölçülmeden donanım siparişi verilmemeli.
- **Kernel modülü macOS'ta çalışmaz.** Geliştirme Docker ile Mac'te yapılabilir (kullanıcı alanı modunda), ancak yük testi için Linux fiziksel sunucu gerekir.
- **Proxy tabanlı çekirdeğin sınırları.** Bacak başına bağımsız session timer, re-INVITE yalıtımı gibi ihtiyaçlar çıkarsa OpenSIPS B2B modülüne ya da ayrı bir B2BUA'ya geçmek gerekebilir.
- **Gerçek operatör uyumu.** Sağlayıcıların SIP farklılıkları ancak canlı bağlantıyla görülür; laboratuvar bunu taklit edemez.
- **Mevzuat.** Türkiye'de operatör olarak işletilecekse BTK yükümlülükleri (yasal dinleme, kayıt saklama, acil çağrı) ayrıca ele alınmalı.
