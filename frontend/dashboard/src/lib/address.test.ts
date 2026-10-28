import { getLocale, setLocale } from '@loong/i18n'
import { afterEach, describe, expect, it } from 'vitest'
import { ADDRESS_PRESETS, addressKind, addressLabel } from './address'

describe('addressLabel', () => {
  const before = getLocale()
  afterEach(() => setLocale(before))

  it('nilai tersimpan bawaan tampil sesuai bahasa', () => {
    setLocale('en')
    expect(ADDRESS_PRESETS.map(addressLabel)).toEqual(['Home', 'Shop', 'Office', 'Other'])
    setLocale('id')
    expect(ADDRESS_PRESETS.map(addressLabel)).toEqual(['Rumah', 'Toko', 'Kantor', 'Lainnya'])
  })

  it('mengenali huruf besar/kecil dan nilai berbahasa Inggris', () => {
    setLocale('id')
    expect(addressLabel('rumah')).toBe('Rumah')
    expect(addressLabel(' HOME ')).toBe('Rumah')
    setLocale('en')
    expect(addressLabel('RUMAH')).toBe('Home')
  })

  it('nama ketikan pengguna apa adanya', () => {
    setLocale('en')
    expect(addressLabel('Rumah Ibu')).toBe('Rumah Ibu')
    expect(addressLabel('Gudang Cikarang')).toBe('Gudang Cikarang')
  })
})

describe('addressKind', () => {
  it('mengenali rumah dan toko dalam dua bahasa', () => {
    expect(addressKind('Rumah')).toBe('home')
    expect(addressKind('My home')).toBe('home')
    expect(addressKind('Toko')).toBe('shop')
    expect(addressKind('Shop')).toBe('shop')
    expect(addressKind('Kantor')).toBe('other')
  })
})
