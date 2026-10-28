/**
 * Format tampilan menurut bahasa: pemisah angka, jam, tanggal, durasi, "x menit lalu". Semua layar dan backend
 * tiruan memakai fungsi ini supaya angka di kartu, peta, dan penjelasan AI selalu seragam. Zona waktu tetap WIB.
 * `locale` bawaan = bahasa aktif; backend tiruan mengoper bahasa permintaan secara eksplisit.
 */
import { DEFAULT_LOCALE, INTL_TAG, getLocale, type Locale } from './locale'
import { registerFormatter, translate } from './translate'
import type { Params } from './translate'

const TZ = 'Asia/Jakarta'

const cache = new Map<string, Intl.NumberFormat | Intl.DateTimeFormat | Intl.ListFormat>()
function memo<T extends Intl.NumberFormat | Intl.DateTimeFormat | Intl.ListFormat>(key: string, make: () => T): T {
  let hit = cache.get(key) as T | undefined
  if (!hit) cache.set(key, (hit = make()))
  return hit
}

const num = (locale: Locale, opts: Intl.NumberFormatOptions = {}) =>
  memo(`n:${locale}:${JSON.stringify(opts)}`, () => new Intl.NumberFormat(INTL_TAG[locale], opts))
const date = (locale: Locale, opts: Intl.DateTimeFormatOptions) =>
  memo(`d:${locale}:${JSON.stringify(opts)}`, () => new Intl.DateTimeFormat(INTL_TAG[locale], { timeZone: TZ, ...opts }))

const t = (locale: Locale, key: string, params?: Params) => translate(locale, key, params)

/** "Rp 46.500" (id) / "Rp 46,500" (en). */
export function formatRupiah(amount: number, locale: Locale = getLocale()): string {
  return `Rp ${num(locale, { maximumFractionDigits: 0 }).format(Math.round(amount))}`
}

/** Tanpa awalan "Rp", untuk kolom tabel yang sudah berjudul "Biaya (Rp)". */
export function formatNumber(n: number, locale: Locale = getLocale()): string {
  return num(locale, { maximumFractionDigits: 0 }).format(Math.round(n))
}

