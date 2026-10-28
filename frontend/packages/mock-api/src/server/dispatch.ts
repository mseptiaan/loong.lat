import {
  ApiError,
  DEFAULT_SETTINGS,
  roadKm,
  type Driver,
  type DriverSuggestion,
  type Plan,
  type Route,
  type Shipment,
  type Vehicle,
  type VehicleType,
} from '@loong/domain'
import { msg, type Msg } from '@loong/i18n'
import type { DB } from '../db'
import { aggregateShipments, fitsVehicle } from '../engine/model'
import { rebaseStart } from '../engine/instantiate'
import { hourWib, speedKmh } from '../engine/route'
import { syncPlanStatus, touchPlan } from './ops'
import type { MockServer } from './server'

/*
 * Penugasan driver. Mesin ini milik kita (aturan, bukan AI): memilih driver dari data armada dan jadwal.
 *  - Pesanan "sekarang": driver terdekat (jarak jalan ke titik ambil) yang online, kosong, dan muat, dalam radius.
 *  - Pesanan "terjadwal": driver yang kosong pada jam itu (tanpa bentrok dengan tugas lain), diurutkan dari
 *    rating, pemerataan beban hari itu, lalu kedekatan.
 * Admin tetap bisa menugaskan atau mengganti driver secara manual; itu jalur pengecualian.
 */

const MIN = 60_000
const DAY = 86_400_000
const iso = (ms: number) => new Date(ms).toISOString()

/** Pesanan "sekarang" hanya mencari driver sejauh ini dari titik ambil. */
export const DISPATCH_RADIUS_KM = 15
/** Jeda aman antar tugas satu driver (menit). */
const BUFFER_MS = 15 * MIN
/** Driver baru ditandai "bertugas" bila tugasnya mulai dalam rentang ini; tugas yang jauh di depan belum mengikatnya. */
export const COMMIT_LEAD_MS = 60 * MIN

export const autoDispatchOn = (db: DB): boolean => (db.settings?.dispatch ?? DEFAULT_SETTINGS.dispatch).auto

export const routeShipments = (plan: Plan, route: Route): Shipment[] => {
  const ids = new Set(route.stops.map((s) => s.shipmentId))
  return plan.shipments.filter((s) => ids.has(s.id))
}

const vehicleWord = (type: VehicleType): Msg => msg(type === 'motor' ? 'srv.ops.vehicle.motor' : 'srv.ops.vehicle.blindvan')

const need = <T>(value: T | undefined | null, message: Msg): T => {
  if (value === undefined || value === null) throw new ApiError('tidak_ditemukan', message)
  return value
}

/* ---------------- saran untuk panel manual (admin) ---------------- */

export function suggestForRoute(server: MockServer, db: DB, plan: Plan, route: Route): DriverSuggestion[] {
  const agg = aggregateShipments(routeShipments(plan, route))
  const first = route.stops[0]!.waypoint.position
  const hour = hourWib(server.now())
  const out: DriverSuggestion[] = []
  for (const d of db.drivers) {
    if (d.status !== 'online') continue
    const v = db.vehicles.find((x) => x.id === d.vehicleId)
    if (!v || v.status !== 'aktif' || v.type !== route.vehicleType) continue
    const km = d.position ? roadKm(d.position, first) : 8
    const eta = Math.max(1, Math.round((km / speedKmh(v.type, hour)) * 60))
    const fits = fitsVehicle(agg, v)
    const score = Math.max(0, Math.min(100, Math.round(100 - km * 7 - (4.95 - d.rating) * 40 - (fits ? 0 : 60))))
    out.push({
      driverId: d.id,
      driverName: d.name,
      vehicleId: v.id,
      vehicleType: v.type,
      plate: v.plate,
      rating: d.rating,
      distanceToPickupKm: Math.round(km * 10) / 10,
      etaToPickupMin: eta,
      fits,
      score,
      reasons: [
        msg('srv.ops.sug.distance', { km, eta }),
        msg('srv.ops.sug.rating', { rating: d.rating, count: d.completedTrips }),
        fits ? msg('srv.ops.sug.fits', { kg: agg.weightKg, model: v.model }) : msg('srv.ops.sug.tooBig', { model: v.model }),
      ],
    })
  }
  return out.sort((a, b) => b.score - a.score)
}

/* ---------------- ketersediaan menurut waktu ---------------- */

