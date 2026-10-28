import { rootHost, type AppId } from '@loong/domain'
import { createRemoteApi, type RemoteApi } from '@loong/mock-api'
import { createHttpApi, type HttpApiOptions } from './client'

export { createHttpApi, type HttpApiOptions } from './client'
export { ARG_NAMES, BACKEND_METHODS, toBody } from './wire'

type ApiApp = Extract<AppId, 'console' | 'backoffice' | 'driver'>

/** Port tiap API pada pengembangan lokal (tanpa gateway): driver 8081, backoffice 8082, console 8083. */
export const DEV_PORT: Readonly<Record<ApiApp, number>> = { driver: 8081, backoffice: 8082, console: 8083 }

const parsePosition = (raw: string | undefined): HttpApiOptions['fallbackPosition'] => {
  const [lat, lng] = (raw ?? '').split(',').map(Number)
  return Number.isFinite(lat) && Number.isFinite(lng) ? { lat: lat!, lng: lng! } : undefined
}

/**
 * Klien backend sebuah aplikasi. `VITE_API_MODE` memilih: `mock` (bawaan, backend tiruan di browser) atau `http`
 * (API asli milik aplikasi ini). Alamat API asli dari `VITE_API_URL`; tanpa itu, pada pengembangan lokal dipakai
 * `<protokol>//<domain induk>:<port bawaan>`, mis. http://loong.localhost:8083 untuk console.loong.localhost.
 * Aplikasi driver di mode http mengirim posisi GPS selama online; `VITE_DRIVER_FALLBACK_POSITION=lat,lng` memberi
 * posisi tetap bila GPS tidak ada (demo di laptop).
 */
export function createApi(app: ApiApp): RemoteApi {
  const env = import.meta.env
  if (env.VITE_API_MODE !== 'http') return createRemoteApi()
  const baseUrl = env.VITE_API_URL || `${location.protocol}//${rootHost()}:${DEV_PORT[app]}`
  return createHttpApi({ app, baseUrl, fallbackPosition: parsePosition(env.VITE_DRIVER_FALLBACK_POSITION) })
}
