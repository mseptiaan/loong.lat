import type { IntentChip, IntentParse, Preset } from '@loong/domain'
import type { Locale } from '@loong/i18n'
import { tAi } from '../i18n'
import { parseNumber } from './speech'

const WIB_MS = 7 * 3_600_000

/** Kata prioritas, Indonesia dan Inggris (pengguna bisa mengetik dalam salah satunya, apa pun bahasa layarnya). */
const PRESET_WORDS: [Preset, RegExp][] = [
  [
    'termurah',
    /\b(hemat|murah|termurah|irit|ekonomis|biaya (?:kecil|rendah)|paling murah|cheap(?:est|ly)?|budget|economical|inexpensive|lowest cost|low cost|least expensive|save money|affordable)\b/,
  ],
  [
    'tercepat',
    /\b(as (?:fast|quickly|soon) as possible|cepat|tercepat|buru-buru|segera|urgent|kilat|secepatnya|mendesak|fast(?:est)?|quick(?:ly|est)?|asap|rush(?:ed)?|hurry|immediately|soonest|express)\b/,
  ],
  ['terpendek', /\b(terpendek|jarak (?:dekat|pendek)|bahan bakar|shortest|short distance|least distance|fuel)\b/],
  ['seimbang', /\b(seimbang|biasa saja|standar|balanced|standard|no preference)\b/],
]

const HOUR_WORD = String.raw`(\d{1,2}|satu|dua|tiga|empat|lima|enam|tujuh|delapan|sembilan|sepuluh|sebelas|dua belas)`
const HOUR_WORD_EN = String.raw`(\d{1,2}|one|two|three|four|five|six|seven|eight|nine|ten|eleven|twelve)`

/** "sebelum jam 5 sore", "sampai jam 17.30", "tiba sebelum 17.00". */
const DEADLINE_ID = new RegExp(
  String.raw`\b(?:sebelum|paling lambat|selambat-lambatnya|maksimal|maks|tiba|sampai|harus sampai|batas)\b[^.]*?\b(?:(?:jam|pukul|pkl)\s*${HOUR_WORD}(?:[.:](\d{2}))?|(\d{1,2})[.:](\d{2})(?!\s*(?:kg|kilo|km)))\s*(pagi|siang|sore|malam)?`, // i18n-ignore: kata kunci pengenal Indonesia dan Inggris
)

/** "before 5 pm", "by 17:00", "no later than 4:30 pm", "before around 5". */
const DEADLINE_EN_BY = new RegExp(
  String.raw`\b(?:before|by|until|till|no later than|not later than|at the latest|deadline|due)\b(?:\s+(?:around|about|roughly|approximately|exactly|at|the|today|tonight))*\s+${HOUR_WORD_EN}(?:[.:](\d{2}))?\s*(a\.?m\.?|p\.?m\.?|o'?clock|in the (?:morning|afternoon|evening)|at night)?(?![\w:])(?!\s*(?:hours?|hrs?|minutes?|mins?|days?|kg|kilos?|km|boxes|items?|parcels?|pieces?))`,
)

/** "arrive at 5 pm", "reach there around 17:00". */
const DEADLINE_EN_AT = new RegExp(
  String.raw`\b(?:arrive|arrives|arriving|reach|reaches|delivered)\b[^.]*?\b(?:at|around)\s+${HOUR_WORD_EN}(?:[.:](\d{2}))?\s*(a\.?m\.?|p\.?m\.?|o'?clock|in the (?:morning|afternoon|evening)|at night)?(?![\w:])`,
)

const TWELVE: Record<string, number> = { 'dua belas': 12 }

interface DeadlineHit {
  text: string
  hour: string
  minute: number
  period?: string
}

/** Menyatukan tiga pola batas waktu; keterangan waktu Inggris dipetakan ke padanan Indonesia (pagi/sore). */
function findDeadline(lower: string): DeadlineHit | undefined {
  const id = DEADLINE_ID.exec(lower)
  if (id) {
    // grup: 1 jam (kata/angka) + 2 menit, atau 3 jam + 4 menit (bentuk "17.30" tanpa "jam"); 5 keterangan
    const hour = id[1] ?? id[3]
    const minute = id[2] ?? id[4]
    if (hour) return { text: id[0], hour, minute: minute ? Number(minute) : 0, period: id[5] }
  }
  for (const re of [DEADLINE_EN_BY, DEADLINE_EN_AT]) {
    const m = re.exec(lower)
    if (!m) continue
    const marker = m[3]?.replace(/\./g, '')
    const period = marker === 'am' || marker === 'in the morning' ? 'pagi' : marker ? 'sore' : undefined
    return { text: m[0], hour: m[1]!, minute: m[2] ? Number(m[2]) : 0, period }
  }
  return undefined
}

