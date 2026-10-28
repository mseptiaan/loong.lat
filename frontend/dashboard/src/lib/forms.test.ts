import { getLocale, setLocale } from '@loong/i18n'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { passwordStrength } from './forms'

// @loong/ui memakai `window` saat dimuat; di sini hanya logika murni yang diuji
vi.mock('@loong/ui', () => ({ scrollBehavior: () => 'auto' }))

describe('passwordStrength', () => {
  const before = getLocale()
  afterEach(() => setLocale(before))

  const samples = [
    ['', 0, '', ''],
    ['abc', 0, 'Too short', 'Terlalu pendek'],
    ['abcdefgh', 1, 'Fair', 'Cukup'],
    ['Abcdefg1', 2, 'Good', 'Baik'],
    ['Abcdefghij12!', 3, 'Strong', 'Kuat'],
  ] as const

  it.each(samples)('%j -> level %i', (pw, level, en, id) => {
    setLocale('en')
    expect(passwordStrength(pw)).toEqual({ label: en, level })
    setLocale('id')
    expect(passwordStrength(pw)).toEqual({ label: id, level })
  })
})
