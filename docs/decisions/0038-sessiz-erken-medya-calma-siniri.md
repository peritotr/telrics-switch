# ADR-0038: Sessiz erken medyalı çağrının çalması medya zaman aşımıyla sınırlıdır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-18

## Bağlam

ADR-0027'ye göre medya aktarıcısı bir çağrının hiçbir akışından `RTP_TIMEOUT` (60) saniye paket görmezse çağrıyı kapattırır. Aktarıcı bu süreyi iki tarafı da bilinen akışa uygular; SDP'li 183 (erken medya, ADR-0028) iki tarafı da bildirdiği için süre çağrı **çalarken** de işler. T-42'de laboratuvarda sınandı: 183'ten sonra hiçbir taraftan paket gelmezse aktarıcı 60. saniyede `teardown` der, SIP çekirdeği henüz doğrulanmamış dialogu bitirir (sağlayıcıya CANCEL), arayana 487 gider, kayıt 487'dir. Bağlantının `ring_timeout_sec` ayarı (ADR-0024, en çok 180) 60'tan büyük olsa da böyledir. SDP'siz 180 ile çalan ve erken medyası gerçekten akan çağrılar etkilenmez.

ADR-0024 "davranışı olmayan ayar olmaz" der; 60'tan büyük `ring_timeout_sec`, sessiz erken medyalı çağrıda etkisizdir. İki karar bu noktada çelişiyordu (S-18).

## Seçenekler

1. **(a) Böyle kalır.** ADR-0027'nin lafzı: 60 saniye hiç ses görmeyen çağrı, çalıyor olsa da kapanır. Belgeye ve paneldeki ayarın yanına "sessiz erken medyada çalma en çok medya zaman aşımı kadar" yazılır. Bedeli: 60. saniyeden sonra cevaplanacak sessiz erken medyalı çağrı kaybedilir.
2. **(b) `RTP_TIMEOUT` en uzun çalma süresinin (180) üstüne çıkarılır.** Ayar her durumda geçerli olur; ölü çağrı ise üç dakikada kapanır ve o arada sayılı ve parası ayrılmış kalır. ADR-0027'nin yerini alan bir karar gerekirdi.
3. **(c) Doğrulanmamış dialogdaki `teardown` yok sayılır.** Aktarıcının bildirimi modülün `teardown` komutu yerine betikteki bir kancaya gelir. Aktarıcı oturumu yine de sildiği için 200 geldiğinde ses yeniden kurulmalıdır (200'de yeni teklif ve cevap); karmaşık, betiğe karar sokar ve ayrıca sınanması gerekir.

## Karar

Birinci seçenek (a). Sessiz erken medyalı (SDP'li 183, hiçbir taraftan paket yok) ve çalarken medya zaman aşımını (`RTP_TIMEOUT`, varsayılan 60 sn) aşan çağrı aktarıcının zaman aşımıyla kapanır; bağlantının `ring_timeout_sec` değeri daha büyük olsa da. ADR-0027 olduğu gibi geçerlidir.

Karar, 2026-11-23.

## Sonuçları

- ADR-0024'ün kuralıyla çelişki belgeyle kapatılır: ayarın davranışı vardır (SDP'siz ya da sesli çalan çağrıda), sessiz erken medyadaki sınırı panelde ayarın açıklamasında ve `docs/08`'de yazılıdır.
- Betik, motor ve arayüz (v1) değişmez. Kayıt 487'dir, motor `failed 487` alır, sıradaki rotaya geçilmez.
- Uzun çalması beklenen bir sağlayıcı için çözüm sağlayıcının erken medyada ses (çaldırma sesi) göndermesidir; o çağrı etkilenmez.
- `RTP_TIMEOUT`'u büyütmek (seçenek b'nin etkisi) kurulumda yine mümkündür; bu, ölü çağrının kapanmasını da aynı ölçüde geciktirir (`docs/09-kurulum.md`).
- Laboratuvarın `connlab.sh early_long` planı ve tohum 8733 elle deneme için kalır; senaryo `make test-routing`'e eklenmez (bölüm 70 saniyeden uzun sürerdi ve var olan "deadcall" bölümü aynı zaman aşımını kanıtlıyor).
