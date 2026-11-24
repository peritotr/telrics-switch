# İzleme: ölçüler, alarmlar ve çağrı izi

Üç parça: servislerin Prometheus'a verdiği ölçüler, bu ölçüler üzerinden çalışan alarm kuralları ve çağrıların SIP sinyalleşmesinin saklanması (çağrı izi).

> **Durum.** Ölçüler, kurallar ve alarmın Alertmanager'a ulaşması duman testinde denenir (`make prod-smoke`): beş servisin okunması, öldürülen SIP sürecinin ve durdurulan bir servisin alarm üretmesi. Çağrı izi gerçek çağrılarla (`make test-routing`), duman testinde ve yük altında (`make load`) denenir. Yük testi raporu ayrıca aktarıcının en çok kullandığı port sayısını, "Ran out of ports" sayısını, arayana 200'den sonra giden 180/183'leri (izden) ve bitişini motorun öğrenemediği çağrıları verir. 180/183 sayısı bilgi içindir; port hatası ve motorda kalan çağrı testi düşürür (T-29). Denenmeyenler: alarmın bir insana ulaşması (alıcı tanımı sizde), sunucu ölçüleri (`node-exporter`, yalnızca üretim yerleşiminde), gerçek bir Homer kurulumuna gönderim, iz deposunun gerçek trafikteki boyutu. Eşikler başlangıç değerleridir; gerçek trafik görülmeden ayarlanmadılar. Hazır gösterge paneli (Grafana) yok.

## Ölçüler

Hiçbirinde kimlik doğrulama yoktur ve trafik hacmini gösterirler: yalnızca özel ağda ya da 127.0.0.1'de açıktırlar.

| Servis | Adres (üretim yerleşimi) | Nasıl açılır |
|---|---|---|
| route-engine | `127.0.0.1:58090/metrics` | her zaman, kendi dinleyicisinde; motorun diğer uçlarının aksine sır (`ENGINE_TOKEN`) istemez |
| api | `127.0.0.1:58091/metrics` | `METRICS_LISTEN` (panelin dinleyicisinden ayrı: o dışarıya bakar) |
| rater | `127.0.0.1:58092/metrics` | `METRICS_LISTEN` |
| trace-collector | `127.0.0.1:58093/metrics` | `METRICS_LISTEN` |
| opensips | `127.0.0.1:8888/metrics` | her zaman (`SIP_METRICS`) |
| rtpengine | `127.0.0.1:2225/metrics` | her zaman (`RTP_METRICS`) |
| sunucu | `127.0.0.1:59100/metrics` | `monitoring` profili (node-exporter) |

