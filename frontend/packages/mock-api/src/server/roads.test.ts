import { beforeEach, describe, expect, it } from 'vitest'
import { routePositionAt, routeGeometry, type ApiClient, type ComputeEvent, type OptionSet, type Route, type Session, type Shipment } from '@loong/domain'
import { getPlace } from '@loong/gazetteer'
import { rebaseStart } from '../engine/instantiate'
import { createFakeRoads } from '../roads/fake'
import { makeItem } from '../seed/catalog'
import { createApi } from './api'
import { createRoadHydrator, routeWaypoints } from './roads'
import { MockServer, memoryRawStorage } from './server'

const T0 = Date.parse('2026-10-20T03:00:00Z') // 10.00 WIB
const DINA: Session = { role: 'customer', userId: 'usr-1', name: 'Dina Pramesti' }
let t = T0

const wp = (id: string) => {
  const p = getPlace(id)!
  return { name: p.name, address: p.address, position: p.position, placeId: p.id, contactName: 'Tes', contactPhone: '081200000000' }
}
const shipments = (): Shipment[] => [
  { id: 's1', pickup: wp('kemang-village'), dropoff: wp('plaza-senayan'), items: [makeItem('paket-kecil')] },
  { id: 's2', pickup: wp('kemang-raya'), dropoff: wp('senayan-city'), items: [makeItem('paket-kecil')] },
]

const newServer = (roads?: ReturnType<typeof createFakeRoads>['provider']) => {
  const server = new MockServer({ storage: memoryRawStorage(), latency: [0, 0], realNow: () => t, roads })
  server.db()
  return server
}

async function compute(server: MockServer): Promise<{ events: ComputeEvent[]; set: OptionSet; planId: string }> {
  const api: ApiClient = createApi(server, DINA)
  const draft = await api.plans.createDraft()
  await api.plans.saveDraft(draft.id, { shipments: shipments(), draftStep: 3 })
  const events: ComputeEvent[] = []
  for await (const e of api.plans.computeOptions(draft.id)) events.push(e)
  const done = events.at(-1)
  if (done?.type !== 'done') throw new Error('hitung tidak selesai')
  return { events, set: done.optionSet, planId: draft.id }
}

beforeEach(() => {
  t = T0
})

