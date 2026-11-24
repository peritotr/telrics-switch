# ADR-0050: Kullanıcıya dönük belgelerin İngilizcesi `docs/en/` içinde durur; kaynak Türkçedir, kayma denetlenir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-24
- **Karar veren:** Karar, 2026-11-24
- **Yerini aldığı:** ADR-0049'un "`docs/` Türkçe kalır" sonucu, kullanıcıya dönük belgeler için (geliştirme belgeleri Türkçe kalır)

## Bağlam

Açık depodaki README İngilizcedir (`README.md`; Türkçesi `README.tr.md`), ama bağlantı verdiği bütün belgeler Türkçeydi. GitHub'daki İngilizce okur kurulum, API, izleme ve mimari için Türkçe belgeye düşüyordu. GitHub kök dizindeki `CONTRIBUTING.md` ve `SECURITY.md` dosyalarını kendi arayüzünde (çekme isteği, güvenlik sekmesi) gösterir; ikisi de Türkçeydi, yalnızca kısa bir İngilizce özetleri vardı.

## Seçenekler

1. Bütün belgeleri çevirmek: geliştirme belgeleri (yol haritası, görevler, karar kayıtları, çağrı arayüzü ayrıntısı, panel notları) sık değişir; iki dilde tutmak her işi ağırlaştırır.
2. Yalnızca kullanıcıya dönük belgeleri çevirmek; geliştirme belgeleri Türkçe kalır ve İngilizce README'de "(Turkish)" diye işaretlenir.
3. Belgeleri İngilizceye taşıyıp Türkçeyi bırakmak: proje Türkçe çalışıyor, belgelerin kaynağı Türkçe.

## Karar

İkinci seçenek.

- İngilizce sürümler `docs/en/` içindedir, adları İngilizcedir:

  | Türkçe kaynak | İngilizce |
  |---|---|
  | `docs/ARCHITECTURE.md` | `docs/en/ARCHITECTURE.md` |
  | `docs/02-mimari.md` | `docs/en/02-capacity-plan.md` |
  | `docs/04-acik-cekirdek-modeli.md` | `docs/en/04-open-core.md` |
  | `docs/05-ozellik-katalogu.md` | `docs/en/05-feature-catalogue.md` |
  | `docs/06-api.md` | `docs/en/06-api.md` |
  | `docs/09-kurulum.md` | `docs/en/09-installation.md` |
  | `docs/10-izleme.md` | `docs/en/10-monitoring.md` |
  | `CONTRIBUTING.tr.md` | `CONTRIBUTING.md` |
  | `SECURITY.tr.md` | `SECURITY.md` |

  Kök dizindeki `CONTRIBUTING.md` ve `SECURITY.md` İngilizcedir (GitHub bunlara bağlantı verir); Türkçeleri `CONTRIBUTING.tr.md` ve `SECURITY.tr.md`, `README.tr.md` onlara bağlanır.
- Türkçe kalanlar: `docs/03-yol-haritasi.md`, `docs/07-panel.md`, `docs/08-cagri-arayuzu.md`, `docs/TASKS.md`, `docs/decisions/`. İngilizce belgeler bunlara Türkçe dosyayla bağlanır ve "(Turkish)" yazar; çevirisi olan belgeye İngilizcesiyle bağlanır.
- **Kaynak Türkçedir.** Türkçe belgeyi değiştiren değişiklik İngilizcesini de aynı değişiklikte günceller. İngilizce metin açık ağaçtaki Türkçeden çevrilir: açık ağaca girmeyen ayrıntılar (Enterprise iç yapısı, özel depo yolları, geliştirme notları) İngilizceye de girmez, çünkü İngilizce dosya olduğu gibi yayımlanır.
- **Kayma denetimi:** her İngilizce dosyanın ilk satırı kaynağını ve çevrildiği Türkçe dosyanın git blob'unu yazar (`<!-- source: docs/09-kurulum.md <blob> -->`). `make check-docs-en` (`tools/check-docs-en.py`) kaynağın çalışma ağacındaki hâlini özetler; özet farklıysa düşer, güncellenmesi gereken İngilizce dosyaları ve farkı gösteren `git diff` komutunu yazar. Değişiklik İngilizceye taşındıktan sonra `tools/check-docs-en.py --stamp <dosya>` yeni blob'u yazar.
- Denetim ve blob'lar özel ağaca özgüdür: açık ağaçta Türkçe belgeler düzenlendiği için blob'lar uymaz. Dışa aktarım betiği denetimi çıkarır ve ilk satırı yalnızca kaynağı adlandıran bir nota çevirir.

## Sonuçları

- GitHub'daki İngilizce okur kurulumdan API'ye kadar İngilizce belgeyle ilerler; geliştirme belgelerinin Türkçe olduğu açıkça yazılıdır.
- Kullanıcıya dönük yedi belgede ve iki kök dosyada her değişiklik iki dilde yapılır; unutulursa `make check-docs-en` düşer. Denetim çevirinin doğruluğunu değil, yalnızca güncelliğini bilir.
- Türkçe belge açık ağaçta düzenleniyorsa (`tools/public/edits.py`, `overlay/`), İngilizce o düzenlenmiş hâlden çevrilir; düzenleme değişince İngilizce de gözden geçirilir.
