# ADR-0045: İki adımlı doğrulama hesap ayarıyla zorunlu kılınır; kurmamış kullanıcının oturumu yalnızca kuruluma açılır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı görev:** T-23

## Bağlam

İki adımlı doğrulama (B7) her panel kullanıcısının kendi seçimiydi. Bir hesabın yöneticisi bunu herkes için şart koşabilmeli; API anahtarları programlar içindir ve bu kuraldan etkilenmemeli. Karar verilmesi gerekenler: ayarın nerede durduğu, ikinci adımı kurmamış kullanıcının girişinin nasıl temsil edildiği, açık oturumlara ne olduğu, yöneticinin kendini dışarıda bırakamaması ve Enterprise'daki bayi hesapları.

## Seçenekler

1. **Ayarın yeri:** (a) `resellers` tablosunda bir sütun; (b) ayrı bir hesap ayarları tablosu; (c) ortam değişkeni (kurulumun tümü için).
2. **Kurmamış kullanıcı:** (a) girişte belirteç verilmez, kurulum için ayrı bir kısa ömürlü belirteç verilir; (b) olağan oturum verilir ama her istekte kısıt hesaplanır ve yalnızca kurulum uçlarına izin verilir; (c) oturuma bir "kısıtlı" bayrağı yazılır.
3. **Açık oturumlar:** (a) ayar açılınca hepsi kapatılır; (b) bir sonraki isteklerinde kısıtlanır; (c) dokunulmaz, kısıt yalnızca yeni girişte.
4. **Kendini dışarıda bırakma:** (a) ayarı açan kişinin kendi ikinci adımı açık olmalı; (b) kural yok (kurulum zaten herkese açık); (c) en az bir yöneticide açık olmalı.

## Karar

- **1a.** `resellers.require_two_step` (`boolean`, varsayılan `false`; şema `0029_require_two_step.sql`). Var olan kurulumlar aynı davranır. Ayar hesabındır: Community'de tek hesap, Enterprise'da her bayi hesabı kendi yöneticisinin açtığı ayarla. Ayrı bir ayarlar tablosu tek bir sütun için gereksizdi; ortam değişkeni bayi başına olamazdı.
- **2b.** Giriş olağan oturumu (`tss_…`) verir. Oturumun kimliği doğrulanırken aynı sorguda `r.require_two_step and not u.totp_on` okunur (`Caller.NeedsTwoStep`); doğruysa `Handle` yalnızca şu uçlara izin verir: `GET /v1/session`, `GET /v1/me`, `GET /v1/me/two-step`, `POST /v1/me/two-step/setup`, `POST /v1/me/two-step/confirm`, `POST /v1/logout`. Diğer her uç (uzantılarınki dahil) `403` ve `{"error", "code": "two_step_required"}` döner. Giriş cevabı ve `GET /v1/session` `two_step_setup_required` alanıyla durumu söyler; girişin denetim kaydına `detail.two_step_setup_required` yazılır. Kısıt saklanmadığı için kurulum onaylanınca aynı oturum hemen açılır; ayrı belirteç türü (2a) ya da saklanan bayrak (2c) bunu karmaşıklaştırırdı.
- **3b.** Kısıt her istekte veritabanından okunduğu için ayar açılınca açık oturumlar bir sonraki isteklerinde kısıtlanır; kapatmaya gerek yoktur. Yöneticinin başka birinin ikinci adımını sıfırlaması da (ayar açıkken) o kişiyi kuruluma geri gönderir.
- **4a.** Oturumla ayarı açan kişinin kendi ikinci adımı açık olmalıdır (yoksa `409`, `code: "own_two_step_off"`): kural herkesten istenmeden önce açan kişide çalıştığı görülür ve açan kendi düğmesiyle kısıtlanmaz. API anahtarının ikinci adımı yoktur; anahtar ayarı koşulsuz açıp kapatabilir. Kimse kilitlenemez: kurulum, kısıtlanan herkese her zaman açıktır. Kısıtlanmış bir yönetici ayarı kapatamaz (o da kısıtlanmıştır).
- Ayar `GET`/`PUT /v1/account/two-step` ile okunur ve yazılır (`{"required"}` → `{"required", "users_without"}`); yazma yönetici ister. Değişiklik `account` kaynağında `require_two_step_on` / `require_two_step_off` işlemi olarak, `changes.require_two_step` ile denetim kaydına yazılır; aynı değeri yeniden yazmak kayıt bırakmaz.
- Kullanıcı ayar açıkken kendi ikinci adımını kapatabilir (telefon değişikliği); kapatınca bir sonraki isteğinde kurulum ekranına döner.

## Sonuçları

- Kimlik doğrulama sorgusu `resellers` satırını zaten okuyordu; yalnızca bir sütun eklendi, istek başına ek sorgu yok.
- Enterprise'ın bayi kullanıcıları aynı yoldan geçer: her bayi hesabı kendi ayarıyla. Üst hesabın alt hesaba ayarı dayatması yoktur; gerekirse Enterprise'da ayrı bir kararla eklenir. Enterprise'ın müşteri portalı kendi kullanıcı tablosunu ve kendi iki adımlı doğrulamasını kullanır; bu kural onu kapsamaz.
- Hata cevabına isteğe bağlı `code` alanı eklendi (`ErrorBody`); şimdilik yalnızca bu iki durumda dolu.
- SSO (T-22, AÇIK SORU S-15) geldiğinde SSO ile girenin ikinci adımı kimlik sağlayıcıda sayılıp sayılmayacağı ayrıca kararlaştırılır.