/** Jam dari kalimat; jam 1-6 tanpa keterangan dianggap sore (konteks pengiriman). */
function resolveHour(word: string, period?: string): number | undefined {
  const base = TWELVE[word] ?? parseNumber(word)
  if (base === undefined || base < 0 || base > 24) return undefined
  if (base >= 13) return base
  if (period === 'sore' || period === 'malam') return base < 12 ? base + 12 : base
  if (period === 'siang') return base <= 5 ? base + 12 : base
  if (period === 'pagi') return base === 12 ? 0 : base
  return base >= 1 && base <= 6 ? base + 12 : base
}

/** Epoch ms untuk jam:menit WIB pada hari `nowMs` (atau besok bila sudah lewat). */
function wibDeadline(nowMs: number, hour: number, minute: number): number {
  const d = new Date(nowMs + WIB_MS)
  let ms = Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), d.getUTCDate(), hour, minute) - WIB_MS
  if (ms <= nowMs) ms += 86_400_000
  return ms
}

/** Kata penghubung dan pengisi yang bukan "bagian yang tidak dipahami" (Indonesia dan Inggris). */
const FILLER_WORDS = new RegExp(
  String.raw`\b(tapi|yang|dan|tolong|mohon|saya|mau|ingin|pengiriman|paket|ini|itu|ya|aja|saja|biar|supaya|agar|paling|sangat|banget|sekali|penting|juga|lebih|harus|sampai|tiba|but|and|please|i'd|i'm|i|me|my|want|would|like|need|it|this|that|the|a|an|to|be|get|just|so|also|really|very|most|more|must|should|have|arrive|arrives|before|by|delivery|parcel|package|route|routes|is|ok|okay|thanks|thank|you|at)\b`, // i18n-ignore: kata kunci pengenal Indonesia dan Inggris
  'g',
)

/**
 * Mengurai kalimat bebas ("paling hemat tapi sampai sebelum jam 5" atau "cheapest but arrive before 5 pm") menjadi chip
 * prioritas dan batas waktu yang bisa disunting pengguna. Berbasis kata kunci; model sungguhan (Muse Spark, keluaran
 * terstruktur) menggantikannya dengan hasil berformat sama. `nowMs` adalah jam virtual sistem; label chip mengikuti
 * bahasa permintaan (`locale`).
 */
export function parseIntent(text: string, nowMs: number, locale: Locale): IntentParse {
  const t = tAi(locale)
  const lower = text.toLowerCase().replace(/\s+/g, ' ').trim()
  const chips: IntentChip[] = []
  const understood: string[] = []
  let rest = lower

  // batas waktu
  const m = findDeadline(lower)
  if (m) {
    const hour = resolveHour(m.hour, m.period)
    if (hour !== undefined && m.minute < 60) {
      const deadline = new Date(wibDeadline(nowMs, hour % 24, m.minute)).toISOString()
      const deadlineMs = Date.parse(deadline)
      const sameDay = new Date(deadlineMs + WIB_MS).toISOString().slice(0, 10) === new Date(nowMs + WIB_MS).toISOString().slice(0, 10)
      chips.push({ id: 'batas', kind: 'batas_waktu', label: t(sameDay ? 'ai.intent.deadline' : 'ai.intent.deadlineOn', { time: deadline }), deadline })
      understood.push(m.text.trim())
      rest = rest.replace(m.text, ' ')
    }
  }

  // prioritas: bila ada kata yang bertentangan (hemat DAN cepat), pilih seimbang dan katakan alasannya
  const found = PRESET_WORDS.flatMap(([preset, re]) => {
    const hit = re.exec(rest)
    return hit ? [{ preset, word: hit[0], at: hit.index }] : []
  }).sort((a, b) => a.at - b.at)
  if (found.length > 0) {
    const distinct = [...new Set(found.map((f) => f.preset))]
    const preset: Preset = distinct.length === 1 ? distinct[0]! : 'seimbang'
    chips.unshift({
      id: 'prioritas',
      kind: 'prioritas',
      preset,
      label:
        distinct.length === 1
          ? t('ai.intent.priority', { preset: t(`dom.preset.${preset}`) })
          : t('ai.intent.priorityMixed', { preset: t('dom.preset.seimbang') }),
    })
    understood.push(...found.map((f) => f.word))
    for (const f of found) rest = rest.replace(f.word, ' ')
  }

  const leftover = rest
    .replace(FILLER_WORDS, ' ')
    .replace(/[.,;!?]/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()

  return { chips, understood, leftover }
}
