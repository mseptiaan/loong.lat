/**
 * Bahasa aktif. Bawaan Inggris; pilihan pengguna disimpan per aplikasi (localStorage origin itu).
 * Tidak menebak dari bahasa peramban: bahasa bawaan selalu `DEFAULT_LOCALE`.
 */

export const LOCALES = ['en', 'id'] as const
export type Locale = (typeof LOCALES)[number]

export const DEFAULT_LOCALE: Locale = 'en'

/** Tiap bahasa ditulis dalam bahasanya sendiri (dipakai pengganti bahasa). */
export const LOCALE_NAME: Record<Locale, string> = { en: 'English', id: 'Bahasa Indonesia' }
export const LOCALE_SHORT: Record<Locale, string> = { en: 'EN', id: 'ID' }

/** Tag BCP 47 untuk Intl. en-GB: jam 24 dan "21 Oct 2026", sesuai kebiasaan pembaca di Indonesia. */
export const INTL_TAG: Record<Locale, string> = { en: 'en-GB', id: 'id-ID' }

export const LOCALE_STORAGE_KEY = 'loong:locale'
/** `?lang=id` menetapkan bahasa lalu dihapus dari alamat. */
export const LOCALE_PARAM = 'lang'

export const isLocale = (value: unknown): value is Locale => typeof value === 'string' && (LOCALES as readonly string[]).includes(value)

let current: Locale = DEFAULT_LOCALE
const listeners = new Set<() => void>()

export function getLocale(): Locale {
  return current
}

function apply(next: Locale) {
  current = next
  if (typeof document !== 'undefined') document.documentElement.lang = next
  for (const l of listeners) l()
}

/** Ganti bahasa dan simpan. Aplikasi dirender ulang oleh `I18nBoundary`. */
export function setLocale(next: Locale): void {
  if (next === current) return
  try {
    globalThis.localStorage?.setItem(LOCALE_STORAGE_KEY, next)
  } catch {
    // penyimpanan diblokir: bahasa tetap berlaku selama halaman terbuka
  }
  apply(next)
}

export function subscribeLocale(listener: () => void): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

/**
 * Dipanggil sekali sebelum render pertama: `?lang=` menang atas pilihan tersimpan, lalu bawaan.
 * Perubahan dari tab lain di aplikasi yang sama ikut berlaku.
 */
export function initLocale(): Locale {
  let next: Locale = DEFAULT_LOCALE
  try {
    const stored = globalThis.localStorage?.getItem(LOCALE_STORAGE_KEY)
    if (isLocale(stored)) next = stored
  } catch {
    // abaikan
  }
  if (typeof location !== 'undefined') {
    const url = new URL(location.href)
    const fromUrl = url.searchParams.get(LOCALE_PARAM)
    if (fromUrl !== null) {
      if (isLocale(fromUrl)) {
        next = fromUrl
        try {
          globalThis.localStorage?.setItem(LOCALE_STORAGE_KEY, fromUrl)
        } catch {
          // abaikan
        }
      }
      url.searchParams.delete(LOCALE_PARAM)
      history.replaceState(history.state, '', url)
    }
  }
  apply(next)
  if (typeof window !== 'undefined') {
    window.addEventListener('storage', (e) => {
      if (e.key === LOCALE_STORAGE_KEY && isLocale(e.newValue) && e.newValue !== current) apply(e.newValue)
    })
  }
  return next
}
