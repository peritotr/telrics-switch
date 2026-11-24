# ADR-0043: Teklifsiz INVITE'ta teklif 200'de, cevap ACK'te medya aktarıcısından geçer

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı görev:** T-43

## Bağlam

SDP taşımayan bir INVITE'ta (RFC 3261 §13.2.1, §14.1; RFC 3264 §5–6) teklifi karşı taraf 200'de yapar, cevap ACK'te gelir. Betik yalnızca SDP'li INVITE'ı medya aktarıcısından geçiriyordu: 200'deki teklif tarafın gerçek ses adresiyle karşıya gidiyor, ses aktarıcıyı atlıyordu (topoloji gizleme, NAT ve SRTP çevirisi yok). İlk INVITE'ta 200'e üstelik `rtpengine_answer` gönderiliyor, aktarıcı "Unknown call-id" diyordu. Karar (2026-11-23): ilk INVITE'ta da çağrı içinde de teklifsiz INVITE'ın sesi aktarıcıdan geçecek.

Üç nokta için mevcut kararların doğrudan cevabı yoktu:

1. **Şifreleme.** ADR-0015'e göre switch'in teklif ettiği sağlayıcı ayağı, sinyalleşme TLS ise SRTP'dir; arayan ayağı arayanın teklifine uyar. Teklifsiz INVITE'ta teklifi sağlayıcı yapar, switch arayana teklif eder.
2. **ACK cevap taşımazsa.** 200'de teklif yapıldıysa ACK cevabı taşımak zorundadır; taşımazsa aktarıcıda yalnızca teklif vardır ve ses kurulamaz (RFC 3261 §13.2.2.4'e göre arayan çağrıyı BYE ile bitirmeliydi).
3. **Aktarıcı 200'deki teklifi almazsa.** ADR-0030 çağrıyı 503 ile reddettirir; 200'den sonra arayana 503 verilemez.

## Seçenekler

Şifreleme için: (a) arayana sağlayıcının teklif ettiği profili geçirmek; (b) arayan ayağına, sağlayıcı ayağındaki kuralın aynısı: teklif edilen taraf, sinyalleşmesi TLS ise SRTP alır.

ACK için: (a) ACK'i iletip bir şey yapmamak (ses akmaz; çağrıyı en geç medya zaman aşımı kapatır, ADR-0027); (b) ACK'i iletip çağrıyı hemen iki taraftan kapatmak.

Aktarıcı reddi için: (a) 200'ü değiştirmeden iletmek (sağlayıcının adresi görünür, ses aktarıcısız); (b) 200'ü SDP'siz iletip çağrıyı hemen kapatmak, motora reddi bildirmek.

## Karar

- **Akış:** teklifsiz ilk INVITE'ın 200'ündeki (ve SDP'li geçici cevaplarındaki) teklif `onreply_route[media]` içinde, teklifsiz çağrı içi INVITE'ın 200'ündeki teklif `onreply_route[reinvite]` içinde `rtpengine_offer` ile yazılır; ACK'teki cevap `route[ack]` içinde `rtpengine_answer` ile. Kodek listesi (ve çeviri) çağrı içi teklifteki gibi teklifin gittiği yöne göre dialogdan uygulanır; NAT bayrağı cevaplardaki gibi `Contact`'a göre verilir. İlk teklifte `ICE=remove` ve profil verilir; çağrı içinde verilmez (her ayak ilk görüşmedekini korur).
- **Şifreleme:** (b). Teklif eden taraf kendi profilini seçer (eskiden yalnızca arayan için yazılıydı, şimdi teklifi yapan sağlayıcı için de); switch'in teklif ettiği taraf, sinyalleşmesi TLS ise SRTP, değilse düz RTP alır. Teklifsiz INVITE'ta bu arayandır. ADR-0015'in kuralı böylece "switch'in teklif ettiği taraf" için geneldir; yerini almaz.
- **ACK cevap taşımazsa:** (b). ACK iletilir, dialog zamanlayıcısı bir saniyeye indirilir; iki taraf BYE alır, motor her zamanki `ended`'i alır (ADR-0026'daki yol). Kayıt 200 ve gerçek süreyle (1–2 saniye) yazılır ve ücretlenir: çağrı cevaplanmıştır, protokolü bozan taraftır.
- **Aktarıcı reddi:** (b), ADR-0030'un karşılığı. 200 SDP'siz iletilir (`remove_body_part`), iki taraf bir saniye sonra BYE alır, sayaç (`opensips_media_refused`) çağrı başına bir artar, motora `answered` yerine `ended` 503 ve `cause: "media"` gider (ADR-0039): kayıt 503, ücret yok, bağlantı suçlanmaz. Sıradaki rota denenmez. Çağrı içi teklifsiz INVITE'ta ise, SDP'li yeni teklifte olduğu gibi, cevap değiştirilmeden iletilir (ADR-0030 kurulmuş çağrıyı kapsamaz).

## Sonuçları

- Arayüz (v1) ve motor değişmez; uygulama SIP çekirdeğinin betiğindedir. Senaryolar `make test-routing`, "late" ve "nomedia" (TASKS: T-43).
- Aktarıcı reddinde sağlayıcı çağrıyı cevaplamıştır ve kısa süreyi faturalayabilir; switch'in kaydında alış tutarı yoktur (uzlaşma elle). `cause: "media"` burada "deneme gönderilmedi" değil "switch sesi kuramadı" anlamındadır; motor ikisini aynı işler.
- Sağlayıcının 200'ünün yeniden iletimi, ilk 200 için aktarıcı beklenirken (en çok ~5 sn, ADR-0030) gelirse SDP'siz geçer: 200 tutulamıyor (`drop()` 2xx'i durdurmuyor) ve sağlayıcının adresi arayana gitmemeli. Aktarıcı bu sürede geç de olsa cevap verirse arayan SDP'siz 200'ü cevapsız ACK'leyebilir ve çağrı kapanır. Aktarıcı olağan durumda milisaniyede cevap verdiği için yalnızca aktarıcı bozulurken görülür.
- PRACK'te ya da UPDATE'te taşınan teklif ve cevap (100rel'li 183'teki teklifin PRACK'teki cevabı) işlenmez (T-42'nin sınırı sürer).
