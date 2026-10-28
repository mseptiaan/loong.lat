import { beforeEach, describe, expect, it } from 'vitest'
import {
  ApiError,
  routePositionAt,
  type ApiClient,
  type AppEvent,
  type ComputeEvent,
  type Plan,
  type Session,
  type Shipment,
} from '@loong/domain'
import { getPlace } from '@loong/gazetteer'
import { renderMsg, type Locale } from '@loong/i18n'
import { makeItem } from '../seed/catalog'
import { createApi, tickAutopilot } from './api'
import { MockServer, memoryRawStorage } from './server'

const T0 = Date.parse('2026-10-20T03:00:00Z') // 10.00 WIB

let t = T0
let server: MockServer
const DINA: Session = { role: 'customer', userId: 'usr-1', name: 'Dina Pramesti' }
const OTHER: Session = { role: 'customer', userId: 'usr-2', name: 'Fajar Nugroho' }
const ADMIN: Session = { role: 'admin', userId: 'admin-1', name: 'Rani Wulandari' }
const budi: Session = { role: 'driver', userId: 'drv-1', name: 'Budi Santoso' }

const as = (ctx: Session | null, locale: Locale = 'en'): ApiClient => createApi(server, ctx, locale)

function wp(id: string) {
  const p = getPlace(id)!
  return { name: p.name, address: p.address, position: p.position, placeId: p.id, contactName: 'Tes', contactPhone: '081200000000' }
}
const shipment = (id: string, from: string, to: string, items = ['paket-kecil']): Shipment => ({
  id,
  pickup: wp(from),
  dropoff: wp(to),
  items: items.map((k) => makeItem(k)),
})

async function computeAll(api: ApiClient, planId: string): Promise<ComputeEvent[]> {
  const out: ComputeEvent[] = []
  for await (const e of api.plans.computeOptions(planId)) out.push(e)
  return out
}

/** Pengirim membuat plan sampai terkonfirmasi. */
async function placeOrder(ctx: Session = DINA, shipments = [shipment('s1', 'kemang-village', 'plaza-senayan'), shipment('s2', 'kemang-raya', 'senayan-city')]) {
  const api = as(ctx)
  const draft = await api.plans.createDraft()
  await api.plans.saveDraft(draft.id, { shipments, draftStep: 3 })
  const events = await computeAll(api, draft.id)
  const done = events.at(-1)
  if (done?.type !== 'done') throw new Error('hitung tidak selesai')
  const option = done.optionSet.options.find((o) => o.recommended) ?? done.optionSet.options[0]!
  await api.plans.chooseOption(draft.id, option.id)
  return api.plans.confirm(draft.id, 'saldo')
}

beforeEach(() => {
  t = T0
  server = new MockServer({ storage: memoryRawStorage(), latency: [0, 0], realNow: () => t })
  server.db() // seed
  // alur di berkas ini menguji penugasan MANUAL oleh admin; penugasan otomatis diuji di dispatch.test.ts
  server.mutate((db) => {
    db.settings.dispatch.auto = false
  })
})

describe('auth', () => {
  it('masuk dengan kredensial demo untuk tiga peran', async () => {
    const api = as(null)
    expect(await api.auth.loginCustomer('dina@contoh.id', 'demo1234')).toMatchObject({ role: 'customer', userId: 'usr-1' })
    expect(await api.auth.loginAdmin('admin@loong.lat', 'admin1234')).toMatchObject({ role: 'admin' })
    expect(await api.auth.loginDriver('0812-3456-7801', '123456')).toMatchObject({ role: 'driver', userId: 'drv-1' })
  })

  it('menolak kredensial salah dengan pesan yang jelas', async () => {
    await expect(as(null).auth.loginCustomer('dina@contoh.id', 'salah')).rejects.toThrow('does not match')
    await expect(as(null).auth.loginDriver('0812-3456-7801', '000000')).rejects.toBeInstanceOf(ApiError)
  })

  it('daftar: validasi per kolom, email unik, lalu bisa masuk', async () => {
    const api = as(null)
    const base = { name: 'Tia Rahmawati', email: 'tia@contoh.id', phone: '081355550000', password: 'rahasia123' }
    await expect(api.auth.register({ ...base, password: 'pendek' })).rejects.toMatchObject({ field: 'password' })
    await expect(api.auth.register({ ...base, email: 'bukan-email' })).rejects.toMatchObject({ field: 'email' })
    await expect(api.auth.register({ ...base, email: 'dina@contoh.id' })).rejects.toMatchObject({ code: 'bentrok', field: 'email' })
    const session = await api.auth.register(base)
    expect(session.role).toBe('customer')
    expect(await api.auth.loginCustomer('tia@contoh.id', 'rahasia123')).toMatchObject({ name: 'Tia Rahmawati' })
  })

  it('akun diblokir tidak bisa masuk', async () => {
    await as(ADMIN).users.setBlocked('usr-2', true)
    await expect(as(null).auth.loginCustomer('fajar.nugroho@contoh.id', 'demo1234')).rejects.toMatchObject({ code: 'ditolak' })
  })
})

