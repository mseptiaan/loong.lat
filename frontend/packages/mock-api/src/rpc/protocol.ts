import type { AppEvent, ApiErrorCode, Session } from '@loong/domain'
import type { Locale, Msg } from '@loong/i18n'

/**
 * Protokol postMessage antara aplikasi (console/backoffice/driver) dan host api.<domain> di iframe tersembunyi.
 * Versi dalam field `loong` agar pesan lain di halaman tidak salah ditafsirkan.
 */
export const PROTOCOL = 1

export interface WireError {
  name: 'ApiError' | 'Error'
  code: ApiErrorCode
  status: number
  /** Teks Inggris (log dan tes); layar memakai `msg` bila ada. */
  message: string
  msg?: Msg
  field?: string
}

export type ClientMessage =
  | { loong: typeof PROTOCOL; kind: 'hello' }
  | { loong: typeof PROTOCOL; kind: 'call'; id: string; path: string[]; args: unknown[]; ctx: Session | null; locale: Locale }
  | { loong: typeof PROTOCOL; kind: 'stream.cancel'; id: string }
  | { loong: typeof PROTOCOL; kind: 'subscribe' }

export type HostMessage =
  | { loong: typeof PROTOCOL; kind: 'ready' }
  | { loong: typeof PROTOCOL; kind: 'result'; id: string; ok: true; value: unknown }
  | { loong: typeof PROTOCOL; kind: 'result'; id: string; ok: false; error: WireError }
  | { loong: typeof PROTOCOL; kind: 'stream.chunk'; id: string; value: unknown }
  | { loong: typeof PROTOCOL; kind: 'stream.end'; id: string }
  | { loong: typeof PROTOCOL; kind: 'stream.error'; id: string; error: WireError }
  | { loong: typeof PROTOCOL; kind: 'event'; event: AppEvent }

export const isMessage = <T extends { loong: number }>(data: unknown): data is T =>
  typeof data === 'object' && data !== null && (data as { loong?: unknown }).loong === PROTOCOL
