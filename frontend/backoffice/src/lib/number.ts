import { INTL_TAG, getLocale } from '@loong/i18n'

/**
 * Angka untuk nilai awal kolom isian: pemisah desimal mengikuti bahasa ("12,5" / "12.5"), tanpa pemisah ribuan
 * agar `parseNum` membacanya kembali tanpa salah.
 */
export const formatInputNumber = (n: number): string => new Intl.NumberFormat(INTL_TAG[getLocale()], { useGrouping: false, maximumFractionDigits: 6 }).format(n)

/** "12,5" atau "12.5" -> 12.5; kosong atau bukan angka -> NaN. */
export const parseNum = (s: string): number => (s.trim() === '' ? Number.NaN : Number(s.replace(',', '.')))