describe('alur pengirim', () => {
  it('draft → hitung (progres berurutan) → pilih → konfirmasi', async () => {
    const api = as(DINA)
    const draft = await api.plans.createDraft()
    expect(draft.status).toBe('draft')
    await api.plans.saveDraft(draft.id, { shipments: [shipment('s1', 'kemang-village', 'plaza-senayan')], draftStep: 3 })

    const events = await computeAll(api, draft.id)
    const progress = events.filter((e) => e.type === 'progress')
    expect(progress.map((e) => e.type === 'progress' && e.step)).toEqual([1, 2, 3, 4])
    expect(events.at(-1)?.type).toBe('done')

    const after = await api.plans.get(draft.id)
    expect(after.status).toBe('opsi_siap')
    const opt = after.optionSet!.options[0]!
    await api.plans.chooseOption(draft.id, opt.id)
    const confirmed = await api.plans.confirm(draft.id, 'transfer')
    expect(confirmed.status).toBe('menunggu_driver')
    expect(confirmed.routes).toHaveLength(opt.routes.length)
    expect(confirmed.totalCostRp).toBe(opt.totals.costRp)
    // kode OTP hanya untuk stop antar
    for (const s of confirmed.routes.flatMap((r) => r.stops)) {
      expect(Boolean(s.deliveryCode)).toBe(s.kind === 'antar')
    }
  })

  it('masukan tidak lengkap ditolak saat menghitung, dengan kolom yang bermasalah', async () => {
    const api = as(DINA)
    const draft = await api.plans.createDraft()
    await expect(computeAll(api, draft.id)).rejects.toMatchObject({ code: 'tidak_valid', field: 'shipments' })
    const s = shipment('s1', 'kemang-village', 'plaza-senayan')
    s.items = []
    await api.plans.saveDraft(draft.id, { shipments: [s] })
    await expect(computeAll(api, draft.id)).rejects.toMatchObject({ field: 'shipments.0.items' })
  })

  it('mengubah barang membuat opsi lama basi sehingga tidak bisa dikonfirmasi', async () => {
    const api = as(DINA)
    const draft = await api.plans.createDraft()
    await api.plans.saveDraft(draft.id, { shipments: [shipment('s1', 'kemang-village', 'plaza-senayan')] })
    await computeAll(api, draft.id)
    expect((await api.plans.get(draft.id)).optionSet).toBeDefined()
    await api.plans.saveDraft(draft.id, { shipments: [shipment('s1', 'kemang-village', 'plaza-senayan', ['galon'])] })
    const stale = await api.plans.get(draft.id)
    expect(stale.optionSet).toBeUndefined()
    expect(stale.status).toBe('draft')
    await expect(api.plans.confirm(draft.id, 'saldo')).rejects.toMatchObject({ code: 'bentrok' })
  })

  it('pengirim hanya melihat plan miliknya; admin melihat semua yang sudah dikonfirmasi', async () => {
    const mine = await as(DINA).plans.list()
    expect(mine.every((p) => p.userId === 'usr-1')).toBe(true)
    const theirs = mine.find((p) => p.userId === 'usr-1')!
    await expect(as(OTHER).plans.get(theirs.id)).rejects.toMatchObject({ code: 'ditolak' })
    const adminList = await as(ADMIN).plans.list()
    expect(adminList.some((p) => p.status === 'draft' || p.status === 'opsi_siap')).toBe(false)
    expect(adminList.length).toBeGreaterThan(mine.length)
  })

  it('draft dan plan terkonfirmasi: aturan hapus & batal', async () => {
    const api = as(DINA)
    const draft = await api.plans.createDraft()
    await api.plans.remove(draft.id)
    await expect(api.plans.get(draft.id)).rejects.toMatchObject({ code: 'tidak_ditemukan' })

    const plan = await placeOrder()
    await expect(api.plans.remove(plan.id)).rejects.toMatchObject({ code: 'bentrok' })
    const cancelled = await api.plans.cancel(plan.id, 'Salah alamat')
    expect(cancelled.status).toBe('dibatalkan')
    expect(cancelled.cancelReason).toBe('Salah alamat')
    await expect(api.plans.cancel(plan.id)).rejects.toMatchObject({ code: 'bentrok' })
  })

  it('jadwal ulang: minimal 20 menit ke depan', async () => {
    const api = as(DINA)
    const plan = await placeOrder()
    const soon = new Date(T0 + 5 * 60_000).toISOString()
    await expect(api.plans.reschedule(plan.id, { mode: 'terjadwal', window: { from: soon, to: soon } })).rejects.toMatchObject({ field: 'schedule' })
    const later = new Date(T0 + 3 * 3_600_000).toISOString()
    const updated = await api.plans.reschedule(plan.id, { mode: 'terjadwal', window: { from: later, to: later } })
    expect(updated.schedule.mode).toBe('terjadwal')
    expect(new Date(updated.routes[0]!.startAt).getTime()).toBe(T0 + 3 * 3_600_000)
  })
})

