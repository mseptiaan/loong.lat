import type { LatLng, LngLat } from '@loong/domain'
import { decodePolyline, encodePolyline } from './polyline'
import { memoryRoadCache, pointKey, type RoadCache, type RoadProvider, type RoadStatus } from './provider'
import { simplifyPath } from './simplify'

/*
 * Penyedia jalan memakai Mapbox Directions + Matrix API (profil `driving`).
 *  - Matrix: 1 permintaan untuk seluruh titik sebuah plan (batas 25 titik, 60 permintaan/menit).
 *  - Directions: 1 permintaan per rute (batas 25 titik, 300 permintaan/menit), geometri penuh lalu disederhanakan.
 * Tidak pernah melempar galat: gagal apa pun (offline, 401/403, 429, timeout) menghasilkan null dan pemanggil
 * jatuh ke perkiraan garis halus. Token tidak pernah masuk ke pesan galat.
 */

const BASE = 'https://api.mapbox.com'
const MAX_COORDS = 25
const PROFILE = 'mapbox/driving'

export interface MapboxRoadsOptions {
  token: string
  fetch?: typeof fetch
  cache?: RoadCache
  now?: () => number
  /** Permintaan serentak maksimum (default 3). */
  maxConcurrent?: number
  timeoutMs?: number
}

const coord = (p: LatLng) => `${p.lng.toFixed(6)},${p.lat.toFixed(6)}`

export function createMapboxRoads(opts: MapboxRoadsOptions): RoadProvider {
  const fetchFn = opts.fetch ?? ((...args: Parameters<typeof fetch>) => globalThis.fetch(...args))
  const cache = opts.cache ?? memoryRoadCache()
  const now = opts.now ?? Date.now
  const maxConcurrent = opts.maxConcurrent ?? 3
  const timeoutMs = opts.timeoutMs ?? 8000

  const stats: RoadStatus = { provider: 'Mapbox', requests: 0, cacheHits: 0, failures: 0 }
  // diagnostik untuk developer (halaman status api.<domain>): sengaja satu bahasa (Inggris), tidak lewat katalog i18n
  const fail = (message: string) => {
    stats.failures += 1
    stats.lastError = message
  }
  let pausedUntil = 0

  /* antrean sederhana: maksimum `maxConcurrent` permintaan berjalan; slot diwariskan langsung ke yang menunggu */
  let active = 0
  const waiting: (() => void)[] = []
  async function acquire(): Promise<void> {
    if (active < maxConcurrent) {
      active += 1
      return
    }
    await new Promise<void>((resolve) => waiting.push(resolve))
  }
  const release = () => {
    const next = waiting.shift()
    if (next) next()
    else active -= 1
  }

  async function getJson(path: string, query: Record<string, string>): Promise<unknown> {
    if (now() < pausedUntil) return null
    await acquire()
    const ctl = new AbortController()
    const timer = setTimeout(() => ctl.abort(), timeoutMs)
    try {
      stats.requests += 1
      const qs = new URLSearchParams({ ...query, access_token: opts.token })
      const res = await fetchFn(`${BASE}${path}?${qs.toString()}`, { signal: ctl.signal })
      if (res.status === 429) {
        const retry = Number(res.headers.get('Retry-After'))
        pausedUntil = now() + (Number.isFinite(retry) && retry > 0 ? Math.min(retry, 120) : 30) * 1000
        stats.pausedUntil = pausedUntil
        fail('Mapbox rate limit reached (429); pausing briefly.')
        return null
      }
      if (res.status === 401 || res.status === 403) {
        pausedUntil = now() + 5 * 60_000
        stats.pausedUntil = pausedUntil
        fail(`Mapbox token rejected (${res.status}). Check the token and its URL restrictions.`)
        return null
      }
      if (!res.ok) {
        fail(`Mapbox responded with ${res.status}.`)
        return null
      }
      return await res.json()
    } catch (err) {
      fail(err instanceof DOMException && err.name === 'AbortError' ? 'Mapbox did not respond (timeout).' : 'Could not reach Mapbox.')
      return null
    } finally {
      clearTimeout(timer)
      release()
    }
  }

  return {
    status: () => ({ ...stats }),

    async matrix(points) {
      if (points.length < 2) return points.length === 1 ? [[0]] : []
      if (points.length > MAX_COORDS) {
        fail(`Matrix of ${points.length} points exceeds the limit of ${MAX_COORDS}; using estimates.`)
        return null
      }
      const key = `m|${points.map(pointKey).join(';')}`
      const hit = cache.get(key) as (number | null)[][] | undefined
      if (hit) {
        stats.cacheHits += 1
        return hit
      }
      const data = (await getJson(`/directions-matrix/v1/${PROFILE}/${points.map(coord).join(';')}`, { annotations: 'distance' })) as {
        code?: string
        distances?: (number | null)[][]
      } | null
      if (!data || data.code !== 'Ok' || !Array.isArray(data.distances) || data.distances.length !== points.length) {
        if (data) fail('Unexpected Mapbox matrix response.')
        return null
      }
      const km = data.distances.map((row) => row.map((m) => (typeof m === 'number' ? Math.round(m) / 1000 : null)))
      cache.set(key, km)
      return km
    },

    async path(points) {
      // titik berurutan yang sama (dua pengiriman di lokasi yang sama) digabung agar Mapbox tidak menolak ruas nol
      const pts = points.filter((p, i) => i === 0 || pointKey(p) !== pointKey(points[i - 1]!))
      if (pts.length < 2) return null
      if (pts.length > MAX_COORDS) {
        fail(`Route of ${pts.length} points exceeds the limit of ${MAX_COORDS}; using an estimated line.`)
        return null
      }
      const key = `p|${pts.map(pointKey).join(';')}`
      const hit = cache.get(key)
      if (typeof hit === 'string') {
        stats.cacheHits += 1
        return decodePolyline(hit)
      }
      const data = (await getJson(`/directions/v5/${PROFILE}/${pts.map(coord).join(';')}`, {
        geometries: 'polyline',
        overview: 'full',
        alternatives: 'false',
        continue_straight: 'false',
      })) as { code?: string; routes?: { geometry?: string }[] } | null
      const geometry = data?.routes?.[0]?.geometry
      if (!data || data.code !== 'Ok' || typeof geometry !== 'string') {
        if (data) fail('Unexpected Mapbox route response.')
        return null
      }
      let line: LngLat[]
      try {
        line = simplifyPath(decodePolyline(geometry))
      } catch {
        fail('Could not read the Mapbox route geometry.')
        return null
      }
      if (line.length < 2) return null
      cache.set(key, encodePolyline(line))
      return line
    },
  }
}
