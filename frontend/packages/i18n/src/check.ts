import type { Messages } from './types'

const names = (text: string) => new Set([...text.matchAll(/\{(\w+)(?:\|\w+)?\}/g)].map((m) => m[1]!))
const tags = (text: string) => new Set([...text.matchAll(/<\/?([A-Za-z][\w-]*)>/g)].map((m) => m[1]!))
const sameSet = (a: Set<string>, b: Set<string>) => a.size === b.size && [...a].every((x) => b.has(x))
/** Bahasa Indonesia tidak membedakan tunggal/jamak, jadi akhiran ini boleh tidak ada. */
const OPTIONAL_VARIANT = /_(zero|one|two)$/

/**
 * Daftar masalah antara katalog Inggris (sumber) dan Indonesia: kunci yang hilang atau berlebih, teks kosong,
 * serta `{parameter}` dan `<tag>` yang tidak sama. Kosong = konsisten. Dipakai tes tiap katalog.
 */
export function catalogProblems(en: Messages, id: Messages): string[] {
  const problems: string[] = []
  for (const [key, text] of Object.entries(en)) {
    if (!text.trim()) problems.push(`${key}: teks Inggris kosong`)
    const other = id[key]
    if (other === undefined) {
      if (!OPTIONAL_VARIANT.test(key)) problems.push(`${key}: belum ada di katalog Indonesia`)
      continue
    }
    if (!other.trim()) problems.push(`${key}: teks Indonesia kosong`)
    if (!sameSet(names(text), names(other))) problems.push(`${key}: parameter berbeda ({${[...names(text)]}} vs {${[...names(other)]}})`)
    if (!sameSet(tags(text), tags(other))) problems.push(`${key}: tag berbeda (<${[...tags(text)]}> vs <${[...tags(other)]}>)`)
  }
  for (const key of Object.keys(id)) if (!(key in en)) problems.push(`${key}: hanya ada di katalog Indonesia`)
  return problems
}
