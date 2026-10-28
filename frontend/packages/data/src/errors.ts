import { ApiError } from '@loong/domain'
import { renderMsg, t } from '@loong/i18n'

/** Pesan galat yang aman ditampilkan ke pengguna, dalam bahasa saat ini. */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.msg ? renderMsg(err.msg) : err.message
  if (err instanceof Error) return err.message
  return t('shell.error.generic')
}

/** Galat jaringan/layanan bisa dicoba lagi; galat validasi/izin tidak. */
export const isRetryable = (err: unknown): boolean =>
  err instanceof ApiError ? err.code === 'jaringan' || err.code === 'layanan' : true

/** Kolom yang bermasalah pada galat validasi, bila ada. */
export const errorField = (err: unknown): string | undefined => (err instanceof ApiError ? err.field : undefined)
