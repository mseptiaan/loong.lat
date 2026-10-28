import { ITEM_CATALOG, catalogName, type DetectedItem, type SpeechParse } from '@loong/domain'
import type { Locale } from '@loong/i18n'

/** Angka dalam kata, Indonesia dan Inggris (pengguna bisa berbicara dalam salah satunya, apa pun bahasa layarnya). */
const NUMBER_WORDS: Record<string, number> = {
  satu: 1,
  sebuah: 1,
  sebiji: 1,
  dua: 2,
  tiga: 3,
  empat: 4,
  lima: 5,
  enam: 6,
  tujuh: 7,
  delapan: 8,
  sembilan: 9,
  sepuluh: 10,
  sebelas: 11,
  setengah: 0.5,
  one: 1,
  two: 2,
  three: 3,
  four: 4,
  five: 5,
  six: 6,
  seven: 7,
  eight: 8,
  nine: 9,
  ten: 10,
  eleven: 11,
  twelve: 12,
  half: 0.5,
}

/** Angka dari kata ("dua", "two", "8", "2,5", "setengah"); undefined bila bukan angka. */
export function parseNumber(token: string): number | undefined {
  const t = token.trim().toLowerCase()
  if (/^\d+([.,]\d+)?$/.test(t)) return Number(t.replace(',', '.'))
  return NUMBER_WORDS[t]
}

interface Synonym {
  key: string
  phrase: string
  /** Kata umum yang sering hanya wadah ("kardus", "box", "paket"): kalah dari barang yang disebut di kalimat yang sama. */
  weak?: boolean
}

/** Sinonim lisan per barang katalog (Indonesia dan Inggris). */
const SYNONYMS: [string, string[]][] = [
  ['kardus-besar', ['kardus besar', 'dus besar', 'kotak besar', 'large cardboard box', 'big cardboard box', 'large box', 'big box', 'large carton']],
  ['kardus-sedang', ['kardus sedang', 'dus sedang', 'medium cardboard box', 'medium box', 'cardboard box']], // i18n-ignore: sinonim lisan Indonesia dan Inggris
  ['kulkas-mini', ['kulkas mini', 'kulkas', 'mini fridge', 'mini refrigerator', 'fridge', 'refrigerator']],
  ['dispenser', ['dispenser', 'water dispenser']],
  ['galon', ['galon', 'water jug', 'refill jug', 'water gallon', 'gallon', 'jug']],
  ['laptop', ['laptop', 'notebook', 'macbook']],
  ['kue', ['kue ulang tahun', 'birthday cake', 'kue', 'tart', 'cake']], // i18n-ignore: sinonim lisan Indonesia dan Inggris
  ['sepatu', ['sepatu', 'shoebox', 'shoe', 'sneaker']],
  ['makanan', ['boks makanan', 'kotak makan', 'makanan', 'nasi', 'katering', 'food box', 'lunch box', 'meal box', 'food', 'meal', 'catering']],
  ['dokumen', ['dokumen', 'berkas', 'surat', 'map', 'document', 'paperwork', 'folder', 'letter', 'envelope']],
  ['kursi', ['kursi', 'office chair', 'chair']],
  ['rak', ['rak buku', 'rak', 'bookshelf', 'bookcase', 'shelves', 'shelf', 'rack']],
  ['paket-kecil', ['paket kecil', 'pakaian', 'baju', 'bungkusan', 'small parcel', 'small package', 'clothing', 'clothes']],
]

/** Wadah dan kata umum: dipakai hanya bila tidak ada barang yang lebih spesifik di kalimat yang sama. */
const WEAK: Synonym[] = [
  { key: 'kardus-sedang', phrase: 'kardus', weak: true },
  { key: 'kardus-sedang', phrase: 'dus', weak: true },
  { key: 'kardus-sedang', phrase: 'box', weak: true },
  { key: 'kardus-sedang', phrase: 'carton', weak: true },
  { key: 'paket-kecil', phrase: 'paket', weak: true },
  { key: 'paket-kecil', phrase: 'parcel', weak: true },
  { key: 'paket-kecil', phrase: 'package', weak: true },
]

/** Frasa yang lebih panjang dicoba dulu ("kardus besar" sebelum "kardus"). */
const ALL_PHRASES: Synonym[] = [...SYNONYMS.flatMap(([key, phrases]) => phrases.map((phrase) => ({ key, phrase }))), ...WEAK].sort((a, b) => b.phrase.length - a.phrase.length)

const escapeRe = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')
/** Kata utuh, boleh berakhiran jamak Inggris ("laptops", "boxes"). */
const phraseRe = (phrase: string) => new RegExp(`\\b${escapeRe(phrase)}(?:e?s)?\\b`)

const NUM = String.raw`(\d+(?:[.,]\d+)?|se(?:buah|biji)|satu|dua|tiga|empat|lima|enam|tujuh|delapan|sembilan|sepuluh|sebelas|setengah|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve|half)`

/** Satuan hitung yang boleh mengikuti angka: "dua buah", "3 biji", "two pieces". */
const COUNT_UNIT = String.raw`(?:buah|biji|pcs|unit|bungkus|kotak|dus|paket|pieces?|units?|boxes|box|packs?|parcels?)`