describe('computeOptions dengan data jalan', () => {
  it('mengukur jarak lewat matriks dan menempelkan jalur jalan ke setiap rute', async () => {
    const { provider, calls } = createFakeRoads({ factor: 2 })
    const server = newServer(provider)
    const { events, set, planId } = await compute(server)

    expect(calls.matrix).toBe(1)
    expect(calls.path).toBeGreaterThan(0)
    // tahapan jujur: ada langkah tambahan untuk menggambar rute, total langkah konsisten
    const progress = events.filter((e): e is Extract<ComputeEvent, { type: 'progress' }> => e.type === 'progress')
    expect(progress.map((p) => p.step)).toEqual([1, 2, 3, 4, 5])
    expect(new Set(progress.map((p) => p.total))).toEqual(new Set([5]))

    for (const option of set.options) {
      for (const route of option.routes) {
        expect(route.geometry?.length ?? 0).toBeGreaterThan(2)
        // jalur siku-siku dari fake, bukan lengkung halus: titik pertama = titik awal rute
        expect(route.geometry![0]).toEqual([route.startPosition.lng, route.startPosition.lat])
      }
    }
    // hasil yang dikirim ke klien = yang tersimpan (konfirmasi memakai jalur yang sama)
    expect(server.db().plans.find((p) => p.id === planId)?.optionSet).toEqual(set)
  })

  it('jarak total memakai matriks jalan (lebih panjang dari perkiraan bawaan) dan konsisten antar angka', async () => {
    const base = await compute(newServer())
    const { provider } = createFakeRoads({ factor: 2 })
    const real = await compute(newServer(provider))

    const pick = (s: OptionSet) => s.options.find((o) => o.recommended)!
    expect(pick(real.set).totals.distanceKm).toBeGreaterThan(pick(base.set).totals.distanceKm * 1.2)
    for (const option of real.set.options) {
      const sum = option.routes.reduce((s, r) => s + r.distanceKm, 0)
      expect(Math.abs(sum - option.totals.distanceKm)).toBeLessThan(0.2)
      for (const r of option.routes) {
        const legs = r.stops.slice(1).reduce((s, st) => s + st.legKm, 0)
        expect(Math.abs(legs - r.distanceKm)).toBeLessThan(0.5)
      }
    }
  })

  it('gagal (offline/429/token ditolak): opsi dan angka sama persis dengan tanpa penyedia, tanpa jalur jalan', async () => {
    const base = await compute(newServer())
    const { provider, calls } = createFakeRoads({ fail: true })
    const failed = await compute(newServer(provider))

    expect(calls.matrix).toBe(1)
    expect(failed.set.options.map((o) => o.totals)).toEqual(base.set.options.map((o) => o.totals))
    expect(failed.set.options.flatMap((o) => o.routes).every((r) => r.geometry === undefined)).toBe(true)
    // tanpa penyedia, langkahnya tetap 4
    expect(base.events.filter((e) => e.type === 'progress')).toHaveLength(4)
  })

  it('jalur tersimpan di DB dan ikut ke rute setelah konfirmasi; kendaraan bergerak mengikuti jalur itu', async () => {
    const { provider } = createFakeRoads()
    const server = newServer(provider)
    // tanpa penugasan otomatis: penugasan memindahkan titik awal rute ke posisi driver, sehingga jalurnya diambil ulang
    server.mutate((db) => {
      db.settings.dispatch.auto = false
    })
    const api = createApi(server, DINA)
    const { set, planId } = await compute(server)
    expect(server.db().plans.find((p) => p.id === planId)?.optionSet?.options[0]?.routes[0]?.geometry?.length).toBeGreaterThan(2)

    const option = set.options.find((o) => o.recommended)!
    await api.plans.chooseOption(planId, option.id)
    const confirmed = await api.plans.confirm(planId, 'saldo')
    const route = confirmed.routes[0]!
    expect(route.geometry?.length).toBeGreaterThan(2)

    // posisi di tengah ruas pertama berada pada jalur siku-siku (garis lurus bujur lalu lintang), bukan di garis lurus langsung
    const running: Route = { ...route, status: 'berjalan', legStartedAt: new Date(T0).toISOString(), stops: route.stops.map((s, i) => (i === 0 ? { ...s, status: 'menuju' } : s)) }
    const mid = routePositionAt(running, T0 + (running.stops[0]!.legMin * 60_000) / 2)
    const line = routeGeometry(running)
    const onLine = line.some(([lng, lat], i) => {
      const next = line[i + 1]
      if (!next) return false
      const within = (v: number, a: number, b: number) => v >= Math.min(a, b) - 1e-6 && v <= Math.max(a, b) + 1e-6
      const onSegment = (lng === next[0] && Math.abs(mid.position.lng - lng) < 1e-6 && within(mid.position.lat, lat, next[1])) || (lat === next[1] && Math.abs(mid.position.lat - lat) < 1e-6 && within(mid.position.lng, lng, next[0]))
      return onSegment
    })
    expect(onLine).toBe(true)
  })
})

