import { afterEach, describe, expect, it } from 'vitest'
import { getLocale, renderMsg, setLocale } from './index'
import { formatLocalized } from './format'

const startLocale = getLocale()
afterEach(() => setLocale(startLocale))

const both = JSON.stringify({ en: 'A parcel is visible at the door.', id: 'Paket terlihat di depan pintu.' })

describe('formatLocalized: teks buatan model dalam dua bahasa', () => {
  it('memilih bahasa yang diminta', () => {
    expect(formatLocalized(both, 'en')).toBe('A parcel is visible at the door.')
    expect(formatLocalized(both, 'id')).toBe('Paket terlihat di depan pintu.')
  })

  it('bahasa yang tidak ada atau kosong jatuh ke Inggris, lalu ke teks pertama yang ada', () => {
    expect(formatLocalized(JSON.stringify({ en: 'Only English.' }), 'id')).toBe('Only English.')
    expect(formatLocalized(JSON.stringify({ en: '   ', id: 'Hanya Indonesia.' }), 'en')).toBe('Hanya Indonesia.')
    expect(formatLocalized(JSON.stringify({ id: 'Hanya Indonesia.' }), 'en')).toBe('Hanya Indonesia.')
  })

  it('nilai yang bukan JSON seperti itu ditampilkan apa adanya', () => {
    expect(formatLocalized('plain text', 'id')).toBe('plain text')
    expect(formatLocalized('"quoted"', 'en')).toBe('"quoted"')
    expect(formatLocalized('["a","b"]', 'en')).toBe('["a","b"]')
    expect(formatLocalized('{"en": 3}', 'en')).toBe('{"en": 3}')
    expect(formatLocalized('', 'en')).toBe('')
  })
})

describe('srv.ai.text dan srv.ai.proof.noNote di katalog', () => {
  const text = { key: 'srv.ai.text', params: { text: both } }

  it('dirender dalam bahasa layar', () => {
    expect(renderMsg(text, 'en')).toBe('A parcel is visible at the door.')
    expect(renderMsg(text, 'id')).toBe('Paket terlihat di depan pintu.')
  })

  it('ikut bahasa aktif ketika dipakai sebagai parameter bersarang', () => {
    setLocale('id')
    expect(renderMsg({ key: 'srv.ai.text', params: { text: both } })).toBe('Paket terlihat di depan pintu.')
  })

  it('catatan kosong punya teks di dua bahasa', () => {
    expect(renderMsg({ key: 'srv.ai.proof.noNote' }, 'en')).toBe('No further detail.')
    expect(renderMsg({ key: 'srv.ai.proof.noNote' }, 'id')).toBe('Tidak ada rincian lebih lanjut.')
  })
})
