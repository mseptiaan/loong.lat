import { describe, expect, it } from 'vitest'
import type { AppEvent, CopilotEvent, Driver, DraftPatch, Effort, OptionSet, Plan, PlanOption, Schedule, Shipment, Vehicle } from '@loong/domain'
import { createHttpApi } from './client'

/*
 * Cerita ujung ke ujung lewat klien http-api terhadap backend yang SUNGGUHAN (tiga API + optimizer + Postgres +
 * Redpanda). Sama dengan e2e Go (backend/e2e), tetapi dari sisi layar: nama metode, nama parameter, token,
 * Idempotency-Key, NDJSON, SSE, dan bentuk jawaban semuanya lewat klien yang dipakai aplikasi.
 *
 * Dilewati kecuali LOONG_LIVE=1. Menyiapkan backend: lihat "Dengan backend asli" di frontend/README.md.
 * Tes ini menulis data (pelanggan, driver, plan) ke database dev; nama dibuat unik per jalan sehingga bisa diulang.
 * Tiap cerita membuat driver sendiri dan memutuskannya (offline) di akhir: driver yang tertinggal online dianggap ada
 * sampai PresenceTTL (2 menit) dan akan menerima plan cerita berikutnya.
 */

const env = (globalThis as { process?: { env: Record<string, string | undefined> } }).process?.env ?? {}
const live = env.LOONG_LIVE === '1'

const urls = {
  console: env.LOONG_CONSOLE_URL ?? 'http://127.0.0.1:8083',
  backoffice: env.LOONG_BACKOFFICE_URL ?? 'http://127.0.0.1:8082',
  driver: env.LOONG_DRIVER_URL ?? 'http://127.0.0.1:8081',
}
const adminEmail = env.LOONG_ADMIN_EMAIL ?? 'admin@loong.test'
const adminPassword = env.LOONG_ADMIN_PASSWORD ?? 'e2e-admin-password'

const fixtures = import.meta.glob<string>('../../../../backend/testdata/parity/batching.request.json', { eager: true, query: '?raw', import: 'default' })
const shipments = (): Shipment[] => (JSON.parse(Object.values(fixtures)[0]!) as { shipments: Shipment[] }).shipments

function memoryStorage() {
  const m = new Map<string, string>()
  return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k) }
}
const client = (app: 'console' | 'backoffice' | 'driver', extra: Partial<Parameters<typeof createHttpApi>[0]> = {}) =>
  createHttpApi({ app, baseUrl: urls[app], storage: memoryStorage(), retryMs: 500, ...extra })

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms))

type Probe<T> = { ok: true; value: T } | { ok: false; why: string }
const ok = <T>(value: T): Probe<T> => ({ ok: true, value })
const wait = (why: string): Probe<never> => ({ ok: false, why })

async function eventually<T>(what: string, ms: number, probe: () => Promise<Probe<T>>): Promise<T> {
  const until = Date.now() + ms
  let why = ''
  while (Date.now() < until) {
    const r = await probe().catch((e: unknown) => wait(String(e)))
    if (r.ok) return r.value
    why = r.why
    await sleep(500)
  }
  throw new Error(`timeout waiting for ${what}: ${why}`)
}

/**
 * Dispatch memberi driver baru pesanan yang paling lama menunggu. Pesanan sisa dari jalan yang gagal akan "mencuri" driver cerita
 * berikutnya, jadi setiap cerita mulai dari antrean kosong: pesanan yang masih menunggu driver dibatalkan lewat admin.
 */
async function clearQueue(admin: ReturnType<typeof client>) {
  const waiting = () => admin.plans.list({ status: ['menunggu_driver'] })
  for (const p of await waiting()) await admin.plans.cancel(p.id, 'pembersihan tes').catch(() => undefined)
  await eventually('the waiting queue to be empty', 60_000, async () => {
    const left = await waiting()
    return left.length === 0 ? ok(true) : wait(`${left.length} plans still waiting`)
  })
}

