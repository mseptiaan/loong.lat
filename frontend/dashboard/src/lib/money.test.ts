import { afterEach, describe, expect, it } from 'vitest'
import { getLocale, setLocale } from '@loong/i18n'
import { formatAmountInput, formatRemaining, parseAmount, safeNext } from './money'

describe('parseAmount', () => {
  it('membuang pemisah ribuan dan simbol; kosong menjadi NaN', () => {
    expect(parseAmount('1.250.000')).toBe(1_250_000)
    expect(parseAmount('Rp 50000')).toBe(50_000)
    expect(parseAmount(' 75.000 ')).toBe(75_000)
    expect(parseAmount('')).toBeNaN()
    expect(parseAmount('abc')).toBeNaN()
  })

  it('format lalu parse kembali ke angka yang sama', () => {
    for (const n of [10_000, 123_456, 5_000_000]) expect(parseAmount(formatAmountInput(n))).toBe(n)
    expect(formatAmountInput(Number.NaN)).toBe('')
  })
})

describe('safeNext', () => {
  it('menerima jalur internal beserta query', () => {
    expect(safeNext('/plan/new?draft=pln-41&step=4')).toBe('/plan/new?draft=pln-41&step=4')
    expect(safeNext('/wallet')).toBe('/wallet')
  })

  it('menolak URL lengkap, protocol-relative, backslash, dan kosong', () => {
    for (const bad of ['https://evil.example/x', '//evil.example', '/\\evil.example', 'plan/new', 'javascript:alert(1)', '', null, undefined]) {
      expect(safeNext(bad)).toBeUndefined()
    }
  })
})

describe('formatRemaining', () => {
  const before = getLocale()
  afterEach(() => setLocale(before))

  it('menulis sisa waktu dalam bahasa Inggris', () => {
    setLocale('en')
    expect(formatRemaining(23 * 3_600_000 + 40 * 60_000)).toBe('23 h 40 min')
    expect(formatRemaining(2 * 3_600_000)).toBe('2 h')
    expect(formatRemaining(12 * 60_000)).toBe('12 min')
    expect(formatRemaining(30_000)).toBe('less than 1 min')
    expect(formatRemaining(0)).toBe('expired')
  })

  it('menulis sisa waktu dalam bahasa Indonesia', () => {
    setLocale('id')
    expect(formatRemaining(23 * 3_600_000 + 40 * 60_000)).toBe('23 j 40 mnt')
    expect(formatRemaining(2 * 3_600_000)).toBe('2 j')
    expect(formatRemaining(12 * 60_000)).toBe('12 mnt')
    expect(formatRemaining(30_000)).toBe('kurang dari 1 mnt')
    expect(formatRemaining(0)).toBe('habis')
  })
})

describe('formatAmountInput', () => {
  const before = getLocale()
  afterEach(() => setLocale(before))

  it('pemisah ribuan mengikuti bahasa', () => {
    setLocale('en')
    expect(formatAmountInput(1_250_000)).toBe('1,250,000')
    setLocale('id')
    expect(formatAmountInput(1_250_000)).toBe('1.250.000')
  })
})
