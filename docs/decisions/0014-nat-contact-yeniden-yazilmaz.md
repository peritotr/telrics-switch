# ADR-0014: NAT: Contact yeniden yazılmaz; gerçek adres dialogda saklanır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23

## Bağlam

NAT arkasındaki uç kendini iç adresiyle tanıtır. Alışılmış çözüm `fix_nated_contact()` ile `Contact`'ı yeniden yazmaktır; bu OpenSIPS sürümünde `async()` öncesinde kullanılamaz (`docs/ARCHITECTURE.md` §4).

## Seçenekler

1. `fix_nated_contact`: standart; bu sürümde `async()` öncesinde kullanılamaz.
2. `Contact` olduğu gibi kalır; tarafın ilk isteğinin geldiği adres dialog değerinde (`caller_hop`, `callee_hop`) saklanır ve çağrı içi isteklerde hedef adres (`$du`) olarak verilir.

## Karar

İkinci seçenek. Cevaplar `force_rport` ile döner. `route[hop]` hem çağrı içi isteklerde hem `local_route`'taki (SIP çekirdeğinin kendi ürettiği) BYE'da çalışır. Bir istek, özel adres (RFC 1918 / RFC 6598) bildiriyor ve o adresten gelmiyorsa NAT'tan geçmiş sayılır. Ses için rtpengine'e `SIP-source-address` ilk tahmin olarak verilir; gerçek kaynağı kendisi öğrenir.

## Sonuçları

- `Contact`'ı yeniden yazan hiçbir işlev çağrılmaz.
- Senaryoların sonunda OpenSIPS işçilerinin düşmediği denetlenir.
- Çağrı sürerken dış adresi değişen cihaza çağrı içi istekler eski adrese gider.
- NAT arkasındaki sağlayıcı ele alınmaz.
- Gerçek yönlendirici ve simetrik NAT ile denenmedi (TASKS: T-08).
