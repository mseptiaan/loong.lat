import { buildRouteGeometry, pointAlong, stopOffsetsKm } from './geo'
import type { LatLng, LngLat, Route } from './types'

export interface RoutePosition {
  position: LatLng
  bearing: number
  /** Indeks stop aktif (yang sedang dituju atau didatangi); sama dengan stops.length bila rute selesai. */
  stopIndex: number
  /** 0..1 progres ruas menuju stop aktif. */
  legFraction: number
  /** Kendaraan sedang berada di stop (sudah tiba / menunggu konfirmasi). */
  atStop: boolean
  /** Menit tersisa menuju stop aktif. */
  remainingLegMin: number
}

interface GeometryCache {
  key: string
  geometry: LngLat[]
  offsets: number[]
}

const cache = new Map<string, GeometryCache>()

function geometryOf(route: Route): GeometryCache {
  const last = route.stops.at(-1)?.waypoint.position
  const key = `${route.id}|${route.stops.length}|${route.startPosition.lat.toFixed(5)},${route.startPosition.lng.toFixed(5)}|${last?.lat.toFixed(5)},${last?.lng.toFixed(5)}|${route.geometry?.length ?? 0}`
  const hit = cache.get(key)
  if (hit) return hit
  const geometry = buildRouteGeometry(route)
  const entry = { key, geometry, offsets: stopOffsetsKm(route) }
  if (cache.size > 200) cache.clear()
  cache.set(key, entry)
  return entry
}

/** Geometri rute dengan cache (untuk UI yang menggambar ulang sering). */
export function routeGeometry(route: Route): LngLat[] {
  return geometryOf(route).geometry
}

/**
 * Posisi kendaraan pada waktu virtual `nowMs`. Fungsi murni dari status rute + jam: tidak butuh simulator,
 * tidak ada event per detik, dan identik di console, backoffice, dan driver.
 */
export function routePositionAt(route: Route, nowMs: number): RoutePosition {
  const stops = route.stops
  const firstOpen = stops.findIndex((s) => s.status !== 'selesai' && s.status !== 'bermasalah')
  const lastPos = stops.at(-1)?.waypoint.position ?? route.startPosition

  if (route.status === 'selesai' || firstOpen === -1) {
    return { position: lastPos, bearing: 0, stopIndex: stops.length, legFraction: 1, atStop: true, remainingLegMin: 0 }
  }
  if (route.status !== 'berjalan') {
    return { position: route.startPosition, bearing: 0, stopIndex: 0, legFraction: 0, atStop: false, remainingLegMin: stops[0]?.legMin ?? 0 }
  }

  const stop = stops[firstOpen]!
  const { geometry, offsets } = geometryOf(route)
  const endKm = offsets[firstOpen] ?? 0
  // ruas dihitung dari stop terakhir yang sudah dilewati (selesai/bermasalah), bukan dari indeks - 1
  const startKm = firstOpen === 0 ? 0 : (offsets[firstOpen - 1] ?? 0)

  if (stop.status === 'tiba') {
    const at = pointAlong(geometry, endKm)
    return { position: at.position, bearing: at.bearing, stopIndex: firstOpen, legFraction: 1, atStop: true, remainingLegMin: 0 }
  }

  const legMs = Math.max(1, stop.legMin) * 60_000
  const startedMs = route.legStartedAt ? new Date(route.legStartedAt).getTime() : nowMs
  const fraction = Math.min(1, Math.max(0, (nowMs - startedMs) / legMs))
  const at = pointAlong(geometry, startKm + (endKm - startKm) * fraction)
  return {
    position: at.position,
    bearing: at.bearing,
    stopIndex: firstOpen,
    legFraction: fraction,
    atStop: fraction >= 1,
    remainingLegMin: Math.max(0, ((1 - fraction) * legMs) / 60_000),
  }
}

/** Jarak (km) yang sudah ditempuh kendaraan sepanjang geometri rute pada waktu `nowMs`. */
export function travelledKm(route: Route, nowMs: number): number {
  const pos = routePositionAt(route, nowMs)
  const { offsets } = geometryOf(route)
  if (route.status === 'selesai' || pos.stopIndex >= route.stops.length) return offsets.at(-1) ?? 0
  if (route.status !== 'berjalan') return 0
  const endKm = offsets[pos.stopIndex] ?? 0
  const startKm = pos.stopIndex === 0 ? 0 : (offsets[pos.stopIndex - 1] ?? 0)
  return startKm + (endKm - startKm) * pos.legFraction
}

/** ETA (ISO) tiap stop yang belum selesai, dihitung dari posisi sekarang dan sisa ruas. */
export function liveEtas(route: Route, nowMs: number): { stopId: string; etaAt: string }[] {
  const pos = routePositionAt(route, nowMs)
  const out: { stopId: string; etaAt: string }[] = []
  let cursor = nowMs + pos.remainingLegMin * 60_000
  for (let i = pos.stopIndex; i < route.stops.length; i++) {
    const s = route.stops[i]!
    if (i > pos.stopIndex) cursor += s.legMin * 60_000
    out.push({ stopId: s.id, etaAt: new Date(cursor).toISOString() })
    cursor += dwellMin(s.kind) * 60_000
  }
  return out
}

/** Waktu layanan di tiap stop (menit). */
export function dwellMin(kind: 'ambil' | 'antar'): number {
  return kind === 'ambil' ? 4 : 3
}
