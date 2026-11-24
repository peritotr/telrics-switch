# ADR-0017: Kenar koruması ve güvenlik duvarı önce uygulama düzeyinde

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Açık bir SIP portu sürekli taranır. Rakip ürünlerde yerleşik güvenlik duvarı vardır. İşletim sistemi düzeyinde (nftables) süzme en etkilisidir, ama konteynere ağ yönetimi yetkisi ister ve yanlış kural sunucuya erişimi kesebilir.

## Seçenekler

1. Yalnızca işletim sistemi düzeyi: etkili; riskli, Mac'te sınanamaz.
2. Uygulama düzeyi: motor kaynağı tanır, SIP çekirdeği tanınmayanı sınırlar ve cevapsız bırakır; operatör kuralları (`firewall_rules`) görüntüyle motora gelir.
3. İkisi birlikte, ikincisi sonra.

## Karar

Karar (2026-11-22): önce uygulama düzeyi. Tanınmayan kaynak 2 saniyede 30 istekle sınırlıdır (`pike`), aşan 120 sn cevapsız kalır; çağrı dışı istekleri hiç cevaplanmaz. Tanınan adres 300 sn önbellekte tutulur. `deny` kuralı `allow`'dan ve trunk tanımından önce gelir.

## Sonuçları

- Paket yine de sunucuya ulaşır; büyük hacimli ve sahte kaynaklı taşkına karşı ağ katmanı gerekir (TASKS: T-10, T-27).
- Kayıtlı ya da parolayla giriş yapan adres "tanınan" sayılmaz; hız sınırı ona da uygulanır.
- Senaryolarda yeni kaynak adresi seçerken başka senaryonun adresi kullanılmaz (engel dakikalarca sürer).
