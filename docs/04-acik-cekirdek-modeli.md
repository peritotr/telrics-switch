# Açık Çekirdek (Open-Core) Modeli

Ürünün iki sürümü vardır: ücretsiz ve açık kaynak **Community** (bu depo, tek düğüm) ve ücretli **Enterprise** (çok düğüm; hedef 20.000 eşzamanlı çağrı).

## İlke: ayrım sayıyla değil yetenekle

Açık kaynak kodda sayısal bir limit zorlanamaz: limiti koyan satırı silen yeniden derler. Bu yüzden iki sürüm arasındaki ayrım **yetenekle** yapılır:

- Community **tek düğümlüdür** ve **tek hesaplıdır**.
- Yüksek kapasiteyi ve sürekliliği mümkün kılan **kümeleme** kodu ve bayi hiyerarşisi gibi çok hesaplı yetenekler Enterprise'dadır.

## Ayrım

| Yetenek | Community | Enterprise |
|---|---|---|
| Yönlendirme (önek, LCR, hata durumunda geçiş, numara dönüşümü) | ✓ | ✓ |
| Faturalama (tarife, prepaid/postpaid, bakiye rezervasyonu) | ✓ | ✓ |
| Çağrı ve CPS limitleri, CDR, temel raporlar | ✓ | ✓ |
| REST API ve yönetim arayüzü | ✓ | ✓ |
| Denetim kaydı, iki adımlı doğrulama | ✓ | ✓ |
| Tek sunucu kurulumu | ✓ | ✓ |
| Çoklu SIP düğümü, dialog çoğaltma, kesintisiz devralma | | ✓ |
| Medya havuzu yönetimi (çoklu rtpengine, bölge seçimi) | | ✓ |
| Veritabanı HA kurulum otomasyonu | | ✓ |
| Kalite tabanlı yönlendirme (ASR/ACD/PDD) | | ✓ |
| Dolandırıcılık tespiti | | ✓ |
| Bayi hiyerarşisi, satış ve alış faturaları, müşteri portalı | | ✓ |
| DID sağlayıcıları ve DID havuzu | | ✓ |
| Transcoding havuzu yönetimi | | ✓ |
| SSO, ayrıntılı yetki | | ✓ |
| Destek ve SLA | | ✓ |

İlke: Community tek başına gerçekten kullanılabilir bir ürün olmalı. Faturalamayı Community'den çıkarmak ürünü işe yaramaz kılar ve topluluk oluşmaz.

## Lisans

Community **AGPL-3.0-only** ile yayımlanır (ADR-0007). Aynı kod Enterprise sürümünün de temeli olduğu için dışarıdan gelen katkılar katkı sözleşmesi (CLA) gerektirir (`CONTRIBUTING.md`).

### GPL bileşenlerle ilişki

- OpenSIPS GPLv2, rtpengine GPLv3 lisanslıdır.
- Kendi servislerimiz (Go) bunlarla **ağ üzerinden** konuşur (HTTP, ng protokolü, MI); ayrı süreçlerdir, bağlanmaz (link edilmez), kodları kopyalanmaz (ADR-0009).
- OpenSIPS'e yazılan C modülü ya da rtpengine'e yapılan değişiklik GPL olur ve açılmak zorundadır. **Enterprise'a özgü hiçbir mantık bu bileşenlerin içine konmaz.**

## Kod organizasyonu

- **İki depo:** açık `core` (Community'nin tamamı, bu depo) ve özel `enterprise`.
- `enterprise`, `core`'u Go modülü olarak içe aktarır ve `core`'un tanımladığı genişleme noktalarına bağlanır (`pkg/ext`: `ClusterCoordinator`, `MediaPoolSelector`, `LicenseProvider`; `pkg/api` genişleme noktaları; motorda rota sırasına ve çağrı kabulüne karışmak için `engine.Config.Adjust`, deneme sonuçlarını dinlemek için `OnLeg`). Liste: `docs/ARCHITECTURE.md` §12.
- `core` bu arayüzlerin tek düğümlü/boş varsayılanlarını içerir; `enterprise`'dan hiçbir şey bilmez ve onu içe aktarmaz.
- Enterprise ikilileri (`route-engine`, `api`, ...) `enterprise` deposunda derlenir: core + eklentiler.
- Arayüz (web) için aynı yaklaşım: Enterprise ekranları ayrı pakette yazılır ve panele `createPanel(...extensions)` ile eklenir; Community panelinde Enterprise'a ait hiçbir iz görünmez.

## Uygulanan ayrım (2026-11-21)

Bayi hiyerarşisi, faturalama ve DID kodu `enterprise` deposundadır.

- `core` tek hesaplıdır: her kayıt kök hesaba aittir ve bir API anahtarı yalnızca kendi hesabının kayıtlarını görür. Müşteri, sağlayıcı, tarife, yönlendirme, CDR ücretlendirme, ödeme, hesap defteri ve raporlar `core`'dadır.
- `enterprise` bu görünürlük kuralını "kendi alt ağacı" ile değiştirir; bayi, DID ve fatura kaynaklarını, uçlarını ve tablolarını ekler; CDR ücretlendirmesine bayi zinciri ve DID fiyatlaması için kanca takar.
- Enterprise şeması `core` şemasının üzerine ayrı migration dosyalarıyla uygulanır (`ent_` önekli). Enterprise ikilisi ikisini sırayla çalıştırır.
- `core`'un Go paketleri Enterprise içe aktarabilsin diye `internal/` yerine `pkg/` altındadır. `pkg/api`'nin dışa açık yüzeyi iki depo arasındaki sözleşmedir.
