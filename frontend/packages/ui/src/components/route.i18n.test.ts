import { DEFAULT_LOCALE, setLocale, t } from '@loong/i18n'
import { afterEach, describe, expect, it } from 'vitest'

afterEach(() => setLocale(DEFAULT_LOCALE))

describe('teks kit UI (kunci ui.*)', () => {
  it('Indonesia memakai teks lama', () => {
    setLocale('id')
    expect(t('ui.route.saving', { cost: 5000, pct: 12 })).toBe('Hemat Rp 5.000 (12%) dibanding kirim terpisah')
    expect(t('ui.route.pricierFaster', { extra: 3000, faster: 8 })).toBe('Rp 3.000 lebih mahal dari kirim terpisah, tapi paket tiba rata-rata 8 mnt lebih cepat')
    expect(t('ui.stopList.pickupMany', { count: 2, place: 'Kemang Village' })).toBe('Ambil 2 paket · Kemang Village')
    expect(t('ui.route.stops', { count: 3 })).toBe('3 stop')
    expect(t('ui.password.show', { noun: t('ui.password.noun') })).toBe('Tampilkan kata sandi')
  })

  it('Inggris memakai bentuk tunggal dan jamak', () => {
    expect(t('ui.route.stops', { count: 1 })).toBe('1 stop')
    expect(t('ui.route.stops', { count: 3 })).toBe('3 stops')
    expect(t('ui.route.motorbikes', { count: 1 })).toBe('1 motorbike')
    expect(t('ui.route.blindVans', { count: 2 })).toBe('2 blind vans')
    expect(t('ui.stopList.dropoffMany', { count: 2, place: 'Senayan' })).toBe('Drop off 2 parcels · Senayan')
    expect(t('ui.route.saving', { cost: 5000, pct: 12 })).toBe('Save Rp 5,000 (12%) vs. sending separately')
    expect(t('ui.password.show', { noun: t('ui.password.noun') })).toBe('Show password')
  })

  it('opsi: label bersarang dirender dalam bahasa yang sama', () => {
    const label = { key: 'ui.route.recommended' }
    expect(t('ui.route.optionLabel', { label, cost: 46500 })).toBe('Recommended, Rp 46,500')
    setLocale('id')
    expect(t('ui.route.optionLabel', { label, cost: 46500 })).toBe('Disarankan, Rp 46.500')
  })
})
