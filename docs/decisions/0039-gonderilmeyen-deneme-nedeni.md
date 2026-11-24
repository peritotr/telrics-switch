# ADR-0039: Gönderilemeyen deneme `ended` olayında `cause: "media"` ile bildirilir; bağlantıya yazılmaz

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **İlgili:** ADR-0002 (sürümlü arayüz), ADR-0030 (aktarıcı yoksa ret), ADR-0008 (genişleme noktaları)

## Bağlam

ADR-0030'a göre medya aktarıcısı teklifi almazsa deneme sağlayıcıya gönderilmez, arayana 503 verilir ve sıradaki rota denenmez. Arayüzde bunu söyleyecek alan olmadığından SIP çekirdeği motora `ended` olayını 503 ile bildiriyordu (T-39). Motor bu olayı motorun seçtiği sağlayıcı bağlantısının sonucu sayıyordu: canlı görünümde o bağlantının `attempts`, `failed_network`, `code_503` sayaçları artıyor (NER düşüyor), `engine.Config.OnLeg` denemeyi o bağlantının başarısız denemesi olarak bildiriyor ve Enterprise'ın kalite tabanlı yönlendirmesi hiç sorulmamış bir sağlayıcıyı geriye alabiliyordu. Sorun switch'teydi, sağlayıcıda değil (T-39, "Karşılaşılan sorunlar" 1, MİMARİ SORU).

## Seçenekler

1. Sapmayı kabul etmek: kayıt ve sayaçlar bağlantıyı suçlamaya devam eder.
2. `ended` olayına isteğe bağlı bir neden alanı eklemek; motor bu nedenle gelen denemeyi bağlantıya yazmaz.
3. Yeni bir olay (`refused` gibi) eklemek: eski motor bilinmeyen olayı yok sayar ve çağrıyı hiç bitirmez (durum süresi dolunca düşer); bu yüzden yükseltme sırası bağlayıcı olur, arayüz sürümü artmalıdır.

## Karar

İkinci seçenek. `POST /v1/events` isteğine isteğe bağlı `cause` alanı eklendi. SIP çekirdeği medya aktarıcısı teklifi almadığında (`route[media_refused]`) `{"event": "ended", "sip_code": 503, "cause": "media"}` gönderir. Motor:

- kaydı bugünkü gibi yazar: 503, cevap yok, ücret yok, sıradaki rota yok, durum deposunda bir şey kalmaz;
- kayıttaki `connection_id`'yi korur: kayıt hangi rotanın seçildiğini gösterir, yalnızca sayılmaz;
- canlı görünümde denemeyi bağlantıya yazmaz (`connections[]` ve ülkenin bağlantı dağılımı değişmez); çağrı müşteri, ülke ve toplam sayaçlarında 503 ile biten çağrı olarak kalır (arayan için çağrı gerçekten başarısızdır), cevap kodu dağılımında "deneme öncesi ret" gibi sayılır;
- `OnLeg`'e `Leg.NotSent = true` verir. Uzantılar böyle bir denemeyi sağlayıcının kalitesine saymamalıdır (Enterprise kalite tabanlı yönlendirme saymaz).

`cause` yalnızca `ended` olayında ve çağrı cevaplanmadan önce anlamlıdır; cevaptan sonra ya da başka olayda yok sayılır. Bilinmeyen `cause` değerleri de yok sayılır (sonraki bir SIP çekirdeği daha çok neden bildirebilsin diye).

Karar, 2026-11-23.

## Sonuçları

- **Sürüm v1 kalır.** ADR-0002 ve ARCHITECTURE §3 kural 1: isteğe bağlı alan eklemek sürümü artırmaz; mevcut alanların anlamı değişmez. Motorun olay çözücüsü bilinmeyen alanı reddetmez (`encoding/json`, `DisallowUnknownFields` yok): eski motor alanı yok sayar ve bugünkü davranışı (bağlantıyı suçlayan) sürdürür; yeni motor alanı taşımayan eski SIP çekirdeğiyle de bugünkü gibi çalışır. Yükseltme sırası bu yüzden önemli değildir; `deploy/prod/upgrade.sh` değişmedi.
- Genişleme noktası `engine.Leg` yeni bir alan aldı (`NotSent`). Sıfır değeri eski anlamdır; alanı bilmeyen uzantı derlenmeye devam eder, ama gönderilmemiş denemeyi bağlantının denemesi sayar. Enterprise deposu buna göre değişti.
- Motorun `CDR`'ı, `api.CDR`, `cdrs` tablosu ve ClickHouse şeması değişmedi: kayıtta "gönderilmedi" bilgisi yoktur; raporlar bu denemeleri bağlantının 503'ü olarak görür. Rapor ayrımı gerekirse ayrı bir karar ve kayıt alanı gerekir.
- Arayüz OpenSIPS kavramı taşımaz: neden "medya aktarıcısı teklifi almadı" demektir, betiğin nasıl anladığı değil.
