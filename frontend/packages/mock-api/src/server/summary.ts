import type { Plan, PlanSummary, User, UserSummary } from '@loong/domain'
import { DEFAULT_LOCALE, msg, tFor, type Locale } from '@loong/i18n'
import type { DB } from '../db'

const ANOMALY_DELAY_MIN = 10

/**
 * Ringkasan satu plan, dihitung ulang tiap permintaan. `locale` = bahasa permintaan untuk teks pengganti yang
 * bertipe string ("Belum dipilih", "Pengguna"); bawaan Inggris.
 */
export function summarize(plan: Plan, db: DB, locale: Locale = DEFAULT_LOCALE): PlanSummary {
  const tr = tFor(locale)
  const user = db.users.find((u) => u.id === plan.userId)
  const selected = plan.optionSet?.options.find((o) => o.id === plan.selectedOptionId)
  const routes = plan.routes
  const stops = routes.flatMap((r) => r.stops)
  const first = routes[0]?.stops[0]?.waypoint.name ?? plan.shipments[0]?.pickup.name ?? tr('srv.ops.summary.notSelected')
  const lastShipment = plan.shipments.at(-1)
  const lastStopName = routes.at(-1)?.stops.at(-1)?.waypoint.name ?? lastShipment?.dropoff.name ?? tr('srv.ops.summary.notSelected')
  const extra = plan.shipments.length > 1 ? ` +${plan.shipments.length - 1}` : ''
  const totals = routes.length
    ? {
        distanceKm: Math.round(routes.reduce((s, r) => s + r.distanceKm, 0) * 10) / 10,
        durationMin: Math.max(...routes.map((r) => r.durationMin)),
      }
    : selected?.totals

  return {
    id: plan.id,
    code: plan.code,
    userId: plan.userId,
    customerName: user?.name ?? tr('srv.ops.summary.user'),
    status: plan.status,
    createdAt: plan.createdAt,
    updatedAt: plan.updatedAt,
    scheduleMode: plan.schedule.mode,
    startAt: routes[0]?.startAt ?? plan.schedule.window?.from,
    preset: plan.priority.preset,
    shipments: plan.shipments.length,
    stops: stops.length || plan.shipments.length * 2,
    fromLabel: first,
    toLabel: `${lastStopName}${extra}`,
    totalCostRp: plan.totalCostRp ?? selected?.totals.costRp,
    distanceKm: totals?.distanceKm,
    durationMin: totals?.durationMin,
    // satu entri per rute (dua rute motor = ['motor', 'motor']); konsumen menghitung dari sini
    vehicleTypes: (routes.length ? routes : (selected?.routes ?? [])).map((r) => r.vehicleType),
    driverNames: routes
      .map((r) => db.drivers.find((d) => d.id === r.driverId)?.name)
      .filter((n): n is string => !!n),
    routesWithoutDriver:
      plan.status === 'menunggu_driver' || plan.status === 'ditugaskan' || plan.status === 'berjalan'
        ? routes.filter((r) => !r.driverId && r.status !== 'selesai').length
        : 0,
    progress: {
      done: stops.filter((s) => s.status === 'selesai' || s.status === 'bermasalah').length,
      total: stops.length,
    },
    savingsPct: selected?.savings.pct,
    savingsRp: selected?.savings.costRp,
    draftStep: plan.draftStep,
    delayMin: Math.round(Math.max(0, ...routes.filter((r) => r.status !== 'selesai').map((r) => r.delayMin ?? 0))),
    problemStops: stops.filter((s) => s.status === 'bermasalah').length,
    hasAnomaly:
      routes.some((r) => (r.delayMin ?? 0) >= ANOMALY_DELAY_MIN && r.status !== 'selesai') ||
      stops.some((s) => s.status === 'bermasalah'),
  }
}

export function userSummary(user: User, db: DB): UserSummary {
  const plans = db.plans.filter((p) => p.userId === user.id && p.status !== 'draft' && p.status !== 'opsi_siap')
  const completed = plans.filter((p) => p.status === 'selesai')
  return {
    id: user.id,
    name: user.name,
    email: user.email,
    phone: user.phone,
    createdAt: user.createdAt,
    blocked: user.blocked,
    plansCount: plans.length,
    completedCount: completed.length,
    totalSpendRp: completed.reduce((s, p) => s + (p.totalCostRp ?? 0), 0),
    balanceRp: db.balances[user.id] ?? 0,
    lastPlanAt: plans.map((p) => p.createdAt).sort().at(-1),
  }
}

export const describeRoutes = (plan: Plan) => msg('srv.ops.summary.routes', { count: plan.routes.length })
