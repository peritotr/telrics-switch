# ADR-0047: IPv6'da medyanın adres ailesini betik hedefin adres biçiminden seçer; kaynak adresi motorda tek biçime çevrilir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı görev:** T-28

## Bağlam

SIP çekirdeği IPv6'da da dinleyince (RFC 5118, RFC 6157) iki soru çıktı:

1. **Medyanın adres ailesi.** rtpengine'e iki aileden adres verilince (`RTP_INTERFACE=172.30.0.11;fd30::11`) teklifte hangisinin yazılacağını kendisi bilmez: bayrak verilmezse teklifi aldığı ailede bırakır. IPv4 arayanın teklifi IPv6 sağlayıcıya IPv4 aktarıcı adresiyle gider ve ses akmaz; laboratuvarda denendi, iki yönde de 0 paket. Teklif sağlayıcının 200'ündeyse (teklifsiz INVITE, ADR-0043) aynı sorun arayan tarafında çıkar. Bir yerde "bu ayak hangi ailede" sorusuna cevap verilmeli.
2. **Kaynak adresinin yazılışı.** OpenSIPS IPv6 adresini `$si`'de tam ve büyük harfle yazar (`FD30:0:0:0:0:0:0:21`; laboratuvarda `cache_fetch` ile görüldü). Motor bu metni güvenlik duvarında, kayıt adreslerinde (`regSrc`), sağlayıcı adresleri dizininde (`peers`, metin anahtarlı), yasaklarda ve çağrı kaydında kullanıyordu; aynı adres her yerde başka bir anahtar olurdu. Öte yandan SIP çekirdeğinin kendi `ban:` önbelleği o biçimle anahtarlanır: yasağı kaldırırken o biçim gerekir.

## Seçenekler

1. Adres ailesi:
   - (a) Betik, isteğin gerçekten gittiği yerin (`$du`, yoksa `$ru`) **adres biçiminden** seçer: köşeli parantezli adres IPv6, noktalı adres IPv4, ad ise hiçbir şey (aktarıcı arayanın ailesini korur). TLS → SRTP kuralıyla (ADR-0015) aynı türden, mekanik bir eşleme.
   - (b) Motor `route` içinde isteğe bağlı yeni bir alan (`media_family`) gönderir.
   - (c) Ad çözülüp ailesine bakılır (betikte ya da motorda).
2. Kaynak adresi:
   - (a) Motor gelen her `src_ip`'i girişte olağan kısa biçime çevirir (`netip`), SIP çekirdeğine özgü biçim yalnızca `pkg/sipctl`'de (yasak kaldırma) bilinir.
   - (b) Betik adresi motora göndermeden önce çevirir.
   - (c) Her karşılaştırma yeri kendisi çözümler.

## Karar

- **1a.** `route[offer]` hedefin biçimine göre `address-family=IP6|IP4` ekler (`route[af_of_to]`); cevap (`rtpengine_answer`) bayraksızdır: aktarıcı arayan tarafının ailesini teklifte gördüğü için kendisi seçer. Teklifsiz INVITE'ta ilk teklif arayana gider ve aktarıcı arayanı henüz görmemiştir: ailesi INVITE'ın geldiği adresten (`$si`) dialog'a yazılır (`$dlg_val(offerless_af)`) ve `route[late_offer]` ilk teklifte onu verir. Çağrı içi yeni tekliflerde iki taraf da bilinir, bayrak verilmez. (b) motora yeni alan eklerdi, ama motorun verdiği adres zaten `uri`/`next_hop` içinde; aynı bilgiyi iki kez göndermek olurdu. (c) çağrı yolunda ad çözmek demekti (karar süresi bütçesi); adla tanımlanan sağlayıcının ailesi aktarıcıya bırakılır.
- **2a.** `canonIP` (`pkg/engine/addr.go`) `authorize`, `source`, `register`, `ban` ve `unban` girişinde çalışır; IPv4'te (iki nokta yok) hiçbir şey yapmaz, çağrı yolunda maliyeti yoktur. Sağlayıcı adresleri dizini de aynı biçimle kurulur (`routing.peerKey`), böylece operatörün `FD30:0::20` yazdığı bağlantı da tanınır. `pkg/sipctl.OpenSIPSUnban` IPv6 adresinin `ban:` anahtarını iki biçimde de siler. (b) betiğe metin işleme koyardı ve her SIP çekirdeği için ayrı yazılırdı; (c) her yeni karşılaştırmada unutulurdu.

## Sonuçları

- Adla tanımlanan bir sağlayıcı yalnızca IPv6'daysa ve arayan IPv4'teyse (ya da tersi) ses akmaz: aktarıcı arayanın ailesini korur. Böyle sağlayıcı bağlantısına adres yazılmalı; `docs/08` ve `docs/09` bunu söyler.
- Sağlayıcıya giden URI'de IPv6 adresi köşeli parantez içindedir (`routing.uriHost`; RFC 3261 §19.1.1); bu düzeltmeden önce motor `sip:…@fd30::20:5060` yazıyordu ve SIP çekirdeği isteği gönderemiyordu.
- Çağrı kaydında, kayıt listesinde ve yasak listesinde IPv6 adresi kısa biçimdedir (`fd30::21`); SIP çekirdeğinin kendi önbelleği tam biçimi kullanmayı sürdürür.
- Arayüz değişmedi (sürüm 1, yeni alan yok).
