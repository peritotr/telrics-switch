# ADR-0006: CDR kaybolmaz: en-az-bir-kez teslim, PostgreSQL asıl kayıt, ClickHouse kopya

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

Çağrı kaydı faturanın kaynağıdır; kayıp ya da çift kayıt kabul edilemez. Aynı zamanda günde on milyonlarca kayıtta arama ve rapor gerekir.

## Seçenekler

1. Motor doğrudan PostgreSQL'e yazar: çağrı yolunu veritabanına bağlar (ADR-0003'e aykırı).
2. Yalnızca ClickHouse: hızlı rapor; bakiye düşümüyle aynı işlemde yazılamaz.
3. Motor → NATS JetStream → rater → PostgreSQL (kayıt + bakiye tek işlemde), ardından ClickHouse'a kopya.

## Karar

Üçüncü seçenek. Teslim en-az-bir-kezdir; tekillik `(call_id, leg)` ve ileti kimliğiyle sağlanır. Kayıt saklanırken aynı işlemde `cdr_export_queue`'ya yazılır; rater ClickHouse kabul ettikten sonra kuyruktan siler. Eski kayıtlar PostgreSQL'den ancak ClickHouse'ta kimliğiyle doğrulandıktan sonra silinir.

## Sonuçları

- Bütün tüketiciler tekrar işlemeye dayanıklı olmak zorundadır.
- Rapor ve liste sorguları iki veritabanı için ayrı yazılır ve aynı alanları vermelidir (`make test-reports`).
- Çağrı kaydına alan eklemek dört yeri birlikte değiştirir: `engine.CDR`, `api.CDR`, `cdrs` tablosu, ClickHouse şeması; rater motordan önce yükseltilir.
- Motor kaydı NATS'a yazana kadar bellekte kuyruklar (200.000 kayıt) ve sonsuza dek yeniden dener; çağrı yolunu bekletmez. Motor bu sırada çökerse kuyruktaki kayıtlar kaybolur: kuralın bilinen açığı. AÇIK SORU: kapatılacak mı (ARCHITECTURE S-4)? — Karar: ADR-0029 (kayıtlar motorun diskinde bekler); uygulandı, T-33.
- Motor `answered`/`ended` olayını hiç alamazsa o çağrının kaydı üretilmeyebilir; bilinen açık (TASKS: T-33). — Karar: ADR-0029 (durum düşerken motor yazar); uygulandı, T-33.
