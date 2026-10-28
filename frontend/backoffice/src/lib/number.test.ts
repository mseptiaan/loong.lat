import { setLocale } from '@loong/i18n'
import { afterEach, describe, expect, it } from 'vitest'
import { formatInputNumber, parseNum } from './number'

afterEach(() => setLocale('en'))

describe('angka isian form', () => {
  it('pemisah desimal mengikuti bahasa dan tanpa pemisah ribuan', () => {
    expect(formatInputNumber(0.12)).toBe('0.12')
    expect(formatInputNumber(5000)).toBe('5000')
    setLocale('id')
    expect(formatInputNumber(0.12)).toBe('0,12')
    expect(formatInputNumber(5000)).toBe('5000')
  })

  it('hasil format dibaca kembali tanpa selisih di kedua bahasa', () => {
    for (const locale of ['en', 'id'] as const) {
      setLocale(locale)
      for (const n of [0, 0.12, 3.2, 20, 1500, 45000]) expect(parseNum(formatInputNumber(n))).toBe(n)
    }
  })
})
