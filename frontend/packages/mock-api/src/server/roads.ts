import type { LatLng, LngLat, OptionSet, Route } from '@loong/domain'
import type { DB } from '../db'
import type { MockServer } from './server'
import { pointKey, type RoadProvider } from '../roads/provider'

/** Titik yang dilalui rute: titik awal lalu tiap stop. */
export const routeWaypoints = (route: Pick<Route, 'startPosition' | 'stops'>): LatLng[] => [
  route.startPosition,
  ...route.stops.map((s) => s.waypoint.position),
]

const routeKey = (route: Pick<Route, 'startPosition' | 'stops'>) => routeWaypoints(route).map(pointKey).join(';')

/**
 * Menempelkan jalur jalan ke setiap rute pada `set` (dimutasi). Rute yang titiknya sama di beberapa opsi hanya
 * diminta sekali. Rute yang gagal diambil dibiarkan tanpa `geometry` (digambar sebagai garis halus).
 * Mengembalikan jumlah rute yang berhasil mendapat jalur jalan.
 */
export async function attachRoadPaths(set: OptionSet, roads: RoadProvider): Promise<number> {
  const routes = set.options.flatMap((o) => o.routes)
  const pending = new Map<string, Promise<LngLat[] | null>>()
  for (const r of routes) {
    const key = routeKey(r)
    if (!pending.has(key)) pending.set(key, roads.path(routeWaypoints(r)))
  }
  let attached = 0
  for (const r of routes) {
    const line = await pending.get(routeKey(r))
    if (line) {
      r.geometry = line
      attached += 1
    }
  }
  return attached
}

const STATUS_PRIORITY: Partial<Record<Route['status'], number>> = { berjalan: 0, ditugaskan: 1, menunggu_driver: 2, selesai: 3 }

/** Jeda sebelum rute yang gagal diambil dicoba lagi. */
const RETRY_MS = 90_000

/**
 * Mengisi `geometry` rute di DB yang belum punya jalur jalan (data seed, rute yang titik awalnya berpindah karena
 * driver ditugaskan, plan buatan generator). Jarak, ETA, dan biaya yang sudah tercatat TIDAK diubah.
 * Dijalankan oleh instance host yang memegang lock autopilot, jadi tidak ada permintaan ganda antar tab.
 */
export function createRoadHydrator(server: MockServer, roads: RoadProvider, opts: { perRun?: number } = {}) {
  const perRun = opts.perRun ?? 6
  const retryAt = new Map<string, number>()
  let running = false

  /** `find` mencari ulang rute di DB terbaru (rute bisa berubah selama menunggu jawaban penyedia). */
  type Todo = { planId: string; key: string; points: LatLng[]; priority: number; find: (db: DB) => Route | undefined }

  function collect(nowMs: number): Todo[] {
    const todo: Todo[] = []
    const add = (t: Todo, route: Route) => {
      if (route.geometry || route.stops.length === 0 || (retryAt.get(t.key) ?? 0) > nowMs) return
      todo.push(t)
    }
    for (const plan of server.db().plans) {
      if (plan.status === 'dibatalkan') continue
      for (const route of plan.routes) {
        const key = routeKey(route)
        add({ planId: plan.id, key, points: routeWaypoints(route), priority: STATUS_PRIORITY[route.status] ?? 4, find: (db) => db.plans.find((p) => p.id === plan.id)?.routes.find((r) => r.id === route.id) }, route)
      }
      // draft yang opsinya sudah dihitung (mis. seed): rute di tiap opsi juga digambar di jalan, paling akhir
      plan.optionSet?.options.forEach((option) =>
        option.routes.forEach((route) => {
          const key = routeKey(route)
          add(
            {
              planId: plan.id,
              key,
              points: routeWaypoints(route),
              priority: 5,
              find: (db) => db.plans.find((p) => p.id === plan.id)?.optionSet?.options.find((o) => o.id === option.id)?.routes.find((r) => r.id === route.id),
            },
            route,
          )
        }),
      )
    }
    // satu permintaan per kumpulan titik: rute yang sama di beberapa tempat diisi pada putaran berikutnya (dari cache penyedia)
    const seen = new Set<string>()
    return todo.sort((a, b) => a.priority - b.priority).filter((t) => !seen.has(t.key) && !!seen.add(t.key))
  }

  /** @returns jumlah rute yang berhasil diisi pada putaran ini. */
  async function run(): Promise<number> {
    if (running) return 0
    running = true
    try {
      const nowMs = server.realNow()
      const batch = collect(nowMs).slice(0, perRun)
      if (!batch.length) return 0
      const results = await Promise.all(batch.map(async (t) => ({ t, line: await roads.path(t.points) })))
      for (const { t, line } of results) if (!line) retryAt.set(t.key, nowMs + RETRY_MS)
      if (!results.some((r) => r.line)) return 0
      let filled = 0
      const touched = new Set<string>()
      server.mutate((db) => {
        for (const { t, line } of results) {
          if (!line) continue
          const route = t.find(db)
          // titik awal bisa berpindah selama menunggu jawaban (driver ditugaskan): jalur lama sudah tak cocok
          if (!route || route.geometry || routeKey(route) !== t.key) continue
          route.geometry = line
          touched.add(t.planId)
          filled += 1
        }
      })
      for (const planId of touched) server.emit({ type: 'plans.changed', planId })
      return filled
    } finally {
      running = false
    }
  }

  return { run }
}
