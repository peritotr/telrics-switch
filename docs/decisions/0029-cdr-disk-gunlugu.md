# ADR-0029: NATS'a yazılamayan çağrı kayıtları motorun diskinde saklanır; düşen çağrı da kaydedilir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-4

## Bağlam

Motor NATS'a yazamadığı kayıtları bellekte kuyruklar; motor bu sırada çökerse kayıtlar kaybolur. Ayrıca kapanışı hiç bildirilmeyen çağrı süresi dolunca kayıtsız düşer. İkisi de "CDR kaybolmaz" kuralına (ADR-0006) aykırıdır.

## Seçenekler

1. Olduğu gibi bırakmak.
2. Kuyruk dolunca çağrı kabulünü durdurmak.
3. Kayıtları arka planda yerel bir günlük dosyasına yazmak ve açılışta yeniden göndermek; süresi dolan çağrı için motorun kendisinin kayıt üretmesi.

## Karar

Üçüncü seçenek. Günlük dosyasına çağrı yolunda değil, kayıt gönderen arka plan işinde yazılır; NATS kabul edince kayıt günlükten düşer. Açılışta günlükteki kayıtlar yeniden gönderilir (ileti kimliği tekrarı zararsız kılar). Süresi dolup düşen çağrı için kayıt yazılır.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- Çağrı yoluna disk yazımı eklenmez (ADR-0003).
- Günlüğün yeri ve boyutu ayarlanabilir; üretim compose'unda kalıcı bir birim.
- Süresi dolup düşen çağrının kaydının içeriği (süre ve ücret) bu kayıtta söylenmedi; ayrıca karara bağlandı: bkz. ADR-0040.
- Görev: T-33.
