import { PROOF_CHECK_LABEL, PROOF_RESULT_LABEL, type ProofAnalysis } from '@loong/domain'
import { LOCALES, renderMsg } from '@loong/i18n'
import { describe, expect, it } from 'vitest'
import { SAMPLE_PROOF_PHOTO, SAMPLE_PROOF_PHOTO_NAME } from '../seed/proofPhoto'
import { MAX_PROOF_PHOTO_CHARS, analyzeProofPhoto, isProofPhoto } from './proof'

const NOW = Date.parse('2026-10-20T07:00:00Z')
const analyze = (name: string, size = 80_000) => analyzeProofPhoto({ name, size }, NOW)
const summaryOf = (a: ProofAnalysis) => a.summary.key

describe('ringkasan foto bukti antar (tiruan)', () => {
  it('deterministik: berkas yang sama menghasilkan ringkasan yang sama', () => {
    expect(analyze('IMG_2041.jpg')).toEqual(analyze('IMG_2041.jpg'))
  })

  it('foto contoh penerima memberi ringkasan orang yang duduk dengan paket yang belum pasti', () => {
    const a = analyze(SAMPLE_PROOF_PHOTO_NAME, SAMPLE_PROOF_PHOTO.length)
    expect(summaryOf(a)).toBe('srv.proof.summary.seated')
    expect(a.checks.map((c) => [c.key, c.result])).toEqual([
      ['recipient', 'ok'],
      ['parcel', 'unclear'],
      ['quality', 'ok'],
    ])
    expect(a.confidence).toBeGreaterThan(0.5)
    expect(a.confidence).toBeLessThan(0.9)
  })

  it('kata di nama berkas menentukan skenario', () => {
    expect(summaryOf(analyze('foto-pintu-depan.jpg'))).toBe('srv.proof.summary.atDoor')
    expect(summaryOf(analyze('paket-diterima.jpg'))).toBe('srv.proof.summary.holding')
    expect(analyze('foto-pintu-depan.jpg').checks.find((c) => c.key === 'recipient')!.result).toBe('missing')
  })

  it('tanpa petunjuk memakai skenario umum (bukan "duduk") dan selalu punya tiga pemeriksaan berurutan', () => {
    for (let i = 0; i < 40; i++) {
      const a = analyze(`IMG_${3000 + i}.jpg`, 40_000 + i * 733)
      expect(['srv.proof.summary.holding', 'srv.proof.summary.atDoor']).toContain(summaryOf(a))
      expect(a.checks.map((c) => c.key)).toEqual(['recipient', 'parcel', 'quality'])
      expect(a.confidence).toBeGreaterThan(0)
      expect(a.confidence).toBeLessThanOrEqual(1)
      expect(a.analyzedAt).toBe(new Date(NOW).toISOString())
    }
  })

  it.each(LOCALES)('semua teks ada di katalog %s dan tidak jatuh ke kunci', (locale) => {
    const names = [SAMPLE_PROOF_PHOTO_NAME, 'foto-pintu.jpg', 'paket.jpg']
    for (const name of names) {
      const a = analyze(name)
      for (const m of [a.summary, ...a.checks.map((c) => c.note)]) {
        const text = renderMsg(m, locale)
        expect(text).not.toMatch(/^srv\./)
        expect(text.length).toBeGreaterThan(10)
      }
    }
  })

  it('label pemeriksaan dan hasil tersedia di domain', () => {
    expect(Object.values(PROOF_CHECK_LABEL).every((l) => !l.startsWith('dom.'))).toBe(true)
    expect(Object.values(PROOF_RESULT_LABEL).every((l) => !l.startsWith('dom.'))).toBe(true)
  })
})

describe('isProofPhoto', () => {
  it('menerima data URL gambar yang masuk akal, termasuk foto contoh', () => {
    expect(isProofPhoto(SAMPLE_PROOF_PHOTO)).toBe(true)
    expect(isProofPhoto('data:image/png;base64,iVBORw0KGgo=')).toBe(true)
  })

  it('menolak bukan gambar, bukan base64, kosong, dan terlalu besar', () => {
    expect(isProofPhoto(undefined)).toBe(false)
    expect(isProofPhoto('')).toBe(false)
    expect(isProofPhoto('foto.jpg')).toBe(false)
    expect(isProofPhoto('data:text/html;base64,PGh0bWw+')).toBe(false)
    expect(isProofPhoto('data:image/svg+xml;base64,PHN2Zz4=')).toBe(false)
    expect(isProofPhoto('data:image/jpeg;base64,')).toBe(false)
    expect(isProofPhoto(`data:image/jpeg;base64,${'A'.repeat(MAX_PROOF_PHOTO_CHARS)}`)).toBe(false)
  })
})
