// SPDX-License-Identifier: AGPL-3.0-only
// Display and parsing helpers. Amounts travel as integer micro-units
// (1/1,000,000) and are never turned into floating point for arithmetic.

const group = (digits: string) => digits.replace(/\B(?=(\d{3})+(?!\d))/g, '.')

/** Formats micro-units with the Turkish locale: 12.480,52 */
export function money(micro: number | null | undefined, decimals = 2): string {
  if (micro == null) return '—'
  let n = BigInt(Math.round(Math.abs(micro)))
  const div = 10n ** BigInt(6 - decimals)
  n = (n + div / 2n) / div
  const s = n.toString().padStart(decimals + 1, '0')
  const whole = group(s.slice(0, s.length - decimals))
  const out = decimals ? `${whole},${s.slice(s.length - decimals)}` : whole
  return micro < 0 && n !== 0n ? `-${out}` : out
}

/** Signed amount for ledgers: the sign is always written. */
export const signed = (micro: number, decimals = 2) => (micro > 0 ? '+' : '') + money(micro, decimals)

/**
 * Parses what a user typed into micro-units. A comma is the decimal
 * separator; without one, a single dot is taken as the decimal separator.
 * Returns null when the text is not an amount with at most 6 decimals.
 */
export function parseMoney(text: string): number | null {
  let s = text.trim().replace(/\s/g, '')
  if (s.includes(',')) s = s.replace(/\./g, '').replace(',', '.')
  const m = /^(-?)(\d{1,12})(?:\.(\d{0,6}))?$/.exec(s)
  if (!m) return null
  const v = Number(m[2]) * 1_000_000 + Number((m[3] ?? '').padEnd(6, '0'))
  return m[1] ? -v : v
}

/** Text for an amount input: no grouping, trailing zeros trimmed to `min` decimals. */
export function moneyInput(micro: number | null | undefined, min = 2): string {
  if (micro == null) return ''
  let s = money(micro, 6).replace(/\./g, '')
  while (s.split(',')[1].length > min && s.endsWith('0')) s = s.slice(0, -1)
  return s
}

export const num = (n: number | null | undefined) => (n == null ? '—' : group(String(Math.round(n))))

/** A number with fixed decimals in the Turkish locale: 1.234,5 */
export function dec(n: number | null | undefined, decimals = 1): string {
  if (n == null || !isFinite(n)) return '—'
  const [whole, frac] = Math.abs(n).toFixed(decimals).split('.')
  return (n < 0 ? '-' : '') + group(whole) + (frac ? `,${frac}` : '')
}

export const pct = (n: number | null | undefined, decimals = 1) =>
  n == null || !isFinite(n) ? '—' : n.toFixed(decimals).replace('.', ',')

const p2 = (n: number) => String(n).padStart(2, '0')

/** GG.AA.YYYY SS:DD in UTC. */
export function dt(iso: string | null | undefined, seconds = false): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const base = `${p2(d.getUTCDate())}.${p2(d.getUTCMonth() + 1)}.${d.getUTCFullYear()} ${p2(d.getUTCHours())}:${p2(d.getUTCMinutes())}`
  return seconds ? `${base}:${p2(d.getUTCSeconds())}` : base
}

export function day(iso: string): string {
  const d = new Date(iso)
  return `${p2(d.getUTCDate())}.${p2(d.getUTCMonth() + 1)}.${d.getUTCFullYear()}`
}

/** Value for <input type="datetime-local"> in UTC, and back. */
export const toLocalInput = (iso: string | null | undefined) => (iso ? new Date(iso).toISOString().slice(0, 16) : '')
export const fromLocalInput = (v: string) => (v ? new Date(v + ':00Z').toISOString() : null)

const sipReasons: Record<number, string> = {
  200: 'OK', 400: 'Bad Request', 401: 'Unauthorized', 403: 'Forbidden', 404: 'Not Found',
  408: 'Request Timeout', 480: 'Temporarily Unavailable', 486: 'Busy Here', 487: 'Request Terminated',
  488: 'Not Acceptable Here', 500: 'Server Error', 502: 'Bad Gateway', 503: 'Service Unavailable', 504: 'Server Time-out',
}
export const sipReason = (code: number) => sipReasons[code] ?? ''

/** Colour for a SIP code: 200 ok, 4xx warning, 403 and 5xx/6xx bad. */
export const sipTone = (code: number) =>
  code >= 200 && code < 300 ? 'var(--ok)' : code === 403 || code >= 500 ? 'var(--bad)' : 'var(--warn)'

export function downloadCSV(name: string, header: string[], rows: (string | number | boolean | null)[][]) {
  const cell = (v: string | number | boolean | null) => {
    const s = v == null ? '' : String(v)
    return /[",\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s
  }
  const text = [header, ...rows].map((r) => r.map(cell).join(',')).join('\n') + '\n'
  const a = document.createElement('a')
  a.href = URL.createObjectURL(new Blob([text], { type: 'text/csv' }))
  a.download = name
  a.click()
  URL.revokeObjectURL(a.href)
}

/** Micro-units as a plain decimal for CSV files (dot separator, as the import expects). */
export const moneyCSV = (micro: number) => money(micro, 6).replace(/\./g, '').replace(',', '.')
