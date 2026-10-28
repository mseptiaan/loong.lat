import { beforeEach, describe, expect, it } from 'vitest'
import { offsetPoint, type ApiClient, type ComputeEvent, type Driver, type Plan, type Schedule, type Session, type Shipment } from '@loong/domain'
import { getPlace } from '@loong/gazetteer'
import { renderMsg, type Msg } from '@loong/i18n'
import { makeItem } from '../seed/catalog'
import { msgProblems } from '../test-msgs'
import { createApi } from './api'
import { DISPATCH_RADIUS_KM, syncCommitments, tickDispatch } from './dispatch'
import { arriveStop, closeStop } from './ops'
import { MockServer, memoryRawStorage } from './server'

const T0 = Date.parse('2026-10-20T03:00:00Z') // 10.00 WIB
const HOUR = 3_600_000
let t = T0
let server: MockServer
const DINA: Session = { role: 'customer', userId: 'usr-1', name: 'Dina Pramesti' }
const ADMIN: Session = { role: 'admin', userId: 'admin-1', name: 'Rani Wulandari' }
const as = (ctx: Session | null): ApiClient => createApi(server, ctx)
const asDriver = (d: Driver): ApiClient => as({ role: 'driver', userId: d.id, name: d.name })

const PICKUP = getPlace('kemang-village')!.position

const wp = (id: string) => {
  const p = getPlace(id)!
  return { name: p.name, address: p.address, position: p.position, placeId: p.id, contactName: 'Tes', contactPhone: '081200000000' }
}
const shipment = (items = ['paket-kecil']): Shipment => ({ id: 's1', pickup: wp('kemang-village'), dropoff: wp('plaza-senayan'), items: items.map((k) => makeItem(k)) })
const later = (ms: number): Schedule => ({ mode: 'terjadwal', window: { from: new Date(T0 + ms).toISOString(), to: new Date(T0 + ms + HOUR).toISOString() } })

/** Pengirim membuat dan mengonfirmasi satu pesanan (bayar tunai agar tidak bergantung pada saldo). */
async function order(opts: { schedule?: Schedule; items?: string[]; prefer?: 'satu-rute' } = {}): Promise<Plan> {
  const api = as(DINA)
  const draft = await api.plans.createDraft()
  await api.plans.saveDraft(draft.id, { shipments: [shipment(opts.items)], schedule: opts.schedule ?? { mode: 'sekarang' }, draftStep: 3 })
  let set
  for await (const e of api.plans.computeOptions(draft.id) as AsyncIterable<ComputeEvent>) if (e.type === 'done') set = e.optionSet
  const option = set!.options.find((o) => o.routes.length === 1) ?? set!.options[0]!
  await api.plans.chooseOption(draft.id, option.id)
  return api.plans.confirm(draft.id, 'tunai')
}

const db = () => server.db()
/** Teks Inggris sebuah pesan tersimpan (peristiwa, notifikasi). */
const en = (m: Msg) => renderMsg(m, 'en')
const hasKey = (m: Msg, prefix: string) => m.key.startsWith(prefix)
const driver = (id: string) => db().drivers.find((d) => d.id === id)!
const plan = (id: string) => db().plans.find((p) => p.id === id)!
const vehicleType = (d: Driver) => db().vehicles.find((v) => v.id === d.vehicleId)?.type

/** Semua driver offline kecuali yang disebut, yang diletakkan `km` dari titik ambil (arah berbeda-beda) dengan rating tertentu. */
function arrange(spec: { id: string; km: number; rating?: number; status?: Driver['status'] }[]) {
  server.mutate((d) => {
    for (const x of d.drivers) x.status = 'offline'
    spec.forEach((s, i) => {
      const dr = d.drivers.find((x) => x.id === s.id)!
      dr.status = s.status ?? 'online'
      dr.position = offsetPoint(PICKUP, s.km, 40 + i * 70)
      if (s.rating !== undefined) dr.rating = s.rating
    })
  })
}

let motors: string[] = []
let vans: string[] = []