/** Rentang waktu yang ditempati sebuah tugas driver (untuk memeriksa bentrok). */
function windowOf(route: Route, nowMs: number): [number, number] {
  const start = route.status === 'berjalan' && route.startedAt ? Date.parse(route.startedAt) : Date.parse(route.startAt)
  let end = start + route.durationMin * MIN
  if (route.status === 'berjalan' && end < nowMs) end = nowMs + BUFFER_MS // tugas yang telat masih menempati driver
  return [start, end]
}

const activeRoutesOf = (db: DB, driverId: string): Route[] =>
  db.plans
    .filter((p) => p.status !== 'dibatalkan' && p.status !== 'selesai')
    .flatMap((p) => p.routes)
    .filter((r) => r.driverId === driverId && (r.status === 'ditugaskan' || r.status === 'berjalan'))

const dayOf = (ms: number) => Math.floor((ms + 7 * 3_600_000) / DAY)

export interface DispatchPick {
  driver: Driver
  vehicle: Vehicle
  distanceKm: number
  etaMin: number
  /** Alasan singkat yang dicatat di riwayat plan dan dilihat admin. */
  reasons: Msg[]
}

/**
 * Memilih driver terbaik untuk sebuah rute, atau null bila belum ada yang cocok.
 * Tidak mengubah data; dipakai juga untuk "dry run" (apakah ada yang bisa ditugaskan sekarang).
 */
export function pickDriver(server: MockServer, db: DB, plan: Plan, route: Route): DispatchPick | null {
  const first = route.stops[0]?.waypoint.position
  if (!first) return null
  const nowMs = server.now()
  const scheduled = plan.schedule.mode === 'terjadwal'
  const agg = aggregateShipments(routeShipments(plan, route))
  const routeStart = scheduled ? Date.parse(route.startAt) : nowMs
  const hour = hourWib(routeStart)

  type Cand = DispatchPick & { score: number }
  const cands: Cand[] = []
  for (const d of db.drivers) {
    if (d.status === 'offline') continue
    // sekarang: driver harus online dan kosong saat ini; terjadwal: yang sedang bertugas pun boleh bila kosong pada jamnya
    if (!scheduled && d.status !== 'online') continue
    const vehicle = db.vehicles.find((v) => v.id === d.vehicleId)
    if (!vehicle || vehicle.status !== 'aktif' || vehicle.type !== route.vehicleType || !fitsVehicle(agg, vehicle)) continue

    const km = d.position ? roadKm(d.position, first) : 8
    if (!scheduled && km > DISPATCH_RADIUS_KM) continue
    const eta = Math.max(1, Math.round((km / speedKmh(vehicle.type, hour)) * 60))
    const from = scheduled ? routeStart - eta * MIN : nowMs
    const to = scheduled ? routeStart + route.durationMin * MIN : nowMs + (eta + route.durationMin) * MIN

    const mine = activeRoutesOf(db, d.id)
    if (mine.some((r) => {
      const [rs, re] = windowOf(r, nowMs)
      return rs < to + BUFFER_MS && re + BUFFER_MS > from
    })) continue

    if (!scheduled) {
      cands.push({
        driver: d,
        vehicle,
        distanceKm: km,
        etaMin: eta,
        score: -km,
        reasons: [msg('srv.ops.pick.nearest', { km, eta }), msg('srv.ops.pick.rating', { rating: d.rating })],
      })
      continue
    }
    const load = mine.filter((r) => dayOf(Date.parse(r.startAt)) === dayOf(routeStart)).length
    // rating dan beban hari itu lebih penting daripada jarak: driver tidak perlu di dekat titik ambil berjam-jam sebelumnya
    const score = 100 - Math.min(km, 30) * 1.2 + (d.rating - 4.5) * 30 - load * 8
    cands.push({
      driver: d,
      vehicle,
      distanceKm: km,
      etaMin: eta,
      score,
      reasons: [
        msg('srv.ops.pick.free', { from, to, rating: d.rating }),
        load === 0 ? msg('srv.ops.pick.noOtherJobs') : msg('srv.ops.pick.otherJobs', { count: load }),
      ],
    })
  }
  if (cands.length === 0) return null
  // sekarang: jarak terpendek, rating menjadi pemecah seri; terjadwal: skor tertinggi
  cands.sort((a, b) => b.score - a.score || b.driver.rating - a.driver.rating || a.driver.id.localeCompare(b.driver.id))
  const { score: _score, ...pick } = cands[0]!
  void _score
  return pick
}

