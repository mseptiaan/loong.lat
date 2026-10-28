import { describe, expect, it } from 'vitest'
import { LOCALES, registeredMessages } from '@loong/i18n'

/*
 * Backend mengirim teks sebagai kunci katalog (`contracts.Msg{Key: "srv.…"}`) dan layar yang menerjemahkannya, jadi
 * kunci yang ditulis di kode Go harus ada di katalog kedua bahasa; kalau tidak, layar menampilkan kunci mentah.
 * Tes ini memindai sumber Go (bukan tes, bukan pembantu tes) seperti `srv-keys.test.ts` memindai sumber mock.
 */
const sources = import.meta.glob<string>(['../../../../backend/**/*.go', '!../../../../backend/**/*_test.go'], { query: '?raw', import: 'default', eager: true })

// paket pembantu tes (testkit, drivertest, aiapitest, ...) dan e2e memakai kunci contoh yang tidak pernah dikirim
const helper = /\/(e2e|[a-z]*test|testkit|testdata)\//

const exact = new Map<string, string>()
const prefixes = new Map<string, string>()
for (const [file, text] of Object.entries(sources)) {
  if (helper.test(file)) continue
  for (const m of text.matchAll(/"((?:srv|dom)\.[A-Za-z0-9_.]+)"/g)) {
    const key = m[1]!
    // kunci yang berakhir titik adalah awalan yang disambung dengan nilai (mis. "dom.issue." + kind)
    ;(key.endsWith('.') ? prefixes : exact).set(key, file.replace('../../../../', ''))
  }
}

describe('kunci pesan yang dikirim backend', () => {
  it('ada yang dipindai (pemindai tidak kosong)', () => {
    expect(exact.size).toBeGreaterThan(150)
    expect(prefixes.size).toBeGreaterThan(3)
  })

  it.each(LOCALES)('semuanya terdaftar di katalog %s', (locale) => {
    const catalog = registeredMessages(locale)
    // varian jamak boleh ditulis eksplisit di kode ("…_one") atau sebagai kunci dasar yang dipilih katalog
    const has = (key: string) => {
      const base = key.replace(/_(one|other)$/, '')
      return key in catalog || base in catalog || `${base}_one` in catalog || `${base}_other` in catalog
    }
    const missing = [...exact].filter(([key]) => !has(key)).map(([key, file]) => `${key} (${file})`)
    expect(missing).toEqual([])
    const noFamily = [...prefixes].filter(([prefix]) => !Object.keys(catalog).some((k) => k.startsWith(prefix))).map(([p, file]) => `${p}* (${file})`)
    expect(noFamily).toEqual([])
  })
})