beforeEach(() => {
  t = T0
  server = new MockServer({ storage: memoryRawStorage(), latency: [0, 0], realNow: () => t })
  server.db()
  // bersihkan antrean seed agar hanya pesanan di tes ini yang dipertimbangkan, dan semua driver kosong
  server.mutate((d) => {
    d.plans = d.plans.filter((p) => p.routes.length === 0 || p.status === 'selesai' || p.status === 'dibatalkan')
    for (const x of d.drivers) x.status = 'online'
  })
  motors = db().drivers.filter((d) => vehicleType(d) === 'motor').map((d) => d.id)
  vans = db().drivers.filter((d) => vehicleType(d) === 'blindvan').map((d) => d.id)
  expect(motors.length).toBeGreaterThanOrEqual(3)
  expect(vans.length).toBeGreaterThanOrEqual(1)
})

describe('pesanan sekarang: driver terdekat', () => {
  it('menugaskan driver terdekat yang online dan kosong saat konfirmasi, lengkap dengan alasan', async () => {
    const [a, b, c] = motors as [string, string, string]
    arrange([
      { id: a, km: 2 },
      { id: b, km: 5 },
      { id: c, km: 1 },
    ])
    const p = await order()
    const route = p.routes[0]!
    expect(route.driverId).toBe(c) // paling dekat
    expect(route.status).toBe('ditugaskan')
    expect(p.status).toBe('ditugaskan')
    expect(driver(c).status).toBe('bertugas')
    expect(driver(a).status).toBe('online')
    const last = p.events.at(-1)!.text
    expect(last.key).toBe('srv.ops.event.autoAssignedWhy')
    expect(en(last)).toMatch(/assigned automatically.*Nearest available/)
    expect(db().notifications.some((n) => n.audience === `driver:${c}` && n.title.key === 'srv.ops.ntf.newJobTitle' && n.planId === p.id)).toBe(true)
    expect(db().notifications.some((n) => n.audience === `customer:${DINA.userId}` && n.title.key === 'srv.ops.ntf.driverAssignedTitle' && n.planId === p.id)).toBe(true)
  })

  it('driver yang sudah punya tugas tidak dipilih lagi: pesanan kedua ke yang terdekat berikutnya', async () => {
    const [a, b, c] = motors as [string, string, string]
    arrange([
      { id: a, km: 2 },
      { id: b, km: 5 },
      { id: c, km: 1 },
    ])
    const first = await order()
    const second = await order()
    expect(first.routes[0]!.driverId).toBe(c)
    expect(second.routes[0]!.driverId).toBe(a)
    const third = await order()
    expect(third.routes[0]!.driverId).toBe(b)
  })

  it('di luar radius: pesanan menunggu dengan catatan sekali saja; begitu ada driver dalam radius, tick menugaskannya', async () => {
    const [a] = motors as [string]
    arrange([{ id: a, km: DISPATCH_RADIUS_KM * 2 }])
    const p = await order()
    expect(p.routes[0]!.driverId).toBeUndefined()
    expect(p.status).toBe('menunggu_driver')
    const noDriver = (events: Plan['events']) => events.filter((e) => hasKey(e.text, 'srv.ops.event.noDriver'))
    expect(noDriver(p.events)).toHaveLength(1)
    expect(noDriver(p.events)[0]!.text).toEqual({ key: 'srv.ops.event.noDriverNow', params: { vehicle: { key: 'srv.ops.vehicle.motor' } } })
    expect(en(noDriver(p.events)[0]!.text)).toMatch(/No suitable motorbike driver found near the pickup point/)

    expect(tickDispatch(server)).toBe(0)
    expect(tickDispatch(server)).toBe(0)
    expect(noDriver(plan(p.id).events)).toHaveLength(1) // tidak berulang

    server.mutate((d) => {
      d.drivers.find((x) => x.id === a)!.position = offsetPoint(PICKUP, 3, 10)
    })
    expect(tickDispatch(server)).toBe(1)
    expect(plan(p.id).routes[0]!.driverId).toBe(a)
    expect(tickDispatch(server)).toBe(0)
  })

  it('driver yang baru online langsung dipertimbangkan untuk pesanan yang menunggu', async () => {
    const [a] = motors as [string]
    arrange([{ id: a, km: 2, status: 'offline' }])
    const p = await order()
    expect(p.routes[0]!.driverId).toBeUndefined()
    await asDriver(driver(a)).driver.setOnline(true)
    expect(plan(p.id).routes[0]!.driverId).toBe(a)
  })

  it('kendaraan harus muat: barang besar menunggu blindvan walau ada motor dekat', async () => {
    const [m] = motors as [string]
    const [v] = vans as [string]
    arrange([
      { id: m, km: 1 },
      { id: v, km: 8, status: 'offline' },
    ])
    const p = await order({ items: ['rak'] })
    expect(p.routes[0]!.vehicleType).toBe('blindvan')
    expect(p.routes[0]!.driverId).toBeUndefined()
    server.mutate((d) => {
      d.drivers.find((x) => x.id === v)!.status = 'online'
    })
    expect(tickDispatch(server)).toBe(1)
    expect(plan(p.id).routes[0]!.driverId).toBe(v)
  })

  it('dua pesanan menunggu dan satu driver: hanya satu yang ditugaskan dalam satu putaran', async () => {
    const [a] = motors as [string]
    arrange([{ id: a, km: 40 }]) // di luar radius dulu agar keduanya menunggu
    const p1 = await order()
    const p2 = await order()
    server.mutate((d) => {
      d.drivers.find((x) => x.id === a)!.position = offsetPoint(PICKUP, 2, 0)
    })
    expect(tickDispatch(server)).toBe(1)
    const assigned = [plan(p1.id), plan(p2.id)].filter((p) => p.routes[0]!.driverId)
    expect(assigned).toHaveLength(1)
    expect(assigned[0]!.id).toBe(p1.id) // yang paling lama menunggu lebih dulu
  })
})