describe('dispatch → driver → selesai', () => {
  async function fullRun() {
    const plan = await placeOrder()
    const admin = as(ADMIN)
    const route = plan.routes[0]!
    const suggestions = await admin.dispatch.suggest(plan.id, route.id)
    expect(suggestions.length).toBeGreaterThan(0)
    expect(suggestions[0]!.score).toBeGreaterThanOrEqual(suggestions.at(-1)!.score)
    return { plan, admin, route, best: suggestions[0]! }
  }

  it('saran driver hanya yang online dengan tipe kendaraan sama, terurut skor', async () => {
    const { best, route, admin, plan } = await fullRun()
    const all = await admin.dispatch.suggest(plan.id, route.id)
    expect(all.every((s) => s.vehicleType === route.vehicleType)).toBe(true)
    expect(all.every((s) => s.fits)).toBe(true)
    expect(best.reasons.length).toBeGreaterThan(1)
  })

  it('assign → driver melihat tugas; tolak driver yang salah tipe/tidak bebas', async () => {
    const { plan, admin, route, best } = await fullRun()
    // driver blindvan untuk rute motor ditolak, tanpa merusak state
    await expect(admin.dispatch.assign(plan.id, route.id, 'drv-6')).rejects.toMatchObject({ code: 'bentrok', field: 'driverId' })
    expect((await admin.plans.get(plan.id)).routes[0]!.driverId).toBeUndefined()
    // driver yang sedang bertugas ditolak
    await expect(admin.dispatch.assign(plan.id, route.id, 'drv-1')).rejects.toMatchObject({ code: 'bentrok' })

    const assigned = await admin.dispatch.assign(plan.id, route.id, best.driverId)
    expect(assigned.status).toBe('ditugaskan')
    expect(assigned.routes[0]!.status).toBe('ditugaskan')

    const drivers = await admin.fleet.drivers.list()
    expect(drivers.find((d) => d.id === best.driverId)!.status).toBe('bertugas')

    const driverApi = as({ role: 'driver', userId: best.driverId, name: best.driverName })
    const mine = await driverApi.driver.routes()
    expect(mine.map((r) => r.planId)).toContain(plan.id)

    // lepas driver mengembalikan semuanya
    const released = await admin.dispatch.unassign(plan.id, route.id)
    expect(released.status).toBe('menunggu_driver')
    expect((await admin.fleet.drivers.list()).find((d) => d.id === best.driverId)!.status).toBe('online')
  })

  it('autoAssign menugaskan semua rute yang belum punya driver', async () => {
    const plan = await placeOrder()
    const assigned = await as(ADMIN).dispatch.autoAssign(plan.id)
    expect(assigned.routes.every((r) => r.driverId)).toBe(true)
    expect(assigned.status).toBe('ditugaskan')
    await expect(as(ADMIN).dispatch.autoAssign(plan.id)).rejects.toMatchObject({ code: 'bentrok' })
  })

  it('driver menjalankan rute: urutan wajib, OTP divalidasi, plan selesai di akhir', async () => {
    const { plan, admin, route, best } = await fullRun()
    await admin.dispatch.assign(plan.id, route.id, best.driverId)
    const dctx: Session = { role: 'driver', userId: best.driverId, name: best.driverName }
    const d = as(dctx)

    await expect(d.driver.arrive(plan.id, route.id, route.stops[0]!.id)).rejects.toMatchObject({ code: 'bentrok' }) // belum mulai
    const started = await d.driver.startRoute(plan.id, route.id)
    expect(started.status).toBe('berjalan')
    expect(started.routes[0]!.stops[0]!.status).toBe('menuju')

    const customerView = await as(DINA).plans.get(plan.id)
    const codeOf = (stopId: string) => customerView.routes[0]!.stops.find((s) => s.id === stopId)!.deliveryCode

    for (const [i, stop] of route.stops.entries()) {
      // tidak boleh melompat ke stop berikutnya
      if (i + 1 < route.stops.length) {
        await expect(d.driver.arrive(plan.id, route.id, route.stops[i + 1]!.id)).rejects.toMatchObject({ code: 'bentrok' })
      }
      await expect(d.driver.complete(plan.id, route.id, stop.id)).rejects.toMatchObject({ code: 'bentrok' }) // belum tiba
      await d.driver.arrive(plan.id, route.id, stop.id)
      if (stop.kind === 'antar') {
        await expect(d.driver.complete(plan.id, route.id, stop.id)).rejects.toMatchObject({ field: 'proof' })
        await expect(d.driver.complete(plan.id, route.id, stop.id, { kind: 'otp', value: '0000' })).rejects.toMatchObject({ field: 'proof' })
        await d.driver.complete(plan.id, route.id, stop.id, { kind: 'otp', value: codeOf(stop.id) })
      } else {
        await d.driver.complete(plan.id, route.id, stop.id)
      }
    }

    const final = await as(DINA).plans.get(plan.id)
    expect(final.status).toBe('selesai')
    expect(final.finishedAt).toBeDefined()
    expect(final.routes[0]!.status).toBe('selesai')
    // bukti OTP tercatat di tiap stop antar, tanpa menyimpan kodenya
    for (const s of final.routes[0]!.stops) {
      if (s.kind === 'antar') expect(s.proof).toEqual({ kind: 'otp', at: s.completedAt })
      else expect(s.proof).toBeUndefined()
    }
    // driver kembali bebas dan riwayat/pendapatan bertambah
    expect((await admin.fleet.drivers.list()).find((x) => x.id === best.driverId)!.status).toBe('online')
    const history = await d.driver.history()
    expect(history[0]!.planId).toBe(plan.id)
    expect(history[0]!.earningsRp).toBeGreaterThan(0)
    const earnings = await d.driver.earnings()
    expect(earnings.byDay).toHaveLength(7)
    expect(earnings.todayRp).toBeGreaterThanOrEqual(history[0]!.earningsRp)
    // notifikasi sampai ke pengirim
    const notes = await as(DINA).notifications.list()
    expect(notes.some((n) => n.planId === plan.id && n.title.key === 'srv.ops.ntf.orderDoneTitle')).toBe(true)
  })

  it('bukti foto: foto dan ringkasan AI tersimpan di stop antar; foto tak valid ditolak', async () => {
    const { plan, admin, route, best } = await fullRun()
    await admin.dispatch.assign(plan.id, route.id, best.driverId)
    const d = as({ role: 'driver', userId: best.driverId, name: best.driverName })
    await d.driver.startRoute(plan.id, route.id)
    const photo = `data:image/jpeg;base64,${'/9j/4AAQSkZJRg'.repeat(20)}`

    let last: Plan = plan
    for (const stop of route.stops) {
      await d.driver.arrive(plan.id, route.id, stop.id)
      if (stop.kind === 'antar') {
        await expect(d.driver.complete(plan.id, route.id, stop.id, { kind: 'foto', value: 'paket.jpg' })).rejects.toMatchObject({ field: 'proof' })
        await expect(d.driver.complete(plan.id, route.id, stop.id, { kind: 'foto', value: 'paket.jpg', photo: 'data:text/html;base64,PGh0bWw+' })).rejects.toMatchObject({ field: 'proof' })
        const err = await d.driver.complete(plan.id, route.id, stop.id, { kind: 'foto', value: 'x.jpg', photo: 'bukan-gambar' }).catch((e: unknown) => e)
        expect(err).toBeInstanceOf(ApiError)
        expect((err as ApiError).msg).toMatchObject({ key: 'srv.api.proofPhotoInvalid' })
        expect((await as(DINA).plans.get(plan.id)).routes[0]!.stops.find((s) => s.id === stop.id)!.status).toBe('tiba') // belum tertutup
        last = await d.driver.complete(plan.id, route.id, stop.id, { kind: 'foto', value: 'paket-diterima.jpg', photo })
      } else {
        last = await d.driver.complete(plan.id, route.id, stop.id)
      }
    }

    const final = await as(DINA).plans.get(plan.id)
    expect(final.status).toBe('selesai')
    expect(last.id).toBe(plan.id)
    const delivered = final.routes[0]!.stops.filter((s) => s.kind === 'antar')
    expect(delivered.length).toBeGreaterThan(0)
    for (const s of delivered) {
      expect(s.proof?.kind).toBe('foto')
      expect(s.proof?.photo).toBe(photo)
      expect(s.proof?.analysis?.checks.map((c) => c.key)).toEqual(['recipient', 'parcel', 'quality'])
      expect(s.proof?.analysis?.summary.key).toBe('srv.proof.summary.holding')
      expect(s.proof?.at).toBe(s.completedAt)
    }
    // pengambilan tidak menyimpan bukti antar
    expect(final.routes[0]!.stops.filter((s) => s.kind === 'ambil').every((s) => s.proof === undefined)).toBe(true)
  })

  it('lapor masalah menutup stop sebagai bermasalah, memberi tahu admin, dan rute tetap lanjut', async () => {
    const { plan, admin, route, best } = await fullRun()
    await admin.dispatch.assign(plan.id, route.id, best.driverId)
    const d = as({ role: 'driver', userId: best.driverId, name: best.driverName })
    await d.driver.startRoute(plan.id, route.id)
    await expect(d.driver.reportIssue(plan.id, route.id, route.stops[0]!.id, 'lainnya')).rejects.toMatchObject({ field: 'note' })
    const after = await d.driver.reportIssue(plan.id, route.id, route.stops[0]!.id, 'tidak_ada_orang')
    expect(after.routes[0]!.stops[0]!.status).toBe('bermasalah')
    expect(after.routes[0]!.stops[1]!.status).toBe('menuju')
    const summary = (await admin.plans.list()).find((p) => p.id === plan.id)!
    expect(summary.hasAnomaly).toBe(true)
    expect((await admin.notifications.list()).some((n) => n.planId === plan.id && n.title.key === 'srv.ops.ntf.problemAdminTitle')).toBe(true)
  })

  it('plan yang sedang berjalan tidak bisa dibatalkan pengirim', async () => {
    const running = (await as(DINA).plans.list()).find((p) => p.status === 'berjalan')!
    await expect(as(DINA).plans.cancel(running.id)).rejects.toMatchObject({ code: 'bentrok' })
  })

  it('izin: pengirim tidak bisa menugaskan driver, driver tidak bisa membaca plan lain', async () => {
    const plan = await placeOrder()
    await expect(as(DINA).dispatch.assign(plan.id, plan.routes[0]!.id, 'drv-4')).rejects.toMatchObject({ code: 'ditolak' })
    await expect(as(null).plans.list()).rejects.toMatchObject({ code: 'ditolak', status: 401 })
    await expect(as(budi).plans.get(plan.id)).rejects.toMatchObject({ code: 'ditolak' })
  })
})