/** Angka dengan tepat `digits` desimal, pemisah mengikuti bahasa. */
export function formatDecimal(n: number, digits = 1, locale: Locale = getLocale()): string {
  return num(locale, { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(n)
}

export function formatKm(km: number, locale: Locale = getLocale()): string {
  return `${formatDecimal(km < 10 ? km : Math.round(km * 10) / 10, 1, locale)} km`
}

export function formatKg(kg: number, locale: Locale = getLocale()): string {
  if (kg >= 10) return `${num(locale, { maximumFractionDigits: 0 }).format(kg)} kg`
  // 2,0 kg -> 2 kg
  return `${num(locale, { minimumFractionDigits: 0, maximumFractionDigits: 1 }).format(kg)} kg`
}

/** Hingga 2 desimal tanpa nol di belakang: 0,12 / 3,2 / 190. */
export function formatFloat(n: number, locale: Locale = getLocale()): string {
  return num(locale, { maximumFractionDigits: 2 }).format(n)
}

export function formatPercent(pct: number, locale: Locale = getLocale()): string {
  return `${num(locale, { maximumFractionDigits: 0 }).format(pct)}%`
}

/** 52 min, 1 h 05 min, 2 h (id: 52 mnt, 1 j 05 mnt, 2 j). */
export function formatDuration(min: number, locale: Locale = getLocale()): string {
  const total = Math.max(0, Math.round(min))
  if (total < 60) return t(locale, 'fmt.duration.min', { n: total })
  const h = Math.floor(total / 60)
  const m = total % 60
  return m === 0 ? t(locale, 'fmt.duration.hour', { h }) : t(locale, 'fmt.duration.hourMin', { h, m: String(m).padStart(2, '0') })
}

type When = string | number | Date

/** 14:20 (en) / 14.20 (id), zona WIB. */
export function formatTime(when: When, locale: Locale = getLocale()): string {
  const text = date(locale, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).format(new Date(when))
  return locale === 'id' ? text.replace(':', '.') : text
}

export function formatDate(when: When, locale: Locale = getLocale()): string {
  return date(locale, { day: 'numeric', month: 'short' }).format(new Date(when))
}

export function formatDateLong(when: When, locale: Locale = getLocale()): string {
  return date(locale, { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' }).format(new Date(when))
}

export function formatDateTime(when: When, locale: Locale = getLocale()): string {
  return `${formatDate(when, locale)}, ${formatTime(when, locale)}`
}

const dayKey = (d: Date) => new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone: TZ }).format(d)

/** "Today, 14:20", "Tomorrow, 09:00", atau "21 Oct, 09:00". */
export function formatRelativeDay(when: When, now: Date = new Date(), locale: Locale = getLocale()): string {
  const d = new Date(when)
  const diff = Math.round((new Date(dayKey(d)).getTime() - new Date(dayKey(now)).getTime()) / 86_400_000)
  const time = formatTime(d, locale)
  if (diff === 0) return t(locale, 'fmt.day.today', { time })
  if (diff === 1) return t(locale, 'fmt.day.tomorrow', { time })
  if (diff === -1) return t(locale, 'fmt.day.yesterday', { time })
  return `${formatDate(d, locale)}, ${time}`
}

/** "2 min ago", "3 h ago", "yesterday". */
export function formatAgo(when: When, now: Date = new Date(), locale: Locale = getLocale()): string {
  const sec = Math.round((now.getTime() - new Date(when).getTime()) / 1000)
  if (sec < 10) return t(locale, 'fmt.ago.now')
  if (sec < 60) return t(locale, 'fmt.ago.seconds', { n: sec })
  const min = Math.round(sec / 60)
  if (min < 60) return t(locale, 'fmt.ago.minutes', { n: min })
  const h = Math.round(min / 60)
  if (h < 24) return t(locale, 'fmt.ago.hours', { n: h })
  const d = Math.round(h / 24)
  return d === 1 ? t(locale, 'fmt.ago.yesterday') : t(locale, 'fmt.ago.days', { count: d })
}

/** "A, B and C" (en) / "A, B, dan C" (id). */
export function formatList(items: readonly string[], locale: Locale = getLocale()): string {
  return memo(`l:${locale}`, () => new Intl.ListFormat(INTL_TAG[locale], { style: 'long', type: 'conjunction' })).format(items)
}

export function formatPhone(raw: string): string {
  const digits = raw.replace(/\D/g, '')
  const local = digits.startsWith('62') ? `0${digits.slice(2)}` : digits
  if (local.length < 9) return local
  return `${local.slice(0, 4)}-${local.slice(4, 8)}-${local.slice(8)}`
}

/**
 * Teks buatan model yang disimpan dalam dua bahasa sekaligus: JSON `{"en": "...", "id": "..."}`. Backend membuatnya
 * tanpa tahu bahasa layar pembacanya (analisis foto bukti berjalan di latar belakang, bukan dalam permintaan pengguna),
 * dan layar memilih bahasanya sendiri. Bahasa yang tidak ada jatuh ke Inggris, lalu ke teks pertama yang ada; nilai yang
 * bukan JSON seperti itu ditampilkan apa adanya.
 */
export function formatLocalized(raw: string, locale: Locale = getLocale()): string {
  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    return raw
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return raw
  const texts = parsed as Record<string, unknown>
  const text = (v: unknown) => (typeof v === 'string' && v.trim() !== '' ? v : undefined)
  return text(texts[locale]) ?? text(texts[DEFAULT_LOCALE]) ?? Object.values(texts).map(text).find((v) => v !== undefined) ?? raw
}

/* ---------- pemformat bernama untuk `{nilai|nama}` di dalam teks katalog ---------- */

registerFormatter('rupiah', (v, l) => formatRupiah(Number(v), l))
registerFormatter('number', (v, l) => formatNumber(Number(v), l))
registerFormatter('decimal', (v, l) => formatDecimal(Number(v), 1, l))
registerFormatter('decimal2', (v, l) => formatDecimal(Number(v), 2, l))
registerFormatter('float', (v, l) => formatFloat(Number(v), l))
registerFormatter('km', (v, l) => formatKm(Number(v), l))
registerFormatter('kg', (v, l) => formatKg(Number(v), l))
registerFormatter('pct', (v, l) => formatPercent(Number(v), l))
registerFormatter('duration', (v, l) => formatDuration(Number(v), l))
registerFormatter('time', (v, l) => formatTime(v, l))
registerFormatter('date', (v, l) => formatDate(v, l))
registerFormatter('dateLong', (v, l) => formatDateLong(v, l))
registerFormatter('datetime', (v, l) => formatDateTime(v, l))
registerFormatter('day', (v, l) => formatRelativeDay(v, new Date(), l))
registerFormatter('ago', (v, l) => formatAgo(v, new Date(), l))
registerFormatter('phone', (v) => formatPhone(String(v)))
registerFormatter('localized', (v, l) => formatLocalized(String(v), l))