/** Panggung satu cerita: admin dengan armada dan satu driver online, dan pelanggan baru dengan saldo Rp 200.000. */
interface Stage {
  admin: ReturnType<typeof client>
  mobile: ReturnType<typeof client>
  web: ReturnType<typeof client>
  driver: Driver
  customerName: string
  events: AppEvent[]
  close(): Promise<void>
}

async function openStage(options: { customer?: boolean } = {}): Promise<Stage> {
  const run = Date.now().toString(36) + Math.floor(Math.random() * 1000).toString(36)
  const phone = `08${String(Date.now()).slice(-10)}`
  const pin = '482913'
  // pelat unik per jalan, dalam bentuk yang diterima backend ("B 1234 ABC")
  const letters = Array.from({ length: 3 }, () => String.fromCharCode(65 + Math.floor(Math.random() * 26))).join('')
  const plate = `B ${1000 + Math.floor(Math.random() * 9000)} ${letters}`

  const admin = client('backoffice')
  const mobile = client('driver', { pingEveryMs: 1000, fallbackPosition: { lat: -6.261, lng: 106.813 } })
  const web = client('console')
  const close = async () => {
    if (mobile.getSession()) await mobile.driver.setOnline(false).catch(() => undefined)
    web.destroy()
    admin.destroy()
    mobile.destroy()
  }
  try {
    // ---- backoffice: armada, driver, dispatch otomatis
    await admin.ready()
    await admin.auth.loginAdmin(adminEmail, adminPassword)
    await clearQueue(admin)
    const motor: Vehicle = await admin.fleet.vehicles.create({
      type: 'motor', plate, model: 'Honda Beat', maxWeightKg: 20, maxVolumeM3: 0.12, maxDimensionCm: 55, status: 'aktif',
      fare: { base: 6000, perKm: 1800, perMin: 150, perExtraStop: 2500, surchargePerKg: 800, surchargeAboveKg: 10 },
    })
    const driver: Driver = await admin.fleet.drivers.create({ name: `Budi ${run}`, phone, vehicleId: motor.id, pin })
    await admin.settings.update({ dispatch: { auto: true } })

    // ---- driver: akunnya sampai ke driver-api lewat event, jadi tunggu sebentar (login gagal dibatasi lajunya)
    await mobile.ready()
    await sleep(3000)
    let signedIn = false
    for (let attempt = 0; attempt < 4 && !signedIn; attempt++) {
      try {
        await mobile.auth.loginDriver(phone, pin)
        signedIn = true
      } catch (e) {
        if (attempt === 3) throw e
        await sleep(5000)
      }
    }
    expect(mobile.getSession()).toMatchObject({ role: 'driver', userId: driver.id })
    await mobile.driver.setOnline(true) // dari sini aplikasi mengirim posisi tiap detik

    // ---- console: pelanggan mendaftar dan mengisi saldo
    await web.ready()
    const events: AppEvent[] = []
    web.events.subscribe((e: AppEvent) => events.push(e))
    const customerName = `Dina ${run}`
    if (options.customer !== false) {
      const session = await web.auth.register({ name: customerName, email: `dina.${run}@loong.test`, phone: `08${String(Date.now()).slice(-9)}1`, password: 'dina-secret-1' })
      expect(session.role).toBe('customer')
      expect(Object.keys(session)).not.toContain('token')
      const topUp = await web.wallet.createTopUp({ amountRp: 200_000, method: 'va_bca' })
      await web.wallet.simulatePay(topUp.id)
      expect((await web.wallet.get()).balanceRp).toBe(200_000)
    }
    return { admin, mobile, web, driver, customerName, events, close }
  } catch (e) {
    await close()
    throw e
  }
}

/**
 * Kuotasi tiga paket dan pilih opsi yang disarankan (plan siap dikonfirmasi). `effort` memilih seberapa keras mesin mencari
 * (bawaan fast). Sub-langkah pencarian mengulang `step` tahap 3, jadi yang dihitung adalah tahap yang berbeda, bukan jumlah peristiwa.
 */
