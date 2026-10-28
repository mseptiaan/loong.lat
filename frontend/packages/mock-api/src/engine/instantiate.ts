import { dwellMin, haversineKm, roadKm, type LatLng, type PlanOption, type Route } from '@loong/domain'
import { hourWib, speedKmh } from './route'

const round1 = (n: number) => Math.round(n * 10) / 10

/** Titik awal bergeser lebih dari ini (km) = jalur jalan lama tak berlaku lagi dan perlu diambil ulang. */
const START_MOVED_KM = 0.02

/**
 * Memindahkan titik awal rute (mis. ke posisi driver yang ditugaskan) dan jam berangkatnya, lalu menghitung
 * ulang ruas pertama serta semua ETA. Biaya tidak berubah: perjalanan menjemput tidak ditagih ke pengirim.
 * Jalur jalan (`geometry`) dibuang bila titik awal berpindah; host mengambilnya lagi di latar belakang.
 */
export function rebaseStart(route: Route, start: LatLng, startMs: number): Route {
  const moved = haversineKm(route.startPosition, start) > START_MOVED_KM
  const geometry = moved ? undefined : route.geometry
  const first = route.stops[0]
  if (!first) return { ...route, startPosition: start, startAt: new Date(startMs).toISOString(), geometry }
  const legKm = roadKm(start, first.waypoint.position)
  const legMin = Math.max(1, round1((legKm / speedKmh(route.vehicleType, hourWib(startMs))) * 60))
  let eta = 0
  const stops = route.stops.map((s, i) => {
    const leg = i === 0 ? { legKm: round1(legKm), legMin } : { legKm: s.legKm, legMin: s.legMin }
    eta += leg.legMin
    const out = { ...s, ...leg, etaMin: round1(eta), arriveAt: new Date(startMs + eta * 60_000).toISOString() }
    eta += dwellMin(s.kind)
    return out
  })
  return {
    ...route,
    startPosition: start,
    startAt: new Date(startMs).toISOString(),
    stops,
    durationMin: round1(eta),
    geometry,
  }
}

/** Rute nyata (id stabil `<plan>:r1`, `<plan>:r1:s1`, ...) dari rute-rute sebuah opsi yang dipilih. */
export function routesFromOption(planId: string, option: PlanOption): Route[] {
  return option.routes.map((r, i) => {
    const id = `${planId}:r${i + 1}`
    return {
      ...r,
      id,
      planId,
      status: 'menunggu_driver',
      stops: r.stops.map((s, j) => {
        const stopId = `${id}:s${j + 1}`
        return {
          ...s,
          id: stopId,
          status: 'menunggu',
          ...(s.kind === 'antar' ? { deliveryCode: deliveryCodeFor(stopId) } : {}),
        }
      }),
    }
  })
}

/** Kode OTP 4 digit yang stabil untuk sebuah stop (tiruan; backend asli membuat kode acak per pengiriman). */
export function deliveryCodeFor(stopId: string): string {
  let h = 2166136261
  for (let i = 0; i < stopId.length; i++) {
    h ^= stopId.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return String(1000 + ((h >>> 0) % 9000))
}

/**
 * Menghitung ulang `arriveAt` semua stop yang belum selesai, dimulai dari `fromIndex`, dengan jam keberangkatan
 * ruas pertama di `legStartMs`. Stop sebelum `fromIndex` tidak disentuh.
 */
export function reanchorEtas(route: Route, fromIndex: number, legStartMs: number): Route {
  let cursor = legStartMs
  const stops = route.stops.map((s, i) => {
    if (i < fromIndex) return s
    cursor += s.legMin * 60_000
    const arrive = cursor
    cursor += dwellMin(s.kind) * 60_000
    return { ...s, arriveAt: new Date(arrive).toISOString() }
  })
  return { ...route, stops }
}
