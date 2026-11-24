# Yönetim paneli

`web/` altındaki React + TypeScript uygulaması. Ölçü, renk ve metinlerin kaynağı ayrı tutulan bir tasarım paketidir (bu depoda değildir); pakette olmayan ekran ve durumlar aşağıdaki sapmalar tablosundadır.

## Çalıştırma

```
set -a; . test/seed/dev.env; set +a; make api-run     # API, laboratuvar anahtarıyla
make seed                                              # boş veritabanına örnek veri (bir kez)
make panel-install && make panel-dev                   # http://127.0.0.1:58081
```

Giriş anahtarı ve laboratuvar kullanıcısı `test/seed/dev.env` içindedir; kullanıcıyı `make seed-user` oluşturur. Üretimde `make panel-build` çıktısı (`web/dist`) API ikilisi tarafından `WEB_DIR` ile sunulur; panel ve API aynı kökende durur.

## Yapı

| Yol | İçerik |
|---|---|
| `src/panel.tsx` | Kabuk, menü, rotalar; `createPanel(...extensions)` |
| `src/lib/api.ts` | API istemcisi, sayfalı liste ve başvuru listesi kancaları |
| `src/lib/format.ts` | Tutar, sayı, tarih biçimleri (dile uyar); tutar ayrıştırma |
| `src/lib/i18n.tsx`, `src/lib/lang.ts` | Dil katmanı: `t`, `tx`, `rich`, dil seçimi ve bağlamı |
| `src/i18n/` | Metin katalogları, alan başına bir dosya (Türkçe ve İngilizce yan yana); `scripts/check-i18n.ts` denetler |
| `src/lib/session.tsx` | Oturum bağlamı ve `PanelExtension` arayüzü |
| `src/ui/ui.css`, `src/ui/kit.tsx` | Tasarım belirteçleri ve ortak bileşenler |
| `src/screens/` | Ekranlar |

## Ekranlar

