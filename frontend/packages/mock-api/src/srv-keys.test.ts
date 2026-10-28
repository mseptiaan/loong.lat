import { describe, expect, it } from 'vitest'
import { LOCALES, registeredMessages } from '@loong/i18n'

/** Semua berkas sumber (bukan tes) di mock-api sebagai teks, untuk memindai kunci katalog yang ditulis di kode. */
const sources = import.meta.glob<string>(['./**/*.ts', '!./**/*.test.ts'], { query: '?raw', import: 'default', eager: true })

const used = new Map<string, string>()
for (const [file, text] of Object.entries(sources)) {
  for (const m of text.matchAll(/'(srv\.(?:engine|ops|proof)\.[\w.]+)'/g)) used.set(m[1]!, file)
}

describe('kunci srv.engine.* dan srv.ops.* yang dipakai kode', () => {
  it('ada yang dipindai (pemindai tidak kosong)', () => {
    expect(used.size).toBeGreaterThan(100)
  })

  it.each(LOCALES)('semuanya terdaftar di katalog %s (kunci tunggal atau varian jamak _other)', (locale) => {
    const catalog = registeredMessages(locale)
    const missing = [...used].filter(([key]) => !(key in catalog) && !(`${key}_other` in catalog)).map(([key, file]) => `${key} (${file})`)
    expect(missing).toEqual([])
  })
})