async function quotePlan(web: Stage['web'], opts: { effort?: Effort } = {}): Promise<{ plan: Plan; chosen: PlanOption; options: PlanOption[]; set: OptionSet }> {
  let plan: Plan = await web.plans.createDraft()
  const patch: DraftPatch = { shipments: shipments(), schedule: { mode: 'sekarang' }, priority: { preset: 'termurah' }, draftStep: 3 }
  plan = await web.plans.saveDraft(plan.id, patch)

  const deep = opts.effort !== undefined && opts.effort !== 'fast'
  const set = await eventually('a quote', deep ? 240_000 : 60_000, async () => {
    const stages = new Set<number>()
    for await (const ev of web.plans.computeOptions(plan.id, opts.effort ? { effort: opts.effort } : undefined)) {
      if (ev.type === 'progress') stages.add(ev.step)
      else {
        expect([...stages]).toEqual([1, 2, 3, 4]) // layar wizard menampilkan tepat empat tahap
        return ok(ev.optionSet)
      }
    }
    return wait('no done event')
  })
  const options = set.options
  const chosen = options.find((o: PlanOption) => o.recommended)
  expect(chosen, 'exactly one recommended option').toBeDefined()
  plan = await web.plans.chooseOption(plan.id, chosen!.id)
  return { plan, chosen: chosen!, options, set }
}

/** Kuotasi, lalu konfirmasi dengan saldo. */
async function confirmPlan(web: Stage['web']): Promise<{ plan: Plan; costRp: number }> {
  const { plan: quoted, chosen } = await quotePlan(web)
  const plan = await web.plans.confirm(quoted.id, 'saldo')
  expect(['menunggu_driver', 'ditugaskan']).toContain(plan.status)
  expect((await web.wallet.get()).balanceRp).toBe(200_000 - chosen.totals.costRp)
  return { plan, costRp: chosen.totals.costRp }
}

/** Menunggu dispatch memberi semua rute ke driver. */
const assigned = (web: Stage['web'], planId: string) =>
  eventually('every route assigned', 40_000, async () => {
    const p = await web.plans.get(planId)
    const open = p.routes.filter((r) => !r.driverId)
    return p.status === 'ditugaskan' && open.length === 0 ? ok(p) : wait(`status ${p.status}, ${open.length} routes without a driver`)
  })

/** Penugasan sampai ke driver-api lewat event tersendiri, jadi daftar tugas bisa tertinggal sesaat dari status plan di console. */
const tasksOf = (mobile: Stage['mobile']) =>
  eventually('the driver to receive the task', 30_000, async () => {
    const list = await mobile.driver.routes()
    return list.length > 0 ? ok(list) : wait('no tasks yet')
  })