Kendi servislerimizin ölçüleri (`pkg/metrics`; sayaçlar çağrı yolunda tek bir atomik toplamadır, veritabanına ya da Redis'e soran değerler yalnızca Prometheus okurken hesaplanır):

| Ölçü | Anlamı |
|---|---|
| `softswitch_build_info{version, goversion}` | Sürecin derlendiği sürüm (her zaman 1; ADR-0051). Motor, API, rater ve iz toplayıcı verir; yükseltmeden sonra bütün servislerin yeni sürümde olduğu `count by (version) (softswitch_build_info)` ile görülür. Geliştirme derlemesi `dev`, `dev-<commit>` ya da `-dirty` ekli sürüm yazar |
| `softswitch_engine_decision_seconds` | Çağrı kararının süresi (histogram). Bütçe: p99 < 10 ms |
| `softswitch_engine_authorize_total{result}` | Sorulan çağrılar: `allowed` / `refused` |
| `softswitch_engine_events_total{event}` | SIP çekirdeğinin bildirdiği olaylar |
| `softswitch_engine_calls_active` | Süren çağrı sayısı |
| `softswitch_engine_snapshot_age_seconds` | Yapılandırmanın son yüklenmesinden beri geçen süre |
| `softswitch_engine_snapshot_load_seconds` | Son yüklemenin süresi; fiyatların yalnızca değişeni okunur (ADR-0048), olağan yenileme milisaniyeler, `RELOAD_FULL_SEC`'teki tam yükleme büyük tarifede saniyeler |
| `softswitch_engine_reload_errors_total`, `…_event_errors_total`, `…_sweep_errors_total` | Yapılandırma yükleme ve durum deposu (Redis) hataları |
| `softswitch_engine_reservations_extended_total`, `…_calls_cut_total`, `…_hangup_errors_total` | Bakiye rezervasyonu: uzatılan, parası bittiği için kapatılan ve kapatılamayan çağrılar |
| `softswitch_engine_source_blocked_total`, `…_bans_reported_total` | Güvenlik duvarı: kuralla engellenen adres için gelen sorular; SIP çekirdeğinin taşkın yüzünden bildirdiği engeller |
| `softswitch_engine_peers_refused`, `…_peer_skipped_total` | Sertifikası reddedildiği için çağrı verilmeyen TLS sağlayıcı bağlantıları ve bu yüzden atlanan rotalar (alarm: `VendorCertificateRefused`) |
| `softswitch_engine_vendors_unreachable`, `…_health_skipped_total` | Motorun yokladığı (`options_interval_sec` verilmiş) sağlayıcı bağlantılarından cevap vermediği için çağrı verilmeyenler ve bu yüzden atlanan rotalar (alarm: `VendorUnreachable`, 5 dakika; uyarı düzeyinde) |
| `softswitch_engine_vendor_registrations{state}` | Switch'in kaydolduğu sağlayıcı bağlantıları (T-14), kaydın durumuna göre: `registered`, `pending`, `failing` (son deneme başarısız: cevap yok, parola reddedildi, sağlayıcı reddetti ya da gönderilemedi), `unknown` (SIP çekirdeğine sorulamadı) (alarm: `VendorRegistrationFailing`, 10 dakika; uyarı düzeyinde) |
| `softswitch_engine_probes_total` | Panelden istenen bağlantı denemeleri ("Dene") |
| `softswitch_sip_cert_expires_seconds` | SIP çekirdeğinin kendi sertifikasının bitişine kalan süre; motor `SIP_TLS_ADDR` adresine bağlanıp okur (alarm: `SIPCertificateExpiring`, 14 gün) |
| `softswitch_engine_cdr_queue_pending`, `…_cdr_publish_errors_total` | Motorun belleğinde günlüğe (günlük yoksa NATS'a) yazılmayı bekleyen çağrı kayıtları; NATS'a başarısız gönderim denemeleri |
| `softswitch_engine_cdr_journal_records`, `…_cdr_journal_bytes`, `…_cdr_journal_max_bytes` | Motorun diskindeki çağrı kaydı günlüğü (ADR-0029): NATS'ın henüz onaylamadığı kayıtlar; günlüğün diskte tuttuğu yer ve en çok tutabileceği (`CDR_JOURNAL_MAX_MB`). NATS kesikken kayıt sayısı artar, NATS gelince sıfıra iner (alarmlar: `CallRecordJournalBacklog`, `CallRecordJournalFilling`) |
| `softswitch_engine_cdr_journal_replayed_total`, `…_cdr_journal_torn_total` | Açılışta günlükte bulunup NATS'a yeniden gönderilen kayıtlar (son dakikanın zaten gönderilmiş olanları da; tekrar zararsızdır); son kaydı yarım kalmış ya da bozuk olduğu için kuyruğu kesilen parçalar |
| `softswitch_engine_cdr_journal_errors_total{kind}` | Günlük hataları: `write` (disk toplu yazımı almadı; kayıtlar bellekte bekler), `full` (günlük dolu), `read` (kayıt geri okunamadı), `remove` (NATS'a ulaşmış parça silinemedi; yeniden başlayınca tekrar gönderilir) (alarmlar: `CallRecordJournalFailing`, `CallRecordJournalFilling`) |
| `softswitch_engine_cdr_expired_total` | Bitişi hiç bildirilmeden durumu düşen çağrılar için motorun yazdığı kayıtlar (konuşma süresi ve ücret 0; `docs/08`, "Bitişi bildirilmeyen çağrı"; alarm: `CallsExpiredUnreported`) |
| `softswitch_engine_register_total{result}`, `…_register_errors_total`, `…_trunk_unreachable_total`, `…_trunk_codec_mismatch_total` | Kayıt istekleri: `stored` / `removed` / `queried` / `refused`; saklanamayan ya da okunamayan kayıtlar (durum deposu; alarm: `EngineStateErrors`); trunk kayıtlı olmadığı için çağrıdan düşen trunk rotaları; arayanın trunk'ıyla ortak kodeki olmadığı için düşen trunk rotaları (ayar sonucu, alarmı yok) |
| `softswitch_engine_transcoded_total`, `…_transcode_full_total`, `…_transcoding_calls`, `…_transcoding_max` | Kodek çevirisi (T-09; `docs/08`, "Kodek çevirisi"): medya aktarıcısı çevirsin diye gönderilen denemeler (sıradaki rotaya geçen çağrıda deneme başına); aynı anda çevrilen çağrılar sınırdayken atlanan rotalar (alarm: `TranscodingAtLimit`); şu anda çevrilen çağrılar (Redis'te `cc:x`, Prometheus okurken sayılır); sınır (`TRANSCODE_MAX`) |
| `softswitch_engine_unauthorized_total` | Motorun, sırrını (`ENGINE_TOKEN`) taşımadığı ya da yanlış taşıdığı için 401 ile reddettiği istekler (alarm: `EngineRefusingCallers`) |
| `softswitch_rater_records_total{result}` | Kuyruktan alınan kayıtlar: `stored` / `dropped` / `retried` |
| `softswitch_rater_queue_pending` | Kuyrukta bekleyen (henüz saklanmamış) kayıt |
| `softswitch_export_queue_pending`, `softswitch_export_records_total`, `softswitch_export_errors_total` | ClickHouse'a kopyalama |
| `softswitch_archive_records_total`, `…_archive_missing_total`, `…_archive_errors_total` | Arşivleme (`CDR_KEEP_DAYS`): PostgreSQL'den silinen eski kayıtlar; ClickHouse'ta bulunamadığı için silinmeyip yeniden aktarıma alınanlar; başarısız geçişler (alarm: `ArchivedRecordsMissing`) |
| `softswitch_trace_received_total`, `softswitch_trace_stored_total`, `softswitch_trace_queue_pending` | Çağrı izi: toplayıcıya ulaşan HEP paketleri, ClickHouse'a yazılan iletiler, yazılmayı bekleyenler |
| `softswitch_trace_dropped_total{reason}`, `softswitch_trace_store_errors_total` | Saklanmayan iletiler: `unreadable` (HEP ya da SIP olarak okunamayan paket), `queue_full` (ClickHouse yetişemediği için bekleme kuyruğuna sığmayan), `store_failed` (üç denemede yazılamayan yığın); başarısız yazma denemeleri (alarm: `CallTracesDropped`) |
| `softswitch_api_requests_total{class}`, `softswitch_api_request_seconds` | Yönetim API'si istekleri |
| `softswitch_mail_outbox_pending`, `softswitch_mail_outbox_oldest_seconds` | Gönderilmeyi bekleyen e-postalar |
| `softswitch_api_db_connections` | API'nin kullandığı veritabanı bağlantısı |
| `softswitch_fx_fetch_failures_total`, `softswitch_fx_fetch_age_seconds` | Kurların dış kaynaktan alınması (`FX_SOURCE=ecb`, T-20): başarısız alma denemeleri (ulaşılamadı, HTTP hatası, okunamayan dosya; son kurlar kalır); son başarılı almadan beri geçen süre (yalnızca kaynak açıkken ve bir kez alındıktan sonra görünür) (alarm: `ExchangeRatesNotFetched`) |

Ölçü uçlarının hiçbiri kimlik doğrulaması istemez; motorunki de bilinçli olarak açık bırakıldı (yalnızca sayaç verir; gerekçesi `docs/08-cagri-arayuzu.md`, "Kimlik doğrulama"). Hepsi yalnızca 127.0.0.1'de ya da özel ağda dinlemelidir.

rater ve route-engine, API paketini içerdikleri için `softswitch_api_*` ölçülerini de sıfır olarak gösterir; anlamlı olanlar `service="api"` etiketli olanlardır.

OpenSIPS (`opensips_*`: istek ve cevap sayaçları, süren diyaloglar, paylaşımlı bellek, süreç yükü) ve rtpengine (`rtpengine_*`: oturumlar, paket ve hata sayaçları) kendi ölçülerini verir. SIP çekirdeğinin betiği de kendi sayacını tutar (`statistics` modülü, `update_stat`; Prometheus'a aynı uçtan, `modparam("prometheus", "statistics", …)` listesindeki adıyla çıkar). Bunlar ayrıca kullanılır:

| Ölçü | Anlamı |
|---|---|
| `opensips_refer_refused` | Çağrı içinde istenip 403 ile reddedilen aktarmalar (REFER, iki yönden; ADR-0028). Çağrı sürer. Sayaç SIP çekirdeği yeniden başlayınca sıfırlanır (alarm: `TransfersRefused`) |
| `opensips_media_refused` | Medya aktarıcısı teklifi almadığı için (cevap vermedi, portu kalmadı ya da hata verdi) 503 ile reddedilen yeni çağrılar (ADR-0030); teklifsiz INVITE'ta sağlayıcının 200'ündeki teklifi almadığı için cevaptan hemen sonra kapatılan çağrılar da (ADR-0043; çağrı başına bir). Sıradaki rota denenmez; motor çağrıyı `ended 503` olarak kaydeder (`cause: "media"`: seçilen bağlantının motor sayaçlarına yazılmaz, ADR-0039). Sayaç SIP çekirdeği yeniden başlayınca sıfırlanır (alarm: `CallsRefusedNoMedia`) |
| `rtpengine_ports_free`, `rtpengine_ports_used` | Aktarıcının ses portları (aralık: `RTP_PORT_MIN`–`RTP_PORT_MAX`; çağrı başına dört). Aktarıcı yalnızca kendi kullandıklarını sayar, başka bir sürecin tuttuğu portları görmez (alarm: `MediaPortsLow`) |
| `rtpengine_transcoded_media`, `rtpengine_transcode_packets_total{chain}` | Aktarıcının o anda çevirdiği akışlar; bir kodekten ötekine çevrilen paketler, yöne göre (`chain="PCMA/8000 -> PCMU/8000"`). Motorun sayacı çağrıyı listelere göre sayar, bunlar gerçekten çevrilen sesi gösterir (`make test-routing`, "transcode", iki yönü bununla denetler) |
| `rtpengine_closed_sessions_total{reason="timeout"}` | Sesi `RTP_TIMEOUT` (60) saniye kesildiği için aktarıcının kapattığı çağrılar (ADR-0027; `reason="silent_timeout"`: bekletmede `RTP_HOLD_TIMEOUT` doldu). Aktarıcı her birini SIP çekirdeğine `teardown` ile kapattırır; `rtpengine_sessions_total` ile oranı alarmın konusudur (alarm: `CallsLosingMedia`) |

## Prometheus ve alarmlar

```bash
./deploy/prod/install.sh --monitoring
```

`monitoring` profilini açar: Prometheus (`127.0.0.1:59090`), Alertmanager (`127.0.0.1:59093`) ve üretim yerleşiminde node-exporter. İkisinin arayüzü de yalnızca sunucunun kendisinden erişilir; uzaktan bakmak için SSH tüneli kullanın. Ölçüler 30 gün saklanır (`METRICS_KEEP`).

Kurallar `deploy/prod/monitoring/alerts.yml` içindedir:

| Alarm | Ne zaman | Önem |
|---|---|---|
| `ServiceDown` | Bir servis bir dakikadır okunamıyor | kritik |
| `SIPCoreRestarted` | SIP çekirdeğinin sayaçları sıfırdan başladı (süreç ölüp yeniden açıldı) | kritik |
| `SIPCoreMemoryLow` | SIP çekirdeğinin paylaşımlı belleğinin %10'undan azı boş | uyarı |
| `DecisionSlow` | Karar süresi p99 > 10 ms (5 dk) | uyarı |
| `ConfigurationStale` | Motor 5 dakikadır yapılandırma yükleyemiyor | kritik |
| `EngineStateErrors` | Motor çağrı durumuna (Redis) erişemiyor | kritik |
| `HangupFailing` | Parası biten çağrı kapatılamıyor | kritik |
| `EngineRefusingCallers` | Motor beş dakikada 20'den çok isteği sırsız ya da yanlış sırlı diye reddetti: SIP çekirdeğinin ya da API'nin `ENGINE_TOKEN` değeri motorunkinden farklı (çağrılar 503 alır), ya da biri motorun portunu yokluyor | kritik |
| `MostCallsRefused` | Çağrıların yarısından çoğu reddediliyor (10 dk, trafik varken) | uyarı |
| `CallsRefusedNoMedia` | Son 5 dakikada en az bir çağrı, medya aktarıcısı teklifi almadığı için 503 ile reddedildi: aktarıcı durmuş ya da ulaşılamıyor (`ServiceDown` rtpengine'i de gösterir) ya da portu kalmadı (günlüğünde "Ran out of ports") | kritik |
| `MediaPortsLow` | Aktarıcının ses portlarının %10'undan azı boş (5 dk): portlar bitince yeni çağrılar 503 alır | uyarı |
| `TranscodingAtLimit` | Son 10 dakikada en az bir rota, aynı anda çevrilen çağrılar `TRANSCODE_MAX`'a ulaştığı için atlandı: çağrı sıradaki rotaya gitti ya da başka rota yoksa reddedildi. İşlemcide yer varsa sınırı yükseltin ya da ilgili trunk ve bağlantıya ortak bir kodek verin | uyarı |
| `CallsLosingMedia` | Çağrıların %5'inden çoğu sesleri kesildiği için kapatılıyor (10 dk, trafik varken): ses hiç ulaşmıyor olabilir (yanlış `RTP_INTERFACE`, kapalı ses portları, NAT) | uyarı |
| `TransfersRefused` | 15 dakikada 20'den çok aktarma (REFER) reddedildi: bir müşterinin cihazı aktarmayı switch üzerinden yapmaya çalışıyor (kullanıcıları aktarmanın başarısız olduğunu görür) ya da biri aktarmayla çağrı açmayı deniyor (ADR-0028) | uyarı |
| `CallRecordsWaiting` | Çağrı kayıtları saklanmadan birikiyor | uyarı |
| `CallRecordJournalFailing` | Motor çağrı kaydı günlüğüne yazamıyor ya da ondan okuyamıyor (son 5 dakikada `write`/`read` hatası); kayıtlar bellekte bekliyor | kritik |
| `CallRecordJournalBacklog` | 10 dakikadır 1.000'den çok kayıt motorun diskinde NATS'ı bekliyor | uyarı |
| `CallRecordJournalFilling` | Günlük boyut sınırının %80'ini aştı ya da doldu | kritik |
| `CallsExpiredUnreported` | Son bir saatte bitişi bildirilmeyen çağrı ücretsiz kaydedildi (SIP çekirdeği yeniden başladı ya da motora ulaşamadı) | uyarı |
| `CallRecordsDropped` | rater bir kaydı attı | kritik |
| `ReportsFallingBehind` | Kayıtlar ClickHouse'a ulaşmıyor | uyarı |
| `CallTracesDropped` | Çağrı izleri saklanamıyor: toplayıcı ileti düşürüyor ya da ClickHouse'a yazamıyor. Çağrılar ve çağrı kayıtları etkilenmez | uyarı |
| `ExchangeRatesNotFetched` | Kurlar dış kaynaktan alınıyorsa (`FX_SOURCE=ecb`): son bir günde bir alma denemesi başarısız oldu ya da iki gündür başarılı alma yok. Son kurlar kullanılmaya devam eder; çağrılar etkilenmez. Nedeni paneldeki kur kartında ve API günlüğünde | uyarı |
| `MailNotGoingOut` | Bir e-posta 15 dakikadır bekliyor | uyarı |
| `APIErrors` | API isteklerinin %5'inden çoğu hata veriyor | uyarı |
| `DiskAlmostFull` | Bir diskin %15'inden azı boş | kritik |

**Alarmların kime gideceği** `deploy/prod/monitoring/alertmanager.yml` içindedir (kurulum örnekten kopyalar; yükseltme üzerine yazmaz). Geldiği haliyle alarmlar toplanır ama **kimseye bildirilmez**: dosyada bir alıcı (e-posta, webhook) tanımlayıp `route.receiver`'ı ona çevirin, sonra `docker compose restart alertmanager`.

İzlemenin kendisi aynı sunucudadır: sunucu giderse alarm da gitmez. Sunucunun dışarıdan yoklanması (ör. 5060'a OPTIONS, panele HTTPS) ayrıca kurulmalıdır.

## Çağrı izi

SIP çekirdeği, taşıdığı her çağrının sinyalleşmesini (arayanın INVITE'ından kapanışa, iki taraf ve bütün rota denemeleri) HEP3 ile kurulumun kendi toplayıcısına (`trace-collector`) kopyalar. Toplayıcı iletileri ClickHouse'taki `sip_messages` tablosuna yazar; panel ve API bir çağrının izini oradan okur.

```
OpenSIPS ──HEP/UDP──▶ trace-collector ──▶ ClickHouse (sip_messages) ──▶ API ──▶ panel, pcap
        └─HEP/UDP──▶ HEP_TARGET (isteğe bağlı: kendi Homer'ınız)
```

**Ne saklanır.** İleti başına: zaman (mikrosaniye), Call-ID, yön (`in`: çekirdeğin aldığı, `out`: gönderdiği), taşıma (`udp`, `tcp`, `tls`), kaynak ve hedef adres ile port, iletinin ham metni.

- **Ne izlenir (ADR-0046).** Motorun bir müşteriye ait saydığı her çağrı: kabul edilip sağlayıcıya gönderilenler (arayanın INVITE'ından kapanışa) ve motorun reddettikleri (bakiye, limit, tarife, arayan kuralı, rota yok; motor karar verirken arayanın vazgeçtiği çağrı da). Reddedilen çağrının izinde üç ileti vardır: gelen INVITE, giden ret ve arayanın ACK'i (`100 Trying` ve karar beklenirken gelen CANCEL, iz başlamadan cevaplandığı için yoktur). Motor bunu `authorize` cevabındaki `trace` alanıyla söyler; betik kendi kararını vermez. İzlenen her reddin bir çağrı kaydı da vardır.
- Parolayla tanınan trunk'ın izi kimlik bilgisini taşıyan INVITE'tan (CSeq 2) başlar: ilk INVITE ve ona verilen 401, arayanın kim olduğu bilinmeden geldiği için izlenmez.
- **Ne izlenmez.** Motorun tanımadığı hiçbir kaynağın isteği: yabancı adresin çağrısı ve ona sorulan 401, yanlış parola (kullanıcı adı var olsa da), motorun "bilinmeyen kaynak" diye reddettiği çağrı, güvenlik duvarının engellediği adres (önceden tanınıyor olsa da), taşkın ve `ban:` engeli, motora ulaşılamadığında verilen 503. Böylece yabancıların trafiği iz deposunu dolduramaz. Müşteri tarafındaki 401'i görmek için sunucuda `tcpdump` gerekir; sağlayıcının bize sorduğu 401/407 ise kabul edilen çağrının izindedir.
- TLS ile taşınan iletiler çekirdeğin çözdüğü hâlleriyle, açık metin olarak saklanır.
- **Parola özeti saklanmaz.** `Authorization` ve `Proxy-Authorization` başlıklarındaki digest cevabı (`response`) yazılmadan önce `***` ile değiştirilir; Digest dışındaki kimlik türlerinde başlığın değeri tümüyle maskelenir. Kullanıcı adı, alan ve nonce kalır. `WWW-Authenticate` ve `Proxy-Authenticate` (soru) sır taşımaz, olduğu gibi saklanır.
- Ses (RTP) ve ses kalitesi raporları (RTCP) saklanmaz. Karar (2026-11-22): ses kaydı kapsam dışı.
- İletiler arayan ve aranan numaraları içerir. Toplayıcının portu ve ClickHouse yalnızca özel ağda ya da 127.0.0.1'de açıktır.

**Saklama süresi.** Varsayılan 7 gün; `.env` içinde `TRACE_KEEP_DAYS` ile değişir (toplayıcı açılırken tablonun TTL'ini buna göre ayarlar; süresi dolan günleri ClickHouse arka planda siler). Tablo günlere bölünür ve süresi dolan gün bütün olarak atılır.

**Disk.** Laboratuvar yük testinde bir çağrı 12 ileti ve yaklaşık 4,8 KB ham metin tuttu. Diskteki boyut sıkıştırmaya bağlıdır: yük testinin birbirinin aynısı iletileri çağrı başına 0,3 KB'a iniyor; gerçek trafikte bunun birkaç katı beklenmelidir ama **ölçülmedi**. Kaba hesap: saniyede 1.000 çağrı günde 86 milyon çağrı ve 400 GB ham metin eder; 7 gün için, sıkıştırma oranına göre yüzlerce GB ayırın ve ilk günlerde `system.parts` üzerinden gerçek boyuta bakıp süreyi ayarlayın. Yer yoksa süreyi kısaltın ya da saklamayı kapatın.

**Çağrılara etkisi.** Toplayıcı çağrı yolunda değildir. Kopya UDP ile, cevap beklenmeden gönderilir: toplayıcı ya da ClickHouse kapalıyken çağrılar sürer, yalnızca o süredeki izler eksik kalır. ClickHouse yavaşladığında toplayıcı en çok 100.000 iletiyi bellekte bekletir, fazlasını sayarak atar (`softswitch_trace_dropped_total`, alarm `CallTracesDropped`). Laboratuvarda 200 CPS / 2.000 eşzamanlı çağrıda (saniyede ~2.400 ileti) toplayıcı aldığı 71.953 iletinin hepsini yazdı, 6.000 çağrının hepsinin izi tamdı; karar süresi p99 3,1 ms (toplayıcısız aynı gün 4,9 ms: fark ölçüm gürültüsü içinde). Reddedilen çağrılar da izlendikten sonra (T-26) aynı yükte toplayıcı 71.861 iletinin hepsini yazdı, hiçbirini atmadı (yük testinin çağrıları hep kabul edilir; ret başına 3 ileti eklenir). Hedef yükte (1.000 CPS) denenmedi.

**Kapatmak.** `.env` içinde `TRACE_TARGET=` (boş) yazıp `docker compose up -d opensips`: çekirdek kopya göndermez. `trace-collector` servisi çalışmaya devam eder ama boşta kalır.

**Kendi Homer'ınıza göndermek.** `HEP_TARGET=<adres>:<port>` verilirse aynı kopya, kendi toplayıcımıza ek olarak o adrese de gider ([Homer](https://sipcapture.org) gibi; bu kurulumun parçası değildir). Bu kopya maskelenmez: SIP çekirdeğinden olduğu gibi çıkar ve digest cevabını içerir. Laboratuvarda gönderim basit bir alıcıyla denenir; gerçek bir Homer kurulumuyla denenmedi. Kopyalar SIP çekirdeğinin ayrı bir soketinden (`HEP_IP:HEP_PORT`, 9061) çıkar; bütün toplayıcılar sunucudaysa bu soket 127.0.0.1'dedir, dışarıda bir `HEP_TARGET` varsa `SIP_IP`'dedir: o toplayıcıya ulaşan iç adresi `HEP_IP` ile verin. Sokete gelen HEP paketi işlenmez, çağrı sayılmaz (T-51; `docs/09-kurulum.md`, "Dışarıya açılmaması gerekenler").

### İzi okumak

- Panel: çağrı kayıtlarında bir çağrıya tıklayınca açılan detayda "SIP akışı" bölümü; "Araçlar → Çağrı izi" sayfasında Call-ID ile arama. Uçlar sütun, iletiler zaman sırasıyla ok olarak çizilir; bir iletiye tıklayınca ham metni görünür.
- API: `GET /v1/trace?call_id=…` ve `GET /v1/trace/pcap?call_id=…` (`docs/06-api.md`).
- Erişim çağrı kayıtlarıyla aynı kurala uyar: hesap, yalnızca kendi müşterilerinden birine ait kaydı olan çağrının izini görür; başkasınınki "bulunamadı" döner. Henüz kaydı olmayan (süren) çağrının izini yalnızca bütün müşterileri gören hesap görebilir.

### pcap dosyası

"pcap indir" düğmesi ve `/v1/trace/pcap`, saklanan iletilerden Wireshark ve tcpdump'ın açtığı bir yakalama dosyası üretir (klasik pcap, Ethernet). Wireshark'ta "Telephony → VoIP Calls → Flow Sequence" ile akış çizilir.

**Bu dosya hattan yakalanmış gerçek paketler değildir.** Her ileti için Ethernet, IP ve UDP ya da TCP başlığı, bilinenlerden (adresler, portlar, zaman) üretilir:

- MAC adresleri uydurmadır; IP kimliği, TTL ve pencere değerleri sabittir.
- TCP ve TLS ile taşınan iletiler düz TCP olarak yazılır: el sıkışma yoktur, sıra numaraları 1'den başlar, uzun iletiler 1.400 baytlık parçalara bölünür. TLS iletileri **çözülmüş** hâlleriyle yer alır; dosyada TLS kaydı ya da sertifika yoktur (5061 portundaki bu düz metni Wireshark SIP olarak çözer).
- Yeniden gönderimler, yalnızca SIP çekirdeğinin işleyip izlediği kadarıyla vardır; ağ düzeyindeki kayıplar, parçalanma ve gecikmeler görünmez. Zaman, çekirdeğin iletiyi işlediği andır.
- Ağ sorunu (paket kaybı, MTU, TLS el sıkışması) ayıklamak için sunucuda `tcpdump` gerekir; bu dosya "kim kime ne dedi" sorusunu cevaplar.

## Paneldeki sistem sayfası

Panelde "Araçlar → Sistem kaynakları" sayfası, Prometheus kurulu olmasa da çalışır: API süreci makineyi beş saniyede bir kendisi örnekler ve son on dakikayı bellekte tutar (`pkg/sysinfo`, `GET /v1/system`).

- **Makine:** işlemci kullanımı, bir dakikalık yük, bellek, sunucunun kendi ağ arayüzlerindeki gelen ve giden trafik, diskler. Rakamlar çekirdeğin `/proc` altındaki sayaçlarından okunur; konteyner içinden okunsa da makinenin tamamını anlatır. Ağ rakamı, API sunucunun ağını kullandığında (üretim yerleşimi) sunucunun arayüzlerini, özel ağda (duman testi) yalnızca konteynerinkini gösterir.
- **Servisler:** PostgreSQL, motor ve ClickHouse'u API kendisi bilir; diğerleri `SYSTEM_SERVICES` ile verilir (`Ad=http://adres/yol, …`; kurulumda SIP çekirdeği, medya ve ücretlendirme tanımlıdır). Bir servis, adresi 500'den küçük bir HTTP koduyla iki saniye içinde cevap veriyorsa çalışıyor sayılır. Adresler panele gönderilmez.
- `SYSTEM_DISKS`: diski gösterilecek yollar (virgülle; varsayılan `/`, yani konteynerin ve Docker verisinin durduğu disk).
- Sayfayı yalnızca sistemi işleten (kök) hesap görür.

Bu sayfa anlık duruma bakmak içindir; geçmiş, eşikler ve alarmlar için Prometheus ve yukarıdaki kurallar kullanılır. Redis ve NATS ayrıca yoklanmaz: erişilemezlerse motorun ve ücretlendirmenin ölçüleri ve alarmları gösterir.
