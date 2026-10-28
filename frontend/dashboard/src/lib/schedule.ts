import type { Schedule } from '@loong/domain'
import { t } from '../i18n'

const WIB = 7 * 3_600_000
const HOUR = 3_600_000

/** "2026-10-21" pada zona WIB untuk sebuah waktu. */
export const dateWib = (ms: number) => new Date(ms + WIB).toISOString().slice(0, 10)
/** "14:30" pada zona WIB. */
export const timeWib = (ms: number) => new Date(ms + WIB).toISOString().slice(11, 16)

/** Gabungkan tanggal + jam (diartikan WIB) menjadi ms; NaN bila tidak valid. */
export const parseWib = (date: string, time: string) => (date && time ? Date.parse(`${date}T${time}:00+07:00`) : Number.NaN)

/** Bawaan jadwal "nanti": satu jam lagi, dibulatkan ke kelipatan 15 menit. */
export function defaultLater(nowMs: number): { date: string; time: string } {
  const t = Math.ceil((nowMs + HOUR) / 900_000) * 900_000
  return { date: dateWib(t), time: timeWib(t) }
}

export function scheduleFrom(date: string, time: string): Schedule {
  const from = parseWib(date, time)
  return { mode: 'terjadwal', window: { from: new Date(from).toISOString(), to: new Date(from + HOUR).toISOString() } }
}

/** Galat bila jadwal terlalu dekat/lampau; undefined bila sah. */
export function scheduleError(s: Schedule, nowMs: number): string | undefined {
  if (s.mode === 'sekarang') return undefined
  const from = s.window ? Date.parse(s.window.from) : Number.NaN
  if (!Number.isFinite(from)) return t('dash.fields.scheduleMissing')
  if (from < nowMs + 20 * 60_000) return t('dash.fields.scheduleSoon')
  if (from > nowMs + 14 * 86_400_000) return t('dash.fields.scheduleFar')
  return undefined
}