/* ---------------- menugaskan ---------------- */

export interface AssignOptions {
  /** 'manual': tindakan admin (driver harus online). 'otomatis': dipilih mesin; ketersediaan sudah diperiksa menurut waktu. */
  mode: 'manual' | 'otomatis'
  reason?: Msg
}

export function assignRoute(server: MockServer, db: DB, plan: Plan, route: Route, driver: Driver, opts: AssignOptions): void {
  if (plan.status === 'dibatalkan' || plan.status === 'selesai') throw new ApiError('bentrok', msg('srv.ops.err.planInactive'))
  if (route.status !== 'menunggu_driver') throw new ApiError('bentrok', msg('srv.ops.err.routeHasDriver'))
  if (opts.mode === 'manual' ? driver.status !== 'online' : driver.status === 'offline') {
    throw new ApiError('bentrok', msg('srv.ops.err.driverUnavailable', { driver: driver.name }), { field: 'driverId' })
  }
  const vehicle = need(
    db.vehicles.find((v) => v.id === driver.vehicleId),
    msg('srv.ops.err.driverNoVehicle', { driver: driver.name }),
  )
  if (vehicle.type !== route.vehicleType) {
    throw new ApiError('bentrok', msg('srv.ops.err.vehicleMismatch', { need: vehicleWord(route.vehicleType), driver: driver.name, have: vehicleWord(vehicle.type) }), { field: 'driverId' })
  }
  if (!fitsVehicle(aggregateShipments(routeShipments(plan, route)), vehicle)) {
    throw new ApiError('bentrok', msg('srv.ops.err.overCapacity', { model: vehicle.model }), { field: 'driverId' })
  }
  const nowMs = server.now()
  const pos = driver.position ?? route.startPosition
  // jadwal: bila terjadwal, berangkat sedemikian rupa sampai di pickup tepat pada jendela waktu
  const trial = rebaseStart(route, pos, 0)
  const approachMin = trial.stops[0]?.legMin ?? 0
  const windowFrom = plan.schedule.mode === 'terjadwal' && plan.schedule.window ? new Date(plan.schedule.window.from).getTime() : 0
  const startMs = Math.max(nowMs + 2 * MIN, windowFrom - approachMin * MIN)
  const rebased = rebaseStart(route, pos, startMs)
  Object.assign(route, rebased, {
    driverId: driver.id,
    vehicleId: vehicle.id,
    status: 'ditugaskan' as const,
    assignedAt: iso(nowMs),
  })
  // tugas yang masih jauh di depan belum mengikat driver; ia tetap bisa menerima pesanan lain yang tidak bentrok
  if (startMs - nowMs <= COMMIT_LEAD_MS) driver.status = 'bertugas'
  syncPlanStatus(plan)
  touchPlan(
    plan,
    nowMs,
    opts.mode !== 'otomatis'
      ? msg('srv.ops.event.assigned', { driver: driver.name, plate: vehicle.plate })
      : opts.reason
        ? msg('srv.ops.event.autoAssignedWhy', { driver: driver.name, plate: vehicle.plate, reason: opts.reason })
        : msg('srv.ops.event.autoAssigned', { driver: driver.name, plate: vehicle.plate }),
  )
  server.notify(
    db,
    `driver:${driver.id}`,
    msg('srv.ops.ntf.newJobTitle'),
    msg('srv.ops.ntf.newJobBody', { code: plan.code, count: route.stops.length, start: startMs }),
    plan.id,
  )
  server.notify(db, `customer:${plan.userId}`, msg('srv.ops.ntf.driverAssignedTitle'), msg('srv.ops.ntf.driverAssignedBody', { driver: driver.name, code: plan.code }), plan.id)
}

export function unassignRoute(server: MockServer, db: DB, plan: Plan, route: Route, note?: Msg): void {
  if (route.status === 'berjalan') throw new ApiError('bentrok', msg('srv.ops.err.routeStarted'))
  const driver = db.drivers.find((d) => d.id === route.driverId)
  delete route.driverId
  delete route.vehicleId
  delete route.assignedAt
  route.status = 'menunggu_driver'
  // driver kembali online hanya bila tidak punya tugas lain yang sudah mengikatnya
  if (driver && driver.status === 'bertugas' && !activeRoutesOf(db, driver.id).some((r) => Date.parse(r.startAt) - server.now() <= COMMIT_LEAD_MS)) {
    driver.status = 'online'
  }
  syncPlanStatus(plan)
  const n = route.id.split(':r')[1]
  touchPlan(plan, server.now(), note ?? (n ? msg('srv.ops.event.unassigned', { n }) : msg('srv.ops.event.unassignedPlain')))
}

