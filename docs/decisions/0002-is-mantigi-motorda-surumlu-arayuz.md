# ADR-0002: İş mantığı motorda; SIP çekirdeğiyle sürümlü, sunucudan bağımsız arayüz

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

OpenSIPS betik dili sınanması ve bakımı zor bir ortamdır. İş kuralları (kim arayabilir, nereye, ne kadar süre) hızla değişecek ve Enterprise bunlara eklenecek. SIP çekirdeğinin ileride değişmesi olasıdır (ADR-0001).

## Seçenekler

1. İş mantığını OpenSIPS betiğine ve SQL modüllerine yazmak: az parça; sınanamaz, çekirdeğe kilitler.
2. OpenSIPS'e C modülü yazmak: hızlı; GPL olur (ADR-0009), çekirdeğe kilitler.
3. Betik ince kalır, her kararı HTTP + JSON ile ayrı bir Go servisine (route-engine) sorar.

## Karar

Üçüncü seçenek. Arayüz `docs/08-cagri-arayuzu.md` içinde tanımlıdır, her istek `"v": 1` taşır, SIP sunucusuna özgü hiçbir kavram içermez. Betik yalnızca sorar, cevabı uygular, olanı bildirir.

## Sonuçları

- Yeni davranış önce motorda ve arayüzde tasarlanır; betiğe yalnızca uygulaması girer.
- Arayüze yalnızca isteğe bağlı alan eklenir; anlam değiştiren ya da zorunlu alan ekleyen değişiklik sürümü artırır.
- Cevap her zaman HTTP 200'dür ve karar gövdededir; 200 dışı cevap motorun cevap veremediği anlamına gelir ve çağrı reddedilir.
- Çağrı başına en az bir HTTP gidiş-dönüşü vardır; karar süresi bütçesi (ADR-0003) bunu sınırlar.
- Motorun SIP çekirdeğine verdiği komutlar (çağrıyı bitir, engeli unut) `pkg/sipctl` sürücüsünün arkasındadır; OpenSIPS'e özgü tek Go kodu odur.
