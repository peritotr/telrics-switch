# ADR-0049: Panelin Türkçenin yanında İngilizce arayüzü olur; dil katmanı bağımlılıksızdır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-24
- **Karar veren:** Karar, 2026-11-24
- **Yerini aldığı:** ADR-0023'ün "Arayüz Türkçedir" sonucu (diğer sonuçları geçerli)
- **Kısmen yerini alan:** ADR-0050 ("`docs/` Türkçe kalır" sonucu: kullanıcıya dönük belgelerin İngilizcesi `docs/en/` içindedir)

## Bağlam

Community deposu GitHub'da yayımlanacak; açık depodaki README İngilizce ve İngilizce ekran görüntüleriyle olmalı. Panel yalnızca Türkçeydi (ADR-0023). Var olan kullanıcılar paneli Türkçe kullanıyor; Enterprise ekranları `PanelExtension` ile eklenir ve şimdilik Türkçedir.

## Seçenekler

1. Bir i18n kütüphanesi (i18next, react-intl/FormatJS, Lingui): çoğul, ICU biçimi, tarih/sayı yardımcıları hazır; ama bağımlılık ve paket boyutu getirir, anahtarların tür denetimi için ek araç ister, iki dil ve tek çoğul kuralı için gereğinden büyüktür.
2. Küçük, bağımlılıksız katman: alan başına bir katalog dosyası (Türkçe ve İngilizce yan yana), İngilizce yarının Türkçe anahtarlarla tiplenmesi, `t()`/`tx()`/`rich()` işlevleri, `{ad}` yer tutucusu ve İngilizce için `{n|tekil|çoğul}`.
3. Ayrı İngilizce derleme: iki panel, iki kod yolu; bakımı ikiye katlar.

## Karar

İkinci seçenek (`web/src/lib/i18n.tsx`, `web/src/lib/lang.ts`, `web/src/i18n/`).

- Varsayılan dil Türkçedir. Seçim sırası: adresteki `?lang=tr|en` (bağlantı ve ekran görüntüsü otomasyonu için; seçilen dil hatırlanır), bu tarayıcının daha önceki seçimi (`localStorage` `tsw.lang`, tema gibi tarayıcı tercihi; oturum anahtarı değildir), sonra Türkçe. Tarayıcının dili bilinçli olarak dikkate alınmaz: paneli Türkçe kullanan biri bir gün İngilizce bulmamalı.
- Dil, üst çubuktaki TR/EN düğmesinden, giriş sayfasından ve Ayarlar › Görünüm'den değişir. Değişince kabuk yeniden çizilir, sayfa yeniden kurulur (oturum ve adres yerinde kalır); `<html lang>` dile uyar.
- Biçimler dile uyar (`web/src/lib/format.ts`): Türkçe `12.480,52` ve `24.11.2026 14:05`; İngilizce `12,480.52` ve `2026-11-24 14:05`. İngilizce tarih ISO 8601 sırasındadır: İngiliz ve Amerikalı okur için aynı anlamı taşır, sıralanabilir, çağrı kaydı araçlarında olağandır ve Türkçe biçimle aynı genişliktedir. Tutar hesabı tamsayı kalır; CSV'deki tutar biçimi dilden bağımsızdır.
- Uzantılar (`PanelExtension.messages`) kendi kataloglarını verir; İngilizcesi eksik anahtar Türkçe gösterilir. Menü öğesi, sekme, durum ve denetim adları anahtar ya da düz metin olabilir (`tx`). Menü grupları kimlikle anılır (`monitoring`, `parties`, `pricing`, `numbers`, `tools`, `system`); eski Türkçe grup adları da kabul edilir.
- API'nin hata metinleri zaten İngilizcedir ve olduğu gibi gösterilir; panel yalnızca kendi cümlelerini ve `code` taşıyan cevapları çevirir.
- `docs/` Türkçe kalır.

## Sonuçları

- Panelde kullanıcıya görünen her metin bir katalog anahtarıdır; yeni metin iki dilde birlikte yazılır. Eksik ya da fazla İngilizce metin `tsc`'yi, yer tutucu uyuşmazlığı ve iki dosyada tanımlı anahtar `npm run check-i18n`'i (derlemede de çalışır) düşürür.
- `t()` modül yüklenirken çağrılmaz (dil değişince güncellenmez); etiket listeleri işlev olarak kurulur. Geliştirme sunucusu bunu konsolda uyarır.
- İngilizce terimler `docs/07-panel.md` içindeki sözlüğe uyar.
- Enterprise ekranları şimdilik Türkçedir; menü, sekme, durum ve denetim adları İngilizceye çevrildi. İngilizce kipte Enterprise ekranları ortak bileşenlerin İngilizce metinleriyle karışık görünür; çevirileri Enterprise deposunda ayrı iştir.