describe('pesanan terjadwal: driver yang cocok pada jamnya', () => {
  const tomorrow = () => later(24 * HOUR)

  it('memilih driver yang kosong pada jam itu dengan rating terbaik walau lebih jauh; belum mengikat driver sekarang', async () => {
    const [a, b] = motors as [string, string]
    arrange([
      { id: a, km: 8, rating: 4.95 },
      { id: b, km: 1, rating: 4.5 },
    ])
    const p = await order({ schedule: tomorrow() })
    const route = p.routes[0]!
    expect(route.driverId).toBe(a)
    expect(route.status).toBe('ditugaskan')
    expect(driver(a).status).toBe('online') // tugasnya masih besok
    const why = p.events.at(-1)!.text
    expect(why.key).toBe('srv.ops.event.autoAssignedWhy')
    expect(en(why)).toMatch(/Available \d\d:\d\d–\d\d:\d\d, rating 4\.95/)
    expect(Date.parse(route.startAt)).toBeGreaterThan(T0 + 20 * HOUR)
  })

  it('beban kerja dipertimbangkan: pesanan berikutnya di hari yang sama ke driver yang lebih longgar', async () => {
    const [a, b] = motors as [string, string]
    arrange([
      { id: a, km: 8, rating: 4.9 },
      { id: b, km: 1, rating: 4.5 },
    ])
    const first = await order({ schedule: later(24 * HOUR) }) // besok 10.00
    const second = await order({ schedule: later(28 * HOUR) }) // besok 14.00 (tidak bentrok)
    expect(first.routes[0]!.driverId).toBe(a)
    expect(second.routes[0]!.driverId).toBe(b)
  })

  it('jadwal yang bentrok tidak diberikan ke driver yang sama', async () => {
    const [a, b] = motors as [string, string]
    arrange([
      { id: a, km: 2, rating: 4.9 },
      { id: b, km: 9, rating: 4.4 },
    ])
    const first = await order({ schedule: later(24 * HOUR) })
    const clash = await order({ schedule: later(24 * HOUR + 20 * 60_000) })
    expect(first.routes[0]!.driverId).toBe(a)
    expect(clash.routes[0]!.driverId).toBe(b)
    // dengan hanya satu driver, jadwal yang bentrok menunggu
    arrange([{ id: a, km: 2 }])
    const third = await order({ schedule: later(24 * HOUR + 30 * 60_000) })
    expect(third.routes[0]!.driverId).toBeUndefined()
  })

  it('tugas besok tidak menghalangi pesanan sekarang untuk driver yang sama', async () => {
    const [a] = motors as [string]
    arrange([{ id: a, km: 2 }])
    const scheduled = await order({ schedule: later(24 * HOUR) })
    expect(scheduled.routes[0]!.driverId).toBe(a)
    const now = await order()
    expect(now.routes[0]!.driverId).toBe(a)
    expect(driver(a).status).toBe('bertugas')
  })

  it('driver menjadi "bertugas" ketika tugas terjadwalnya sudah dekat', async () => {
    const [a] = motors as [string]
    arrange([{ id: a, km: 2 }])
    await order({ schedule: later(24 * HOUR) })
    expect(driver(a).status).toBe('online')
    expect(syncCommitments(server)).toBe(0)
    t += 23.5 * HOUR // tugas mulai sekitar 30 menit lagi
    expect(syncCommitments(server)).toBe(1)
    expect(driver(a).status).toBe('bertugas')
  })

  it('driver offline: tugas yang belum dimulai kembali ke antrean dan dicarikan driver lain', async () => {
    const [a, b] = motors as [string, string]
    arrange([
      { id: a, km: 2, rating: 4.9 },
      { id: b, km: 3, rating: 4.4 },
    ])
    const p = await order({ schedule: later(24 * HOUR) })
    expect(p.routes[0]!.driverId).toBe(a)
    await asDriver(driver(a)).driver.setOnline(false)
    const released = plan(p.id)
    expect(released.events.some((e) => /offline/i.test(en(e.text)))).toBe(true)
    expect(released.routes[0]!.driverId).toBeUndefined()
    expect(driver(a).status).toBe('offline')
    expect(db().notifications.some((n) => n.audience === 'admin' && /queue/i.test(en(n.title)))).toBe(true)
    expect(tickDispatch(server)).toBe(1)
    expect(plan(p.id).routes[0]!.driverId).toBe(b)
  })
})