describe('jam virtual, posisi, dan autopilot', () => {
  it('rute seed yang berjalan bergerak maju saat waktu bergeser', async () => {
    const live = await as(ADMIN).live.routes()
    const running = live.find((l) => l.route.status === 'berjalan')!
    const before = routePositionAt(running.route, server.now())
    await as(ADMIN).demo.advance(3)
    const after = routePositionAt(running.route, server.now())
    expect(server.now()).toBe(T0 + 3 * 60_000)
    // sama ruas → progres naik; atau sudah pindah ke ruas berikutnya
    expect(after.stopIndex > before.stopIndex || after.legFraction > before.legFraction).toBe(true)
  })

  it('kecepatan waktu 60x mempercepat jam virtual tanpa menggeser waktu saat ini', async () => {
    await as(ADMIN).demo.setSpeed(60)
    const t1 = server.now()
    t += 10_000 // 10 detik nyata
    expect(server.now() - t1).toBe(600_000) // 10 menit virtual
    await expect(as(ADMIN).demo.setSpeed(7)).rejects.toMatchObject({ code: 'tidak_valid' })
  })

  it('autopilot menyelesaikan rute berjalan sampai plan selesai', async () => {
    const admin = as(ADMIN)
    const running = (await admin.live.routes()).find((l) => l.route.status === 'berjalan')!
    await admin.demo.setAutopilot(true)
    let guard = 0
    while (guard++ < 400) {
      const plan = await admin.plans.get(running.planId)
      if (plan.status === 'selesai') break
      t += 60_000 // maju 1 menit tiap iterasi
      tickAutopilot(server)
    }
    const done = await admin.plans.get(running.planId)
    expect(done.status).toBe('selesai')
    expect(done.routes.every((r) => r.status === 'selesai')).toBe(true)
    expect(done.routes.flatMap((r) => r.stops).every((s) => s.status === 'selesai')).toBe(true)
  })

  it('autopilot mati: tick tidak mengubah apa pun', () => {
    expect(tickAutopilot(server)).toBe(0)
  })

  it('suntik keterlambatan: ETA mundur, anomali muncul, notifikasi terkirim', async () => {
    const admin = as(ADMIN)
    const running = (await admin.live.routes()).find((l) => l.route.status === 'berjalan' && !(l.route.delayMin))!
    const before = running.route.stops.at(-1)!.arriveAt
    await admin.demo.triggerDelay(running.route.id, 15)
    const plan = await admin.plans.get(running.planId)
    const route = plan.routes.find((r) => r.id === running.route.id)!
    expect(new Date(route.stops.at(-1)!.arriveAt).getTime()).toBe(new Date(before).getTime() + 15 * 60_000)
    expect(route.delayMin).toBe(15)
    const summary = (await admin.plans.list()).find((p) => p.id === running.planId)!
    expect(summary.hasAnomaly).toBe(true)
    // ringkasan menyebut JENIS anomalinya agar antrean bisa menjelaskan dirinya sendiri
    expect(summary.delayMin).toBe(15)
    await expect(admin.demo.triggerDelay('rute-tidak-ada', 5)).rejects.toMatchObject({ code: 'tidak_ditemukan' })
  })

  it('ringkasan plan memuat satu jenis kendaraan per rute (bukan per jenis unik)', async () => {
    const admin = as(ADMIN)
    const summaries = await admin.plans.list()
    const multi = summaries.filter((p) => p.vehicleTypes.length > 1)
    expect(multi.length).toBeGreaterThan(0) // seed punya plan berrute banyak
    for (const s of summaries.filter((p) => ['menunggu_driver', 'ditugaskan', 'berjalan', 'selesai'].includes(p.status))) {
      const plan = await admin.plans.get(s.id)
      expect(s.vehicleTypes).toEqual(plan.routes.map((r) => r.vehicleType))
    }
  })

  it('spawnPlan menambah plan ke antrean dan memberi tahu admin', async () => {
    const admin = as(ADMIN)
    const before = (await admin.plans.list({ needsDriver: true })).length
    const plan = await admin.demo.spawnPlan()
    expect(plan.status).toBe('menunggu_driver')
    expect((await admin.plans.list({ needsDriver: true })).length).toBe(before + 1)
  })
})

