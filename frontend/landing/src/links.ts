import { appOrigin, type AppId } from '@loong/domain'
import { getLocale } from '@loong/i18n'

type LinkedApp = Exclude<AppId, 'api'>

/** Aplikasi yang ditautkan berjalan di backend asli (build produksi, VITE_API_MODE=http) atau backend tiruan di browser (dev). */
export const LIVE: boolean = import.meta.env.VITE_API_MODE === 'http'

/** Alamat produksi dari .env.production; kosong saat dev, lalu diturunkan dari hostname halaman (appOrigin). */
const CONFIGURED: Record<LinkedApp, string | undefined> = {
  console: import.meta.env.VITE_CONSOLE_URL,
  backoffice: import.meta.env.VITE_BACKOFFICE_URL,
  driver: import.meta.env.VITE_DRIVER_URL,
}

const origin = (app: LinkedApp) => (CONFIGURED[app] || appOrigin(app)).replace(/\/+$/, '')

/** Alamat aplikasi, membawa bahasa halaman ini supaya aplikasi terbuka dalam bahasa yang sama. */
export const appHref = (app: LinkedApp) => `${origin(app)}/?lang=${getLocale()}`

/** Host yang ditampilkan di kartu aplikasi, mis. "console.loong.lat". */
export const appHost = (app: LinkedApp) => origin(app).replace(/^https?:\/\//, '')