describe('pengaturan dan jalur manual', () => {
  it('dimatikan: pesanan menunggu, tick tidak menugaskan, admin tetap bisa menugaskan manual', async () => {
    const [a] = motors as [string]
    arrange([{ id: a, km: 2 }])
    await as(ADMIN).settings.update({ dispatch: { auto: false } })
    const p = await order()
    expect(p.routes[0]!.driverId).toBeUndefined()
    expect(p.status).toBe('menunggu_driver')
    expect(p.events.some((e) => hasKey(e.text, 'srv.ops.event.autoAssigned'))).toBe(false)
    expect(tickDispatch(server)).toBe(0)
    const assigned = await as(ADMIN).dispatch.assign(p.id, p.routes[0]!.id, a)
    expect(assigned.routes[0]!.driverId).toBe(a)
    expect(assigned.events.at(-1)!.text.key).toBe('srv.ops.event.assigned') // bukan "otomatis"
  })

  it('dinyalakan lagi: pesanan yang sudah menunggu langsung ditugaskan', async () => {
    const [a] = motors as [string]
    arrange([{ id: a, km: 2 }])
    await as(ADMIN).settings.update({ dispatch: { auto: false } })
    const p = await order()
    await as(ADMIN).settings.update({ dispatch: { auto: true } })
    expect(plan(p.id).routes[0]!.driverId).toBe(a)
    expect((await as(DINA).settings.get()).dispatch.auto).toBe(true)
  })

  it('pengaturan hanya bisa diubah admin, dan bagian AI tidak ikut berubah', async () => {
    await expect(as(DINA).settings.update({ dispatch: { auto: false } })).rejects.toMatchObject({ code: 'ditolak' })
    const next = await as(ADMIN).settings.update({ dispatch: { auto: false } })
    expect(next.dispatch.auto).toBe(false)
    expect(next.ai).toEqual({ vision: true, voice: true, intent: true, copilot: true })
  })

  it('"Tugaskan otomatis" dari admin memakai mesin yang sama walau pengaturan mati', async () => {
    const [a, b] = motors as [string, string]
    arrange([
      { id: a, km: 6 },
      { id: b, km: 2 },
    ])
    await as(ADMIN).settings.update({ dispatch: { auto: false } })
    const p = await order()
    const done = await as(ADMIN).dispatch.autoAssign(p.id)
    expect(done.routes[0]!.driverId).toBe(b)
    expect(hasKey(done.events.at(-1)!.text, 'srv.ops.event.autoAssigned')).toBe(true)
  })

  it('driver mulai rute terjadwal: status menjadi bertugas', async () => {
    const [a] = motors as [string]
    arrange([{ id: a, km: 2 }])
    const p = await order({ schedule: later(2 * HOUR) })
    expect(p.routes[0]!.driverId).toBe(a)
    t += 1.5 * HOUR // sudah dalam 45 menit sebelum jadwal
    await asDriver(driver(a)).driver.startRoute(p.id, p.routes[0]!.id)
    expect(driver(a).status).toBe('bertugas')
    expect(plan(p.id).routes[0]!.status).toBe('berjalan')
  })
})

