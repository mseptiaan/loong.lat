import { ApiError, dwellMin, type IssueKind, type Plan, type Route, type RouteStop } from '@loong/domain'
import { msg, type Msg } from '@loong/i18n'
import type { DB } from '../db'
import { reanchorEtas } from '../engine/instantiate'
import type { MockServer } from './server'

const iso = (ms: number) => new Date(ms).toISOString()

/** Indeks stop pertama yang belum selesai/bermasalah, atau -1. */
export const openIndex = (route: Route) =>
  route.stops.findIndex((s) => s.status !== 'selesai' && s.status !== 'bermasalah')

export function findPlan(db: DB, planId: string): Plan {
  const plan = db.plans.find((p) => p.id === planId)
  if (!plan) throw new ApiError('tidak_ditemukan', msg('srv.ops.err.planNotFound'))
  return plan
}

export function findRoute(plan: Plan, routeId: string): Route {
  const route = plan.routes.find((r) => r.id === routeId)
  if (!route) throw new ApiError('tidak_ditemukan', msg('srv.ops.err.routeNotFound'))
  return route
}

export function findStop(route: Route, stopId: string): RouteStop {
  const stop = route.stops.find((s) => s.id === stopId)
  if (!stop) throw new ApiError('tidak_ditemukan', msg('srv.ops.err.stopNotFound'))
  return stop
}

/** Catat peristiwa pada plan dan perbarui `updatedAt`. */
export function touchPlan(plan: Plan, nowMs: number, text?: Msg): void {
  plan.updatedAt = iso(nowMs)
  if (text) plan.events.push({ id: `evt-${plan.events.length + 1}-${nowMs.toString(36)}`, at: iso(nowMs), text })
}

const driverOf = (db: DB, route: Route) => db.drivers.find((d) => d.id === route.driverId)

/** Nama driver untuk teks; bila belum ada driver dipakai kata "Driver" (mengikuti bahasa layar). */
const driverName = (driver: { name: string } | undefined): string | Msg => driver?.name ?? msg('srv.ops.driver')

/** Masalah yang dilaporkan driver: jenis (label domain) dan catatan bebas. Teks bebas lama (string) tetap diterima apa adanya. */
export interface StopIssue {
  kind: IssueKind
  note?: string
}

const issueMsg = (issue: StopIssue | string): Msg => {
  if (typeof issue === 'string') return msg('srv.ops.issue.text', { text: issue })
  const kind = msg(`dom.issue.${issue.kind}`)
  return issue.note ? msg('srv.ops.issue.withNote', { issue: kind, note: issue.note }) : kind
}

/** Driver menandai tiba di stop aktif. */
export function arriveStop(server: MockServer, db: DB, plan: Plan, route: Route, stop: RouteStop): void {
  const nowMs = server.now()
  stop.status = 'tiba'
  stop.arrivedAt = iso(nowMs)
  const driver = driverOf(db, route)
  if (driver) {
    driver.position = stop.waypoint.position
    driver.lastSeen = iso(nowMs)
  }
  touchPlan(plan, nowMs, msg('srv.ops.event.driverArrived', { place: stop.waypoint.name }))
  server.notify(
    db,
    `customer:${plan.userId}`,
    msg(stop.kind === 'ambil' ? 'srv.ops.ntf.arrivedPickupTitle' : 'srv.ops.ntf.arrivedDropoffTitle'),
    msg('srv.ops.ntf.arrivedBody', { driver: driverName(driver), place: stop.waypoint.name, code: plan.code }),
    plan.id,
  )
}