/** Kata yang boleh berada di antara angka dan barang: "dua kotak sepatu", "two boxes of shoes", "two fragile laptops". */
const FILLER = /^(buah|biji|pcs|unit|units|kotak|dus|boks|bungkus|pasang|piece|pieces|box|boxes|pack|packs|pair|pairs|of|fragile|breakable|heavy|big|small|large|medium|new)$/

function findQty(segment: string, matchStart: number, matchEnd: number): number {
  const before = segment.slice(0, matchStart).trim().split(/\s+/).slice(-3)
  // "dua kardus", "2 buah kardus", "two boxes of shoes", "a laptop"
  for (let i = before.length - 1; i >= 0; i--) {
    const word = before[i]!
    const n = parseNumber(word)
    if (n !== undefined && n >= 1) return Math.round(n)
    if (word === 'a' || word === 'an') return 1
    if (!FILLER.test(word)) break
  }
  // "kardus sedang dua buah"
  const after = new RegExp(`^\\s*${NUM}\\s+${COUNT_UNIT}`).exec(segment.slice(matchEnd))
  if (after) {
    const n = parseNumber(after[1]!)
    if (n !== undefined && n >= 1) return Math.round(n)
  }
  return 1
}

const KILO = String.raw`(?:kilogram|kilo|kg)s?`

/** "8 kilo", "2,5 kg", "satu setengah kilo", "one and a half kilos" -> kg. */
function findWeight(segment: string): number | undefined {
  const half = new RegExp(String.raw`(satu|dua|tiga|empat|lima|one|two|three|four|five|\d+)\s+(?:setengah|and a half|half)\s*${KILO}`).exec(segment)
  if (half) return (parseNumber(half[1]!) ?? 0) + 0.5
  const m = new RegExp(`${NUM}\\s*${KILO}`).exec(segment)
  return m ? parseNumber(m[1]!) : undefined
}

const FRAGILE = /mudah pecah|pecah belah|fragile|breakable|hati-hati|handle with care|careful/

/** Pemisah antar barang: koma, "dan", "lalu", ... dan padanan Inggrisnya. "one and a half kilos" dilindungi di `parseSpeech`. */
const SPLIT = /\s*(?:,|;|&|\bdan\b|\blalu\b|\bserta\b|\bkemudian\b|\bditambah\b|\bterus\b|\band\b|\bthen\b|\bplus\b|\balso\b|\bas well as\b)\s*/

/**
 * Mengurai transkrip lisan ("dua kardus sedang, satu laptop mudah pecah, total sekitar delapan kilo" atau "two medium
 * boxes, a fragile laptop, about eight kilos in total") menjadi barang katalog. Berbasis aturan agar hasilnya selalu
 * bisa dihitung engine; model sungguhan (Muse Voice + Spark) menggantikannya dengan keluaran terstruktur berformat sama.
 * Bagian yang tidak dipahami dikembalikan apa adanya. Nama barang mengikuti bahasa permintaan (`locale`).
 */
export function parseSpeech(transcript: string, locale: Locale): SpeechParse {
  // "one and a half kilos" jangan terpecah oleh "and"
  const text = transcript
    .toLowerCase()
    .replace(/\s+/g, ' ')
    .trim()
    .replace(/\b(\w+) and a half\b/g, '$1 half')
  const items: DetectedItem[] = []
  const matches: SpeechParse['matches'] = []
  const leftover: string[] = []
  let totalWeightKg: number | undefined

  for (const raw of text.split(SPLIT).filter(Boolean)) {
    const segment = raw.trim()
    // kalimat berat total ("total sekitar delapan kilo") bukan barang
    const total = /\btotal\b/.test(segment) ? findWeight(segment) : undefined
    if (total !== undefined) {
      totalWeightKg = total
      continue
    }
    // frasa terpanjang dulu; kata wadah umum ("box", "kardus") hanya bila tak ada barang lain yang disebut
    const found = ALL_PHRASES.flatMap((p) => {
      const m = phraseRe(p.phrase).exec(segment)
      return m ? [{ ...p, at: m.index, end: m.index + m[0].length }] : []
    })
    const hit = found.find((p) => !p.weak) ?? found[0]
    if (!hit) {
      leftover.push(segment)
      continue
    }
    const qty = findQty(segment, hit.at, hit.end)
    const c = ITEM_CATALOG.find((x) => x.key === hit.key)!
    const weight = findWeight(segment)
    const generic = hit.phrase === 'paket' || hit.phrase === 'dus' || hit.phrase === 'map' || hit.phrase === 'parcel' || hit.phrase === 'package' || hit.phrase === 'box'
    const existing = items.find((i) => i.key === hit.key)
    if (existing) {
      existing.qty += qty
      matches.push({ text: segment, itemIndex: items.indexOf(existing) })
      continue
    }
    items.push({
      key: c.key,
      name: catalogName(c.key, locale),
      qty,
      weightKg: weight && qty === 1 ? weight : c.weightKg,
      lengthCm: c.lengthCm,
      widthCm: c.widthCm,
      heightCm: c.heightCm,
      fragile: c.fragile || FRAGILE.test(segment),
      confidence: generic ? 0.72 : 0.9,
    })
    matches.push({ text: segment, itemIndex: items.length - 1 })
  }

  // berat total hanya mengubah berat satuan bila ada tepat satu jenis barang
  if (totalWeightKg !== undefined && items.length === 1) {
    items[0]!.weightKg = Math.round((totalWeightKg / items[0]!.qty) * 100) / 100
  }

  return { items, matches, leftover: leftover.join(', ') }
}
