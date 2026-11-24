# ADR-0042: Trunk'ın ve müşterinin sınırları iki yönü birlikte sayar; teslim edilen çağrı hedef müşterinin sınırlarına da tabidir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **İlgili:** ADR-0003 (çağrı yolunda SQL yok), ADR-0005 (rezervasyon; defter Redis'te, betikler atomik), ADR-0013 (kayıtlar motorda), ADR-0032 (gelen çağrı genişleme noktası)

## Bağlam

T-06, kayıtlı trunk'a teslim edilen çağrıya hedef trunk'ın kendi sınırlarını (`client_trunks.max_calls`/`max_cps`) uyguladı, ama iki şeyi açık bıraktı:

- **S-19:** trunk'ın sınırı iki yönde ayrı sayılıyordu. Trunk'ın yaptığı çağrılar `cc:s:<trunk>` / `cps:s:<trunk>:<saniye>`, ona teslim edilenler `cc:t:<trunk>` / `cps:t:<trunk>:<saniye>` içindeydi; `max_calls = 1` olan trunk aynı anda bir çağrı yapıp bir çağrı alabiliyordu.
- **S-20:** teslim edilen çağrı hedef trunk'ın **müşterisinin** sınırlarına (`clients.max_calls`/`max_cps`) hiç uğramıyordu; müşterinin sınırı yalnızca onun yaptığı çağrıları sayıyordu.

Bir trunk'ın ya da müşterinin sınırı pratikte karşı taraftaki santralin kanal sayısıdır (PRI, lisans, hat); iki yönün ayrı sayılması bu kapasiteyi iki katına çıkarıyordu.

Kısıt: olağan (sağlayıcıya giden) çağrı kabulde yine tek Redis gidişi (`evalsha`) yapmalıdır (`TestVendorCallStoreTrips`); aynı betiğe anahtar eklemek ancak ölçülebilir bir maliyeti yoksa kabul edilir.

## Seçenekler

1. Ayrı saymayı sürdürmek, belgelemek: kod değişmez; sınır santralin kanal sayısını korumaz.
2. Trunk için iki ayrı sınır (gelen/giden) alanı: esnek; şema, panel ve API değişir, çoğu kullanıcının istediği "toplam" yine ayrıca hesaplanmalıdır.
3. **Tek sayaç:** trunk'ın iki yönü tek kümede ve tek saniye sayacında; teslim edilen çağrı hedef müşterinin kümesine ve saniye sayacına da yazılır, müşterinin sınırları onda da denetlenir. Hepsi var olan kabul ve sıradaki rota betiklerinin içinde.

## Karar

Üçüncü seçenek (karar, 2026-11-23).

- **Trunk:** `max_calls` ve `max_cps` trunk'ın yaptığı ve ona teslim edilen çağrıları birlikte sayar. Sayaçlar `cc:t:<trunk>` ve `cps:t:<trunk>:<saniye>`; `cc:s:`/`cps:s:` kullanılmaz. `max_calls = 1` olan trunk'ın aynı anda iki yönden yalnızca bir çağrısı olabilir.
- **Müşteri:** teslim edilen çağrı hedef trunk'ın müşterisinin kümesine (`cc:c:<müşteri>`) ve saniye sayacına (`cps:c:<müşteri>:<saniye>`) da yazılır; o müşterinin `max_calls`/`max_cps` sınırı dolmuşsa rota, sağlayıcının sınırı dolmuş gibi atlanır (sıradaki kayıt ya da rota, yoksa 503). Müşterinin sınırı böylece yaptığı ve aldığı çağrıları birlikte sayar. Çağrının parası yine yalnızca arayan müşteriden ayrılır.
- **Bir çağrı her sayaçta bir kez sayılır:** aynı trunk'ın başka kaydına ya da aynı müşterinin başka trunk'ına geçen çağrı yeni çağrı sayılmaz (saniye sayacı yeniden artmaz). Müşterinin kendi trunk'ına yaptığı çağrı (ya da trunk'ın kendine dönen çağrısı) arayan tarafında kabul edilirken denetlenmiştir; teslim tarafında yeniden denetlenmez, yalnızca küme üyeliği tutulur. Gelen çağrı (ADR-0032) adı verilen müşterinin sayacına zaten yazılıyordu; o müşterinin kendi trunk'ına teslim edildiğinde bir kez sayılır.
- **Betik düzeni:** sayım kabulün ve sıradaki rotaya geçişin tek betiğindedir. Rotalar betiğe genişlikleriyle verilir (`ARGV[4]`): yalnızca sağlayıcı rotası olan çağrıda 3 (küme, saniye sayacı, çevrilen çağrılar), müşteriye teslim edilen rotası olan çağrıda 5 (ayrıca hedef müşterinin kümesi ve saniye sayacı). Olağan çağrının anahtarları aynı sayıdadır, tek fark trunk sayacının adı ve bir argümandır; Redis gidişi yine birdir.
- **Bırakma:** çağrı biterken (`ended`, son `failed`, süresi dolup motorun kaydettiği çağrı) her kümeden çıkar; sıradaki rotaya geçerken bıraktığı rotanın kümesinden çıkar, ama arayan olarak da sayıldığı kümede (kendi trunk'ı, kendi müşterisi) kalır.

## Sonuçları

- **Var olan kurulumlar:** iki yönün ayrı kapasitesine güvenen trunk ya da müşteri artık daha erken sınırlanır. Örneğin `max_calls = 10` olan ve aynı anda 10 çağrı yapıp 10 çağrı alan bir trunk, yükseltmeden sonra toplam 10 çağrıda kalır; sınırı yükseltmek gerekir. Teslim edilen çağrısı olmayan (yalnızca sağlayıcıya çağrı yapan) kurulumlarda davranış değişmez; Community kendi başına trunk'a teslim etmez (yalnızca bir uzantı `routing.ToTrunk` ile yapar).
- **Geçiş anı:** route-engine açılırken eski `cc:s:<trunk>` kümelerindeki çağrıları `cc:t:<trunk>`'a katar ve eski kümeyi siler (`engine.MigrateTrunkCounts`; tekrarlanabilir). Eski saniye sayaçları iki saniyede kendiliğinden düşer. Yükseltme anında süren ve bir trunk'a teslim edilmiş çağrılar hedef müşterinin kümesinde yoktur: bittikleri ya da süreleri dolana kadar o müşterinin sınırı onları saymaz (geçici olarak az sayar, sızıntı bırakmaz). Birden çok motorun sırayla yükseltildiği kurulumda eski motor yükseltilene kadar `cc:s:` yazmayı sürdürür; o çağrılar yeni sınırda görünmez, kümeler kimsenin okumadığı kayıtlar olarak kalır ve bir sonraki açılışta taşınır.
- Canlı izlemedeki müşteri sayısı (`cc:c:`) ve müşteriye göre canlı çağrı listesi teslim edilen çağrıları da gösterir; liste satırındaki müşteri arayandır.
- Teslim edilen rotası olan çağrının betiğinde rota başına iki anahtar ve iki argüman fazladır; olağan çağrıda yoktur. Ölçüm T-45'te (`docs/TASKS.md`).
- Ayrı yön sınırı istenirse yeni bir karar ve şema gerekir (seçenek 2).