describe('kegagalan tiruan dan reset', () => {
  it('error / offline / empty mempengaruhi panggilan data tapi tidak kontrol demo', async () => {
    const admin = as(ADMIN)
    await admin.demo.setFault('error')
    await expect(admin.plans.list()).rejects.toMatchObject({ code: 'layanan' })
    expect((await admin.demo.state()).fault).toBe('error') // kontrol demo tetap jalan
    await admin.demo.setFault('offline')
    await expect(admin.fleet.vehicles.list()).rejects.toMatchObject({ code: 'jaringan', status: 0 })
    await admin.demo.setFault('empty')
    expect(await admin.plans.list()).toEqual([])
    expect(await admin.fleet.drivers.list()).toEqual([])
    await admin.demo.setFault('none')
    expect((await admin.plans.list()).length).toBeGreaterThan(0)
  })

  it('reset mengembalikan data seed dan memancarkan data.reset', async () => {
    const admin = as(ADMIN)
    await as(DINA).plans.createDraft()
    const seen: AppEvent[] = []
    admin.events.subscribe((e) => seen.push(e))
    await admin.demo.reset()
    expect(seen.some((e) => e.type === 'data.reset')).toBe(true)
    expect((await as(DINA).plans.list()).filter((p) => p.status === 'draft')).toHaveLength(1)
  })

  it('operasi yang gagal di tengah jalan tidak meninggalkan perubahan setengah jadi', async () => {
    const admin = as(ADMIN)
    const plan = await placeOrder()
    const before = JSON.stringify(server.db().drivers)
    await expect(admin.dispatch.assign(plan.id, plan.routes[0]!.id, 'drv-6')).rejects.toBeInstanceOf(ApiError)
    expect(JSON.stringify(server.db().drivers)).toBe(before)
  })
})

