// SPDX-License-Identifier: AGPL-3.0-only
// The interface language in force. Kept apart from the message catalogues so
// that format.ts (also used by pages that are not the panel) can follow the
// language without pulling the catalogues in. Only the panel ever changes it;
// everything else stays Turkish.

export type Lang = 'tr' | 'en'

let current: Lang = 'tr'

export const getLang = (): Lang => current

/** Sets the language and the page's lang attribute (screen readers, hyphenation, spell checking). */
export function applyLang(lang: Lang) {
  current = lang
  if (typeof document !== 'undefined') document.documentElement.lang = lang
}
