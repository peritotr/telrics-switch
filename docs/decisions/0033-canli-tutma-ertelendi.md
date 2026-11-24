# ADR-0033: Kayıtlı trunk'a canlı tutma şimdilik yok

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-13

## Bağlam

Kayıtlar kısa süreyle (NAT'ta 60 sn) yenilenmeye zorlanıyor; OPTIONS ile canlı tutma gönderilmiyor (ADR-0013).

## Seçenekler

1. Canlı tutma eklemek.
2. Kısa süreli kaydı korumak ve gerçek NAT denemesinden sonra yeniden bakmak.

## Karar

İkinci seçenek. Gerçek bir NAT yönlendiricisiyle deneme (T-08) yapılınca karar yeniden değerlendirilir.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- ADR-0013'teki sınır geçerliliğini korur.
