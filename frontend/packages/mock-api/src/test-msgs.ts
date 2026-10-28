import { LOCALES, renderMsg, type Msg } from '@loong/i18n'

/* Alat tes: memeriksa bahwa pesan netral-bahasa (`Msg`) yang dibuat kode benar-benar terdaftar di katalog. */

const MSG_KEY = /^(srv|dom|cat|fmt|ui|shell|ai)\.[\w.]+$/

const isMsg = (v: unknown): v is Msg =>
  typeof v === 'object' && v !== null && typeof (v as Msg).key === 'string' && MSG_KEY.test((v as Msg).key)

/** Semua `Msg` di dalam nilai apa pun (objek, larik, dan `Msg` bersarang di `params`). */
export function collectMsgs(value: unknown, out: Msg[] = []): Msg[] {
  if (isMsg(value)) {
    out.push(value)
    for (const p of Object.values(value.params ?? {})) collectMsgs(p, out)
  } else if (Array.isArray(value)) {
    for (const v of value) collectMsgs(v, out)
  } else if (typeof value === 'object' && value !== null) {
    for (const v of Object.values(value)) collectMsgs(v, out)
  }
  return out
}

/**
 * Masalah penerjemahan semua `Msg` di dalam `value`, di tiap bahasa: kunci tidak terdaftar (hasil sama dengan kuncinya)
 * atau parameter kurang (sisa `{nama}` pada hasil). Kosong = semua kunci ada dan parameternya lengkap.
 */
export function msgProblems(value: unknown): string[] {
  const seen = new Set<string>()
  const problems: string[] = []
  for (const m of collectMsgs(value)) {
    for (const locale of LOCALES) {
      const text = renderMsg(m, locale)
      const id = `${locale}:${m.key}:${JSON.stringify(m.params ?? {})}`
      if (seen.has(id)) continue
      seen.add(id)
      if (text === m.key) problems.push(`${locale}: unregistered key ${m.key}`)
      else if (/[{}]/.test(text)) problems.push(`${locale}: missing parameter in ${m.key}: ${text}`)
    }
  }
  return problems
}
