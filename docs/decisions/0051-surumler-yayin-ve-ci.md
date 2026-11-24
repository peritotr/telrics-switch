# ADR-0051: Sürümler anlamsal sürümlemeyle GitHub'da yayımlanır; sunucu sürüm etiketinden yükseltilir; CI laboratuvarsız denetimleri yapar

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-24
- **Karar veren:** Karar, 2026-11-24

## Bağlam

Açık depo (`github.com/peritotr/telrics-switch`) yayımlandı, ama sürüm kavramı yoktu: sunucuya ne kurulduğu yalnızca kopyalanan ağaçtan anlaşılıyordu, panel ve API hangi kodla çalıştığını söylemiyordu, yükseltme "`git fetch && git checkout <sürüm>`, sonra `upgrade.sh`" diye anlatılıyordu ama etiket yoktu. İlk üretim sunucusu `git archive` ile kopyalanmış bir ağaçtan kuruldu: git kopyası değil, bu yüzden yükseltme ağacı elle değiştirmek demekti. Açık depoya gelen değişikliklerin derlendiğini ve testlerinin geçtiğini gösteren bir denetim de yoktu.

## Karar

Üç parça birlikte:

1. **Sürümler.** Anlamsal sürümleme, `vANA.ARA.YAMA`; ilk açık sürüm `v0.1.0`. Sürüm açık depoda açıklamalı (annotated) etikettir ve GitHub Release olarak yayımlanır; notları depodaki İngilizce `CHANGELOG.md`'nin (Keep a Changelog biçimi) o sürümün bölümüdür. 1.0.0'a kadar ara sürüm ayar ya da API değiştirebilir; böyle değişiklikler "Upgrade notes" başlığına yazılır.
   - Sürüm derleme anında gömülür: Go ikilileri `-ldflags "-X softswitch/core/pkg/version.Version=…"` ile (`pkg/version`; verilmezse `dev`, Go derleyicinin kaydettiği commit varsa `dev-<commit>[-dirty]`), panel `VITE_VERSION` ile. İmajları derleyen betikler değeri kopyadan alır (`git describe --tags --always --dirty`, `deploy/prod/lib.sh`; laboratuvarda `Makefile`) ve compose dosyaları imaj derlemesine `VERSION` olarak geçirir. SIP çekirdeğinin imajına sürüm verilmez: sürüm değişti diye SIP çekirdeği yeniden başlamaz, süren çağrılar düşmez.
   - API sürümü `GET /v1/session` cevabında `edition`'ın yanında verir. Ayrı ve açık bir `GET /v1/version` ucu düşünüldü ve seçilmedi: kimliği doğrulanmamış herkese sürümü söylemek, tarayıcılara hangi bilinen hatayı deneyeceklerini söylemektir; panel zaten açılışta oturumu okur, ek istek gerekmez. Yeni uç olmadığı için OpenAPI tanımı yalnızca cevaptaki alanla değişir, denetim kaydına bir şey düşmez (okuma).
   - Her süreç `softswitch_build_info{version, goversion}` ölçüsünü (değeri 1) verir. Ölçü `pkg/metrics` paketi yüklenirken bir kez kaydedilir: servislerin ve onları kullanan sürümlerin (Enterprise) `main`'leri ayrı olduğundan tek kayıt yeri orasıdır; aynı seri iki kez kaydedilemez kuralına da böylece uyulur.
   - Panel sürümü menünün altında küçük ve soluk yazar; panelin kendi derlendiği sürüm sunucununkinden farklıysa (yükseltmeden önce açılmış sekme) ikisini de gösterir.
