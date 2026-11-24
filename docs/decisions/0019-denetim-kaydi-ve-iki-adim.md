# ADR-0019: Denetim kaydı işlemin içinde yazılır; sırlar tek biçimde şifrelenir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-22

## Bağlam

Kimin neyi ne zaman değiştirdiği izlenebilmeli; panel girişleri ikinci adımla korunabilmelidir. Saklanan sırlar (TOTP sırrı, sağlayıcı parolası) veritabanında açık durmamalıdır.

## Seçenekler

1. Denetim kaydını ayrı, sonradan yazmak: basit; değişiklik olup kayıt düşmeyebilir.
2. Değişikliğin işlemi içinde `Server.Audit` ile yazmak.
3. Her sır için ayrı şifreleme biçimi.
4. Tek biçim: `pkg/totp` (sürüm baytı + nonce + AES-GCM, anahtar `SECRET_KEY`).

## Karar

İkinci ve dördüncü seçenekler. Genel CRUD kendiliğinden kaydeder; özel işlem işlemin içinde `s.Audit` çağırır; hiçbir şeyi değiştirmeyen çağrı `AuditNothing` kullanır. Sırlar alan adına ve `Resource.Hide`'a göre maskelenir. İki adım TOTP ve yedek kodlardır.

## Sonuçları

- Sır olmayan bir ayrıntıya `…_token`, `…_key`, `recovery_code` gibi ad verilmez; değeri `***` olur.
- `pkg/totp` saklama biçimi Enterprise portalıyla ortaktır; değiştirilmez.
- Motor, sağlayıcı parolasını okuyabilmek için API ile aynı `SECRET_KEY` değerini ister.
- SSO ve iki adımı herkese zorunlu kılma yoktur (TASKS: T-22, T-23).
