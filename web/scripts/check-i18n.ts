// SPDX-License-Identifier: AGPL-3.0-only
// Checks the panel's message catalogues (src/i18n/*.ts) beyond what tsc
// sees: both languages have the same keys, no key is defined in two files,
// no text is empty, and a key's English text has the same placeholders as
// its Turkish one. Keys no source file names are listed as a warning.
//
// Run with `npm run check-i18n` (Node 23.6+ runs TypeScript as is); `npm run
// build` runs it too. An extra directory may be given to check another
// package's sources for key use as well.
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('../src', import.meta.url))
const dir = join(root, 'i18n')
const problems: string[] = []
const owner = new Map<string, string>()

// Placeholders without the plural choice: {n|one|other} counts as {n}.
const holes = (s: string) => [...new Set([...s.matchAll(/\{(\w+)(?:\|[^}]*)?\}/g)].map((m) => m[1]))].sort().join(',')

for (const file of readdirSync(dir).filter((f) => f.endsWith('.ts') && f !== 'index.ts').sort()) {
  const m = (await import(join(dir, file))) as { tr: Record<string, string>; en: Record<string, string> }
  if (!m.tr || !m.en) { problems.push(`${file}: must export tr and en`); continue }
  for (const key of Object.keys(m.tr)) {
    const was = owner.get(key)
    if (was) problems.push(`${key}: defined in ${was} and ${file}`)
    owner.set(key, file)
    if (!(key in m.en)) problems.push(`${file}: ${key} has no English text`)
    else if (holes(m.tr[key]) !== holes(m.en[key])) problems.push(`${file}: ${key} placeholders differ: tr {${holes(m.tr[key])}} en {${holes(m.en[key])}}`)
    if (!m.tr[key].trim() || !String(m.en[key] ?? 'x').trim()) problems.push(`${file}: ${key} is empty`)
  }
  for (const key of Object.keys(m.en)) if (!(key in m.tr)) problems.push(`${file}: ${key} is English only`)
}

function sources(d: string, out: string[] = []) {
  for (const e of readdirSync(d, { withFileTypes: true })) {
    const p = join(d, e.name)
    if (e.isDirectory()) { if (p !== dir) sources(p, out) } else if (/\.tsx?$/.test(e.name)) out.push(readFileSync(p, 'utf8'))
  }
  return out
}
const code = sources(root).join('\n') + process.argv.slice(2).map((d) => sources(d).join('\n')).join('\n')
const unused = [...owner.keys()].filter((k) => !code.includes(`'${k}'`) && !code.includes(`"${k}"`))

if (unused.length) console.warn(`check-i18n: ${unused.length} keys not named in the sources (built at run time, or unused):\n  ${unused.join('\n  ')}`)
if (problems.length) {
  console.error(`check-i18n: ${problems.length} problems\n  ${problems.join('\n  ')}`)
  process.exit(1)
}
console.log(`check-i18n: ${owner.size} keys, Turkish and English in step`)