2. **Yayın akışı.** Sürümü kaynak ağacındaki `tools/release.sh` yerelde hazırlar: `main`'de ve temiz olunduğunu, sürümün yeni ve sonuncusundan büyük olduğunu, `CHANGELOG.md` bölümünü, `web/package.json` sürümünü ve İngilizce belgelerin güncelliğini denetler; açık ağacı dışa aktarır (`tools/public/export.sh`, kendi denetimleriyle), açık deponun yerel kopyasında açık yazar kimliğiyle "Release vX.Y.Z" commit'ini ve etiketi, kaynak ağacında da aynı adlı etiketi oluşturur, sürüm notlarını dosyaya yazar. **Göndermez ve GitHub'da sürüm oluşturmaz**: `git push` ve `gh release create … --notes-file …` komutlarını yazar, sahip onaylayınca elle çalıştırılır. Yayımlanmış etiket taşınmaz; düzeltme yeni yama sürümüyle çıkar. Betik açık ağaca girmez.
3. **Sunucunun sürüm etiketinden yükseltilmesi.** `upgrade.sh <etiket>`: yedek alır, etiketi depodan indirir (`SOFTSWITCH_REPO`, boşsa açık depo), etiketin depoda olduğunu ve izlenen dosyalarda elle değişiklik olmadığını denetler, kopyayı etikete geçirir ve **yeni sürümün kendi** `upgrade.sh`'ını çalıştırır (betik çalışırken kendi dosyası değiştiği için tek bir işlev olarak okunur, sonra yenisine `exec` eder). `install.sh --version <etiket>` aynı yolla önce sürüme geçer. Kendiliğinden yükseltme yoktur. Her geçiş, öncesinde alınan yedekle `upgrades.log`'a yazılır.
   - **Geri dönüş:** şema adımları yalnızca ileri gider (geri alma betiği yazılmaz; her adım var olan satırları eski davranışında bırakır, ama eski kod yeni şemayı tanımaz). Bu yüzden geri dönüş `upgrade.sh <eski etiket> --restore <o sürümden ayrılırken alınan yedek>` ile yapılır: eski imajlar derlenir, veritabanı yedeğe döner, eski sürüm o veritabanıyla başlar; geri dönüşe kadar yazılanlar kaybolur. Yedeksiz geri dönüş yalnızca şema değişmediyse güvenlidir.
   - **Git'siz kurulum** bir kez taşınır: eski dizin yerinde bırakılır, yanına sürümün temiz bir kopyası kurulur, `.env`, `certs/`, yedekler ve Alertmanager ayarı taşınır, servisler `docker compose down` ile (birimler kalır) durdurulup dizinler değiştirilir ve yeni kopyadan `upgrade.sh` çalıştırılır. Yerinde `git init` yerine bu yol seçildi: eski ağaçta kalan ve depoda olmayan dosyalar yeni ağaca karışmaz, eski dizin geri dönüş için durur. Veriler proje adıyla adlanan birimlerdedir ve proje adı (dizin adı, `prod`) değişmez. Adımlar `docs/09-kurulum.md`'de.
4. **CI** (`.github/workflows/ci.yml`, açık depoda): her gönderimde, çekme isteğinde ve sürüm etiketinde `gofmt`, `go vet`, `go build`, `go test ./...` ve panelde `npm ci`, tür denetimi, `check-i18n`, derleme. İzinler yalnızca okuma (`contents: read`); her eylem tam commit özetiyle sabitlenir (yanına sürümü yorum olarak); `tools/public/check.py` iş akışlarında bunları denetler. Yerelde aynısı `make ci`.
   - **CI'ın kapsamadığı:** SIPp senaryoları (`make test-routing`, `make test-journal`), laboratuvar PostgreSQL'i, Redis'i ve ClickHouse'u isteyen testler (`make test-api`, `make test-engine`, `make test-archive`, `make test-reports`; CI'da adresleri verilmediği için kendilerini atlarlar), yük testi (`make load`, karar süresi bütçesi), kurulum duman testi (`make prod-smoke`) ve OpenAPI'nin Redocly denetimi. Bunlar laboratuvar ister; bakımcı sürümden önce çalıştırır.

## Seçenekler (reddedilenler)

- **Hazır imajları bir imaj deposunda yayımlamak:** kurulum bugün imajları sunucuda kaynaktan derliyor (ADR-0021); imaj deposu, imzalama ve çok mimarili derleme ayrı bir iş. Etiketten derleme aynı sonucu verir.
- **Kendiliğinden yükseltme ya da yeni sürüm denetimi:** çağrı taşıyan bir sunucunun sahibinin bilgisi dışında değişmesi istenmez; sürüm notları okunmadan yükseltme yapılmamalı.
- **Sürümü açık bir uçta vermek:** yukarıda.

## Sonuçları

- Destek isteğinde sürüm panelden, API'den ya da ölçülerden okunur; yükseltmeden sonra bütün servislerin yeni sürümde olduğu `count by (version) (softswitch_build_info)` ile görülür.
- Sürümler arasında geçiş tek komuttur, ama geri dönüş veri kaybettirir: bu açıkça yazılıdır ve betik geri dönüş komutunu yedeğiyle birlikte yazar.
- `v0.1.0`'dan önceki kurulumlar (git'siz) bir kez elle taşınır.
- Açık depoya gelen her değişiklik laboratuvarsız denetimlerden geçer; çağrı davranışını ise yalnızca laboratuvar denetler.
