# Karar kayıtları (ADR)

Verilmiş her önemli mimari karar ayrı bir dosyadır: bağlam, seçenekler, karar, sonuçları. Kararlar bağlayıcıdır; birine aykırı değişiklik yapılmaz. Bir kararı değiştirmek gerekiyorsa önce sorulur, sonra eskisini "Yerini aldı: ADR-NNNN" diye işaretleyen yeni bir kayıt yazılır; eski kayıt silinmez.

Yeni kayıt: sıradaki numara, `NNNN-kisa-ad.md`, aynı başlıklar. Karar verilmemiş noktalar kayıtlarda **AÇIK SORU** diye geçer ve `docs/ARCHITECTURE.md` sonundaki listede toplanır.

| No | Karar | Tarih |
|---|---|---|
| [0001](0001-sip-yigini-yazilmaz.md) | SIP yığını yazılmaz: OpenSIPS ve rtpengine | 2026-11-21 |
| [0002](0002-is-mantigi-motorda-surumlu-arayuz.md) | İş mantığı motorda; SIP çekirdeğiyle sürümlü, sunucudan bağımsız arayüz | 2026-11-21 |
| [0003](0003-cagri-yolunda-sql-yok.md) | Çağrı yolunda SQL yok: bellekte görüntü, bildirimle yenileme | 2026-11-21 |
| [0004](0004-para-tamsayidir.md) | Para tamsayıdır: int64 mikro birim | 2026-11-21 |
| [0005](0005-rezervasyon-tabanli-faturalama.md) | Rezervasyon tabanlı faturalama ve motorun çağrıyı kapatması | 2026-11-21 |
| [0006](0006-cdr-kaybolmaz.md) | CDR kaybolmaz: en-az-bir-kez teslim, PostgreSQL asıl kayıt, ClickHouse kopya | 2026-11-21 |
| [0007](0007-acik-cekirdek-iki-depo-agpl.md) | Açık çekirdek: iki depo, AGPLv3 + CLA, ayrım yetenekle | 2026-11-21 |
| [0008](0008-genisleme-noktalari.md) | Enterprise, core'a yalnızca genişleme noktalarından bağlanır | 2026-11-21 |
| [0009](0009-gpl-bilesenlerle-yalnizca-ag.md) | GPL bileşenlerle yalnızca ağ üzerinden konuşulur | 2026-11-21 |
| [0010](0010-tek-fiyat-secim-kodu.md) | Fiyat seçimi ve tutar hesabı tek koddur; çağrı başladığı andaki fiyatla ücretlenir | 2026-11-22 |
| [0011](0011-para-birimleri.md) | Tarife sahibinin para birimindedir; kur yalnızca karşılaştırma ve rapor içindir | 2026-11-22 |
| [0012](0012-agirlikli-dagitim-durumsuz.md) | Ağırlıklı dağıtım durumsuzdur: sıra call_id'den türetilir | 2026-11-22 |
| [0013](0013-kayitlar-motorda.md) | Kayıt defteri (registrar) motordadır, Redis'te durur | 2026-11-23 |
| [0014](0014-nat-contact-yeniden-yazilmaz.md) | NAT: Contact yeniden yazılmaz; gerçek adres dialogda saklanır | 2026-11-23 |
| [0015](0015-tls-srtp-ve-ad-denetimi.md) | SRTP yalnızca TLS ile; sağlayıcı sertifikasındaki adı motor denetler | 2026-11-22 |
| [0016](0016-motor-arayuzu-ortak-sir.md) | Motorun arayüzü tek ortak sırla korunur | 2026-11-22 |
| [0017](0017-kenar-korumasi-uygulama-duzeyinde.md) | Kenar koruması ve güvenlik duvarı önce uygulama düzeyinde | 2026-11-22 |
| [0018](0018-cagri-izi-yalnizca-sinyallesme.md) | Çağrı izi: kendi HEP toplayıcımız, yalnızca sinyalleşme; ses kaydı yok | 2026-11-22 |
| [0019](0019-denetim-kaydi-ve-iki-adim.md) | Denetim kaydı işlemin içinde yazılır; sırlar tek biçimde şifrelenir | 2026-11-22 |
| [0020](0020-trunk-adresleri.md) | Trunk kaynakları adres ve ağ listesidir; en dar ağ kazanır | 2026-11-22 |
| [0021](0021-tek-sunucu-kurulumu-compose.md) | Kurulum: Docker Compose ile tek sunucu; çağrı yolu sunucunun ağında | 2026-11-22 |
| [0022](0022-pazar-yeri-modulleri.md) | Ülkeye ve sağlayıcıya özgü entegrasyonlar pazar yeri modülüdür | 2026-11-22 |
| [0023](0023-panel-teknolojisi-ve-kurallari.md) | Panel: React + TypeScript + Vite; tasarım paketine uyulur ("Arayüz Türkçedir" sonucunun yerini 0049 aldı) | 2026-11-21 |
| [0024](0024-baglanti-ayarlari-temel-gelismis.md) | Bağlantı ayarları Temel ve Gelişmiş olarak ayrılır; davranışı olmayan ayar olmaz | 2026-11-23 |
| [0025](0025-izleme-bagimliliksiz-olculer.md) | İzleme: bağımlılıksız ölçü paketi, Prometheus biçimi | 2026-11-22 |
| [0026](0026-ack-gelmeyen-cagri.md) | Cevaplanan çağrı 32 saniyede ACK'lenmezse kapatılır | 2026-11-23 |
| [0027](0027-olu-cagri-medya-zaman-asimi.md) | Ölü çağrıyı medya zaman aşımı kapatır; switch oturum yenilemesi istemez | 2026-11-23 |
| [0028](0028-refer-prack-erken-medya.md) | PRACK ve erken medya geçer; REFER reddedilir | 2026-11-23 |
| [0029](0029-cdr-disk-gunlugu.md) | NATS'a yazılamayan çağrı kayıtları motorun diskinde saklanır; düşen çağrı da kaydedilir | 2026-11-23 |
| [0030](0030-medya-aktaricisi-yoksa-ret.md) | Medya aktarıcısı cevap vermezse çağrı 503 ile reddedilir | 2026-11-23 |
| [0031](0031-faks-dtmf.md) | T.38 yeni teklifi olduğu gibi geçer; DTMF dönüşümü yok | 2026-11-23 |
| [0032](0032-gelen-cagri-genisleme-noktasi.md) | Müşteri olmayan kaynaktan gelen çağrı için motorda yeni genişleme noktası | 2026-11-23 |
| [0033](0033-canli-tutma-ertelendi.md) | Kayıtlı trunk'a canlı tutma şimdilik yok | 2026-11-23 |
| [0034](0034-trunk-ag-siniri.md) | Trunk ağlarında /8 (IPv4) ve /16 (IPv6) genişlik sınırı kalır | 2026-11-23 |
| [0035](0035-saglayiciya-kayit-sip-cekirdeginden.md) | Sağlayıcıya kayıt SIP çekirdeğinden gider; yoklama motorda kalır | 2026-11-23 |
| [0036](0036-kur-kaynagi.md) | Kurlar isteğe bağlı olarak Avrupa Merkez Bankası'ndan alınır | 2026-11-23 |
| [0037](0037-pazar-yeri-modul-modeli.md) | Pazar yeri modülleri ikiliye derlenir ve lisansla açılır | 2026-11-23 |
| [0038](0038-sessiz-erken-medya-calma-siniri.md) | Sessiz erken medyalı çağrının çalması medya zaman aşımıyla sınırlıdır | 2026-11-23 |
| [0039](0039-gonderilmeyen-deneme-nedeni.md) | Gönderilemeyen deneme `ended` olayında `cause: "media"` ile bildirilir; bağlantıya yazılmaz | 2026-11-23 |
| [0040](0040-bitisi-bildirilmeyen-cagrinin-ucreti.md) | Bitişi bildirilmeyen çağrı süresiz ve ücretsiz kaydedilir | 2026-11-23 |
| [0041](0041-kodek-cevirisi-tek-dugum.md) | Kodek çevirisi listelere göre, izinle ve tek düğümde yapılır; eşzamanlı sınırı motor sayar | 2026-11-23 |
| [0042](0042-sinirlar-iki-yonu-birlikte-sayar.md) | Trunk'ın ve müşterinin sınırları iki yönü birlikte sayar; teslim edilen çağrı hedef müşterinin sınırlarına da tabidir | 2026-11-23 |
| [0043](0043-teklifsiz-invite.md) | Teklifsiz INVITE'ta teklif 200'de, cevap ACK'te medya aktarıcısından geçer; cevapsız ACK ve aktarıcı reddi çağrıyı kapatır | 2026-11-23 |
| [0044](0044-resmi-tatil-takvimi.md) | Resmî tatil, hesabın takviminde başka bir gün (varsayılan pazar) sayılır; fiyat ve rota pencereleri tatili aynı kodla bilir | 2026-11-23 |
| [0045](0045-iki-adim-zorunlulugu.md) | İki adımlı doğrulama hesap ayarıyla zorunlu kılınır; kurmamış kullanıcının oturumu yalnızca kuruluma açılır | 2026-11-23 |
| [0046](0046-reddedilen-cagrinin-izi.md) | Reddedilen çağrı, motor müşterinin olduğunu söylediğinde izlenir; yabancının hiçbir isteği izlenmez | 2026-11-23 |
| [0047](0047-ipv6-adres-ailesi.md) | IPv6'da medyanın adres ailesini betik hedefin adres biçiminden seçer; kaynak adresi motorda tek biçime çevrilir | 2026-11-23 |
| [0048](0048-gorunumde-yalnizca-degisen-fiyatlar.md) | Görüntü yüklenirken fiyatların yalnızca değişeni okunur; değişikliği veritabanındaki tetikleyici not eder | 2026-11-24 |
| [0049](0049-panelin-ingilizce-arayuzu.md) | Panelin Türkçenin yanında İngilizce arayüzü olur; dil katmanı bağımlılıksızdır | 2026-11-24 |
| [0050](0050-kullaniciya-donuk-belgelerin-ingilizcesi.md) | Kullanıcıya dönük belgelerin İngilizcesi `docs/en/` içinde durur; kaynak Türkçedir, kayma `make check-docs-en` ile denetlenir | 2026-11-24 |
| [0051](0051-surumler-yayin-ve-ci.md) | Sürümler `vANA.ARA.YAMA` etiketiyle GitHub'da yayımlanır, sürüm derlemeye gömülür; sunucu `upgrade.sh <etiket>` ile yükseltilir, geri dönüş yedekle; CI laboratuvarsız denetimleri yapar | 2026-11-24 |