describe('armada', () => {
  const input = {
    type: 'motor' as const,
    plate: 'B 7777 XYZ',
    model: 'Honda PCX',
    maxWeightKg: 20,
    maxVolumeM3: 0.12,
    maxDimensionCm: 55,
    fare: { base: 6000, perKm: 1800, perMin: 150, perExtraStop: 2500, surchargePerKg: 800, surchargeAboveKg: 10 },
    status: 'aktif' as const,
  }

  it('CRUD kendaraan dengan validasi plat dan duplikat', async () => {
    const admin = as(ADMIN)
    await expect(admin.fleet.vehicles.create({ ...input, plate: '1234' })).rejects.toMatchObject({ field: 'plate' })
    await expect(admin.fleet.vehicles.create({ ...input, plate: 'b 3412 kjr' })).rejects.toMatchObject({ code: 'bentrok', field: 'plate' })
    await expect(admin.fleet.vehicles.create({ ...input, maxWeightKg: 0 })).rejects.toMatchObject({ field: 'maxWeightKg' })
    const v = await admin.fleet.vehicles.create(input)
    expect(v.plate).toBe('B 7777 XYZ')
    const updated = await admin.fleet.vehicles.update(v.id, { model: 'Honda PCX 160' })
    expect(updated.model).toBe('Honda PCX 160')
    await admin.fleet.vehicles.remove(v.id)
    expect((await admin.fleet.vehicles.list()).some((x) => x.id === v.id)).toBe(false)
  })

  it('kendaraan yang dipakai rute aktif tidak bisa dihapus atau dinonaktifkan', async () => {
    const admin = as(ADMIN)
    const running = (await admin.live.routes()).find((l) => l.route.status === 'berjalan')!
    const vehicleId = running.route.vehicleId!
    await expect(admin.fleet.vehicles.remove(vehicleId)).rejects.toMatchObject({ code: 'bentrok' })
    await expect(admin.fleet.vehicles.update(vehicleId, { status: 'servis' })).rejects.toMatchObject({ code: 'bentrok' })
  })

  it('tarif kendaraan mengubah hasil engine untuk plan berikutnya', async () => {
    const admin = as(ADMIN)
    const base = await placeOrder(DINA, [shipment('s1', 'kemang-village', 'plaza-senayan')])
    const motors = (await admin.fleet.vehicles.list()).filter((v) => v.type === 'motor')
    for (const m of motors) await admin.fleet.vehicles.update(m.id, { fare: { ...m.fare, perKm: m.fare.perKm * 2 } })
    const pricier = await placeOrder(DINA, [shipment('s1', 'kemang-village', 'plaza-senayan')])
    expect(pricier.totalCostRp!).toBeGreaterThan(base.totalCostRp!)
  })

  it('driver: nomor HP unik dan kendaraan tidak boleh dipakai dua driver', async () => {
    const admin = as(ADMIN)
    await expect(admin.fleet.drivers.create({ name: 'Tes', phone: '0812-3456-7801' })).rejects.toMatchObject({ code: 'bentrok', field: 'phone' })
    await expect(admin.fleet.drivers.create({ name: 'Tes', phone: '081234500000', vehicleId: 'veh-1' })).rejects.toMatchObject({ field: 'vehicleId' })
    const d = await admin.fleet.drivers.create({ name: 'Rudi Hartono', phone: '081234500000' })
    expect(d.status).toBe('offline')
    await expect(admin.fleet.drivers.update(d.id, { status: 'bertugas' })).rejects.toMatchObject({ field: 'status' })
  })

  it('driver: PIN dari admin dipakai untuk masuk, 6 digit, dan bisa diganti', async () => {
    const admin = as(ADMIN)
    await expect(admin.fleet.drivers.create({ name: 'Tes', phone: '081234500001', pin: '12ab56' })).rejects.toMatchObject({ code: 'tidak_valid', field: 'pin' })
    const d = await admin.fleet.drivers.create({ name: 'Sari Dewi', phone: '081234500002', pin: '482913' })

    await expect(as(null).auth.loginDriver('081234500002', '123456')).rejects.toBeInstanceOf(ApiError) // PIN demo tidak berlaku lagi
    expect(await as(null).auth.loginDriver('081234500002', '482913')).toMatchObject({ role: 'driver', userId: d.id })

    await expect(admin.fleet.drivers.update(d.id, { pin: '123' })).rejects.toMatchObject({ field: 'pin' })
    await admin.fleet.drivers.update(d.id, { name: 'Sari D.' }) // tanpa pin: PIN lama tetap
    expect(await as(null).auth.loginDriver('081234500002', '482913')).toMatchObject({ userId: d.id })
    await admin.fleet.drivers.update(d.id, { pin: '654321' })
    await expect(as(null).auth.loginDriver('081234500002', '482913')).rejects.toBeInstanceOf(ApiError)
    expect(await as(null).auth.loginDriver('081234500002', '654321')).toMatchObject({ userId: d.id })
  })
})

