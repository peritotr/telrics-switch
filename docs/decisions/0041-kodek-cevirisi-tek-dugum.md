# ADR-0041: Kodek çevirisi listelere göre, izinle ve tek düğümde yapılır; eşzamanlı sınırı motor sayar

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **İlgili:** ADR-0002 (sürümlü arayüz), ADR-0003 (çağrı yolunda SQL yok), ADR-0024 (bağlantı ayarları), ADR-0030 (aktarıcı yoksa ret), ADR-0031 (T.38 ve tuşlar)

## Bağlam

Ortak kodeği olmayan arayan ile sağlayıcı arasında çağrı kurulamıyordu: trunk'ın ve bağlantının kodek listeleri örtüşmezse motor rotayı atlıyordu (ADR-0024, "Kodek çevirisi yoktur"; TASKS: T-09, yol haritası B6). Medya aktarıcısı (rtpengine 10.5, Debian paketi) kodek çevirebiliyor; imajda G.711, G.722, G.729 (bcg729), Opus ve diğerleri için kütüphaneler bulunuyor. Çeviri aktarıcının işlemcisini kullanır ve tek sunucu kurulumunda aktarıcı bütün çağrılarla paylaşılır. Çeviri havuzunun (ayrı sunucular) yönetimi Enterprise özelliğidir (`docs/04-acik-cekirdek-modeli.md`).

Motor SIP teklifini (SDP) görmez; yalnızca yapılandırmadaki listeleri bilir.

## Seçenekler

1. Kararı SIP çekirdeği versin (arayan teklif ettiği kodeklerden hiçbirini bağlantı almıyorsa çevirsin): teklifi gören taraftır, ama sınırı sayamaz (betik karar içermez, durum Redis'te motordadır) ve sayılmayan çeviri sınırı delerdi.
2. `authorize` isteğine arayanın teklif ettiği kodekleri ekleyip kararı motorda SDP'ye göre vermek: en doğru sonuç; arayüze her çağrıda yeni alan, betiğe SDP ayrıştırma (statik yük türlerinin adları dahil) girer, sonraki denemelerde de teklifin saklanması gerekir.
3. Kararı motorda **listelere göre** vermek: trunk'ın ve bağlantının (ya da hedef trunk'ın) listeleri dolu, ortak kodek yok ve biri izin veriyorsa rota çevrilerek kalır; motor bunu rotanın isteğe bağlı alanıyla bildirir ve sınırı kabul betiğinde sayar.

## Karar

Üçüncü seçenek.

- Ayar bağlantıda ve trunk'ta `transcode` (varsayılan kapalı; kapalıyken davranış aynıdır). İzin iki taraftan birinden gelir: çeviri bizim işlemcimizi kullanır, sağlayıcının umurunda değildir.
- Arayüz (v1): `route.transcode` isteğe bağlı alanı, arayan tarafının kodek listesidir; varsa SIP çekirdeği `codecs` listesini arayan sunmasa da teklif eder ve aktarıcı çevirir (`codec-mask-all`, `codec-transcode-…`). Çağrı içinde karşı uçtan gelen yeni teklife ters yön uygulanır. Bayrak adları arayüze girmez.
- Sınır: aynı anda çevrilen çağrılar `TRANSCODE_MAX` (varsayılan 200, 0 = hiçbiri). Sayım, kabulün ve sıradaki rotaya geçişin var olan tek Redis betiğindedir (her rotaya `cc:x` kümesi ve bir limit argümanı); olağan çağrıya yeni bir Redis gidişi eklenmez. Sınırda rota, sağlayıcının çağrı limiti dolmuş gibi atlanır.
- Çevrilecek rota plandaki yerinde kalır; çevrilmeyen rotalar öne alınmaz.
- Tek düğüm: çeviriyi çağrının kendi aktarıcısı yapar. Birden çok aktarıcıya ya da ayrı çeviri sunucularına dağıtma Enterprise'tadır ve buraya yalnızca bu sınır düşer.
- Tuşlar ve faks ADR-0031'deki gibidir: `telephone-event` iki tarafın teklifinde kalır, SIP INFO'ya dönüştürülmez; T.38 teklifine kodek bayrakları dokunmaz.

Kullanıcının onayladığı görev (T-09), 2026-11-23.

## Sonuçları

- Listesi olmayan trunk "ne sunulursa" sayılır ve çağrısı çevrilmez; arayan bağlantının listesindekilerden hiçbirini sunmuyorsa eskisi gibi 488 ile o rotadan geçmez. Bir müşterinin çağrıları çevrilecekse trunk'ına kodekleri yazılmalıdır.
- Trunk'ının listesine rağmen karşı tarafın kodeğini de sunan bir arayanın çağrısı aktarıcıda çevrilmeden geçer ama sınırda yer tutar (sayım listelere göredir).
- ADR-0024'teki "Kodek çevirisi yoktur" sonucu bu kayıtla değişti; ADR-0024'ün geri kalanı geçerlidir.
- İmajdaki G.729 kodlayıcısı (bcg729) Debian paketinin bağımlılığıdır; hukuki değerlendirme kurulumu yapana bırakılır (`docs/09-kurulum.md`, "Kodek çevirisi").
