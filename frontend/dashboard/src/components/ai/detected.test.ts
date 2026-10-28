import { afterEach, describe, expect, it } from 'vitest'
import { catalogName, type DetectedItem } from '@loong/domain'
import { getLocale, setLocale } from '@loong/i18n'
import { toItems, toRows } from './rows'

const detected = (name: string, confidence = 0.9): DetectedItem => ({ key: 'laptop', name, qty: 1, weightKg: 2.8, lengthCm: 42, widthCm: 30, heightCm: 10, fragile: true, confidence })

describe('toItems', () => {
  const before = getLocale()
  afterEach(() => setLocale(before))

  it('nama bawaan katalog yang tidak diubah tetap mengikuti bahasa (catalogKey terisi)', () => {
    setLocale('en')
    const [item] = toItems(toRows([detected(catalogName('laptop'))]), 'foto')
    expect(item?.catalogKey).toBe('laptop')
  })

  it('nama yang diubah pengguna menjadi teks biasa tanpa catalogKey', () => {
    const [item] = toItems(toRows([detected('Laptop kantor')]), 'suara')
    expect(item?.catalogKey).toBeUndefined()
    expect(item?.name).toBe('Laptop kantor')
  })

  it('nama kosong diganti kata bawaan dan baris yang tidak dicentang dibuang', () => {
    setLocale('en')
    const rows = toRows([detected('  '), detected('Kamera', 0.3)])
    expect(toItems(rows, 'foto').map((i) => i.name)).toEqual(['Item'])
  })
})