describe('pengisian jalur jalan untuk data yang sudah ada', () => {
  const snapshot = (server: MockServer) =>
    Object.fromEntries(server.db().plans.flatMap((p) => p.routes.map((r) => [r.id, [r.distanceKm, r.durationMin, r.costRp, r.stops.map((s) => s.legKm)]])))

  it('mengisi seluruh rute tanpa mengubah jarak, durasi, atau biaya yang tercatat', async () => {
    const { provider, calls } = createFakeRoads()
    const server = newServer(provider)
    const before = snapshot(server)
    const missing = server.db().plans.flatMap((p) => p.routes).filter((r) => !r.geometry)
    expect(missing.length).toBeGreaterThan(5)

    const draftOptionRoutes = () => server.db().plans.filter((p) => p.status !== 'dibatalkan').flatMap((p) => p.optionSet?.options.flatMap((o) => o.routes) ?? [])
    expect(draftOptionRoutes().filter((r) => !r.geometry).length).toBeGreaterThan(0) // draft seed dengan opsi yang belum digambar di jalan

    const changes: string[] = []
    server.subscribe((e) => e.type === 'plans.changed' && changes.push(e.planId ?? ''))
    const hydrator = createRoadHydrator(server, provider, { perRun: 1000 })
    const filled = await hydrator.run()
    expect(filled).toBeGreaterThan(5)
    // rute yang titiknya sama dengan yang sudah diisi menyusul pada putaran berikutnya
    for (let i = 0; i < 5 && (await hydrator.run()) > 0; i++);

    const stillMissing = server.db().plans.filter((p) => p.status !== 'dibatalkan').flatMap((p) => p.routes).filter((r) => !r.geometry)
    expect(stillMissing).toHaveLength(0)
    expect(draftOptionRoutes().filter((r) => !r.geometry)).toHaveLength(0)
    expect(snapshot(server)).toEqual(before)
    expect(changes.length).toBeGreaterThan(0)

    const paths = calls.path
    expect(await hydrator.run()).toBe(0) // idempoten: tidak meminta lagi
    expect(calls.path).toBe(paths)
  })

  it('mendahulukan rute yang sedang berjalan', async () => {
    const { provider } = createFakeRoads()
    const server = newServer(provider)
    const order: string[] = []
    const spy = { ...provider, path: async (points: Parameters<typeof provider.path>[0]) => (order.push(points[0]!.lat.toFixed(5)), provider.path(points)) }
    const hydrator = createRoadHydrator(server, spy, { perRun: 1 })
    await hydrator.run()
    const firstFilled = server.db().plans.flatMap((p) => p.routes).find((r) => r.geometry)!
    expect(firstFilled.status).toBe('berjalan')
    expect(order).toHaveLength(1)
  })

  it('gagal diambil: dicoba lagi hanya setelah jeda, tanpa menggerus data', async () => {
    const { provider, calls } = createFakeRoads({ fail: true })
    const server = newServer(provider)
    const before = snapshot(server)
    const hydrator = createRoadHydrator(server, provider, { perRun: 1000 })
    expect(await hydrator.run()).toBe(0)
    const first = calls.path
    expect(first).toBeGreaterThan(0)
    await hydrator.run()
    expect(calls.path).toBe(first) // masih dalam jeda
    t += 2 * 60_000
    await hydrator.run()
    expect(calls.path).toBeGreaterThan(first)
    expect(snapshot(server)).toEqual(before)
  })

  it('jalur yang tiba setelah titik awal berpindah dibuang (tidak menimpa data baru)', async () => {
    const { provider } = createFakeRoads()
    const server = newServer(provider)
    const target = server.db().plans.filter((p) => p.status !== 'dibatalkan').flatMap((p) => p.routes).find((r) => r.status === 'menunggu_driver')!
    const slow = { ...provider, path: async (pts: Parameters<typeof provider.path>[0]) => {
      const line = await provider.path(pts)
      // titik awal berpindah selagi menunggu jawaban
      server.mutate((db) => {
        const r = db.plans.flatMap((p) => p.routes).find((x) => x.id === target.id)!
        r.startPosition = { lat: r.startPosition.lat + 0.01, lng: r.startPosition.lng }
      })
      return line
    } }
    const hydrator = createRoadHydrator(server, slow, { perRun: 1000 })
    await hydrator.run()
    const routes = server.db().plans.flatMap((p) => p.routes)
    expect(routes.find((r) => r.id === target.id)!.geometry).toBeUndefined()
    // pembanding: rute lain pada putaran yang sama tetap terisi, jadi tes ini tidak lolos karena alasan lain
    expect(routes.filter((r) => r.geometry).length).toBeGreaterThan(5)
  })
})

describe('rebaseStart dan jalur jalan', () => {
  const route = (): Route => {
    const server = newServer()
    return structuredClone(server.db().plans.flatMap((p) => p.routes).find((r) => r.stops.length > 1)!)
  }

  it('titik awal berpindah: jalur lama dibuang', () => {
    const r = { ...route(), geometry: routeWaypoints(route()).map((p) => [p.lng, p.lat] as [number, number]) }
    const moved = rebaseStart(r, { lat: r.startPosition.lat + 0.02, lng: r.startPosition.lng }, T0)
    expect(moved.geometry).toBeUndefined()
  })

  it('titik awal sama (atau bergeser kurang dari 20 m): jalur dipertahankan', () => {
    const r = { ...route(), geometry: routeWaypoints(route()).map((p) => [p.lng, p.lat] as [number, number]) }
    expect(rebaseStart(r, r.startPosition, T0).geometry).toBe(r.geometry)
    expect(rebaseStart(r, { lat: r.startPosition.lat + 0.00005, lng: r.startPosition.lng }, T0).geometry).toBe(r.geometry)
  })
})
