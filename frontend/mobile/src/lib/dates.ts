import { formatRelativeDay } from '@loong/domain'
import { INTL_TAG, getLocale, type Locale } from '@loong/i18n'

const TZ = 'Asia/Jakarta'

/** Kunci hari (YYYY-MM-DD) di zona WIB, untuk mengelompokkan riwayat per hari. */
export const dayKeyOf = (iso: string | number | Date): string =>
  new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone: TZ }).format(new Date(iso))

/** "Today", "Yesterday", atau "21 Oct" (tanpa jam); mengikuti bahasa aktif. */
export const dayLabel = (iso: string | number | Date, nowMs: number): string => formatRelativeDay(iso, new Date(nowMs)).split(',')[0]!

const weekdayFmts = new Map<Locale, Intl.DateTimeFormat>()
const weekdayFmt = (locale: Locale) => {
  let fmt = weekdayFmts.get(locale)
  if (!fmt) weekdayFmts.set(locale, (fmt = new Intl.DateTimeFormat(INTL_TAG[locale], { weekday: 'short', timeZone: TZ })))
  return fmt
}

/** Nama hari singkat dari kunci hari WIB ("Mon", "Tue", ... / "Sen", "Sel", ...), mengikuti bahasa aktif. */
export const weekdayShort = (dayKey: string): string => weekdayFmt(getLocale()).format(new Date(`${dayKey}T12:00:00+07:00`))
