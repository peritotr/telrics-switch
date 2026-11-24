// SPDX-License-Identifier: AGPL-3.0-only
// Windows of days and hours on rates and routes: the form fields, and the
// conversion between what the form holds and what the API takes (a set of
// day bits, Monday = 1 up to Sunday = 64, and minutes since midnight).
import type { FieldDef } from '../ui/kit'

const presets = [{ value: '127', label: 'Her gün' }, { value: '31', label: 'Hafta içi' }, { value: '96', label: 'Hafta sonu' }]
const names = ['Pzt', 'Sal', 'Çar', 'Per', 'Cum', 'Cmt', 'Paz']

/** Minutes since midnight as HH:MM. */
export const clock = (m: number) => `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
const minutes = (s: string): number | null => {
  const m = /^([01]?\d|2[0-3]):([0-5]\d)$/.exec(s.trim())
  return m ? Number(m[1]) * 60 + Number(m[2]) : null
}

/** The days of a set, in words. */
function daysLabel(days: number) {
  const preset = presets.find((p) => Number(p.value) === days)
  return preset ? preset.label : names.filter((_, i) => days & (1 << i)).join(', ')
}

/** A row's window in words; "" when it has none. */
export function windowLabel(r: Record<string, any>) {
  const hours = r.time_from !== r.time_to ? `${clock(r.time_from)}–${clock(r.time_to)}` : ''
  if (r.days === 127) return hours
  return [daysLabel(r.days), hours].filter(Boolean).join(' · ')
}

/** Form fields for a window; days is the set a row being edited has. */
export function windowFields(zone: string, days?: number): FieldDef[] {
  const custom = days != null && !presets.some((p) => Number(p.value) === days) ? [{ value: String(days), label: daysLabel(days) }] : []
  return [
    { key: 'days', label: 'Günler', type: 'seg', options: [...presets, ...custom], span: true },
    { key: 'time_from', label: 'Saat başlangıç', type: 'mono', placeholder: '22:00', hint: `${zone} saatiyle. İkisi de boş = tüm gün` },
    { key: 'time_to', label: 'Saat bitiş', type: 'mono', placeholder: '06:00', hint: 'Başlangıçtan küçükse gece yarısını aşar' },
  ]
}

/** What the form starts with for a row (or for a new one). */
export function windowInitial(r?: Record<string, any>) {
  if (!r) return { days: '127', time_from: '', time_to: '' }
  const whole = r.time_from === r.time_to
  return { days: String(r.days), time_from: whole ? '' : clock(r.time_from), time_to: whole ? '' : clock(r.time_to) }
}

/** Errors in the window fields, by field. */
export function windowErrors(v: Record<string, any>) {
  const e: Record<string, string> = {}
  const from = String(v.time_from ?? '').trim(), to = String(v.time_to ?? '').trim()
  if (from && minutes(from) == null) e.time_from = 'SS:DD biçiminde girin, ör. 22:00.'
  if (to && minutes(to) == null) e.time_to = 'SS:DD biçiminde girin, ör. 06:00.'
  if (!e.time_from && !e.time_to && !!from !== !!to) e[from ? 'time_to' : 'time_from'] = 'Başlangıç ve bitiş birlikte girilir.'
  if (!e.time_from && !e.time_to && from && minutes(from) === minutes(to)) e.time_to = 'Bitiş başlangıçtan farklı olmalı.'
  return e
}

/** The window as the API takes it. */
export function windowBody(v: Record<string, any>) {
  const from = minutes(String(v.time_from ?? '')), to = minutes(String(v.time_to ?? ''))
  return { days: Number(v.days ?? 127), time_from: from ?? 0, time_to: from == null ? 0 : to ?? 0 }
}
