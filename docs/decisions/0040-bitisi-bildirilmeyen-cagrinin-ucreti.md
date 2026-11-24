# ADR-0040: Bitişi bildirilmeyen çağrı süresiz ve ücretsiz kaydedilir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **İlgili:** ADR-0029 (onu tamamlar, yerini almaz), ADR-0005 (rezervasyon), ADR-0006 (CDR kaybolmaz)

## Bağlam

ADR-0029, motorun hiç `ended` duymadığı çağrının (SIP çekirdeği yeniden başladı ya da çağrı biterken motora ulaşamadı) durumu düşerken kaydının yazılmasını karara bağladı, ama kaydın içeriğini söylemedi. T-33 bunu bir kuralla uyguladı (`docs/08-cagri-arayuzu.md`, "Bitişi bildirilmeyen çağrı"); kural bir karar kaydına geçmemişti.

Switch böyle bir çağrının ne zaman bittiğini bilmez. Bildiği: cevaplanıp cevaplanmadığı, cevap anı, kabulde verilen azami süre ve ayrılan tutar.

## Seçenekler

1. **Bilinen yazılır, bilinmeyen ücretlenmez.** Cevaplanmış deneme 200 ile, bitiş = cevap anı (konuşma süresi ve ücret 0); cevaplanmamış deneme 408 ile, bitiş = durumun düştüğü an. Ayrılan tutarın tamamı bırakılır.
2. **Ayrılan süreye kadar ücretlemek.** Bitiş = cevap anı + rezervasyonun kapsadığı süre ya da azami süre.
3. **Kayıt yazmamak** (ADR-0029'dan önceki davranış).

## Karar

Birinci seçenek: T-33'te uygulanan kural olduğu gibi kalır.

Neden: ayrılan ya da verilen süreye göre ücretlemek gerçek süreyle ilgisizdir. Azami süre bakiyenin yettiği süredir (en çok 7200 saniye): 30 saniye süren bir çağrı iki saate kadar ücretlenebilirdi. Müşteriyi bilinmeyen bir süre için ücretlendirmek, eksik ücretlendirmekten daha kötüdür. Üçüncü seçenek ADR-0006'ya aykırıdır.

Karar, 2026-11-23.

## Sonuçları

- Kayıt raporda `answer_at = end_at` ve 200 koduyla (cevapsızsa 408) tanınır; motor her birini sayar (`softswitch_engine_cdr_expired_total`) ve alarm verir (`CallsExpiredUnreported`).
- Sağlayıcı bu çağrının gerçek süresini faturalayabilir; switch'in kaydında süre yoktur. Sağlayıcının faturasıyla **elle uzlaşılır**; uzlaşma aracı yoktur.
- Çağrı, durumu düşene kadar (azami süre + 300 sn) sayılı ve parası ayrılmış kalır; bu süre bu kararla değişmez (ADR-0029'un sınırı).
- `OnLeg` bu kaydı da duyar (konuşma süresi 0); canlı görünüm saymaz.
- Uygulama: `pkg/engine/engine.go` (`SweepExpired`, `expire`), birim testleri `pkg/engine/expire_test.go`, senaryolar `make test-journal`.