/** Menutup stop (selesai atau bermasalah) lalu memajukan rute ke stop berikutnya atau menyelesaikannya. */
export function closeStop(
  server: MockServer,
  db: DB,
  plan: Plan,
  route: Route,
  stop: RouteStop,
  result: 'selesai' | 'bermasalah',
  issue?: StopIssue | string,
): void {
  const nowMs = server.now()
  stop.status = result
  stop.completedAt = iso(nowMs)
  stop.arrivedAt ??= iso(nowMs)
  const issueText = issue ? issueMsg(issue) : msg('dom.issue.lainnya')
  if (issue) stop.issue = issueText

  const driver = driverOf(db, route)
  if (driver) {
    driver.position = stop.waypoint.position
    driver.lastSeen = iso(nowMs)
  }
  const place = stop.waypoint.name
  const pickup = stop.kind === 'ambil'
  touchPlan(
    plan,
    nowMs,
    result === 'selesai'
      ? msg(pickup ? 'srv.ops.event.pickupDone' : 'srv.ops.event.dropoffDone', { place })
      : msg(pickup ? 'srv.ops.event.pickupProblem' : 'srv.ops.event.dropoffProblem', { place, issue: issueText }),
  )

  if (result === 'bermasalah') {
    server.notify(db, 'admin', msg('srv.ops.ntf.problemAdminTitle', { code: plan.code }), msg('srv.ops.ntf.problemAdminBody', { driver: driverName(driver), issue: issueText, place }), plan.id)
    server.notify(db, `customer:${plan.userId}`, msg('srv.ops.ntf.problemCustomerTitle'), msg('srv.ops.ntf.problemCustomerBody', { issue: issueText, place, code: plan.code }), plan.id)
  } else if (stop.kind === 'antar') {
    server.notify(db, `customer:${plan.userId}`, msg('srv.ops.ntf.deliveredTitle'), msg('srv.ops.ntf.deliveredBody', { place, code: plan.code }), plan.id)
  }

  const next = openIndex(route)
  if (next === -1) {
    finishRoute(server, db, plan, route, nowMs)
    return
  }
  route.stops[next]!.status = 'menuju'
  route.legStartedAt = iso(nowMs)
  route.stops = reanchorEtas(route, next, nowMs).stops
}

function finishRoute(server: MockServer, db: DB, plan: Plan, route: Route, nowMs: number): void {
  route.status = 'selesai'
  route.finishedAt = iso(nowMs)
  const driver = driverOf(db, route)
  if (driver) {
    driver.status = 'online'
    driver.completedTrips += 1
  }
  if (plan.routes.every((r) => r.status === 'selesai')) {
    plan.status = 'selesai'
    plan.finishedAt = iso(nowMs)
    touchPlan(plan, nowMs, msg('srv.ops.event.allDone'))
    server.notify(db, `customer:${plan.userId}`, msg('srv.ops.ntf.orderDoneTitle'), msg('srv.ops.ntf.orderDoneBody', { code: plan.code }), plan.id)
  }
}

/** Status plan diturunkan dari status rute-rutenya (dipanggil setelah assign/unassign/start). */
export function syncPlanStatus(plan: Plan): void {
  if (plan.status === 'dibatalkan' || plan.status === 'selesai' || plan.status === 'draft' || plan.status === 'opsi_siap') return
  if (plan.routes.some((r) => r.status === 'berjalan')) plan.status = 'berjalan'
  else if (plan.routes.length > 0 && plan.routes.every((r) => r.status === 'ditugaskan' || r.status === 'selesai')) plan.status = 'ditugaskan'
  else plan.status = 'menunggu_driver'
}

/**
 * Satu langkah autopilot untuk satu rute berjalan: tiba bila kendaraan sudah sampai di stop,
 * lalu selesai setelah waktu layanan lewat. Mengembalikan true bila ada perubahan.
 */
export function autopilotStep(server: MockServer, db: DB, plan: Plan, route: Route, arrivedFraction: number): boolean {
  const idx = openIndex(route)
  if (idx === -1) return false
  const stop = route.stops[idx]!
  const nowMs = server.now()
  if (stop.status === 'menuju' && arrivedFraction >= 1) {
    arriveStop(server, db, plan, route, stop)
    return true
  }
  if (stop.status === 'tiba') {
    const waited = nowMs - new Date(stop.arrivedAt ?? nowMs).getTime()
    if (waited >= dwellMin(stop.kind) * 60_000) {
      // penerima simulasi menyebut kodenya: stop antar selalu punya bukti, sama seperti saat driver menyelesaikannya
      if (stop.kind === 'antar') stop.proof = { kind: 'otp', at: iso(nowMs) }
      closeStop(server, db, plan, route, stop, 'selesai')
      return true
    }
  }
  return false
}
