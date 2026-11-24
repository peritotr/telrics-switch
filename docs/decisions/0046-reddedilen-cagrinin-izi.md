# ADR-0046: Reddedilen çağrı, motor müşterinin olduğunu söylediğinde izlenir; yabancının hiçbir isteği izlenmez

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı görev:** T-26
- **Değiştirdiği:** ADR-0018'in "Sonuçları" bölümündeki "yalnızca sağlayıcıya gönderilen çağrılar izlenir" maddesi (kararın geri kalanı geçerli)

## Bağlam

Çağrı izi (ADR-0018) yalnızca motorun kabul edip sağlayıcıya gönderdiği çağrılarda başlıyordu: `trace()` yetkilendirmeden sonra, dialog kurulurken çağrılıyordu. Operatörün en çok sorduğu sorulardan biri ise "müşterinin çağrısı neden reddedildi"dir (bakiye, limit, tarife, arayan kuralı). Öte yandan yabancı kaynakların istekleri (tarayıcılar, parola deneyenler, taşkınlar) izlenirse diski doldurabilir ve hiçbir müşteriye ait olmayan iz kimsenin işine yaramaz. Karar verilmesi gerekenler:

- SIP çekirdeği bir çağrının "tanınan bir müşterinin" olduğunu nereden bilecek? Betik karar vermemeli (ADR-0002: betik ince kalır).
- Parolayla tanınan trunk'ın ilk INVITE'ı ve ona verilen 401, motor arayanın kim olduğunu bilmeden önce gelir. Onlar ne olacak?
- Motora sorulmadan SIP çekirdeğinde verilen retler (taşkın, `ban:` önbelleği), güvenlik duvarının engellediği adres, yanlış parola.

## Seçenekler

1. **Tanınmanın işareti:** (a) var olan `source_known` alanı; (b) `authorize` cevabına yeni, isteğe bağlı bir alan (`trace`); (c) betiğin kendi kuralı (ör. "kodu 403 değilse izle").
2. **Parolayla gelen çağrının başı:** (a) izlenmez, iz kimlik bilgisini taşıyan INVITE'tan başlar; (b) yabancının iletileri kısa süre bellekte tutulur, çağrı tanınınca geriye dönük yazılır; (c) parolası denenen her istek izlenir.
3. **Yanlış parola** (kullanıcı adı var, parola yanlış): (a) izlenmez; (b) izlenir.
4. **Güvenlik duvarının engellediği, önceden tanınan adres:** (a) izlenmez; (b) motor trunk'ı bulup izlenmesini söyler.

## Karar

- **1b.** `authorize` cevabında `trace: true`: çağrı bir müşterinindir (kabul edilmiş ya da reddedilmiş). Motor bunu reddi kaydettiği koşulla aynı koşulda verir (`ClientID != 0`): izlenen her ret bir çağrı kaydı da bırakır, ve çağrının izini görme hakkı (`mayTrace`) o kayda bakar. `source_known` bu iş için yetmezdi: parolayla tanınan arayanda (adresi tanınmaz) ve müşteri olmayan kaynaktan bir müşteri adına alınan çağrıda (`Incoming`) yanlıştır, ama çağrı bir müşterinindir. Alan isteğe bağlıdır (arayüz sürüm 1 kalır): eski motor göndermez, yeni betik o zaman yalnızca eskisi gibi kabul edilen çağrıları izler; eski betik alanı yok sayar. Betik yalnızca motorun sözüne bakar (`route[trace_refused]`), kendi kuralı yoktur (1c'yi bu yüzden seçmedik).
- Reddedilen çağrının dialog'u yoktur; izi işlem (transaction) kapsamındadır: `trace("calls", "t", "sip")`, motorun cevabından sonra, ret gönderilmeden hemen önce. Saklanan: gelen INVITE, giden ret ve arayanın ACK'i. İşlem SIP çekirdeğinde motordan önce açıldığı için `100 Trying` ve motor karar verirken gelen CANCEL ile ona verilen 200 izde yoktur. Motor kabul ettiği hâlde arayan karar beklenirken vazgeçtiyse (487, T-01) de aynısı uygulanır: kaydı vardır, izi de olur.
- **2a.** Parolayla gelen çağrının izi kimlik bilgisini taşıyan INVITE'tan (CSeq 2) başlar; ilk INVITE ve 401 izlenmez. O iki ileti geldiğinde arayan herhangi biri olabilir; 2b, yabancının iletilerini bellekte tutmak demekti (taşkında belleği doldurur, çekirdeğe durum ekler) ve betiğe karar sokardı; 2c parola deneyen herkesi izlemek olurdu. Kimlik bilgisini taşıyan INVITE izde `Authorization` başlığıyla durur, `response` maskelenir (ADR-0018). `WWW-Authenticate` (bizim sorumuz) sır taşımaz: nonce ve alan adı yalnızca bir kez geçerli bir sorudur; maskelenmez.
- **3a.** Yanlış parola izlenmez. Kullanıcı adının var olması arayanın o müşteri olduğunu kanıtlamaz (adlar tahmin edilebilir), motor çağrıya hiç karar vermez ve kayıt yazılmaz: izi kimse bulamaz, ama parola deneyen biri diski doldurabilir.
- **4a.** Güvenlik duvarının engellediği adres izlenmez: motor böyle bir çağrıyı müşterisini aramadan reddeder ve bilerek kaydını yazmaz ("engelli adres defteri dolduramaz"); iz de aynı kurala uyar.
- Motora hiç sorulmayan her şey izlenmez: taşkın (`pike`), `ban:` önbelleğindeki adres, yabancının 401'i ve diğer istekleri, motora ulaşılamadığında verilen 503.

## Sonuçları

- İzlenen ileti sayısı artık kabul edilen çağrılara ek olarak müşterilerin reddedilen çağrılarıyla da (ret başına 3 ileti) orantılıdır; bu sayı müşterinin kendi trafiğine bağlıdır ve çağrı kaydı sayısıyla aynı şekilde büyür. Limitini çok aşan bir müşteri (ör. sınırının üstünde CPS gönderen) iz deposunu da büyütür; toplayıcı yetişemediğini sayarak atar (`softswitch_trace_dropped_total`), çağrılar etkilenmez.
- Erişim kuralı değişmedi (`mayTrace`): reddedilen çağrının kaydı müşteriye yazıldığı için o müşteriyi gören hesap izi görür, başkası "bulunamadı" alır.
- Kimlik sorusu (401) yalnızca sağlayıcı tarafında izlenir (sağlayıcının bize sorduğu 401/407 ve `uac_auth` ile ikinci deneme, kabul edilen çağrının dialog izinin içindedir). Müşteri tarafındaki 401 hiçbir zaman izlenmez; parolası hep yanlış olan bir cihazı ayıklamak için sunucuda `tcpdump` gerekir.
- `deploy/images/opensips/run.sh` iki dosya yazar: `trace-call.cfg` (dialog kapsamı) ve `trace-refused.cfg` (işlem kapsamı); iz kapalıyken ikisi de boştur.