describe('pelanggan: alamat, profil, notifikasi', () => {
  it('menambah, mengubah, menghapus alamat tersimpan', async () => {
    const api = as(DINA)
    const before = (await api.account.profile()).savedAddresses.length
    await expect(api.account.addresses.add({ label: ' ', place: { name: 'x', address: 'y', position: { lat: -6.2, lng: 106.8 } }, contactName: 'a', contactPhone: '1' })).rejects.toMatchObject({ field: 'label' })
    const adr = await api.account.addresses.add({ label: 'Kantor', place: { name: 'SCBD', address: 'SCBD, Jakarta', position: { lat: -6.2266, lng: 106.8096 } }, contactName: 'Dina', contactPhone: '081311220001' })
    expect((await api.account.profile()).savedAddresses).toHaveLength(before + 1)
    expect((await api.account.addresses.update(adr.id, { label: 'Kantor pusat' })).label).toBe('Kantor pusat')
    await api.account.addresses.remove(adr.id)
    expect((await api.account.profile()).savedAddresses).toHaveLength(before)
  })

  it('notifikasi per peran dan bisa ditandai terbaca', async () => {
    const api = as(DINA)
    const list = await api.notifications.list()
    expect(list.every((n) => n.audience === 'customer:usr-1')).toBe(true)
    await api.notifications.markRead()
    expect((await api.notifications.list()).every((n) => n.read)).toBe(true)
    expect((await as(ADMIN).notifications.list()).every((n) => n.audience === 'admin')).toBe(true)
  })

  it('pencarian lokasi tanpa API key', async () => {
    const res = await as(DINA).places.search('plaza sen')
    expect(res[0]!.name).toBe('Plaza Senayan')
    const d = await as(DINA).places.describe(getPlace('plaza-senayan')!.position)
    expect(d.placeId).toBe('plaza-senayan')
    const far = await as(DINA).places.describe({ lat: -6.2, lng: 106.9 })
    expect(far.name).toBe('Pinned location')
    expect((await as(DINA, 'id').places.describe({ lat: -6.2, lng: 106.9 })).name).toBe('Titik di peta')
  })
})

describe('peristiwa', () => {
  it('konfirmasi plan memancarkan plans.changed dan notifications.changed', async () => {
    const seen: AppEvent[] = []
    const off = as(ADMIN).events.subscribe((e) => seen.push(e))
    const plan: Plan = await placeOrder()
    off()
    expect(seen.some((e) => e.type === 'plans.changed' && e.planId === plan.id)).toBe(true)
    expect(seen.some((e) => e.type === 'notifications.changed')).toBe(true)
  })
})

