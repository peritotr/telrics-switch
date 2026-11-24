// SPDX-License-Identifier: AGPL-3.0-only
// Display and parsing helpers. Amounts travel as integer micro-units
// (1/1,000,000) and are never turned into floating point for arithmetic.
//
// Separators and dates follow the interface language (lib/lang.ts): Turkish
// writes 12.480,52 and 24.11.2026 14:05; English writes 12,480.52 and
// 2026-11-24 14:05 (ISO 8601 order, read the same way in the UK and the US,
// and as wide as the Turkish form, so columns keep their size).
import { getLang } from './lang'

const marks = () => (getLang() === 'en' ? { group: ',', point: '.' } : { group: '.', point: ',' })
const group = (digits: string) => digits.replace(/\B(?=(\d{3})+(?!\d))/g, marks().group)

/** Formats micro-units in the interface language: 12.480,52 or 12,480.52 */
export function money(micro: number | null | undefined, decimals = 2): string {
  if (micro == null) return '—'
  let n = BigInt(Math.round(Math.abs(micro)))
  const div = 10n ** BigInt(6 - decimals)
  n = (n + div / 2n) / div
  const s = n.toString().padStart(decimals + 1, '0')
  const whole = group(s.slice(0, s.length - decimals))
  const out = decimals ? `${whole}${marks().point}${s.slice(s.length - decimals)}` : whole
  return micro < 0 && n !== 0n ? `-${out}` : out
}

/** Signed amount for ledgers: the sign is always written. */
export const signed = (micro: number, decimals = 2) => (micro > 0 ? '+' : '') + money(micro, decimals)

/**
 * Parses what a user typed into micro-units. In Turkish a comma is the
 * decimal separator and dots group thousands; without a comma, a single dot
 * is taken as the decimal separator. In English a dot is the decimal
 * separator and commas group thousands. Returns null when the text is not an
 * amount with at most 6 decimals.
 */
export function parseMoney(text: string): number | null {
  let s = text.trim().replace(/\s/g, '')
  if (getLang() === 'en') s = s.replace(/,/g, '')
  else if (s.includes(',')) s = s.replace(/\./g, '').replace(',', '.')
  const m = /^(-?)(\d{1,12})(?:\.(\d{0,6}))?$/.exec(s)
  if (!m) return null
  const v = Number(m[2]) * 1_000_000 + Number((m[3] ?? '').padEnd(6, '0'))
  return m[1] ? -v : v
}

/** Text for an amount input: no grouping, trailing zeros trimmed to `min` decimals. */
export function moneyInput(micro: number | null | undefined, min = 2): string {
  if (micro == null) return ''
  const { group: g, point } = marks()
  let s = money(micro, 6).split(g).join('')
  while (s.split(point)[1].length > min && s.endsWith('0')) s = s.slice(0, -1)
  return s
}

export const num = (n: number | null | undefined) => (n == null ? '—' : group(String(Math.round(n))))

/** A number with fixed decimals in the interface language: 1.234,5 or 1,234.5 */
export function dec(n: number | null | undefined, decimals = 1): string {
  if (n == null || !isFinite(n)) return '—'
  const [whole, frac] = Math.abs(n).toFixed(decimals).split('.')
  return (n < 0 ? '-' : '') + group(whole) + (frac ? `${marks().point}${frac}` : '')
}

export const pct = (n: number | null | undefined, decimals = 1) =>
  n == null || !isFinite(n) ? '—' : n.toFixed(decimals).replace('.', marks().point)

const p2 = (n: number) => String(n).padStart(2, '0')

/** A date in UTC: GG.AA.YYYY in Turkish, YYYY-MM-DD in English. */
const ymd = (d: Date) => getLang() === 'en'
  ? `${d.getUTCFullYear()}-${p2(d.getUTCMonth() + 1)}-${p2(d.getUTCDate())}`
  : `${p2(d.getUTCDate())}.${p2(d.getUTCMonth() + 1)}.${d.getUTCFullYear()}`

/** Date and time in UTC: GG.AA.YYYY SS:DD, or YYYY-MM-DD HH:MM in English. */
export function dt(iso: string | Date | null | undefined, seconds = false): string {
  if (!iso) return '—'
  const d = new Date(iso)
  const base = `${ymd(d)} ${p2(d.getUTCHours())}:${p2(d.getUTCMinutes())}`
  return seconds ? `${base}:${p2(d.getUTCSeconds())}` : base
}

export function day(iso: string | Date): string {
  return ymd(new Date(iso))
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
export function moneyCSV(micro: number): string {
  const n = BigInt(Math.round(Math.abs(micro))).toString().padStart(7, '0')
  return `${micro < 0 ? '-' : ''}${n.slice(0, -6)}.${n.slice(-6)}`
}
