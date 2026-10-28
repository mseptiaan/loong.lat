import { describe, expect, it } from 'vitest'
import { BRAND, LOCKUP, MARK_SIZE, MARK_STROKES, WORDMARK, lockupSvg, markSvg } from './brand'

// Berkas di luar paket ini dibaca sebagai teks mentah (pola yang sama dengan srv-keys.test.ts).
const files = import.meta.glob<string>(
  [
    '../../../brand/*.svg',
    '../../../{dashboard,backoffice,mobile}/public/{favicon,icon}.svg',
    '../../../mobile/public/manifest.webmanifest',
    '../../../{dashboard,backoffice,mobile}/index.html',
  ],
  { query: '?raw', import: 'default', eager: true },
)
const read = (file: string): string => {
  const text = files[`../../../${file}`]
  if (text === undefined) throw new Error(`berkas tidak ditemukan: ${file}`)
  return text
}

describe('identitas visual loong.lat', () => {
  it('berkas SVG di repo sama dengan keluaran brand.ts (jalankan `npm run build:brand` bila gagal)', () => {
    expect(read('brand/loong-lat.svg')).toBe(lockupSvg())
    expect(read('brand/loong-lat-mark.svg')).toBe(markSvg())
    for (const app of ['dashboard', 'backoffice', 'mobile']) expect(read(`${app}/public/favicon.svg`)).toBe(markSvg())
    expect(read('mobile/public/icon.svg')).toBe(markSvg({ bleed: true }))
  })

  it('ikon PWA tanpa sudut membulat, favicon dan logo dengan sudut membulat', () => {
    expect(markSvg({ bleed: true })).not.toContain(' rx="18"')
    expect(markSvg()).toContain(' rx="18"')
  })

  it('wordmark muat di dalam kanvas lockup dan tidak mengandung font', () => {
    expect(LOCKUP.width).toBeGreaterThan(MARK_SIZE + WORDMARK.width)
    expect(lockupSvg()).not.toMatch(/<text|font-family/)
  })

  it('tanda memakai warna merek, dan jalinan cincin punya dua celah berwarna kotak', () => {
    const svg = markSvg()
    expect(svg).toContain(BRAND.accent)
    expect(svg).toContain(BRAND.onAccent)
    expect(MARK_STROKES.filter((s) => s.tone === 'tile')).toHaveLength(2)
  })

  it('ikon PWA dan favicon dirujuk oleh aplikasi', () => {
    expect(read('mobile/public/manifest.webmanifest')).toContain('"/icon.svg"')
    for (const app of ['dashboard', 'backoffice', 'mobile']) expect(read(`${app}/index.html`)).toContain('href="/favicon.svg"')
  })
})