/** Galat dan teks tersimpan berupa `Msg` (kunci + parameter mentah); bahasa dipilih saat dirender dan oleh bahasa permintaan. */
describe('bahasa permintaan', () => {
  const failure = async (promise: Promise<unknown>): Promise<ApiError> => {
    try {
      await promise
    } catch (err) {
      return err as ApiError
    }
    throw new Error('seharusnya gagal')
  }

  it('galat membawa Msg: message berbahasa Inggris, layar merender bahasa pilihannya', async () => {
    const err = await failure(as(null).plans.list())
    expect(err.msg).toEqual({ key: 'srv.api.signInFirst' })
    expect(err.message).toBe('Please sign in first.')
    expect(err.status).toBe(401)
    expect(renderMsg(err.msg!, 'id')).toBe('Silakan masuk dulu.')
    // bahasa permintaan tidak mengubah isi galat: yang berbeda hanya saat dirender
    expect((await failure(as(null, 'id').plans.list())).msg).toEqual(err.msg)
  })

  it('galat dengan parameter dan Msg bersarang', async () => {
    const admin = as(ADMIN)
    const input: Parameters<ApiClient['fleet']['vehicles']['create']>[0] = {
      type: 'motor',
      plate: 'B 7777 XYZ',
      model: 'Honda PCX 160',
      maxWeightKg: 0,
      maxVolumeM3: 0.2,
      maxDimensionCm: 90,
      fare: { base: 6000, perKm: 1800, perMin: 150, perExtraStop: 2500, surchargePerKg: 800, surchargeAboveKg: 10 },
      status: 'aktif',
    }
    const err = await failure(admin.fleet.vehicles.create(input))
    expect(err.field).toBe('maxWeightKg')
    expect(renderMsg(err.msg!, 'en')).toBe('Maximum weight must be greater than 0.')
    expect(renderMsg(err.msg!, 'id')).toBe('Berat maksimum harus lebih dari 0.')
    const taken = await failure(admin.fleet.vehicles.create({ ...input, maxWeightKg: 50, plate: 'b 3412 kjr' }))
    expect(taken.msg).toMatchObject({ key: 'srv.api.plateTaken', params: { plate: 'B 3412 KJR' } })
  })

  it('validasi masukan memakai kunci per kolom', async () => {
    const api = as(null)
    const base = { name: 'Tia', email: 'tia@contoh.id', phone: '081200000001', password: 'rahasia123' }
    expect((await failure(api.auth.register({ ...base, password: 'pendek' }))).msg?.key).toBe('srv.api.passwordShort')
    expect((await failure(api.auth.register({ ...base, email: 'bukan-email' }))).msg?.key).toBe('srv.api.emailInvalid')
    expect((await failure(api.auth.register({ ...base, phone: '12' }))).msg?.key).toBe('srv.api.phoneIncomplete')
    expect((await failure(api.auth.loginCustomer('dina@contoh.id', 'salah'))).msg?.key).toBe('srv.api.badCredentials')
  })

  it('konfirmasi menyimpan peristiwa dan notifikasi sebagai Msg dengan parameter mentah', async () => {
    const plan = await placeOrder()
    const option = plan.optionSet!.options.find((o) => o.id === plan.selectedOptionId)!
    const confirmed = plan.events.find((e) => e.text.key === 'srv.api.event.confirmed')!
    // jumlah uang tersimpan sebagai angka, bukan "Rp 46.500": format terjadi saat layar merender
    expect(confirmed.text.params).toMatchObject({ amount: option.totals.costRp, option: option.label })
    expect(renderMsg(confirmed.text, 'en')).toContain('Order confirmed · Rp ')
    expect(renderMsg(confirmed.text, 'id')).toContain('Pesanan dikonfirmasi · Rp ')
    const note = (await as(DINA).notifications.list()).find((n) => n.planId === plan.id && n.title.key === 'srv.api.notif.confirmed.title')!
    expect(note.body).toEqual({ key: 'srv.api.notif.confirmed.body', params: { code: plan.code } })
    expect(renderMsg(note.body, 'id')).toBe(`${plan.code} sedang mencari driver.`)
    expect(renderMsg(note.body, 'en')).toBe(`${plan.code} is looking for a driver.`)
  })

  it('alasan batal bawaan dan peristiwa pembatalan mengikuti bahasa permintaan', async () => {
    const plan = await placeOrder()
    const cancelled = await as(DINA, 'id').plans.cancel(plan.id)
    expect(cancelled.cancelReason).toBe('Dibatalkan pengirim')
    expect(cancelled.events.at(-1)!.text).toEqual({ key: 'srv.api.event.cancelled' })

    const second = await placeOrder()
    expect((await as(ADMIN, 'en').plans.cancel(second.id)).cancelReason).toBe('Cancelled by admin')
    const third = await placeOrder()
    const withReason = await as(DINA).plans.cancel(third.id, 'Salah alamat')
    expect(withReason.cancelReason).toBe('Salah alamat')
    expect(withReason.events.at(-1)!.text).toEqual({ key: 'srv.api.event.cancelledReason', params: { reason: 'Salah alamat' } })
  })

  it('label progres hitung opsi mengikuti bahasa permintaan', async () => {
    const labels = async (locale: Locale) => {
      const api = as(DINA, locale)
      const draft = await api.plans.createDraft()
      await api.plans.saveDraft(draft.id, { shipments: [shipment('s1', 'kemang-village', 'plaza-senayan'), shipment('s2', 'kemang-raya', 'senayan-city')], draftStep: 3 })
      return (await computeAll(api, draft.id)).flatMap((e) => (e.type === 'progress' ? [e.label] : []))
    }
    expect(await labels('en')).toEqual([
      'Reading 4 locations',
      'Calculating distances and travel times between points',
      'Building vehicle combinations for 2 shipments',
      'Comparing cost, distance, and time',
    ])
    expect(await labels('id')).toEqual([
      'Membaca 4 titik lokasi',
      'Menghitung jarak dan waktu tempuh antar titik',
      'Menyusun kombinasi kendaraan untuk 2 pengiriman',
      'Membandingkan biaya, jarak, dan waktu',
    ])
  })

  it('opsi dan alasan yang dibuat engine tetap Msg (tidak ada teks berformat tersimpan)', async () => {
    const plan = await placeOrder()
    const option = plan.optionSet!.options[0]!
    expect(typeof option.label.key).toBe('string')
    for (const r of option.reasons) expect(r.key).toMatch(/^srv\./)
  })
})
