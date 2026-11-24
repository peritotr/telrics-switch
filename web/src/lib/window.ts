// SPDX-License-Identifier: AGPL-3.0-only
// Windows of days and hours on rates and routes: the form fields, and the
// conversion between what the form holds and what the API takes (a set of
// day bits, Monday = 1 up to Sunday = 64, and minutes since midnight).
import type { FieldDef } from '../ui/kit'
import { t } from './i18n'

// Functions, not constants: the labels follow the interface language.
const presets = () => [{ value: '127', label: t('window.everyDay') }, { value: '31', label: t('window.weekdays') }, { value: '96', label: t('window.weekend') }]
const names = () => [t('window.mon'), t('window.tue'), t('window.wed'), t('window.thu'), t('window.fri'), t('window.sat'), t('window.sun')]

/** Minutes since midnight as HH:MM. */
export const clock = (m: number) => `${String(Math.floor(m / 60)).padStart(2, '0')}:${String(m % 60).padStart(2, '0')}`
const minutes = (s: string): number | null => {
  const m = /^([01]?\d|2[0-3]):([0-5]\d)$/.exec(s.trim())
  return m ? Number(m[1]) * 60 + Number(m[2]) : null
}

/** The days of a set, in words. */
function daysLabel(days: number) {
  const preset = presets().find((p) => Number(p.value) === days)
  return preset ? preset.label : names().filter((_, i) => days & (1 << i)).join(', ')
}

/** A row's window in words; "" when it has none. */
export function windowLabel(r: Record<string, any>) {
  const hours = r.time_from !== r.time_to ? `${clock(r.time_from)}–${clock(r.time_to)}` : ''
  if (r.days === 127) return hours
  return [daysLabel(r.days), hours].filter(Boolean).join(' · ')
}

/** Form fields for a window; days is the set a row being edited has. */
export function windowFields(zone: string, days?: number): FieldDef[] {
  const custom = days != null && !presets().some((p) => Number(p.value) === days) ? [{ value: String(days), label: daysLabel(days) }] : []
  return [
    { key: 'days', label: t('window.days'), type: 'seg', options: [...presets(), ...custom], span: true },
    { key: 'time_from', label: t('window.from'), type: 'mono', placeholder: '22:00', hint: t('window.fromHint', { zone }) },
    { key: 'time_to', label: t('window.to'), type: 'mono', placeholder: '06:00', hint: t('window.toHint') },
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
  if (from && minutes(from) == null) e.time_from = t('window.errFrom')
  if (to && minutes(to) == null) e.time_to = t('window.errTo')
  if (!e.time_from && !e.time_to && !!from !== !!to) e[from ? 'time_to' : 'time_from'] = t('window.errPair')
  if (!e.time_from && !e.time_to && from && minutes(from) === minutes(to)) e.time_to = t('window.errSame')
  return e
}

/** The window as the API takes it. */
export function windowBody(v: Record<string, any>) {
  const from = minutes(String(v.time_from ?? '')), to = minutes(String(v.time_to ?? ''))
  return { days: Number(v.days ?? 127), time_from: from ?? 0, time_to: from == null ? 0 : to ?? 0 }
}
