# ADR-0032: Müşteri olmayan kaynaktan gelen çağrı için motorda yeni genişleme noktası

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-12

## Bağlam

Enterprise'ın gelen numara (DID) çağrısını müşteriye yönlendirebilmesi için, kaynağı hiçbir trunk'a uymayan çağrının kabul edilebilmesi gerekir. Bugün böyle çağrı `unknown source` ile reddediliyor ve `Adjust` hiç çağrılmıyor.

## Seçenekler

1. `engine.Config`'e kaynak bir trunk'a uymadığında çağrılan, çağrıyı bir müşteri adına kabul ettirebilen yeni bir uç.
2. Sağlayıcı bağlantılarının adreslerini "gelen çağrı kaynağı" olarak tanıyan genel bir çekirdek kavramı.

## Karar

Birinci seçenek: çekirdek DID'den habersiz kalır; uç verilmemişse davranış bit bit aynıdır.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- Uç yalnızca bellekten karar verir; güvenlik duvarı hükmü uçtan önce gelir.
- Görev: T-04; Enterprise E16 buna bağlanır.

## Uygulama (T-04, 2026-11-23)

- Uç: `engine.Config.Incoming(CallInfo, *routing.Snapshot) (routing.Inbound, bool)`; yalnızca kaynağı hiçbir trunk'a uymayan ve giriş yapmamış (`auth_user` boş) çağrıda sorulur. `routing.Inbound` müşteriyi ve isteğe bağlı olarak numarayı, fiyatı ve rotaları taşır; karar `Snapshot.AuthorizeFor` ile müşterinin trunk'sız çağrısı olarak verilir.
- Uç adresi tanınmış yapmaz (`source_known` yanlış kalır, `POST /v1/source` değişmedi): kaynağın tanınması eskisi gibi sağlayıcı bağlantısı, trunk ya da güvenlik duvarı `allow` kuralıyla olur. Böylece yabancılara karşı koruma ve arayüz değişmedi. Ayrıntı: `docs/08-cagri-arayuzu.md`, "Müşteri olmayan kaynaktan gelen çağrı".
