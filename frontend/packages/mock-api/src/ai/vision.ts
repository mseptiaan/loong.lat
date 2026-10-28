import { ITEM_CATALOG, catalogName, type CatalogItem, type DetectedItem, type ImageInput } from '@loong/domain'
import type { Locale } from '@loong/i18n'

export const MAX_IMAGE_BYTES = 8 * 1024 * 1024

/** FNV-1a: hasil tiruan harus sama untuk berkas yang sama (demo bisa diulang). */
export function hash32(s: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

/** Kata di nama berkas yang menentukan barang utama (mis. "foto-laptop.jpg"). */
const NAME_HINTS: [RegExp, string][] = [
  [/laptop|notebook|macbook/i, 'laptop'],
  [/\bkue\b|cake|\btart\b/i, 'kue'],
  [/galon|gallon|\bjug\b/i, 'galon'],
  [/dispenser/i, 'dispenser'],
  [/kardus|\bdus\b|\bbox\b|boks/i, 'kardus-sedang'],
  [/dokumen|surat|berkas|\bdoc|letter|folder/i, 'dokumen'],
  [/sepatu|shoe/i, 'sepatu'],
  [/kursi|chair/i, 'kursi'],
  [/\brak\b|shelf/i, 'rak'],
  [/kulkas|fridge/i, 'kulkas-mini'],
  [/makan|food|nasi/i, 'makanan'],
  [/baju|pakaian|paket|parcel|package|cloth/i, 'paket-kecil'],
]

/** Barang sehari-hari yang masuk akal muncul bersama dalam satu foto. */
const EVERYDAY = ['dokumen', 'paket-kecil', 'sepatu', 'kue', 'laptop', 'makanan', 'kardus-sedang', 'kardus-besar']

const byKey = (key: string): CatalogItem => ITEM_CATALOG.find((c) => c.key === key)!

const round2 = (n: number) => Math.round(n * 100) / 100

function toDetected(c: CatalogItem, qty: number, confidence: number, locale: Locale): DetectedItem {
  return {
    key: c.key,
    name: catalogName(c.key, locale),
    qty,
    weightKg: c.weightKg,
    lengthCm: c.lengthCm,
    widthCm: c.widthCm,
    heightCm: c.heightCm,
    fragile: c.fragile,
    confidence: round2(confidence),
  }
}

/**
 * Tebakan "AI baca gambar" yang deterministik per berkas. Barang utama berkeyakinan tinggi; barang tambahan sengaja
 * lebih rendah sehingga alur "periksa yang keyakinannya rendah" terlihat di demo. Model sungguhan (Muse Spark) mengganti ini
 * dengan keluaran terstruktur berformat sama.
 */
export function detectItems(input: ImageInput, locale: Locale): DetectedItem[] {
  const h = hash32(`${input.name}|${input.size}|${input.type}`)
  const hinted = NAME_HINTS.find(([re]) => re.test(input.name))?.[1]
  const primaryKey = hinted ?? EVERYDAY[h % EVERYDAY.length]!
  const count = hinted ? 1 + ((h >>> 3) % 2) : 1 + ((h >>> 3) % 3) // 1..3 jenis; berkas bernama jelas lebih sederhana

  const keys = [primaryKey]
  for (let i = 1; keys.length < count && i < 16; i++) {
    const next = EVERYDAY[(h >>> (4 + i)) % EVERYDAY.length]!
    if (!keys.includes(next)) keys.push(next)
  }

  return keys.map((key, i) => {
    const c = byKey(key)
    const qty = c.fragile ? 1 : 1 + ((h >>> (9 + i)) % 3)
    const spread = ((h >>> (11 + i)) % 10) / 100 // 0..0,09
    const confidence = i === 0 ? (hinted ? 0.95 + spread / 3 : 0.88 + spread) : i === 1 ? 0.62 + spread * 1.6 : 0.5 + spread * 1.8
    return toDetected(c, qty, Math.min(0.99, confidence), locale)
  })
}
