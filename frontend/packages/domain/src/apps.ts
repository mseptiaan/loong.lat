import { t } from '@loong/i18n'

/**
 * Hostname tiap aplikasi. Domain induk TIDAK di-hardcode: diturunkan dari hostname saat berjalan, jadi
 * kode yang sama benar di produksi (console.loong.lat), dev (console.loong.localhost), maupun domain lain.
 *
 *   console.<root>     Dashboard pengirim
 *   backoffice.<root>  Backoffice
 *   driver.<root>      Driver
 *   api.<root>         Backend tiruan (iframe tersembunyi yang menyimpan & membagi state ke tiga app).
 *                      Nanti diganti backend asli di hostname yang sama.
 */
export type AppId = 'console' | 'backoffice' | 'driver' | 'api'

export const APP_IDS: readonly AppId[] = ['console', 'backoffice', 'driver', 'api']

export const APP_SUBDOMAIN: Record<AppId, string> = {
  console: 'console',
  backoffice: 'backoffice',
  driver: 'driver',
  api: 'api',
}

export const APP_TITLE: Record<AppId, string> = {
  get console() {
    return t('dom.app.console')
  },
  get backoffice() {
    return t('dom.app.backoffice')
  },
  get driver() {
    return t('dom.app.driver')
  },
  get api() {
    return t('dom.app.api')
  },
}

type LocationLike = Pick<Location, 'protocol' | 'hostname' | 'port'>

const currentLocation = (): LocationLike => globalThis.location

/** Hostname tanpa subdomain aplikasi: "console.loong.lat" -> "loong.lat"; "localhost" tetap "localhost". */
export function rootHost(hostname: string = currentLocation().hostname): string {
  const labels = hostname.split('.')
  const first = labels[0]
  if (labels.length >= 2 && first && (APP_IDS as readonly string[]).includes(first)) return labels.slice(1).join('.')
  return hostname
}

/** Aplikasi mana yang sedang berjalan, dari label pertama hostname (undefined bila tidak dikenali). */
export function currentApp(hostname: string = currentLocation().hostname): AppId | undefined {
  const first = hostname.split('.')[0]
  return (APP_IDS as readonly string[]).includes(first ?? '') ? (first as AppId) : undefined
}

/** Origin lengkap sebuah aplikasi, mis. "https://backoffice.loong.lat" atau "http://driver.loong.localhost:4000". */
export function appOrigin(app: AppId, loc: LocationLike = currentLocation()): string {
  const port = loc.port ? `:${loc.port}` : ''
  return `${loc.protocol}//${APP_SUBDOMAIN[app]}.${rootHost(loc.hostname)}${port}`
}
