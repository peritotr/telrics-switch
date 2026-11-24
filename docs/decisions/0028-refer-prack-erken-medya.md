# ADR-0028: PRACK ve erken medya geçer; REFER reddedilir

- **Durum:** Kabul edildi
- **Tarih:** 2026-11-23
- **Kapattığı soru:** S-3

## Bağlam

REFER, 100rel/PRACK ve erken medya (183) için tanımlı bir politika yoktu; davranış kendiliğinden "olduğu gibi geçer"di.

## Seçenekler

1. Hepsini olduğu gibi geçirmek.
2. PRACK ve 183'ü geçirmek, REFER'i reddetmek.
3. REFER'i switch'te uygulamak (aktarmayı kendisi yapmak).

## Karar

İkinci seçenek. Çağrı içindeki REFER 403 ile reddedilir: Class 4 trafiğinde aktarma beklenmez, yetkisiz yeni çağrı ve dolandırıcılık riskidir. PRACK ve 183 (SDP'li) olduğu gibi iletilir ve senaryoyla kanıtlanır.

Karar, 2026-11-23: önerilen kararların ve iş sırasının listesi bütünüyle onaylandı (`docs/TASKS.md`, "Onaylanan sıra").

## Sonuçları

- Aktarma isteyen bir müşteri çıkarsa yeni bir karar gerekir.
