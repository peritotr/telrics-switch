// SPDX-License-Identifier: AGPL-3.0-only
// Interface text in Turkish (the default) and English, without a library.
//
// Every text the panel shows is a key in the catalogues under src/i18n/: one
// file per area, each holding the Turkish text and the English one side by
// side. The English half is typed by the Turkish keys, so a missing or extra
// English text fails `tsc`; `npm run check-i18n` also compares placeholders.
//
// `t` reads the language in force (lib/lang.ts). When the language changes,
// the panel re-renders its shell and remounts the page (panel.tsx), so code
// may call `t` anywhere during rendering, but never at module load: a list
// of labels built at import time would stay in the language of the first
// load. Build such lists in a function instead.
import { Fragment, createContext, createElement, useContext, type ReactNode } from 'react'
import { en as coreEn, tr as coreTr } from '../i18n'
import { applyLang, getLang, type Lang } from './lang'

export { getLang, type Lang }

export type MessageKey = keyof typeof coreTr
export type Params = Record<string, string | number>

/**
 * What an extension adds to the catalogues. Turkish is required; an English
 * text missing for a key falls back to the Turkish one, so an edition can
 * ship its screens in Turkish first.
 */
export interface Messages {
  tr: Record<string, string>
  en?: Record<string, string>
}

export const languages: { value: Lang; label: string; name: string }[] = [
  { value: 'tr', label: 'TR', name: 'Türkçe' },
  { value: 'en', label: 'EN', name: 'English' },
]

const extra: Messages[] = []

/** Adds an extension's texts; core keys always win over an extension's. */
export function addMessages(m: Messages) {
  extra.push(m)
}

function lookup(key: string): string | undefined {
  if (getLang() === 'en') {
    const s = (coreEn as Record<string, string>)[key]
    if (s != null) return s
    for (const m of extra) if (m.en?.[key] != null) return m.en[key]
  }
  const s = (coreTr as Record<string, string>)[key]
  if (s != null) return s
  for (const m of extra) if (m.tr[key] != null) return m.tr[key]
  return undefined
}

let started = false
const warned = new Set<string>()

function text(key: string): string | undefined {
  if (import.meta.env.DEV && !started && !warned.has(key)) {
    warned.add(key)
    console.warn(`i18n: "${key}" was read before the panel started; it will not follow a change of language`)
  }
  return lookup(key)
}

/**
 * Fills placeholders: `{name}` takes params.name; `{n|one|other}` picks the
 * first word when params.n is 1 and the second otherwise (English plurals;
 * Turkish texts do not need it).
 */
export function fill(s: string, params?: Params): string {
  if (!params) return s
  return s.replace(/\{(\w+)(?:\|([^|}]*)\|([^}]*))?\}/g, (whole, name: string, one?: string, other?: string) => {
    const v = params[name]
    if (v == null) return whole
    if (one != null) return Number(v) === 1 ? one : other ?? ''
    return String(v)
  })
}

/** The text for a key in the language in force. */
export function t(key: MessageKey, params?: Params): string {
  return fill(text(key) ?? key, params)
}

/**
 * For text whose key is known only at run time: an extension's key, or a
 * label that may be a key or already plain text (an extension's menu item
 * written in Turkish). Anything that is not a key comes back unchanged.
 */
export function tx(keyOrText: string, params?: Params): string {
  return fill(text(keyOrText) ?? keyOrText, params)
}

/** Like `t`, with placeholders filled by elements: `rich('x', { name: <b>…</b> })`. */
export function rich(key: MessageKey, params: Record<string, ReactNode>): ReactNode {
  const s = text(key) ?? key
  const parts = s.split(/\{(\w+)\}/)
  return createElement(Fragment, null, ...parts.map((p, i) => (i % 2 ? params[p] ?? `{${p}}` : p)))
}

// ---- choosing the language ----

const storeKey = 'tsw.lang'
const isLang = (v: unknown): v is Lang => v === 'tr' || v === 'en'

/**
 * The language a page load starts with: `?lang=` in the address (for links
 * and screenshots), else what this browser chose before, else Turkish. The
 * browser's own language is not consulted, so that nobody who used the
 * panel in Turkish finds it in English one day.
 */
export function startLang(): Lang {
  let lang: Lang = 'tr'
  try {
    const stored = localStorage.getItem(storeKey)
    if (isLang(stored)) lang = stored
  } catch { /* storage blocked */ }
  const asked = new URLSearchParams(window.location.search).get('lang')
  if (isLang(asked)) {
    lang = asked
    remember(asked)
  }
  applyLang(lang)
  started = true
  return lang
}

function remember(lang: Lang) {
  // A preference of this browser, like the theme; never the session key.
  try { localStorage.setItem(storeKey, lang) } catch { /* private window */ }
}

/** Changes the language and remembers it for this browser. */
export function chooseLang(lang: Lang) {
  applyLang(lang)
  remember(lang)
}

interface LangCtx {
  lang: Lang
  setLang: (lang: Lang) => void
}

export const LangContext = createContext<LangCtx>({ lang: 'tr', setLang: () => {} })

/** The language in force and the way to change it; a component using it re-renders on a change. */
export const useLang = () => useContext(LangContext)
