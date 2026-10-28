import { t } from '../i18n'

/** Nilai tersimpan untuk pilihan nama alamat bawaan. Ini data (seperti nilai enum), bukan teks tampilan. */
export const ADDRESS_PRESETS = ['Rumah', 'Toko', 'Kantor', 'Lainnya'] as const

/**
 * Teks tampilan nama alamat tersimpan. Pilihan bawaan (Rumah, Toko, Kantor, Lainnya) mengikuti bahasa;
 * nama yang diketik pengguna ditampilkan apa adanya.
 */
export function addressLabel(label: string): string {
  switch (label.trim().toLowerCase()) {
    case 'rumah':
    case 'home':
      return t('dash.account.labelHome')
    case 'toko':
    case 'shop':
      return t('dash.account.labelShop')
    case 'kantor':
    case 'office':
      return t('dash.account.labelOffice')
    case 'lainnya':
    case 'other':
      return t('dash.account.labelOther')
    default:
      return label
  }
}

/** Jenis alamat untuk ikon: rumah, toko, atau lainnya (mengenali kata Indonesia maupun Inggris). */
export const addressKind = (label: string): 'home' | 'shop' | 'other' => (/rumah|home/i.test(label) ? 'home' : /toko|shop|store/i.test(label) ? 'shop' : 'other')
