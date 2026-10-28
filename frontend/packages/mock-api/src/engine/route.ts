import {
  dwellMin,
  roadKm,
  type LatLng,
  type Route,
  type RouteStop,
  type Shipment,
  type Vehicle,
  type VehicleType,
} from '@loong/domain'
import { msg } from '@loong/i18n'
import { aggregateItems } from './model'
import { computeFare } from './fare'

/** Jarak lewat jalan (km) antara dua titik. Default: perkiraan haversine x faktor jalan; bisa diganti data jalan sungguhan. */
export type RoadKm = (a: LatLng, b: LatLng) => number

export interface SeqItem {
  /** Indeks shipment di dalam grup. */
  s: number
  kind: 'ambil' | 'antar'
}

/** Jam di WIB (UTC+7, tanpa DST). */
export const hourWib = (ms: number) => (new Date(ms).getUTCHours() + 7) % 24

/** Pengali kecepatan menurut jam: jam sibuk Jakarta jauh lebih lambat. */
export function trafficFactor(hour: number): number {
  if (hour >= 7 && hour < 9) return 0.62
  if (hour >= 16 && hour < 19) return 0.6
  if (hour >= 11 && hour < 14) return 0.85
  if (hour >= 22 || hour < 5) return 1.2
  return 0.95
}

export function speedKmh(type: VehicleType, hour: number): number {
  return (type === 'motor' ? 30 : 24) * trafficFactor(hour)
}

const round1 = (n: number) => Math.round(n * 10) / 10

export function positionOf(shipment: Shipment, kind: 'ambil' | 'antar'): LatLng {
  return (kind === 'ambil' ? shipment.pickup : shipment.dropoff).position
}

/** Panjang urutan stop (km jalan), termasuk perjalanan awal dari `start` ke stop pertama. */
export function sequenceKm(shipments: Shipment[], seq: SeqItem[], start: LatLng, legKm: RoadKm = roadKm): number {
  let km = 0
  let prev = start
  for (const item of seq) {
    const p = positionOf(shipments[item.s]!, item.kind)
    km += legKm(prev, p)
    prev = p
  }
  return km
}

export interface BuildRouteArgs {
  id: string
  planId: string
  vehicle: Vehicle
  shipments: Shipment[]
  seq: SeqItem[]
  start: LatLng
  startMs: number
  /** Jarak antar titik; default perkiraan. */
  km?: RoadKm
}

/** Menyusun rute lengkap (ETA tiap stop, tarif) dari urutan stop. */
export function buildRoute(a: BuildRouteArgs): Route {
  const stops: RouteStop[] = []
  let prev = a.start
  let etaMin = 0
  let billableKm = 0
  let firstArrive = 0
  const kmBetween = a.km ?? roadKm

  a.seq.forEach((item, i) => {
    const shipment = a.shipments[item.s]!
    const waypoint = item.kind === 'ambil' ? shipment.pickup : shipment.dropoff
    const legKm = kmBetween(prev, waypoint.position)
    const speed = speedKmh(a.vehicle.type, hourWib(a.startMs + etaMin * 60_000))
    const legMin = Math.max(1, round1((legKm / speed) * 60))
    etaMin += legMin
    if (i === 0) firstArrive = etaMin
    else billableKm += legKm

    const agg = aggregateItems(shipment.items)
    stops.push({
      id: `${a.id}:s${i + 1}`,
      seq: i + 1,
      kind: item.kind,
      shipmentId: shipment.id,
      waypoint,
      legKm: round1(legKm),
      legMin,
      etaMin: round1(etaMin),
      arriveAt: new Date(a.startMs + etaMin * 60_000).toISOString(),
      status: 'menunggu',
      itemsSummary: msg('srv.engine.itemsSummary', { count: agg.itemCount, kg: agg.weightKg }),
      items: shipment.items.map((it) => ({
        name: it.name,
        ...(it.catalogKey ? { catalogKey: it.catalogKey } : {}),
        qty: it.qty,
        detail: msg('dom.stopItem.detail', { kg: it.weightKg * it.qty, l: it.lengthCm, w: it.widthCm, h: it.heightCm }),
        fragile: it.fragile,
      })),
    })
    etaMin += dwellMin(item.kind)
    prev = waypoint.position
  })

  const totalMin = etaMin
  const billableMin = Math.max(1, totalMin - firstArrive)
  const totalWeight = aggregateItems(a.shipments.flatMap((s) => s.items)).weightKg
  const fare = computeFare(a.vehicle, { km: billableKm, min: billableMin, stops: a.seq.length, weightKg: totalWeight })

  return {
    id: a.id,
    planId: a.planId,
    vehicleType: a.vehicle.type,
    status: 'menunggu_driver',
    startPosition: a.start,
    startAt: new Date(a.startMs).toISOString(),
    stops,
    distanceKm: round1(billableKm),
    durationMin: round1(totalMin),
    costRp: fare.total,
    fare: fare.lines,
    totalWeightKg: round1(totalWeight),
  }
}

/** Menggeser seluruh jadwal rute ke `newStartMs` (ETA relatif tetap). */
export function shiftRoute(route: Route, newStartMs: number): Route {
  const delta = newStartMs - new Date(route.startAt).getTime()
  return {
    ...route,
    startAt: new Date(newStartMs).toISOString(),
    stops: route.stops.map((s) => ({ ...s, arriveAt: new Date(new Date(s.arriveAt).getTime() + delta).toISOString() })),
  }
}