describe.skipIf(!live)('backend sungguhan', () => {
  it('plan hidup lintas layanan: daftar, kuotasi, bayar, tugaskan, antar, selesai', { timeout: 180_000 }, async () => {
    const { admin, mobile, web, events, close } = await openStage()
    try {
      const { plan: confirmed } = await confirmPlan(web)

      // ---- backoffice melihatnya, dispatch memberi semua rute ke driver, console mengikuti
      await eventually('the plan in the back office queue', 60_000, async () => {
        const list = await admin.plans.list()
        return list.some((p) => p.id === confirmed.id) ? ok(true) : wait('plan not listed')
      })
      const plan = await assigned(web, confirmed.id)

      // ---- driver menjalankan tiap rute; kode serah-terima hanya terlihat oleh pelanggan
      const codes = new Map(plan.routes.flatMap((r) => r.stops.map((s) => [s.id, s.deliveryCode] as const)))
      const tasks = await tasksOf(mobile)
      for (const task of tasks) {
        await mobile.driver.startRoute(task.planId, task.route.id)
        for (const stop of task.route.stops) {
          expect(stop.deliveryCode ?? '').toBe('') // tidak bocor ke driver
          await mobile.driver.arrive(task.planId, task.route.id, stop.id)
          const proof = stop.kind === 'antar' ? { kind: 'otp' as const, value: codes.get(stop.id) ?? '' } : undefined
          await mobile.driver.complete(task.planId, task.route.id, stop.id, proof)
        }
      }

      const done = await eventually('the console to see the plan done', 30_000, async () => {
        const p = await web.plans.get(plan.id)
        return p.status === 'selesai' ? ok(p) : wait(`status ${p.status}`)
      })
      await eventually('the back office to see the plan done', 30_000, async () => {
        const p = await admin.plans.get(plan.id)
        return p.status === 'selesai' ? ok(p) : wait(`status ${p.status}`)
      })
      for (const r of done.routes) expect(r.status).toBe('selesai')
      expect(await mobile.driver.history()).toHaveLength(tasks.length)

      // layar yang berlangganan diberi tahu perubahan plan miliknya lewat SSE
      expect(events.some((e) => e.type === 'plans.changed')).toBe(true)
    } finally {
      await close()
    }
  })

  it('pembatalan: dua fase, refund sekali, tugas driver hilang, notifikasi ketiga pihak', { timeout: 180_000 }, async () => {
    const { admin, mobile, web, driver, close } = await openStage()
    try {
      const { plan, costRp } = await confirmPlan(web)
      await assigned(web, plan.id)
      await tasksOf(mobile)

      const asked = await web.plans.cancel(plan.id, 'salah alamat')
      expect(asked.pendingAction === 'cancel' || asked.status === 'dibatalkan').toBe(true)

      const cancelled = await eventually('the plan cancelled', 40_000, async () => {
        const p = await web.plans.get(plan.id)
        return p.status === 'dibatalkan' && !p.pendingAction ? ok(p) : wait(`status ${p.status} pending=${p.pendingAction}`)
      })
      expect(cancelled.cancelReason).toBe('salah alamat')
      await eventually('the refund', 20_000, async () => {
        const balance = (await web.wallet.get()).balanceRp
        return balance === 200_000 ? ok(true) : wait(`balance ${balance}, paid ${costRp}`)
      })
      const txs = await web.wallet.transactions()
      expect(txs.filter((t) => t.kind === 'refund')).toHaveLength(1)

      await eventually('the driver free again and the task gone', 30_000, async () => {
        const tasks = await mobile.driver.routes()
        const me = (await admin.fleet.drivers.list()).find((d) => d.id === driver.id)
        return tasks.length === 0 && me?.status === 'online' ? ok(true) : wait(`${tasks.length} tasks, driver ${me?.status}`)
      })
      await eventually('the back office to see it cancelled', 30_000, async () => {
        const p = await admin.plans.get(plan.id)
        return p.status === 'dibatalkan' ? ok(true) : wait(`status ${p.status}`)
      })

      // notifikasi: pelanggan (dibatalkan, refund), driver (tugas dibatalkan), admin
      const mine = await eventually('the customer notifications', 30_000, async () => {
        const list = await web.notifications.list()
        return list.length >= 2 ? ok(list) : wait(`${list.length} notifications`)
      })
      expect(mine.every((n) => n.audience.startsWith('customer:') && typeof n.title.key === 'string')).toBe(true)
      await eventually('the driver notification', 30_000, async () => {
        const list = await mobile.notifications.list()
        return list.length >= 1 ? ok(list) : wait('no driver notification')
      })
      await web.notifications.markRead([mine[0]!.id])
      const after = await web.notifications.list()
      expect(after.find((n) => n.id === mine[0]!.id)?.read).toBe(true)
      expect(after.some((n) => !n.read)).toBe(true)
      await web.notifications.markRead()
      expect((await web.notifications.list()).every((n) => n.read)).toBe(true)

      // sudah dibatalkan: tidak bisa dibatalkan lagi
      await expect(web.plans.cancel(plan.id)).rejects.toMatchObject({ code: 'bentrok' })
    } finally {
      await close()
    }
  })

  it('jadwal ulang: rute kembali lewat dispatch dan mendapat jam baru', { timeout: 180_000 }, async () => {
    const { mobile, web, close } = await openStage()
    try {
      const { plan } = await confirmPlan(web)
      await assigned(web, plan.id)

      const from = new Date(Date.now() + 3 * 3_600_000)
      from.setSeconds(0, 0)
      const schedule: Schedule = { mode: 'terjadwal', window: { from: from.toISOString(), to: new Date(from.getTime() + 3_600_000).toISOString() } }
      const moved = await web.plans.reschedule(plan.id, schedule)
      expect(moved.schedule.mode).toBe('terjadwal')

      // kurang dari 20 menit ke depan ditolak
      const soon: Schedule = { mode: 'terjadwal', window: { from: new Date(Date.now() + 5 * 60_000).toISOString(), to: new Date(Date.now() + 3_600_000).toISOString() } }
      await expect(web.plans.reschedule(plan.id, soon)).rejects.toMatchObject({ code: 'tidak_valid', field: 'schedule' })

      await eventually('the routes re-timed and assigned again', 60_000, async () => {
        const p = await web.plans.get(plan.id)
        for (const r of p.routes) {
          if (!r.driverId) return wait(`route ${r.id} has no driver (plan ${p.status})`)
          const startAt = r.startAt ? new Date(r.startAt).getTime() : 0
          if (Math.abs(startAt - from.getTime()) > 2000) return wait(`route starts at ${r.startAt}`)
        }
        return p.schedule.mode === 'terjadwal' && p.status === 'ditugaskan' ? ok(true) : wait(`plan ${p.status}`)
      })
      expect((await mobile.driver.routes()).length).toBeGreaterThan(0)
    } finally {
      await close()
    }
  })

  it('pengguna dan keuangan di backoffice mengikuti saldo; memblokir menolak masuk', { timeout: 180_000 }, async () => {
    const { admin, web, customerName, close } = await openStage()
    try {
      const { costRp } = await confirmPlan(web)

      const summary = await eventually('the customer with the balance', 40_000, async () => {
        const list = await admin.users.list(customerName)
        const u = list.find((x) => x.name === customerName)
        return u && u.balanceRp === 200_000 - costRp && u.plansCount === 1 ? ok(u) : wait(`users: ${JSON.stringify(list.map((x) => [x.name, x.balanceRp, x.plansCount]))}`)
      })
      const detail = await admin.users.get(summary.id)
      expect(detail.summary.id).toBe(summary.id)
      expect(detail.plans).toHaveLength(1)
      expect(Array.isArray(detail.user.savedAddresses)).toBe(true)

      const overview = await admin.finance.overview()
      expect(overview.usersWithBalance).toBeGreaterThanOrEqual(1)
      expect(overview.paidTodayCount).toBeGreaterThanOrEqual(1)
      const topUps = await admin.finance.topUps({ userId: summary.id })
      expect(topUps).toHaveLength(1)
      expect(topUps[0]).toMatchObject({ amountRp: 200_000, status: 'berhasil', userName: customerName })
      const ledger = await admin.finance.transactions(summary.id)
      expect(ledger.map((t) => t.kind).sort()).toEqual(['pembayaran', 'topup'])

      // blokir: sesi pelanggan berhenti dan masuk ditolak; buka blokir mengembalikannya
      expect((await admin.users.setBlocked(summary.id, true)).blocked).toBe(true)
      await eventually('the console to refuse the blocked customer', 30_000, async () => {
        try {
          await web.wallet.get()
          return wait('the session still works')
        } catch {
          return ok(true)
        }
      })
      await admin.users.setBlocked(summary.id, false)
      const again = client('console')
      try {
        await eventually('the unblocked customer back in', 30_000, async () => {
          try {
            await again.auth.loginCustomer(`dina.${customerName.split(' ')[1]}@loong.test`, 'dina-secret-1')
            return ok(true)
          } catch (e) {
            return wait(String(e))
          }
        })
      } finally {
        again.destroy()
      }
    } finally {
      await close()
    }
  })

  it('akun, alamat tersimpan dan pengaturan dilayani console', { timeout: 120_000 }, async () => {
    const { admin, web, close } = await openStage()
    try {
      // admin mematikan penugasan otomatis; console mengetahuinya lewat Kafka
      await admin.settings.update({ dispatch: { auto: false } })
      await eventually('the console to learn the setting', 30_000, async () => {
        const s = await web.settings.get()
        return !s.dispatch.auto ? ok(s) : wait('dispatch.auto still on')
      })
      await admin.settings.update({ dispatch: { auto: true } })

      const profile = await web.account.profile()
      expect(profile.savedAddresses).toEqual([])
      const updated = await web.account.update({ name: 'Dina P.' })
      expect(updated.name).toBe('Dina P.')
      const address = await web.account.addresses.add({
        label: 'Kantor', contactName: 'Dina', contactPhone: '081200000001',
        place: { name: 'SCBD', address: 'SCBD, Jakarta', position: { lat: -6.2266, lng: 106.8096 } },
      })
      expect((await web.account.profile()).savedAddresses.map((a) => a.id)).toEqual([address.id])
      expect((await web.account.addresses.update(address.id, { label: 'Kantor pusat' })).label).toBe('Kantor pusat')
      await web.account.addresses.remove(address.id)
      expect((await web.account.profile()).savedAddresses).toEqual([])
    } finally {
      await close()
    }
  })

  it('kuotasi thorough: opsi datang dengan method.effort thorough, tersimpan di plan, dan hitung ulang mengganti serta menghapus pilihan', { timeout: 300_000 }, async () => {
    const { web, close } = await openStage()
    try {
      const { plan, options, set } = await quotePlan(web, { effort: 'thorough' })
      expect(options.length).toBeGreaterThan(0)
      expect(set.method?.effort).toBe('thorough')
      expect(set.method?.trials).toBeGreaterThan(0)
      expect(set.method?.reviewer).toBeUndefined() // peninjau hanya untuk reviewed
      expect(set.report).toBeUndefined() // laporan hanya atas permintaan

      const stored = await web.plans.get(plan.id)
      expect(stored.optionSet?.method).toEqual(set.method)
      expect(stored.selectedOptionId).toBeDefined() // quotePlan memilih satu opsi

      // hitung ulang dengan tingkat lain: optionSet diganti dan pilihan dihapus
      let again: OptionSet | undefined
      for await (const ev of web.plans.computeOptions(plan.id, { effort: 'fast' })) if (ev.type === 'done') again = ev.optionSet
      expect(again?.method?.effort).toBe('fast')
      const replaced = await web.plans.get(plan.id)
      expect(replaced.optionSet?.method?.effort).toBe('fast')
      expect(replaced.selectedOptionId).toBeUndefined()
    } finally {
      await close()
    }
  })
})

