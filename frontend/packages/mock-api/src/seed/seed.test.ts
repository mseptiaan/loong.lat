import { describe, expect, it } from 'vitest'
import { haversineKm, routePositionAt, type Plan } from '@loong/domain'
import { renderMsg } from '@loong/i18n'
import { collectMsgs, msgProblems } from '../test-msgs'
import { createSeed } from './index'

const NOW = Date.parse('2026-10-20T07:00:00Z') // 14.00 WIB
const db = createSeed(NOW)

const byStatus = (s: Plan['status']) => db.plans.filter((p) => p.status === s)

describe('createSeed', () => {
  it('berisi pengguna, driver, armada, dan plan di semua status', () => {
    expect(db.users).toHaveLength(12)
    expect(db.drivers).toHaveLength(9)
    expect(db.vehicles).toHaveLength(8)
    expect(byStatus('selesai')).toHaveLength(26)
    expect(byStatus('dibatalkan')).toHaveLength(2)
    expect(byStatus('berjalan')).toHaveLength(3)
    expect(byStatus('ditugaskan')).toHaveLength(2)
    expect(byStatus('menunggu_driver')).toHaveLength(4)
    expect(byStatus('draft')).toHaveLength(1)
    expect(byStatus('opsi_siap')).toHaveLength(1)
  })

  it('id dan kode plan unik, kode naik menurut urutan pembuatan', () => {
    const ids = new Set(db.plans.map((p) => p.id))
    const codes = new Set(db.plans.map((p) => p.code))
    expect(ids.size).toBe(db.plans.length)
    expect(codes.size).toBe(db.plans.length)
    expect(db.counters.plan).toBe(db.plans.length)
  })

  it('demo penugasan punya kandidat kuat: driver menganggur dekat titik ambil rute yang menunggu', () => {
    const idle = db.drivers.filter((d) => d.status === 'online' && d.position)
    const waiting = byStatus('menunggu_driver').flatMap((p) => p.routes)
    const covered = waiting.filter((r) => {
      const first = r.stops[0]!.waypoint.position
      return idle.some((d) => db.vehicles.find((v) => v.id === d.vehicleId)?.type === r.vehicleType && haversineKm(d.position!, first) < 3)
    })
    expect(covered.length).toBeGreaterThanOrEqual(2)
  })

  it('driver yang bertugas punya tepat satu rute aktif, driver bebas tidak punya', () => {
    const active = db.plans.flatMap((p) => p.routes).filter((r) => r.status === 'ditugaskan' || r.status === 'berjalan')
    const driverIds = active.map((r) => r.driverId!)
    expect(new Set(driverIds).size).toBe(driverIds.length)
    for (const d of db.drivers) {
      expect(d.status === 'bertugas').toBe(driverIds.includes(d.id))
    }
  })

  it('driver dipasang sesuai tipe kendaraan rute, kapasitasnya cukup, dan akun demo Budi punya rute berjalan', () => {
    const vehicleOf = (id?: string) => db.vehicles.find((v) => v.id === id)
    for (const route of db.plans.flatMap((p) => p.routes).filter((r) => r.driverId)) {
      const driver = db.drivers.find((d) => d.id === route.driverId)!
      const vehicle = vehicleOf(driver.vehicleId)!
      expect(vehicle, `${driver.name} harus punya kendaraan`).toBeDefined()
      expect(vehicle.type, `rute ${route.id} butuh ${route.vehicleType}`).toBe(route.vehicleType)
      expect(route.vehicleId).toBe(vehicle.id)
      expect(route.totalWeightKg).toBeLessThanOrEqual(vehicle.maxWeightKg)
    }
    const budi = db.plans.flatMap((p) => p.routes).find((r) => r.driverId === 'drv-1' && r.status === 'berjalan')
    expect(budi).toBeDefined()
  })

  it('tiap rute di antrean punya setidaknya satu driver bebas bertipe sama untuk di-assign', () => {
    for (const route of byStatus('menunggu_driver').flatMap((p) => p.routes)) {
      const free = db.drivers.filter((d) => d.status === 'online' && vehicleType(d.vehicleId) === route.vehicleType)
      expect(free.length, `rute ${route.id} (${route.vehicleType})`).toBeGreaterThan(0)
    }
    function vehicleType(id?: string) {
      return db.vehicles.find((v) => v.id === id)?.type
    }
  })

  it('selalu ada driver bebas untuk tiap tipe kendaraan agar antrean bisa di-assign saat demo', () => {
    for (const type of ['motor', 'blindvan'] as const) {
      const free = db.drivers.filter((d) => d.status === 'online' && db.vehicles.find((v) => v.id === d.vehicleId)?.type === type)
      expect(free.length).toBeGreaterThan(0)
    }
  })

  it('rute yang berjalan: stop selesai di belakang, stop aktif "menuju", posisi mengikuti rute', () => {
    for (const plan of byStatus('berjalan')) {
      const route = plan.routes[0]!
      expect(route.status).toBe('berjalan')
      const idx = route.stops.findIndex((s) => s.status === 'menuju')
      expect(idx).toBeGreaterThan(-1)
      expect(route.stops.slice(0, idx).every((s) => s.status === 'selesai')).toBe(true)
      expect(route.stops.slice(idx + 1).every((s) => s.status === 'menunggu')).toBe(true)

      const pos = routePositionAt(route, NOW)
      expect(pos.stopIndex).toBe(idx)
      expect(pos.legFraction).toBeGreaterThan(0.1)
      expect(pos.legFraction).toBeLessThan(0.95)
      // posisi tidak boleh melompat jauh dari ruas yang sedang ditempuh
      const target = route.stops[idx]!.waypoint.position
      expect(haversineKm(pos.position, target)).toBeLessThan(route.stops[idx]!.legKm * 1.3 + 0.1)
    }
  })

  it('plan selesai: semua stop punya waktu aktual dan rute berakhir setelah mulai', () => {
    for (const plan of byStatus('selesai')) {
      expect(plan.finishedAt).toBeDefined()
      for (const r of plan.routes) {
        expect(r.status).toBe('selesai')
        expect(r.driverId).toBeDefined()
        expect(new Date(r.finishedAt!).getTime()).toBeGreaterThan(new Date(r.startedAt!).getTime())
        for (const s of r.stops) {
          expect(['selesai', 'bermasalah']).toContain(s.status)
          expect(s.arrivedAt).toBeDefined()
          expect(s.completedAt).toBeDefined()
        }
      }
      expect(new Date(plan.finishedAt!).getTime()).toBeLessThan(NOW)
    }
  })

  it('antrean: belum punya driver; satu terjadwal di masa depan', () => {
    const queue = byStatus('menunggu_driver')
    for (const p of queue) expect(p.routes.every((r) => !r.driverId && r.status === 'menunggu_driver')).toBe(true)
    const scheduled = queue.filter((p) => p.schedule.mode === 'terjadwal')
    expect(scheduled).toHaveLength(1)
    expect(new Date(scheduled[0]!.schedule.window!.from).getTime()).toBeGreaterThan(NOW)
  })

  it('akun demo punya draft yang bisa dilanjutkan dan opsi yang sudah dihitung', () => {
    const draft = byStatus('draft')[0]!
    expect(draft.userId).toBe('usr-1')
    expect(draft.draftStep).toBe(2)
    const ready = byStatus('opsi_siap')[0]!
    expect(ready.optionSet?.options.length).toBeGreaterThan(0)
    expect(ready.selectedOptionId).toBeUndefined()
  })

  it('nominal plan konsisten dengan opsi terpilih', () => {
    for (const p of db.plans.filter((x) => x.routes.length > 0)) {
      const sum = p.routes.reduce((s, r) => s + r.costRp, 0)
      expect(p.totalCostRp).toBe(sum)
    }
  })

  it('ada anomali untuk demo: keterlambatan di rute berjalan dan stop bermasalah di riwayat', () => {
    const delayed = db.plans.flatMap((p) => p.routes).filter((r) => r.status === 'berjalan' && (r.delayMin ?? 0) >= 10)
    expect(delayed.length).toBeGreaterThan(0)
    const problem = db.plans.flatMap((p) => p.routes).flatMap((r) => r.stops).filter((s) => s.status === 'bermasalah')
    expect(problem.length).toBeGreaterThan(0)
  })

  it('kredensial demo bekerja: pengirim, admin, driver', () => {
    expect(db.credentials['usr-1']).toBe('demo1234')
    expect(db.credentials['admin-1']).toBe('admin1234')
    expect(db.credentials['drv-1']).toBe('123456')
    expect(db.users[0]!.email).toBe('dina@contoh.id')
    expect(db.drivers[0]!.phone).toBe('081234567801')
  })
  it('semua teks tersimpan (peristiwa, notifikasi, tarif, alasan opsi, catatan saldo) adalah Msg yang terdaftar di kedua bahasa', () => {
    expect(msgProblems(db)).toEqual([])
    for (const p of db.plans) for (const e of p.events) expect(typeof e.text).toBe('object')
    for (const n of db.notifications) {
      expect(typeof n.title).toBe('object')
      expect(typeof n.body).toBe('object')
    }
    for (const t of db.walletTx) expect(t.title.key).toMatch(/^srv\.ops\.wallet\.(topUp|payment|refund)$/)
    const keys = new Set(collectMsgs([db.plans.map((p) => p.events), db.notifications]).map((m) => m.key))
    for (const k of ['srv.ops.seed.planCreated', 'srv.ops.seed.optionsPicked', 'srv.ops.seed.orderConfirmed', 'srv.ops.seed.driverAssigned', 'srv.ops.event.allDone', 'srv.ops.seed.cancelledBySender']) {
      expect(keys.has(k), k).toBe(true)
    }
  })

  it('teks seed tampil sesuai bahasa: Indonesia memakai kalimat asli, Inggris terjemahannya', () => {
    const cancelled = byStatus('dibatalkan')[0]!
    expect(renderMsg(cancelled.events.at(-1)!.text, 'id')).toBe('Pesanan dibatalkan oleh pengirim')
    expect(renderMsg(cancelled.events.at(-1)!.text, 'en')).toBe('Order cancelled by the sender')
    expect(renderMsg(cancelled.events[0]!.text, 'id')).toBe('Plan dibuat')
    expect(renderMsg(cancelled.events[0]!.text, 'en')).toBe('Plan created')
    const summary = db.notifications.find((n) => n.title.key === 'srv.ops.seed.summaryTitle')!
    expect(renderMsg(summary.body, 'id')).toBe('31 plan selesai, 94% tepat waktu.')
    expect(renderMsg(summary.body, 'en')).toBe('31 plans completed, 94% on time.')
    const confirmed = cancelled.events.find((e) => e.text.key === 'srv.ops.seed.orderConfirmed')!
    expect(renderMsg(confirmed.text, 'id')).toMatch(/^Pesanan dikonfirmasi · [\d.]+$/)
    expect(renderMsg(confirmed.text, 'en')).toMatch(/^Order confirmed · Rp [\d,]+$/)
  })

  it('barang seed membawa catalogKey, dan salinannya di stop ikut membawanya', () => {
    for (const p of db.plans) {
      for (const s of p.shipments) for (const it of s.items) expect(it.catalogKey, `${p.code} ${it.name}`).toBeDefined()
      for (const r of [...p.routes, ...(p.optionSet?.options.flatMap((o) => o.routes) ?? [])]) {
        for (const stop of r.stops) {
          expect(stop.items?.length).toBeGreaterThan(0)
          for (const it of stop.items ?? []) {
            expect(it.catalogKey).toBeDefined()
            expect(it.detail.key).toBe('dom.stopItem.detail')
          }
          expect(stop.itemsSummary.key).toBe('srv.engine.itemsSummary')
        }
      }
    }
  })

  it('stop antar yang selesai punya bukti: kode OTP, dan satu foto penerima dengan ringkasan AI di pln-26', () => {
    const delivered = db.plans.flatMap((p) => p.routes.map((r) => ({ plan: p, route: r }))).flatMap(({ plan, route }) => route.stops.filter((s) => s.kind === 'antar' && s.status === 'selesai').map((stop) => ({ plan, stop })))
    expect(delivered.length).toBeGreaterThan(20)
    for (const { stop } of delivered) {
      expect(stop.proof).toBeDefined()
      expect(new Date(stop.proof!.at).getTime()).toBe(new Date(stop.completedAt!).getTime())
    }
    // stop pengambilan, bermasalah, atau belum selesai tidak punya bukti antar
    const others = db.plans.flatMap((p) => p.routes).flatMap((r) => r.stops).filter((s) => s.kind === 'ambil' || s.status !== 'selesai')
    expect(others.every((s) => s.proof === undefined)).toBe(true)

    const photos = delivered.filter(({ stop }) => stop.proof!.kind === 'foto')
    expect(photos).toHaveLength(1)
    const { plan, stop } = photos[0]!
    expect(plan.id).toBe('pln-26')
    expect(stop.proof!.photo).toMatch(/^data:image\/jpeg;base64,/)
    expect(stop.proof!.analysis!.checks).toHaveLength(3)
    expect(delivered.filter(({ stop: s }) => s.proof!.kind === 'otp').length).toBe(delivered.length - 1)
  })

  it('stop bermasalah di riwayat menyimpan jenis masalah sebagai Msg domain', () => {
    const problem = db.plans.flatMap((p) => p.routes).flatMap((r) => r.stops).find((s) => s.status === 'bermasalah')!
    expect(problem.issue).toEqual({ key: 'dom.issue.tidak_ada_orang' })
  })
})
