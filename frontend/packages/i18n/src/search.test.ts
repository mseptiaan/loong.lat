import { afterEach, describe, expect, it } from 'vitest'
import { LOCALES, enShared, getLocale, idShared, renderMsg, setLocale } from './index'

/*
 * Teks kedalaman pencarian dan laporan AI yang dikirim backend sebagai kunci (`progress.msg`, galat). Kunci dari kode Go
 * dipindai di http-api/backend-keys.test.ts; di sini dipastikan EN dan ID sama-sama ada, berparameter sama, dan benar-benar
 * diterjemahkan (bukan salinan Inggris).
 */

const base = (key: string) => key.replace(/_(one|other)$/, '')
const names = (text: string) => [...text.matchAll(/\{(\w+)(?:\|\w+)?\}/g)].map((m) => m[1]!).sort()

const SEARCH = ['experiments', 'polish', 'round', 'reviewAI', 'reviewRules', 'satisfied', 'more'].map((k) => `srv.engine.search.${k}`)
const OTHER = ['srv.api.badEffort', 'srv.ai.report.noOptions', 'srv.ai.report.unverified']

const entries = (catalog: Record<string, string>, key: string) => Object.entries(catalog).filter(([k]) => base(k) === key)

const startLocale = getLocale()
afterEach(() => setLocale(startLocale))

describe('kunci pesan kedalaman pencarian dan laporan AI', () => {
  it.each([...SEARCH, ...OTHER])('%s ada di Inggris dan Indonesia dengan parameter yang sama', (key) => {
    const en = entries(enShared, key)
    const id = entries(idShared as Record<string, string>, key)
    expect(en.length, `${key} (en)`).toBeGreaterThan(0)
    expect(id.length, `${key} (id)`).toBeGreaterThan(0)
    for (const [, text] of [...en, ...id]) expect(text.trim().length).toBeGreaterThan(5)
    // parameter yang dipakai teks Indonesia sama dengan Inggris (jamak boleh hanya `_other`)
    const enOther = en.find(([k]) => k.endsWith('_other')) ?? en[0]!
    expect(names(id[0]![1])).toEqual(names(enOther[1]))
    // diterjemahkan, bukan disalin
    expect(id[0]![1]).not.toBe(enOther[1])
  })

  it('kata-kata yang diminta brief untuk Inggris', () => {
    setLocale('en')
    expect(renderMsg({ key: 'srv.engine.search.experiments', params: { n: 12, count: 12 } })).toBe('Trying 12 ways of grouping the stops')
    expect(renderMsg({ key: 'srv.engine.search.polish' })).toBe('Polishing the best routes')
    expect(renderMsg({ key: 'srv.engine.search.round', params: { round: 2 } })).toBe('Review round 2')
    expect(renderMsg({ key: 'srv.engine.search.reviewAI' })).toBe('The AI reviewer is checking the routes')
    expect(renderMsg({ key: 'srv.engine.search.reviewRules' })).toBe('Checking the routes against the review rules')
    expect(renderMsg({ key: 'srv.engine.search.satisfied' })).toBe('The review is satisfied')
    expect(renderMsg({ key: 'srv.engine.search.more', params: { n: 3, count: 3 } })).toBe('The review asks for 3 more attempts')
    expect(renderMsg({ key: 'srv.api.badEffort' })).toBe('Unknown search depth.')
    expect(renderMsg({ key: 'srv.ai.report.noOptions' })).toBe('There are no options to explain yet.')
    expect(renderMsg({ key: 'srv.ai.report.unverified' })).toBe('The AI summary could not be checked against the numbers, so it is not shown.')
  })

  it('Indonesia dirender tanpa kunci mentah dan tanpa penampung yang tersisa', () => {
    setLocale('id')
    for (const key of [...SEARCH, ...OTHER]) {
      const text = renderMsg({ key, params: { n: 2, count: 2, round: 1 } })
      expect(text, key).not.toContain('srv.')
      expect(text, key).not.toMatch(/\{\w+\}/)
    }
  })

  it('katalog bersama tetap konsisten untuk seluruh kunci baru (tidak ada yang hanya di satu bahasa)', () => {
    const only = (a: Record<string, string>, b: Record<string, string>) => Object.keys(a).filter((k) => /^(srv\.engine\.search|srv\.ai\.report|srv\.api\.badEffort|srv\.api\.feature\.report)/.test(k) && !(k in b) && !/_(one|two|zero)$/.test(k))
    expect(only(enShared, idShared as Record<string, string>)).toEqual([])
    expect(only(idShared as Record<string, string>, enShared)).toEqual([])
    expect(LOCALES).toEqual(['en', 'id'])
  })
})