/*
 * Fitur AI lewat gerbang ke model Meta sungguhan. Lambat dan berbiaya kecil, jadi butuh LOONG_LIVE_AI=1 di samping
 * LOONG_LIVE=1, dan backend harus berjalan dengan AI_URL (console dan backoffice) dan META_API_KEY (layanan ai).
 */
const liveAi = live && env.LOONG_LIVE_AI === '1'

const collect = async (stream: AsyncIterable<CopilotEvent>) => {
  const events: CopilotEvent[] = []
  for await (const ev of stream) events.push(ev)
  const text = events.flatMap((e) => (e.type === 'token' ? [e.text] : [])).join('')
  const done = events.find((e) => e.type === 'done')
  const tools = events.flatMap((e) => (e.type === 'tool' ? [e.trace] : []))
  return { events, text, done, tools }
}

describe.skipIf(!liveAi)('AI (Meta) lewat backend sungguhan', () => {
  it('Copilot rencana dan pembaca maksud di console; bendera admin ditaati', { timeout: 300_000 }, async () => {
    const { admin, web, close } = await openStage()
    try {
      // bendera AI = bendera admin DAN gerbang terkonfigurasi
      const flags = await web.settings.get()
      expect(flags.ai).toEqual({ vision: true, voice: true, intent: true, copilot: true })

      const { plan, chosen } = await quotePlan(web)
      const answer = await collect(web.ai.copilot({ kind: 'plan', planId: plan.id }, 'Which option is the cheapest, and what does it cost?'))
      expect(answer.tools.some((t) => t.status === 'done')).toBe(true)
      expect(answer.text.trim().length).toBeGreaterThan(20)
      expect(answer.done).toMatchObject({ type: 'done' })
      expect(typeof (answer.done as { requestId: string }).requestId).toBe('string')
      // angka jawaban berasal dari alat: harga opsi terpilih muncul (apa pun format penulisannya)
      expect(answer.text.replace(/[.,\s]/g, '')).toContain(String(chosen.totals.costRp))

      // plan milik orang lain / tak ada: ditolak sebelum model dipanggil
      await expect(collect(web.ai.copilot({ kind: 'plan', planId: 'pln-tidak-ada' }, 'status?'))).rejects.toMatchObject({ code: 'tidak_ditemukan' })

      const intent = await web.ai.parseIntent('kirim yang paling murah saja')
      expect(intent.chips.length).toBeGreaterThan(0)
      for (const chip of intent.chips) expect(['prioritas', 'batas_waktu']).toContain(chip.kind)

      // admin mematikan Copilot: console menolak dan bendera ikut turun
      await admin.settings.update({ ai: { copilot: false } })
      try {
        await eventually('the console to learn the switch', 30_000, async () => ((await web.settings.get()).ai.copilot ? wait('still on') : ok(true)))
        await expect(collect(web.ai.copilot({ kind: 'plan', planId: plan.id }, 'hello'))).rejects.toMatchObject({ code: 'ditolak' })
      } finally {
        await admin.settings.update({ ai: { copilot: true } })
      }
    } finally {
      await close()
    }
  })

  it('Copilot operasional dan wawasan analitik di backoffice', { timeout: 300_000 }, async () => {
    const { admin, web, close } = await openStage()
    try {
      await confirmPlan(web)
      const ops = await collect(admin.ai.copilot({ kind: 'ops' }, 'Which plans need attention right now?'))
      expect(ops.tools.some((t) => t.status === 'done')).toBe(true)
      expect(ops.text.trim().length).toBeGreaterThan(20)
      expect(ops.done).toMatchObject({ type: 'done' })

      const insights = await collect(admin.ai.insights())
      expect(insights.tools.length).toBeGreaterThan(0)
      expect(insights.text.trim().length).toBeGreaterThan(20)
    } finally {
      await close()
    }
  })

  it('kuotasi reviewed lalu laporan AI: teks datang utuh dan plans.get mengembalikannya sebagai optionSet.report', { timeout: 420_000 }, async () => {
    const { web, close } = await openStage()
    try {
      // belum ada opsi: ditolak dengan pesan khusus, tanpa memanggil model
      const empty = await web.plans.createDraft()
      await expect(collect(web.ai.explainOptions(empty.id))).rejects.toMatchObject({ msg: { key: 'srv.ai.report.noOptions' } })

      const { plan, set } = await quotePlan(web, { effort: 'reviewed' })
      expect(set.method?.effort).toBe('reviewed')
      expect(['ai', 'rules']).toContain(set.method?.reviewer) // 'rules' bila model peninjau tak tersedia

      const report = await collect(web.ai.explainOptions(plan.id))
      expect(report.events.map((e) => e.type)).toEqual(['token', 'done']) // teks diperiksa dulu di server, jadi satu token utuh
      expect(report.done).toMatchObject({ type: 'done', actions: [] })
      expect(report.text.trim().length).toBeGreaterThan(40)

      const stored = (await web.plans.get(plan.id)).optionSet?.report
      expect(stored?.text).toBe(report.text)
      expect(stored?.locale).toBe('en')
      expect(Number.isNaN(Date.parse(stored?.at ?? ''))).toBe(false)
    } finally {
      await close()
    }
  })
})
