import { formatDuration, formatNumber } from '@loong/domain'
import { t } from '../i18n'

/** "1.250.000", "Rp 50000", " 75.000 " -> angka; kosong atau tanpa digit -> NaN. */
export function parseAmount(raw: string): number {
  const digits = raw.replace(/\D/g, '')
  return digits ? Number(digits) : Number.NaN
}

/** 1250000 -> "1.250.000" (id) / "1,250,000" (en), untuk isian jumlah; bukan angka -> "". */
export const formatAmountInput = (n: number) => (Number.isFinite(n) ? formatNumber(n) : '')

/**
 * Tujuan kembali yang aman dari query (?next=): hanya jalur di aplikasi ini ("/plan/new?draft=..."). Menolak URL
 * lengkap, "//host", dan backslash, supaya tautan dari luar tidak bisa mengalihkan pengguna ke situs lain setelah top up.
 */
export function safeNext(raw: string | null | undefined): string | undefined {
  if (!raw || !raw.startsWith('/') || raw.startsWith('//') || raw.includes('\\')) return undefined
  try {
    return new URL(raw, 'http://internal.invalid').host === 'internal.invalid' ? raw : undefined
  } catch {
    return undefined
  }
}

/** Sisa waktu "23 j 40 mnt" / "23 h 40 min", "kurang dari 1 mnt"; <= 0 -> "habis". */
export function formatRemaining(ms: number): string {
  if (ms <= 0) return t('dash.fields.remainExpired')
  const min = Math.floor(ms / 60_000)
  if (min < 1) return t('dash.fields.remainLessThanMin')
  return formatDuration(min)
}