Giriş, Genel bakış, Çağrı kayıtları, Canlı izleme, Canlı çağrılar, Düşük bakiye uyarıları, Müşteriler ve detay (Genel, Trunk'lar, Hesap hareketleri, Çağrı kayıtları), Sağlayıcılar ve detay, Tarifeler ve detay (fiyat tablosu, fiyat ekle/düzenle, dört adımlı CSV yükleme), Yönlendirme planları ve detay, Arayan numara kuralları, Resmî tatiller, Rota simülatörü, Zamanlanmış raporlar, Ayarlar (API anahtarları, görünüm), Kullanıcılar ve roller. Denetim kaydı (Sistem menüsü: süzgeçli, sayfalı tablo; satıra tıklayınca değişen alanların önceki ve yeni değerleri). İki adımlı doğrulama: girişte parola adımından sonra kod adımı (yedek kod seçeneğiyle); kurulum, yedek kodları yenileme ve kapatma Kullanıcılar ve roller ekranındaki "İki adımlı doğrulama" kartında; yönetici başka bir kullanıcınınkini aynı ekrandan sıfırlar. Zorunlu kılma (T-23): Ayarlar ekranındaki "Güvenlik" kartında anahtar; hesap ikinci adımı zorunlu kılmışsa kurmamış kullanıcı giriş sonrasında yalnızca "İki adımlı doğrulamayı kurun" sayfasını görür, kurulum bitince panel açılır. Denetim kaydı süzgeçleriyle "CSV indir" düğmesinden iner.

## Sürümler

Community panelinde Enterprise özelliği hiçbir biçimde görünmez. Başka sürümlerin ekranları kendi depolarında yazılır ve `PanelExtension` ile eklenir: metin kataloğu (`messages`; "Dil"), menü öğesi (grubu kimlikle: `monitoring`, `parties`, `pricing`, `numbers`, `tools`, `system`; eski Türkçe grup adları da geçer), rota, müşteri/sağlayıcı sekmesi, müşteri formuna bölüm, oturuma göre menü süzme (`filterNav`), üst çubuk içeriği (`topBar`) durum etiketleri (`addStatuses`) ve denetim kaydındaki kaynak, işlem ve ayrıntı adları (`addAuditLabels`, `addAuditDetails`; `screens/AuditLog.tsx`). Yeni bir eklenme noktası gerekiyorsa önce burada genel amaçlı olarak tanımlanır.

## Dil

Panel Türkçe (varsayılan) ve İngilizcedir (ADR-0049). Seçim: adresteki `?lang=en` ya da `?lang=tr` (seçilen hatırlanır; ekran görüntüsü otomasyonu bunu kullanır), üst çubuktaki TR/EN düğmesi, giriş sayfasındaki aynı düğme ya da Ayarlar › Görünüm › Dil. Tercih bu tarayıcıda `localStorage` `tsw.lang` olarak durur; tarayıcının kendi dili dikkate alınmaz. Dil değişince kabuk yeniden çizilir, açık sayfa yeniden kurulur (açık pencere kapanır, oturum ve adres kalır); `<html lang>` dile uyar.

- **Metin yazmak:** her metin `src/i18n/<alan>.ts` içinde anahtarla, iki dilde birlikte. `en` nesnesi `Record<keyof typeof tr, string>` olduğu için eksik ya da fazla İngilizce metin `tsc`'yi düşürür. Yer tutucu `{ad}`; İngilizce çoğul `{n|call|calls}` (n 1 ise ilki). Ekranda `t('anahtar', { ad })`; öğe yerleştirmek için `rich`; çalışma anında bilinen anahtar ya da düz metin (uzantının etiketi) için `tx`. `t` modül yüklenirken çağrılmaz; etiket listeleri işlev olarak kurulur (geliştirme sunucusu konsolda uyarır).
- **Denetim:** `npm run check-i18n` (Node 23.6+; `npm run build` de çalıştırır): iki dilde aynı anahtarlar, iki dosyada tanımlı anahtar yok, boş metin yok, yer tutucular aynı; kaynakta adı geçmeyen anahtarları uyarı olarak listeler (menü grup adları çalışma anında kurulduğu için listede görünür).
- **Biçimler:** Türkçe `12.480,52`, `24.11.2026 14:05`; İngilizce `12,480.52`, `2026-11-24 14:05` (ISO 8601 sırası: İngiliz ve Amerikalı okur için aynı anlam, sütun genişliği aynı). Tutar girişi İngilizcede nokta ondalık, virgül binlik ayırıcıdır. CSV'deki tutarlar dilden bağımsız (nokta ondalık). Tarih-saat giriş kutularını tarayıcı kendi diliyle çizer.
- **Sunucu metinleri:** API'nin hata iletileri İngilizcedir ve iki dilde de olduğu gibi gösterilir (bu yüzden Türkçe kipte de İngilizce görünür; eskiden de böyleydi). Panel kendi cümlelerini HTTP durumuna ve `code` alanına göre çevirir (`two_step_required`, tarife yükleme hata kodları).
- **Uzantılar:** `PanelExtension.messages` (`{ tr, en? }`); İngilizcesi olmayan anahtar Türkçe görünür. Enterprise'ın menü, sekme, durum ve denetim adları çevrildi; Enterprise ekranları ve müşteri portalı şimdilik Türkçe (İngilizce kipte bu ekranlarda ortak bileşenlerin metinleri İngilizce, ekranın kendi metinleri Türkçe görünür).
- **Bilinen sınırlar:** güvenlik duvarında "taşkın için kalıcı engel" notu, o anki dilde kurala veri olarak yazılır; CSV sütun adları API alan adlarıdır, çevrilmez; laboratuvar ve örnek veriler (adlar, notlar) Türkçe kalır.

### İngilizce terim sözlüğü

| Türkçe | İngilizce |
|---|---|
| Müşteri | Client |
| Sağlayıcı | Vendor |
| Sağlayıcı bağlantısı, bağlantı | Vendor connection, connection |
| Taraflar (menü) | Partners |
| Fiyat ve rota (menü) | Rating and routing |
| Trunk | Trunk |
| Tarife; alış tarifesi | Tariff; buy tariff |
| Fiyat; dakika fiyatı | Rate; rate per minute |
| Bağlantı ücreti; ilk süre; artım; ücretsiz süre | Connection fee; initial increment; billing increment; free period |
| Geçerlilik başlangıcı / sonu | Effective from / until |
| Yönlendirme planı; rota; politika | Routing plan; route; policy |
| Zararına rota | Loss-making route |
| Önek; teknik önek | Prefix; tech prefix |
| Arayan numara; arayan numara kuralları | Caller ID (CLI); CLI rules |
| Aranan | Called number |
| Kimlik başlığı | Identity header (P-Asserted-Identity) |
| Çağrı kaydı | CDR |
| Eşzamanlı çağrı | Concurrent calls |
| ASR, ACD, PDD, NER, CPS, LCR | aynı |
| Yedek rotaya geçiş | Failover |
| Ağırlık; öncelik | Weight; priority |
| Çalma süresi; azami süre | Ring timeout; max duration |
| Sağlayıcıyı yokla (OPTIONS); erişim | OPTIONS ping; reachability |
| Taşıma; sunucu | Transport; host |
| Kayıt (REGISTER) | Registration |
| Digest doğrulama | Digest authentication |
| Kodek çevirisi | Transcoding |
| Paket dakika | Minute bundle |
| Bakiye; kredi limiti | Balance; credit limit |
| Ödeme; düzeltme; iade | Payment; adjustment; refund |
| Hesap hareketleri | Transactions |
| Sağlayıcıya borcumuz | Owed to the vendor |
| Satış; alış; kâr; marj | Sell; buy; margin; margin % |
| Gelir; maliyet | Revenue; cost |
| Kur; para birimi | Exchange rate; currency |
| Resmî tatil | Public holiday |
| Rota simülatörü | Route simulator |
| Canlı izleme; canlı çağrılar | Live monitor; live calls |
| Konuşuyor; çalıyor; kuruluyor | Talking; ringing; setting up |
| Kırılım; gün bazlı | Breakdown; by day |
| Güvenlik duvarı; engel; engeli kaldır | Firewall; block; lift block |
| Çağrı izi | Call trace |
| Sistem kaynakları | System resources |
| Düşük bakiye uyarısı; eşik | Low balance alert; threshold |
| Zamanlanmış rapor | Scheduled report |
| Denetim kaydı | Audit log |
| Yönetici; salt okunur | Admin; read-only |
| Oturum aç; oturumu kapat | Sign in; sign out |
| İki adımlı doğrulama; yedek kod | Two-step verification; recovery code |
| API anahtarı; iptal et | API key; revoke |
| Gelişmiş ayarlar | Advanced settings |
| Görünüm; tema; tablo yoğunluğu (Orta/Sıkı) | Appearance; theme; table density (Comfortable/Compact) |
| Vazgeç | Cancel |

## Tasarımdan bilinçli sapmalar

| Tasarım | Uygulama | Neden |
|---|---|---|
| Tek "Tarih aralığı" metin kutusu | Başlangıç ve bitiş için iki tarih-saat alanı | Tasarımda gerçek seçici yok |
| Çağrı detayında satış/alış dakika fiyatı | Satış/alış öneki | CDR birim fiyatı saklamıyor; tutardan türetmek yuvarlama hatası veriyor |
| Çağrı detayında tüm rota denemeleri | Yalnızca çağrıyı taşıyan bağlantı | Deneme geçmişi route-engine bağlanınca gelecek (Faz 1) |
| Simülatörde "Atlandı" satırları ve örnek senaryolar | Yok | API atlanan rotaları döndürmüyor; örnek senaryolar sahte veri gerektirir |
| Tarife listesinde "Önek sayısı", tarife başlığında "N müşteride kullanılıyor" | Yok | API'de karşılığı yok |
| Trunk listesi ve "Trunk ekle" formu | "Şifreleme" sütunu ve seçeneği (Serbest / TLS zorunlu / TLS ve istemci sertifikası), sertifika seçilince "Sertifikadaki ad" alanı | Tasarımda yok; trunk başına TLS kuralı (B1b) için ortak form bileşenleriyle eklendi |
| Trunk listesi ve "Trunk ekle" formu | "Kaynak IP" alanı yerine çok satırlı "Kaynak adresler" alanı (satır başına ya da virgülle ayrılmış adres ve ağ; biçim denetimi alanın altında, çakışma ve sunucu reddi form altındaki uyarıda); listede sütun "Adresler / kullanıcı": sığdığı kadar adres ve birden çoksa parantez içinde sayısı, tamamı ipucunda; satırda "Düzenle" (ad, adresler, teknik önek, şifreleme; kimlik doğrulama türü değişmez) | Tasarımda tek IP alanı var ve trunk düzenleme yok; trunk başına birden çok adres ve ağ (B3) için ortak form bileşenleriyle eklendi. Çok satırlı alan için `FormModal` alan tanımına `mono` ve `rows` eklendi |
| Trunk listesi ve "Trunk ekle" formu | Kayıt durumu, ayrı bir sütun açmadan "Adresler / kullanıcı" sütununda, kullanıcı adının yanında: kayıtlıysa yeşil "Kayıtlı" düğmesi (birden çok kayıtta sayısı; ipucunda kayıtların geldiği adresler), değilse soluk "kayıtlı değil", motor okunamıyorsa "kayıt bilinmiyor". Düğme "Trunk kaydı" penceresini açar: her kayıt için geldiği adres ve port, taşıma, "NAT arkasında" / "Bağlantı üzerinden" rozeti, cihazın bildirdiği adres, ilk kayıt, geçerlilik sonu, cihaz adı ve onaylı "Kaydı sil". Durum 15 saniyede bir yenilenir. Formda, kullanıcı adıyla tanınan trunk için "Kayıt sayısı" (1–10) | Tasarımda yok; kayıt (REGISTER) ve adres çevirisi (B2) için ortak bileşenlerle (rozet, satır içi düğme, pencere, onay) eklendi. Ayrı sütun tabloyu 1440 px genişlikte yatay kaydırmaya zorladığı için durum var olan esnek sütuna kondu |
| Plan detayında rota satırı | "Pay" sütunu (öncelik politikasında; LCR'de pay fiyata bağlı olduğundan gösterilmez), satırda "Düzenle" (öncelik ve ağırlık) | Tasarımda yok; ağırlıklı dağıtım (C2) için ortak bileşenlerle eklendi |
| "Bağlantı ekle" formu | Taşıma TLS seçilince "Sertifikadaki ad" alanı | Tasarımda yok; sağlayıcı sertifikasında ad denetimi (B1c) için |
| "Bağlantı ekle" ve "Trunk ekle" formları (tek parça) | Formlar ikiye ayrıldı: üstte temel alanlar (bağlantıda ad, sunucu, port, taşıma; trunk'ta ad, nasıl tanındığı ve adresleri ya da kullanıcı adı), altta kapalı gelen "Gelişmiş ayarlar" bölümü (ok işaretli başlık; tıklayınca açılır). Kapalıyken başlığın yanında, varsayılandan farklı ayar varsa mavi "N ayar değiştirilmiş" rozeti, yoksa soluk "varsayılan değerlerde; çoğu kurulumda dokunmak gerekmez" yazısı durur; bölümdeki bir alan hatalıysa bölüm kendiliğinden açılır. Bölüm içi, kalın ara başlıklarla (ve altında tek satır açıklamayla) gruplanır; her alanın altında sade bir yardım satırı vardır. Eski alanlardan teknik önek, silinecek hane, limitler, sertifikadaki ad, şifreleme ve kayıt sayısı bu bölüme taşındı. Pencere 640 px | Tasarımda yok; karar (2026-11-23): ayrıntılı ayarlar olsun ama "temel" ve "gelişmiş" olarak ayrılsın. Ortak bileşenlere eklendi (`kit.tsx`: `FormModal` alanlarında `advanced`, `Disclosure`, ara başlık için `type: 'section'`), Enterprise ekranları da kullanabilir |
| "Bağlantı ekle" formu, Gelişmiş ayarlar: "Çalma süresi sınırı" açıklaması | Açıklamaya ikinci cümle eklendi: "Sağlayıcı çalarken ses kanalını açıp (183) hiç ses göndermezse çağrı en çok medya zaman aşımı kadar (varsayılan 60 sn) çalar." | Tasarımda yok; ADR-0038 (S-18) kararı: sessiz erken medyalı çağrıyı medya aktarıcısının zaman aşımı, ayar daha büyük olsa da kapatır. Ayarın bu durumdaki sınırı ADR-0024 gereği alanın yanında söylenir |
| "Bağlantı ekle" ve "Trunk ekle" formları, Gelişmiş ayarlar → "Ses kodekleri" | "Kodek çevirisi" onay kutusu ("Ortak kodek yoksa sesi çevir") ve sade bir açıklama satırı; varsayılan kapalı, işaretlenince "N ayar" sayacına girer. Kodek listesinin açıklamasındaki "Sistem kodek çevirmez" cümlesi kaldırıldı, bağlantıda yerine "çeviri açıksa ve trunk'ta kodek seçiliyse gönderilir" yazıldı | Tasarımda yok; kodek çevirisi (B6, T-09) için ortak form alanıyla (`type: 'check'`) eklendi |
| — | Kodek seçimi: sıralı çoklu seçim (`kit.tsx`: `PickList`, alan türü `pick`). Seçenekler `Seg` düğmeleriyle aynı görünümde; seçilen düğme vurgulanır ve seçilme sırasını gösteren küçük bir numara taşır | Tasarımda sırası önemli çoklu seçim yok |
| Bağlantı listesi: "Eklenecek önek" ve "Silinecek hane" sütunları | Yerlerine "Erişim" (yoklama durumu: yeşil "Erişilebilir", kırmızı "Cevap vermiyor", nötr "Denetleniyor", soluk "Denetlenmiyor"; ipucunda ne zamandır ve son cevap) ve "Gelişmiş ayar" ("N ayar" rozeti ya da soluk "varsayılan") sütunları; satırda "Dene" ve "Düzenle" | İki alan gelişmiş bölüme taşındı; liste 1440 px'te yatay kaydırmadan sığsın diye sütun sayısı korunmadı. Bağlantı artık düzenlenebiliyor (eskiden yalnızca eklenip açılıp kapatılabiliyordu) |
| Bağlantı listesi ve "Bağlantı ekle" formu | Gelişmiş ayarlar → "Sağlayıcıya giriş" bölümünde, parolanın altında "Kayıt" onay kutusu ("Sağlayıcıya kayıt ol (REGISTER)"); işaretlenince "Kayıt süresi (saniye)" (60–86400, varsayılan 3600) ve "Kayıt alanı" (boş = sunucu) alanları açılır; kullanıcı adı yoksa kutunun altında hata. İşaretli kutu "N ayar" sayacına girer. Listede "Erişim"in yanında "Kayıt" sütunu: kayıt kapalıysa soluk "—", bağlantı kapalıysa "Kapalı", ad yoksa "Giriş bilgisi yok"; yeşil "Kayıtlı" (ipucunda ne zamandır ve sağlayıcının verdiği süre), nötr "Kaydediliyor", kırmızı "Parola reddedildi" ya da "Kayıt olmadı" (ipucunda nedeni: cevap yok, reddedildi, gönderilemedi; ve yeniden deneneceği), motor okunamıyorsa soluk "bilinmiyor". Durum 15 saniyede bir yenilenir; tablonun en küçük genişliği 1250 px | Tasarımda yok; sağlayıcıya kayıt (T-14) için ortak bileşenlerle (`check`, `int`, `mono` alanları, `Badge`) eklendi. Ayrı sütun, "Erişim" ile birleştirilseydi iki ayrı durumun (yoklama, kayıt) hangisinin kırmızı olduğu okunmazdı |
| — | "Bağlantıyı dene" penceresi (520 px): sağlayıcıya tek bir yoklama gönderilir; sonuç rozet ("Çalışıyor" yeşil, "Dikkat" sarı, "Sorun var" kırmızı), tek cümlelik başlık (cevap kodu ve süre, "Cevap gelmedi", "Sunucu adı bulunamadı", "Bu adreste ve portta dinleyen yok", "Sağlayıcının sertifikası kabul edilmedi") ve ne yapılacağını söyleyen bir açıklama olarak gösterilir; "Yeniden dene" | Tasarımda yok; ortak bileşenlerle |
| Trunk listesi: "Durum" sütunu (Açık / Kapalı rozeti) | Kaldırıldı; durum satır sonundaki anahtardan okunur (bağlantı listesindeki gibi). Yerine "Gelişmiş ayar" sütunu | Liste 1440 px'te yatay kaydırmadan sığsın diye |
| Trunk formunda numara biçimi | Çoklu seçim (`pick`): "Başında + ile", "Başında 00 ile", "Ülke kodu olmadan, başında 0 ile" (seçilince "Ülke kodu" sorulur); hiçbiri seçili değilse numara ülke koduyla, olduğu gibi beklenir. Altında çok satırlı "Başka kurallar" alanı: satır başına "silinecek > eklenecek" (ör. `011 > 1`, `> 90`); okunamayan satır ve aynı başlangıç için iki kural alan hatasıdır. API'de kural listesidir (`number_rules`, T-17); seçim kutularındaki sıra numarası anlamsızdır (en uzun eşleşen kural uygulanır). "N ayar" sayacında seçilen biçimler ve başka kurallar ayrı birer ayar sayılır | Profesyonel olmayan kullanıcı için adlandırılmış seçenekler; aynı yaklaşım "Sağlayıcı çağrıyı kuramazsa" (dört adlandırılmış davranış, kod listesi yalnızca "uzman" seçeneğinde) ve "Sağlayıcıyı yokla" (kapalı / 30 sn / 1 dk / 5 dk) alanlarında |
| Fiyat ve rota formları | "Günler" (her gün / hafta içi / hafta sonu) ve saat aralığı alanları; listelerde "Gün ve saat" sütunu; tarife ve plan için "Saat dilimi"; simülatörde "Çağrı zamanı" | Tasarımda yok; güne ve saate göre fiyat ve rota (C6) için |
| Ayarlar | "Döviz kurları" kartı (ekle, düzenle, sil) | Tasarımda yok; çoklu para birimi (C7) için |
| Ayarlar → "Döviz kurları" | Kurlar dış kaynaktan alınıyorsa (`FX_SOURCE=ecb`, T-20): "Kaynak" sütunu (soluk "Elle" ya da `ECB` rozeti ve yayın günü; tabloda bulunmayan kur için sarı "Yayında yok"); araç çubuğunda "Kurları şimdi al"; kartın üstünde son yayın ve son alma satırı; son alma başarısızsa sarı uyarı şeridi (ne zaman, neden, hangi tarihli kurların kullanıldığı), yayında olmayan kurlar için ayrı sarı şerit. Ekleme ve düzenleme formunda "Kaynak" seçimi (Elle / ECB (günlük)); ECB seçilince kur alanı gizlenir, kur son yayından hesaplanır. Kapalıyken kart eskisi gibidir | Tasarımda yok; ortak bileşenlerle (`Seg`, `Badge`, `banner warn`, alanın `show` koşulu) eklendi |
| Genel bakış, çağrı kayıtları ve simülatörde "USD" başlıkları | Hesabın para birimi; çağrı detayında tarafın kendi para birimindeki tutar parantez içinde | Tutarlar artık farklı para birimlerinde olabiliyor |
| Genel bakış tablosu | "NER %" ve "PDD sn" sütunları | Tasarımda yok; geçmiş raporlarda kalite (C8) için |
| — | "Zamanlanmış raporlar" ekranı (Sistem menüsü): liste, form, "Şimdi gönder", etkin anahtarı | Tasarımda yok; C8 için ortak bileşenlerle eklendi |
| — | "Araçlar" menü grubu ve "Sistem kaynakları" sayfası: göstergeler (küçük çizgi grafiklerle), servisler ve diskler tabloları | Tasarımda yok; işletim araçları (T1) için ortak bileşenlerle eklendi |
| — | "Araçlar → Güvenlik duvarı" sayfası: kurallar tablosu ve formu, otomatik engellenenler tablosu ("Engeli kaldır", "Kalıcı engelle", "İzin ver") | Tasarımda yok; T2 için ortak bileşenlerle eklendi |
| Çağrı kayıtları: "Sağlayıcı" sütunu ve çağrı detayı | Müşterinin trunk'ına teslim edilen çağrıda sütunda mavi "Trunk" rozeti ve hedef trunk'ın adı; detayda "Hedef trunk" satırı (trunk ve müşterisi), "Rota denemeleri"nde rozetli trunk satırı, "SIP akışı" bölümü bu çağrılarda da gösterilir | Tasarımda yok; çağrı kaydında hedef trunk (T-05) için ortak bileşenlerle (`Badge`) eklendi. Ayrı sütun tabloyu genişleteceği için sağlayıcının olmadığı sütun kullanıldı |
| Çağrı detayı | "SIP akışı" bölümü: uçlar sütun (Arayan / Switch / Sağlayıcı ve adres), iletiler zaman sırasıyla ok (istek koyu, 1xx kesikli ve soluk, 2xx yeşil, 3xx sarı, 4xx–6xx kırmızı), solda ilk iletiye göre saniye; iletiye tıklayınca ham metin; "Büyük görünüm" ve "pcap indir". Üçten fazla uç olunca yatay kaydırılır | Tasarımda yok; çağrı izi (T3) için. Çizim SVG; renkler ortak belirteçlerden |
| — | "Araçlar → Çağrı izi" sayfası: Call-ID alanı, "İzi getir", aynı akışın geniş hâli, "Çağrı kaydı" bağlantısı; boş, bulunamadı ve okunamadı durumları | Tasarımda yok; T3 için ortak bileşenlerle eklendi |
| — | "Resmî tatiller" ekranı (Fiyat ve rota menüsü): yıl seçimli, sunucu taraflı sayfalı tablo (tarih, kendi günü, sayıldığı gün rozeti, ad), ekle/düzenle formu (tarih seçici, "Sayıldığı gün" yedi seçenekli `Seg`, varsayılan Paz; ad), onaylı sil. Ortak forma `type: 'date'` alanı eklendi | Tasarımda yok; T-19 için ortak bileşenlerle eklendi |
| CSV yükleme penceresinin "Dosya" adımı | Beklenen sütunlara `days,time_from,time_to` eklendi ve altına pencere sütunlarını anlatan kısa not; hata listesinde üç yeni neden (gün kümesi, saat biçimi, başlangıç/bitiş eşi); "CSV indir" pencereyi de yazar (her gün ve tüm gün boş) | Tasarımda pencere sütunu yok; T-19 |
| — | "Arayan numara kuralları" ekranı (Fiyat ve rota menüsü): liste, ekle/düzenle formu, etkin anahtarı, sil | Tasarımda yok; C4 için ortak bileşenlerle eklendi |
| — | "Arayan numara kuralları": formda "Bakılan numara" seçimi ("From" / "Kimlik (PAI)", varsayılan From; yardım satırında başlık yoksa numarasız sayıldığı), dördüncü işlem "Numarasızı reddet" (seçilince önek alanı gizlenir), kimlik numarasında "Dönüştür" seçilirse alan hatası; listede "Numara" sütunu, kırmızı "Numarasızı reddet" rozeti, bu kuralda önek "—"; sayfa açıklaması ve araç çubuğu metni iki numarayı ve 433'ü anlatır | Tasarımda yok; T-16 için ortak bileşenlerle (`Seg`, `Badge`) eklendi |
| Müşteri detayı sekmeleri | "Paket dakikalar" sekmesi: liste (önekler, aylık süre, bu ay kullanılan, kalan, durum), ekle/düzenle formu (süre dakika ve ek saniye olarak girilir), aç/kapat, sil | Tasarımda yok; paket dakikalar (C6) için ortak bileşenlerle eklendi. Boş önekli ("tüm numaralar") paket yalnızca API'den tanımlanır |
| Simülatör girdileri: kaynak IP ve aranan numara | İsteğe bağlı "Arayan numara" alanı; dönüşüm karar adımlarında görünür. İsteğe bağlı "Kimlik numarası (PAI)" alanı (T-16); ret nedeni "Numarasız arayan kabul edilmiyor" | Arayan numara kurallarını denemek için |
| Simülatörde "Kullanılacak" / "Yedek" | Ağırlıkla paylaşılan rotalarda "İlk deneme %N" ve "Yedek · %N" | İlk rota artık her çağrıda aynı değil |
| "Sütun filtreleri" düğmesi | Yok; arama ve ekrana özgü filtreler var | Tasarımda davranışı tanımlı değil |
| CSV indir | CDR'de süzgeçle eşleşen tüm kayıtlar (sunucudan, en çok 200.000); tarifede şu an geçerli tüm fiyatlar | |
| Canlı izleme: lisans doluluğu | Gösterilmiyor | Lisans Enterprise kavramı |
| Canlı izleme: ülke telefon kodu, kapalı bağlantı durumu | Gösterilmiyor | API vermiyor |
| Canlı izleme: daire konumları | Ülkenin haritadaki en büyük parçasının merkezi | Tasarımdaki elle yerleştirilmiş noktalar yalnızca örnek ülkeler içindi |
| Canlı izleme: harita çerçevesi | Tüm dünya (Antarktika hariç) | Tasarımdaki çerçeve Doğu Asya ve Okyanusya'yı dışarıda bırakıyordu; karar 2026-11-21 |
| Canlı izleme: CPS | Kartta son saniye, ülke ve müşteride 60 sn ortalaması | API'nin verdiği bu |
| Canlı izleme: boş ve hata durumu metinleri, eşikler | "Trafik yok", "Canlı veri alınamadı" vb.; eşikler tasarımdaki değerler | Tasarımda olmayanlar geliştirmeye bırakıldı (2026-11-21) |
| Canlı izleme: harita yakınlaştırma | Düğmeler (+, −, Tümü), fare tekerleği ve sürükleme; 1–8 kat | Tasarımda yok; karar 2026-11-21 |
| Giriş | İki sekme: "E-posta ve parola" (varsayılan) ve "API anahtarı" | Tasarımda yalnızca anahtarla giriş vardı |
| Kullanıcılar ve roller | Ortak bileşenlerle yazıldı (tablo, ekleme/düzenleme/parola pencereleri, rol açıklamaları) | Tasarım paketinde ekranı yok |
| Düşük bakiye uyarıları | Ortak bileşenlerle yazıldı (Açık / Kapananlar sekmeleri, tablo); eşik müşteri detayında Genel sekmesinde | Tasarım paketinde ekranı yok |
| Canlı çağrılar | Ortak bileşenlerle yazıldı (süzgeç, tablo, sayfalama, onaylı "Kapat") | Tasarım paketinde ekranı yok; karar 2026-11-21 |
| Canlı çağrılar → çağrı izi | Satırın son sütununda "İz" düğmesi (`btn inline`, "Kapat"ın solunda), "Araçlar → Çağrı izi" sayfasını o Call-ID ile açar; iz sayfasında ve boş durumunda "Yenile" (süren çağrının izi yazılmaya devam eder) | Tasarımda yok; T-25 için ortak bileşenlerle eklendi |
| Denetim kaydı | Ortak bileşenlerle yazıldı: Çağrı kayıtları ekranındaki süzgeç şeridi, tablo, sayfalama ve sağdan açılan detay paneli (440 px). İşlem renkleri: oluşturma yeşil, değişiklik mavi, silme kırmızı, hatalı giriş ve iki adımlı doğrulamanın kapanması sarı. Değişen alanlar API'deki sütun adlarıyla gösterilir (`max_calls` gibi); gizlenen değer "gizli", boş değer "boş" yazılır. Tutarlar `format.ts` ile biçimlenir | Tasarım paketinde ekranı yok (B7, 2026-11-22). Alan adları çevrilmedi: her kaynağın her sütunu için ayrı bir sözlük tutmak yerine API belgesiyle aynı adlar |
| İki adımlı doğrulama | Kendi hesabının kurulumu Kullanıcılar ve roller ekranında bir kartta (ayrı bir "Hesabım" sayfası yok; "Parolamı değiştir" de bu ekranda). Kurulum penceresi: kare kod (beyaz zemin, iki temada da), elle girilecek anahtar, 6 haneli kod alanı; ardından bir kez gösterilen yedek kodlar ("kaydettim" işaretlenmeden kapanmaz). Kullanıcı tablosuna "İki adım" sütunu ve yöneticiye "İki adımı sıfırla" düğmesi eklendi. Giriş ekranına kod adımı eklendi ("Telefonuma ulaşamıyorum, yedek kod kullan" bağlantısıyla) | Tasarım paketinde yok (B7, 2026-11-22). Metinler ve akış Enterprise müşteri portalındaki iki adımlı doğrulamayla aynı tutuldu. Kare kod için `qrcode.react` (ISC lisansı, AGPL ile uyumlu) eklendi |
| İki adımı zorunlu kılma (Ayarlar) | Ayarlar ekranında API anahtarlarının altında "Güvenlik" kartı: "İki adımlı doğrulama zorunlu" satırı, Açık/Kapalı etiketi, `Toggle` anahtarı ve ikinci adımı kurmamış etkin kullanıcı sayısı. Açmadan ve kapatmadan önce onay penceresi (açarken kurmamış kullanıcı sayısıyla). Kişinin kendi ikinci adımı kapalıysa anahtar kapalı kalır ve altında "Zorunlu kılmadan önce kendi hesabınızda açın" notu çıkar; salt okunur rolde anahtar pasif | Tasarım paketinde yok (T-23, 2026-11-23). Ayrı bir "Güvenlik" sayfası açılmadı; tek ayar için Ayarlar ekranı yeterli |
| İki adım kurulum zorunluluğu (giriş sonrası) | Giriş ekranının çerçevesinde (`Frame`) "İki adımlı doğrulamayı kurun" sayfası: açıklama, oturumdaki e-posta, "Kurulumu başlat" (Kullanıcılar ekranındaki kurulum penceresinin aynısı), "Oturumu kapat". Panel açıkken ayar açılırsa ilk `two_step_required` cevabında oturum yeniden okunur ve bu sayfaya geçilir; kurulum bitince panel kendiliğinden açılır | Tasarım paketinde yok (T-23, 2026-11-23). Menü ve üst çubuk gösterilmez: sunucu kurulum dışındaki her isteği reddeder |
| Dil seçimi | Üst çubukta saatin solunda küçük `Split` (TR / EN); giriş sayfasında marka satırının sağında aynısı; Ayarlar › Görünüm kartında "Dil" alanı (`Seg`: Türkçe / English). İngilizcede bazı dar sütunlar biraz genişletildi (satır sonundaki "Delete" düğmeleri 50 → 64 px, güvenlik duvarında "Block permanently" 120 → 130 px) | Tasarım paketinde yok (ADR-0049, 2026-11-24). Sayfa başlığı ve kalıcı yer olarak üst çubuk seçildi; girişten önce de değiştirilebilmeli |
| Sürüm | Menünün altında, oturum satırının altında küçük, soluk, eşit aralıklı yazıyla "Sürüm v0.1.0" (sunucunun `GET /v1/session` ile bildirdiği). Panelin kendi derlendiği sürüm (`VITE_VERSION`) farklıysa yanına `*` ve üzerine gelince ikisi ("Sunucu … · panel …"): yükseltmeden önce açılmış sekme demektir, sayfa yenilenince geçer | Tasarım paketinde yok (ADR-0051, 2026-11-24). Ayrı bir "Hakkında" sayfası açılmadı; sürüm destek isteğinde sorulan ilk şey olduğu için her sayfada, göze batmadan |
| Denetim kaydı: CSV indir | Başlığın sağında "CSV indir" düğmesi (Çağrı kayıtlarındaki gibi; liste boşken pasif, indirirken "Hazırlanıyor…"); dosya `denetim-kaydi.csv`, süzgeçler listeyle aynı. Sütun adları API'deki gibi İngilizce; `changes`/`detail` JSON metni | Tasarım paketinde yok (T-23, 2026-11-23). Sütunlar çevrilmedi: dosya başka araçlara (SIEM, tablo) girdi olarak düşünüldü |

## Geliştirme notu

Üretim derlemesi anahtarı yalnızca bellekte tutar. Geliştirme sunucusunda (`make panel-dev`) kod değişikliğinin yol açtığı yenilemelerde oturum düşmesin diye anahtar `sessionStorage`'da saklanır; bu yol üretim derlemesinde yoktur.

## Kapasite notları

- CDR listesi sayım yapmaz; sonraki sayfanın varlığı bir fazla satır istenerek anlaşılır.
- Müşteri, sağlayıcı, tarife ve plan adları için başvuru listeleri en çok 1.000 kayıt çeker; daha büyük kurulumlarda ad yerine `#id` görünür. Bu sınır aşılmadan önce sunucu taraflı ad çözümleme eklenmeli.