/** Catatan sekali saja (tidak berulang tiap percobaan) bahwa belum ada driver yang cocok. */
function noteNoDriver(server: MockServer, plan: Plan, route: Route): void {
  const scheduled = plan.schedule.mode === 'terjadwal'
  const text = msg(scheduled ? 'srv.ops.event.noDriverScheduled' : 'srv.ops.event.noDriverNow', { vehicle: vehicleWord(route.vehicleType) })
  if (JSON.stringify(plan.events.at(-1)?.text) === JSON.stringify(text)) return
  touchPlan(plan, server.now(), text)
}

/** Menugaskan driver untuk semua rute plan yang belum punya. Mengembalikan jumlah rute yang berhasil ditugaskan. */
export function dispatchPlan(server: MockServer, db: DB, plan: Plan, opts: { note?: boolean } = {}): number {
  let assigned = 0
  for (const route of plan.routes) {
    if (route.driverId || route.status !== 'menunggu_driver') continue
    const pick = pickDriver(server, db, plan, route)
    if (!pick) {
      if (opts.note !== false) noteNoDriver(server, plan, route)
      continue
    }
    assignRoute(server, db, plan, route, pick.driver, { mode: 'otomatis', reason: pick.reasons[0] })
    assigned += 1
  }
  return assigned
}

/**
 * Driver yang tugasnya (sudah ditugaskan) akan mulai dalam COMMIT_LEAD_MS ditandai "bertugas". Dipanggil berkala karena
 * waktu berjalan sendiri; tidak menulis ke DB bila tidak ada yang berubah.
 */
export function syncCommitments(server: MockServer): number {
  const nowMs = server.now()
  const due = (db: DB) =>
    db.drivers.filter((d) => d.status === 'online' && activeRoutesOf(db, d.id).some((r) => Date.parse(r.startAt) - nowMs <= COMMIT_LEAD_MS))
  if (due(server.db()).length === 0) return 0
  const n = server.mutate((db) => {
    const drivers = due(db)
    for (const d of drivers) d.status = 'bertugas'
    return drivers.length
  })
  server.emit({ type: 'fleet.changed' })
  return n
}

/**
 * Satu putaran penugasan otomatis untuk semua plan yang masih menunggu driver (dipanggil berkala oleh host
 * api.<domain> dan saat driver online). Tidak menulis ke DB bila belum ada yang bisa ditugaskan.
 */
export function tickDispatch(server: MockServer): number {
  syncCommitments(server)
  const db = server.db()
  if (!autoDispatchOn(db)) return 0
  const open = (p: Plan) => (p.status === 'menunggu_driver' || p.status === 'ditugaskan') && p.routes.some((r) => !r.driverId && r.status === 'menunggu_driver')
  const waiting = db.plans.filter(open)
  if (!waiting.some((p) => p.routes.some((r) => !r.driverId && r.status === 'menunggu_driver' && pickDriver(server, db, p, r)))) return 0

  // yang mendesak dulu: pesanan sekarang (paling lama menunggu), lalu terjadwal menurut jamnya
  const order = (p: Plan) => (p.schedule.mode === 'terjadwal' ? 1 : 0)
  const ids = waiting
    .sort((a, b) => order(a) - order(b) || (order(a) ? a.routes[0]!.startAt.localeCompare(b.routes[0]!.startAt) : a.createdAt.localeCompare(b.createdAt)))
    .map((p) => p.id)

  const touched: string[] = []
  let count = 0
  server.mutate((fresh) => {
    for (const id of ids) {
      const plan = fresh.plans.find((p) => p.id === id)
      if (!plan || !open(plan)) continue
      const n = dispatchPlan(server, fresh, plan, { note: false })
      if (n > 0) {
        count += n
        touched.push(plan.id)
      }
    }
  })
  for (const id of touched) server.emit({ type: 'plans.changed', planId: id })
  if (count > 0) {
    server.emit({ type: 'fleet.changed' })
    server.emit({ type: 'notifications.changed' })
  }
  return count
}
