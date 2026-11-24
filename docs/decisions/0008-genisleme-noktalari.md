# ADR-0008: Enterprise, core'a yalnızca genişleme noktalarından bağlanır

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-21

## Bağlam

Enterprise özellikleri (bayi kuralları, DID fiyatlaması, kalite tabanlı yönlendirme, dolandırıcılık tespiti) çekirdeğin karar ve ücretlendirme akışına karışmak zorundadır, ama kodları açık depoda duramaz.

## Seçenekler

1. Çatallama (fork): hızlı; her birleştirmede çakışma.
2. Eklenti süreçleri (RPC): tam yalıtım; çağrı yolunda ek gecikme.
3. Core, Go düzeyinde adlandırılmış genişleme noktaları tanımlar ve tek hesaplı/tek düğümlü varsayılanlarını içerir; Enterprise kendi ikililerini core + eklentilerle derler.

## Karar

Üçüncü seçenek. API tarafı `pkg/api` içindedir (`Extension`, `AddResource`, `Handle`, `HandlePublic`, `SetScope`, `OnCDRPrice`, `OnCDRStored`, `AllowCDRField`, `Audit…`, `QueueMail`…); motor tarafı `engine.Config` içindedir (`Adjust`, `OnLeg`, `Peers`, `MaxCalls`) ve `routing.ToTrunk`. Güncel liste `docs/ARCHITECTURE.md` §12'dedir. Go paketleri bu yüzden `internal/` değil `pkg/` altındadır.

## Sonuçları

- Buraya Enterprise özelliği yazılmaz; yalnızca genişleme noktası ve varsayılanı eklenir.
- Genişleme noktasının imzasını değiştiren her değişiklikten sonra Enterprise deposu derlenir ve test edilir.
- `Adjust` çağrı yolundadır: yalnızca bellek, bekleme yok.
- Panelde Enterprise'a ait hiçbir şey görünmez: kilitli ekran, soluk menü ya da etiket yok. Enterprise ekranları `createPanel(...extensions)` ile eklenir.
- route-engine, rater ve api aynı `api.Server` üzerinden çalışır; Enterprise kuralları üç serviste de geçerlidir.
