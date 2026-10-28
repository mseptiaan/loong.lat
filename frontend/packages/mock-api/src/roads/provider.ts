import { roadKm, type LatLng, type LngLat } from '@loong/domain'
import type { RoadKm } from '../engine/route'

/*
 * Penyedia data jalan (alat bantu). Engine optimasi tetap milik kita: penyedia hanya menjawab
 * "berapa km lewat jalan antar titik ini" (matriks) dan "jalan mana yang dilalui" (jalur untuk digambar).
 * Penyedia tidak pernah memilih urutan stop, kendaraan, atau opsi.
 */

export interface RoadStatus {
  /** Nama penyedia, untuk halaman status. */
  provider: string
  requests: number
  cacheHits: number
  failures: number
  /** Pesan galat terakhir (tanpa URL/token). */
  lastError?: string
  /** Sampai kapan permintaan ditahan (setelah 429/401/403), ms epoch. */
  pausedUntil?: number
}

export interface RoadProvider {
  /** Jarak lewat jalan dalam km: hasil[i][j] = dari titik i ke titik j. null = tak tersedia (jatuh ke perkiraan). */
  matrix(points: LatLng[]): Promise<(number | null)[][] | null>
  /** Jalur jalan melewati titik-titik berurutan ([lng, lat]); null bila gagal. */
  path(points: LatLng[]): Promise<LngLat[] | null>
  status(): RoadStatus
}

/* ---------------- kunci koordinat ---------------- */

/** ±1 m. Dua titik dengan kunci sama dianggap titik yang sama. */
export const pointKey = (p: LatLng) => `${p.lat.toFixed(5)},${p.lng.toFixed(5)}`

export function uniquePoints(points: LatLng[]): LatLng[] {
  const seen = new Set<string>()
  const out: LatLng[] = []
  for (const p of points) {
    const k = pointKey(p)
    if (!seen.has(k)) {
      seen.add(k)
      out.push(p)
    }
  }
  return out
}

/**
 * Jarak antar titik dari matriks; pasangan yang tak ada di matriks (mis. titik awal sementara yang belum
 * punya driver) atau bernilai null memakai perkiraan haversine x faktor jalan.
 */
export function matrixKm(points: LatLng[], km: (number | null)[][]): RoadKm {
  const index = new Map(points.map((p, i) => [pointKey(p), i]))
  return (a, b) => {
    const i = index.get(pointKey(a))
    const j = index.get(pointKey(b))
    if (i === undefined || j === undefined) return roadKm(a, b)
    return km[i]?.[j] ?? roadKm(a, b)
  }
}

/* ---------------- cache ---------------- */

export interface RoadCache {
  get(key: string): unknown
  set(key: string, value: unknown): void
}

export function memoryRoadCache(): RoadCache {
  const map = new Map<string, unknown>()
  return { get: (k) => map.get(k), set: (k, v) => void map.set(k, v) }
}

const CACHE_KEY = 'loong:roads:v1'

/**
 * Cache di localStorage (origin api.<domain>): alamat demo yang sama tidak meminta ke jaringan lagi, jadi demo
 * tetap jalan saat offline atau saat kuota habis. Entri tertua dibuang bila melebihi `maxEntries`.
 */
export function localRoadCache(maxEntries = 160, key = CACHE_KEY): RoadCache {
  type Entry = { t: number; v: unknown }
  let entries: Record<string, Entry> = {}
  try {
    const raw = globalThis.localStorage?.getItem(key)
    if (raw) entries = JSON.parse(raw) as Record<string, Entry>
  } catch {
    entries = {}
  }
  const save = () => {
    try {
      globalThis.localStorage?.setItem(key, JSON.stringify(entries))
    } catch {
      // penyimpanan penuh: cache hanya berlaku selama halaman terbuka
    }
  }
  return {
    get: (k) => entries[k]?.v,
    set(k, v) {
      entries[k] = { t: Date.now(), v }
      const keys = Object.keys(entries)
      if (keys.length > maxEntries) {
        keys.sort((a, b) => entries[a]!.t - entries[b]!.t)
        for (const old of keys.slice(0, keys.length - maxEntries)) delete entries[old]
      }
      save()
    },
  }
}
