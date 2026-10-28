import { setLocale } from '@loong/i18n'
import { afterEach, describe, expect, it } from 'vitest'
import { NAV_GROUPS, NAV_ITEMS, sectionLabel } from './nav'
import { t } from '../i18n'

afterEach(() => setLocale('en'))

describe('navigasi backoffice', () => {
  it('setiap kunci label (grup dan item) punya teks di kedua bahasa', () => {
    const keys = [...NAV_GROUPS.map((g) => g.labelKey), ...NAV_ITEMS.map((n) => n.labelKey)]
    for (const locale of ['en', 'id'] as const) {
      setLocale(locale)
      for (const key of keys) expect(t(key), `${locale}: ${key}`).not.toBe(key)
    }
  })

  it('label bagian aktif mengikuti bahasa dan jalur detail mengikuti induknya', () => {
    expect(sectionLabel('/')).toBe('Command center')
    expect(sectionLabel('/plan/abc')).toBe('Plans')
    expect(sectionLabel('/balances')).toBe('Balances & top-ups')
    setLocale('id')
    expect(sectionLabel('/')).toBe('Pusat komando')
    expect(sectionLabel('/plan/abc')).toBe('Plan')
    expect(sectionLabel('/tidak-ada')).toBe('Backoffice')
  })
})