describe('data lama tanpa pengaturan penugasan', () => {
  it('dianggap otomatis menyala', async () => {
    const [a] = motors as [string]
    arrange([{ id: a, km: 2 }])
    server.mutate((d) => {
      delete (d.settings as { dispatch?: unknown }).dispatch
    })
    const p = await order()
    expect(p.routes[0]!.driverId).toBe(a)
    expect((await as(ADMIN).settings.get()).dispatch.auto).toBe(true)
  })
})

describe('teks netral-bahasa (srv.ops.*)', () => {
  async function assigned() {
    const [a] = motors as [string]
    arrange([{ id: a, km: 2 }])
    const p = await order()
    return { a, p }
  }

  it('saran driver: alasan berupa Msg berparameter mentah yang tampil sesuai bahasa', async () => {
    const [a, b] = motors as [string, string]
    arrange([
      { id: a, km: 2 },
      { id: b, km: 5 },
    ])
    await as(ADMIN).settings.update({ dispatch: { auto: false } })
    const p = await order()
    const list = await as(ADMIN).dispatch.suggest(p.id, p.routes[0]!.id)
    expect(list.length).toBeGreaterThan(1)
    const top = list[0]!
    expect(top.reasons.map((r) => r.key)).toEqual(['srv.ops.sug.distance', 'srv.ops.sug.rating', 'srv.ops.sug.fits'])
    expect(top.reasons[0]!.params).toMatchObject({ km: expect.any(Number), eta: expect.any(Number) })
    expect(top.reasons[1]!.params).toMatchObject({ rating: expect.any(Number), count: expect.any(Number) })
    expect(en(top.reasons[0]!)).toMatch(/^\d+\.\d km from the pickup point, about \d+ min$/)
    expect(renderMsg(top.reasons[0]!, 'id')).toMatch(/^\d+,\d km dari titik ambil, sekitar \d+ mnt$/)
    expect(msgProblems(list)).toEqual([])
  })

  it('penugasan otomatis: peristiwa membawa alasan sebagai Msg bersarang, notifikasi terdaftar', async () => {
    const { p } = await assigned()
    const stored = plan(p.id)
    expect(stored.events.at(-1)!.text).toMatchObject({
      key: 'srv.ops.event.autoAssignedWhy',
      params: { driver: expect.any(String), plate: expect.any(String), reason: { key: 'srv.ops.pick.nearest', params: { km: expect.any(Number), eta: expect.any(Number) } } },
    })
    expect(msgProblems([stored.events, db().notifications])).toEqual([])
    const job = db().notifications.find((n) => n.title.key === 'srv.ops.ntf.newJobTitle' && n.planId === p.id)!
    expect(job.body.params).toMatchObject({ code: stored.code, count: stored.routes[0]!.stops.length, start: expect.any(Number) })
    expect(en(job.body)).toMatch(/^PL-\d+: 2 stops, starting \d\d:\d\d\.$/)
    expect(renderMsg(job.body, 'id')).toMatch(/^PL-\d+: 2 stop, mulai \d\d\.\d\d\.$/)
  })

  it('stop tiba, selesai, dan bermasalah: peristiwa, notifikasi, dan stop.issue berupa Msg', async () => {
    const { a, p } = await assigned()
    server.mutate((d) => {
      const pl = d.plans.find((x) => x.id === p.id)!
      const route = pl.routes[0]!
      route.status = 'berjalan'
      route.stops[0]!.status = 'menuju'
      arriveStop(server, d, pl, route, route.stops[0]!)
      closeStop(server, d, pl, route, route.stops[0]!, 'selesai')
      arriveStop(server, d, pl, route, route.stops[1]!)
      closeStop(server, d, pl, route, route.stops[1]!, 'bermasalah', { kind: 'lainnya', note: 'Gerbang terkunci' })
    })
    const stored = plan(p.id)
    const dropoff = stored.routes[0]!.stops[1]!
    const place = dropoff.waypoint.name
    const issue = { key: 'srv.ops.issue.withNote', params: { issue: { key: 'dom.issue.lainnya' }, note: 'Gerbang terkunci' } }
    expect(dropoff.issue).toEqual(issue)
    const keys = stored.events.map((e) => e.text.key)
    expect(keys).toEqual(expect.arrayContaining(['srv.ops.event.driverArrived', 'srv.ops.event.pickupDone', 'srv.ops.event.dropoffProblem', 'srv.ops.event.allDone']))
    const problem = stored.events.find((e) => e.text.key === 'srv.ops.event.dropoffProblem')!.text
    expect(problem).toEqual({ key: 'srv.ops.event.dropoffProblem', params: { place, issue } })
    expect(en(problem)).toBe(`Drop-off at ${place} had a problem: Other problem · Gerbang terkunci`)
    expect(renderMsg(problem, 'id')).toBe(`Pengantaran di ${place} bermasalah: Masalah lain · Gerbang terkunci`)

    const ntf = db().notifications.filter((n) => n.planId === p.id)
    expect(ntf.some((n) => n.audience === 'admin' && n.title.key === 'srv.ops.ntf.problemAdminTitle')).toBe(true)
    expect(ntf.some((n) => n.audience === `customer:${DINA.userId}` && n.title.key === 'srv.ops.ntf.problemCustomerTitle')).toBe(true)
    expect(ntf.some((n) => n.title.key === 'srv.ops.ntf.orderDoneTitle')).toBe(true)
    expect(driver(a).status).toBe('online')
    expect(msgProblems([stored, db().notifications])).toEqual([])
  })

  it('masalah berupa teks bebas lama (string) dibungkus sebagai Msg apa adanya', async () => {
    const { p } = await assigned()
    server.mutate((d) => {
      const pl = d.plans.find((x) => x.id === p.id)!
      const route = pl.routes[0]!
      route.status = 'berjalan'
      arriveStop(server, d, pl, route, route.stops[0]!)
      closeStop(server, d, pl, route, route.stops[0]!, 'bermasalah', 'Motor mogok')
    })
    const stop = plan(p.id).routes[0]!.stops[0]!
    expect(stop.issue).toEqual({ key: 'srv.ops.issue.text', params: { text: 'Motor mogok' } })
    expect(msgProblems(plan(p.id))).toEqual([])
  })

  it('galat penugasan manual adalah ApiError dengan Msg dan teks Inggris untuk log', async () => {
    const [a] = motors as [string]
    const [v] = vans as [string]
    arrange([
      { id: a, km: 2 },
      { id: v, km: 3 },
    ])
    await as(ADMIN).settings.update({ dispatch: { auto: false } })
    const p = await order()
    const route = p.routes[0]!
    await expect(as(ADMIN).dispatch.assign(p.id, route.id, v)).rejects.toMatchObject({
      code: 'bentrok',
      field: 'driverId',
      msg: { key: 'srv.ops.err.vehicleMismatch', params: { need: { key: 'srv.ops.vehicle.motor' }, have: { key: 'srv.ops.vehicle.blindvan' } } },
      message: expect.stringMatching(/^This route needs a motorbike, but .+ drives a blind van\.$/),
    })
    server.mutate((d) => {
      d.drivers.find((x) => x.id === a)!.status = 'offline'
    })
    await expect(as(ADMIN).dispatch.assign(p.id, route.id, a)).rejects.toMatchObject({ msg: { key: 'srv.ops.err.driverUnavailable' } })
    await expect(as(ADMIN).plans.get('pln-tidak-ada')).rejects.toMatchObject({ code: 'tidak_ditemukan' })
  })

  it('perintah lepas driver tanpa catatan mencatat rute yang dilepas', async () => {
    const { p } = await assigned()
    const route = p.routes[0]!
    await as(ADMIN).dispatch.unassign(p.id, route.id)
    expect(plan(p.id).events.at(-1)!.text).toEqual({ key: 'srv.ops.event.unassigned', params: { n: '1' } })
    expect(en(plan(p.id).events.at(-1)!.text)).toBe('Driver removed from route 1')
  })
})
