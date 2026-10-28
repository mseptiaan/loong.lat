import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError, TOPUP_MAX_RP, TOPUP_MIN_RP, type ApiClient, type ComputeEvent, type Session, type Shipment } from '@loong/domain'
import { getPlace } from '@loong/gazetteer'
import { renderMsg } from '@loong/i18n'
import { makeItem } from '../seed/catalog'
import { msgProblems } from '../test-msgs'
import { createApi } from './api'
import { MockServer, memoryRawStorage } from './server'
import { createFinanceApi } from './wallet'

const T0 = Date.parse('2026-10-20T03:00:00Z')
let t = T0
let server: MockServer
const DINA: Session = { role: 'customer', userId: 'usr-1', name: 'Dina Pramesti' }
const FAJAR: Session = { role: 'customer', userId: 'usr-2', name: 'Fajar Nugroho' }
const ADMIN: Session = { role: 'admin', userId: 'admin-1', name: 'Rani Wulandari' }
const as = (ctx: Session | null): ApiClient => createApi(server, ctx)

const wp = (id: string) => {
  const p = getPlace(id)!
  return { name: p.name, address: p.address, position: p.position, placeId: p.id, contactName: 'Tes', contactPhone: '081200000000' }
}
const shipments = (): Shipment[] => [{ id: 's1', pickup: wp('kemang-village'), dropoff: wp('plaza-senayan'), items: [makeItem('paket-kecil')] }]

async function readyDraft(ctx: Session = DINA) {
  const api = as(ctx)
  const draft = await api.plans.createDraft()
  await api.plans.saveDraft(draft.id, { shipments: shipments(), draftStep: 3 })
  let set
  for await (const e of api.plans.computeOptions(draft.id) as AsyncIterable<ComputeEvent>) if (e.type === 'done') set = e.optionSet
  const option = set!.options.find((o) => o.recommended) ?? set!.options[0]!
  await api.plans.chooseOption(draft.id, option.id)
  return { api, id: draft.id, cost: option.totals.costRp }
}

beforeEach(() => {
  t = T0
  server = new MockServer({ storage: memoryRawStorage(), latency: [0, 0], realNow: () => t })
  server.db()
})

describe('buku besar seed', () => {
  it('saldo tiap pengguna = jumlah transaksinya, dan saldo setelah tiap transaksi konsisten dan tidak negatif', () => {
    const db = server.db()
    for (const u of db.users) {
      const rows = db.walletTx.filter((x) => x.userId === u.id)
      const sum = rows.reduce((s, x) => s + x.amountRp, 0)
      expect(db.balances[u.id]).toBe(sum)
      let running = 0
      for (const r of [...rows].sort((a, b) => a.at.localeCompare(b.at) || a.id.localeCompare(b.id, undefined, { numeric: true }))) {
        running += r.amountRp
        expect(r.balanceAfterRp).toBe(running)
        expect(running).toBeGreaterThanOrEqual(0)
      }
    }
  })

  it('tiap plan seed yang dibayar saldo punya pembayaran; yang dibatalkan juga punya pengembalian', () => {
    const db = server.db()
    const paid = db.plans.filter((p) => p.paymentMethod === 'saldo' && p.confirmedAt)
    expect(paid.length).toBeGreaterThan(3)
    for (const p of paid) {
      const pay = db.walletTx.find((x) => x.planId === p.id && x.kind === 'pembayaran')
      expect(pay?.amountRp).toBe(-p.totalCostRp!)
      expect(db.walletTx.some((x) => x.planId === p.id && x.kind === 'refund')).toBe(p.status === 'dibatalkan')
    }
    expect(db.plans.some((p) => p.status === 'dibatalkan' && p.paymentMethod === 'saldo')).toBe(true)
  })

  it('akun demo punya saldo cukup dan satu top up yang menunggu pembayaran', async () => {
    const w = await as(DINA).wallet.get()
    expect(w.balanceRp).toBeGreaterThanOrEqual(200_000)
    expect(w.pendingTopUps).toHaveLength(1)
    expect(w.pendingTopUps[0]).toMatchObject({ method: 'va_bca', amountRp: 100_000, status: 'menunggu' })
  })
})

describe('top up', () => {
  it('validasi jumlah dan cara bayar dengan galat per kolom', async () => {
    const api = as(DINA)
    await expect(api.wallet.createTopUp({ amountRp: TOPUP_MIN_RP - 1, method: 'qris' })).rejects.toMatchObject({ code: 'tidak_valid', field: 'amountRp' })
    await expect(api.wallet.createTopUp({ amountRp: TOPUP_MAX_RP + 1, method: 'qris' })).rejects.toMatchObject({ field: 'amountRp' })
    await expect(api.wallet.createTopUp({ amountRp: 25_000.5, method: 'qris' })).rejects.toMatchObject({ field: 'amountRp' })
    await expect(api.wallet.createTopUp({ amountRp: 50_000, method: 'bitcoin' as never })).rejects.toMatchObject({ field: 'method' })
  })

  it('membuat instruksi pembayaran; saldo baru bertambah setelah dibayar, dan bayar dua kali tidak menggandakan', async () => {
    const api = as(DINA)
    const before = (await api.wallet.get()).balanceRp
    const top = await api.wallet.createTopUp({ amountRp: 150_000, method: 'va_mandiri' })
    expect(top).toMatchObject({ status: 'menunggu', amountRp: 150_000 })
    expect(top.reference).toMatch(/^88608\d{11}$/)
    expect((await api.wallet.get()).balanceRp).toBe(before) // belum dibayar = saldo belum berubah

    const events: string[] = []
    server.subscribe((e) => events.push(e.type))
    const paid = await api.wallet.simulatePay(top.id)
    expect(paid.status).toBe('berhasil')
    expect(events).toContain('wallet.changed')
    expect((await api.wallet.get()).balanceRp).toBe(before + 150_000)

    await api.wallet.simulatePay(top.id)
    expect((await api.wallet.get()).balanceRp).toBe(before + 150_000)
    const txs = await api.wallet.transactions()
    expect(txs[0]).toMatchObject({ kind: 'topup', amountRp: 150_000, balanceAfterRp: before + 150_000, topUpId: top.id })
  })

  it('kedaluwarsa setelah batas waktu: tidak bisa dibayar lagi, dan tidak lagi dihitung menunggu', async () => {
    const api = as(DINA)
    const top = await api.wallet.createTopUp({ amountRp: 50_000, method: 'qris' }) // berlaku 30 menit
    t += 31 * 60_000
    expect((await api.wallet.getTopUp(top.id)).status).toBe('kedaluwarsa')
    await expect(api.wallet.simulatePay(top.id)).rejects.toMatchObject({ code: 'bentrok' })
    expect((await api.wallet.get()).pendingTopUps.map((x) => x.id)).not.toContain(top.id)
  })

  it('dibatalkan: tidak bisa dibayar; hanya yang menunggu yang bisa dibatalkan', async () => {
    const api = as(DINA)
    const top = await api.wallet.createTopUp({ amountRp: 50_000, method: 'va_bni' })
    expect((await api.wallet.cancelTopUp(top.id)).status).toBe('dibatalkan')
    await expect(api.wallet.simulatePay(top.id)).rejects.toMatchObject({ code: 'bentrok' })
    await expect(api.wallet.cancelTopUp(top.id)).rejects.toMatchObject({ code: 'bentrok' })
  })

  it('maksimal tiga top up menunggu sekaligus', async () => {
    const api = as(DINA) // seed sudah punya satu
    await api.wallet.createTopUp({ amountRp: 50_000, method: 'qris' })
    await api.wallet.createTopUp({ amountRp: 50_000, method: 'qris' })
    await expect(api.wallet.createTopUp({ amountRp: 50_000, method: 'qris' })).rejects.toMatchObject({ code: 'bentrok' })
  })

  it('hanya pemilik yang boleh melihat atau membayar; admin tidak punya dompet', async () => {
    const top = await as(DINA).wallet.createTopUp({ amountRp: 50_000, method: 'qris' })
    await expect(as(FAJAR).wallet.getTopUp(top.id)).rejects.toMatchObject({ code: 'ditolak' })
    await expect(as(FAJAR).wallet.simulatePay(top.id)).rejects.toMatchObject({ code: 'ditolak' })
    await expect(as(ADMIN).wallet.get()).rejects.toBeInstanceOf(ApiError)
    await expect(as(null).wallet.get()).rejects.toMatchObject({ status: 401 })
    expect((await as(FAJAR).wallet.transactions()).every((x) => x.userId === FAJAR.userId)).toBe(true)
  })
})

describe('pembayaran dengan saldo', () => {
  it('konfirmasi memotong saldo sebesar biaya dan mencatat transaksi pembayaran', async () => {
    const { api, id, cost } = await readyDraft()
    const before = (await api.wallet.get()).balanceRp
    const plan = await api.plans.confirm(id, 'saldo')
    expect((await api.wallet.get()).balanceRp).toBe(before - cost)
    expect((await api.wallet.transactions())[0]).toMatchObject({ kind: 'pembayaran', amountRp: -cost, planId: id, title: { key: 'srv.ops.wallet.payment', params: { code: plan.code } } })
  })

  it('saldo kurang: ditolak dengan pesan jelas, plan tetap bisa dikonfirmasi setelah top up', async () => {
    const { api, id, cost } = await readyDraft()
    // kosongkan saldo hampir habis
    server.mutate((db) => {
      db.balances[DINA.userId] = cost - 1
    })
    await expect(api.plans.confirm(id, 'saldo')).rejects.toMatchObject({ code: 'tidak_valid', field: 'paymentMethod', msg: { key: 'srv.ops.err.insufficientBalanceDetail', params: { balance: cost - 1, cost } }, message: expect.stringMatching(/^Insufficient balance: your balance is Rp [\d,]+ and the cost is Rp [\d,]+\./) })
    expect((await api.plans.get(id)).status).toBe('opsi_siap') // tidak berubah
    expect((await api.wallet.get()).balanceRp).toBe(cost - 1)

    const top = await api.wallet.createTopUp({ amountRp: 50_000, method: 'qris' })
    await api.wallet.simulatePay(top.id)
    await expect(api.plans.confirm(id, 'saldo')).resolves.toMatchObject({ paymentMethod: 'saldo', totalCostRp: cost })
    expect((await api.wallet.get()).balanceRp).toBe(cost - 1 + 50_000 - cost)
  })

  it('transfer dan tunai tidak menyentuh saldo', async () => {
    for (const method of ['transfer', 'tunai'] as const) {
      const { api, id } = await readyDraft()
      const before = (await api.wallet.get()).balanceRp
      const count = (await api.wallet.transactions()).length
      await api.plans.confirm(id, method)
      expect((await api.wallet.get()).balanceRp).toBe(before)
      expect((await api.wallet.transactions()).length).toBe(count)
    }
  })

  it('pembatalan mengembalikan saldo penuh sekali saja', async () => {
    const { api, id, cost } = await readyDraft()
    const before = (await api.wallet.get()).balanceRp
    await api.plans.confirm(id, 'saldo')
    await api.plans.cancel(id, 'Berubah pikiran')
    expect((await api.wallet.get()).balanceRp).toBe(before)
    const refunds = (await api.wallet.transactions()).filter((x) => x.kind === 'refund' && x.planId === id)
    expect(refunds).toHaveLength(1)
    expect(refunds[0]!.amountRp).toBe(cost)
    // dibatalkan admin pada plan yang dibayar tunai tidak mengembalikan apa pun
    const other = await readyDraft()
    await other.api.plans.confirm(other.id, 'tunai')
    const bal = (await other.api.wallet.get()).balanceRp
    await as(ADMIN).plans.cancel(other.id, 'Uji')
    expect((await other.api.wallet.get()).balanceRp).toBe(bal)
  })

  it('pengiriman yang dibuat generator tidak pernah membuat saldo negatif', async () => {
    server.mutate((db) => {
      for (const u of db.users) db.balances[u.id] = 0
      db.walletTx = []
    })
    for (let i = 0; i < 4; i++) {
      t += 1000
      await as(ADMIN).demo.spawnPlan()
    }
    const db = server.db()
    expect(Object.values(db.balances).every((b) => b >= 0)).toBe(true)
    expect(db.plans.slice(-4).every((p) => p.paymentMethod === 'transfer')).toBe(true)
  })

  it('admin melihat saldo pengguna di ringkasan', async () => {
    const detail = await as(ADMIN).users.get(DINA.userId)
    expect(detail.summary.balanceRp).toBe((await as(DINA).wallet.get()).balanceRp)
  })
})

describe('finance (admin)', () => {
  it('hanya admin: pengirim dan pemanggil tanpa sesi ditolak', async () => {
    await expect(as(DINA).finance.overview()).rejects.toMatchObject({ code: 'ditolak' })
    await expect(as(DINA).finance.topUps()).rejects.toMatchObject({ code: 'ditolak' })
    await expect(as(DINA).finance.transactions('usr-2')).rejects.toMatchObject({ code: 'ditolak' })
    await expect(as(null).finance.overview()).rejects.toMatchObject({ status: 401 })
  })

  it('ringkasan: total saldo = jumlah saldo semua pengguna, dan angka top up cocok dengan datanya', async () => {
    const db = server.db()
    const o = await as(ADMIN).finance.overview()
    expect(o.totalBalanceRp).toBe(Object.values(db.balances).reduce((s, b) => s + b, 0))
    expect(o.usersWithBalance).toBe(db.users.filter((u) => (db.balances[u.id] ?? 0) > 0).length)
    const pending = db.topups.filter((x) => x.status === 'menunggu')
    expect(o.pendingCount).toBe(pending.length)
    expect(o.pendingAmountRp).toBe(pending.reduce((s, x) => s + x.amountRp, 0))
    expect(o.pendingCount).toBeGreaterThan(0) // seed punya top up Dina yang menunggu
    const paid30 = db.topups.filter((x) => x.status === 'berhasil' && Date.parse(x.paidAt!) >= T0 - 30 * 86_400_000)
    expect(o.paid30dCount).toBe(paid30.length)
    expect(o.paid30dRp).toBe(paid30.reduce((s, x) => s + x.amountRp, 0))
  })

  it('ringkasan ikut berubah: top up dibayar menaikkan total saldo dan masuk hitungan hari ini; pembayaran pesanan menurunkannya', async () => {
    const admin = as(ADMIN)
    const before = await admin.finance.overview()
    const top = await as(DINA).wallet.createTopUp({ amountRp: 200_000, method: 'va_bri' })
    const pendingNow = await admin.finance.overview()
    expect(pendingNow.pendingCount).toBe(before.pendingCount + 1)
    expect(pendingNow.pendingAmountRp).toBe(before.pendingAmountRp + 200_000)
    expect(pendingNow.totalBalanceRp).toBe(before.totalBalanceRp) // belum dibayar

    await as(DINA).wallet.simulatePay(top.id)
    const paid = await admin.finance.overview()
    expect(paid.totalBalanceRp).toBe(before.totalBalanceRp + 200_000)
    expect(paid.pendingCount).toBe(before.pendingCount)
    expect(paid.paidTodayRp).toBe(before.paidTodayRp + 200_000)
    expect(paid.paidTodayCount).toBe(before.paidTodayCount + 1)

    const { api, id, cost } = await readyDraft()
    await api.plans.confirm(id, 'saldo')
    expect((await admin.finance.overview()).totalBalanceRp).toBe(paid.totalBalanceRp - cost)
  })

  it('daftar top up: terbaru dulu, memuat nama pengguna, dan bisa disaring', async () => {
    const admin = as(ADMIN)
    const all = await admin.finance.topUps()
    expect(all.length).toBe(server.db().topups.length)
    for (let i = 1; i < all.length; i++) expect(all[i - 1]!.createdAt >= all[i]!.createdAt).toBe(true)
    expect(all.find((x) => x.userId === 'usr-1')?.userName).toBe('Dina Pramesti')

    const waiting = await admin.finance.topUps({ status: ['menunggu'] })
    expect(waiting.length).toBeGreaterThan(0)
    expect(waiting.every((x) => x.status === 'menunggu')).toBe(true)

    const mine = await admin.finance.topUps({ userId: 'usr-1' })
    expect(mine.length).toBeGreaterThan(0)
    expect(mine.every((x) => x.userId === 'usr-1')).toBe(true)

    const byRef = await admin.finance.topUps({ q: waiting[0]!.reference })
    expect(byRef.map((x) => x.id)).toContain(waiting[0]!.id)
    const byName = await admin.finance.topUps({ q: 'dina pram' })
    expect(byName.length).toBeGreaterThan(0)
    expect(byName.every((x) => x.userName === 'Dina Pramesti')).toBe(true)
    expect(await admin.finance.topUps({ q: 'zzz-tidak-ada' })).toEqual([])
    expect((await admin.finance.topUps({ method: 'qris' })).every((x) => x.method === 'qris')).toBe(true)
  })

  it('top up yang lewat batas waktu tampil kedaluwarsa dan tidak lagi dihitung menunggu', async () => {
    const admin = as(ADMIN)
    const top = await as(FAJAR).wallet.createTopUp({ amountRp: 50_000, method: 'qris' })
    const before = await admin.finance.overview()
    t += 31 * 60_000
    const rows = await admin.finance.topUps({ userId: FAJAR.userId })
    expect(rows.find((x) => x.id === top.id)?.status).toBe('kedaluwarsa')
    expect((await admin.finance.overview()).pendingCount).toBe(before.pendingCount - 1)
  })

  it('riwayat per pengguna: hanya milik pengguna itu; pengguna yang tidak ada ditolak', async () => {
    const rows = await as(ADMIN).finance.transactions('usr-1')
    expect(rows.length).toBeGreaterThan(0)
    expect(rows.every((x) => x.userId === 'usr-1')).toBe(true)
    expect(rows[0]!.balanceAfterRp).toBe(server.db().balances['usr-1'])
    await expect(as(ADMIN).finance.transactions('usr-999')).rejects.toMatchObject({ code: 'tidak_ditemukan' })
  })
})

describe('teks netral-bahasa (srv.ops.*)', () => {
  const failure = async (promise: Promise<unknown>): Promise<ApiError> => {
    try {
      await promise
    } catch (e) {
      return e as ApiError
    }
    throw new Error('seharusnya gagal')
  }

  it('catatan saldo seed dan hasil transaksi memakai kunci terdaftar dengan parameter mentah', () => {
    const db = server.db()
    expect(msgProblems([db.walletTx, db.notifications, db.plans.map((p) => p.events)])).toEqual([])
    const pay = db.walletTx.find((x) => x.kind === 'pembayaran')!
    expect(pay.title).toEqual({ key: 'srv.ops.wallet.payment', params: { code: db.plans.find((p) => p.id === pay.planId)!.code } })
    const topUp = db.walletTx.find((x) => x.kind === 'topup')!
    expect(topUp.title.key).toBe('srv.ops.wallet.topUp')
    expect(topUp.title.params).toEqual({ method: { key: `dom.topupMethod.${db.topups.find((x) => x.id === topUp.topUpId)!.method}` } })
    expect(renderMsg({ key: 'srv.ops.wallet.topUp', params: { method: { key: 'dom.topupMethod.qris' } } }, 'en')).toBe('Top-up via QRIS')
    expect(renderMsg({ key: 'srv.ops.wallet.topUp', params: { method: { key: 'dom.topupMethod.va_bca' } } }, 'id')).toBe('Top up via Virtual account BCA')
  })

  it('top up dibayar: transaksi dan notifikasi berupa Msg, jumlahnya mentah', async () => {
    const api = as(DINA)
    const top = await api.wallet.createTopUp({ amountRp: 150_000, method: 'qris' })
    await api.wallet.simulatePay(top.id)
    const [tx] = await api.wallet.transactions()
    expect(tx!.title).toEqual({ key: 'srv.ops.wallet.topUp', params: { method: { key: 'dom.topupMethod.qris' } } })
    const ntf = server.db().notifications.at(-1)!
    expect(ntf.title).toEqual({ key: 'srv.ops.wallet.topUpDoneTitle' })
    expect(ntf.body).toEqual({ key: 'srv.ops.wallet.topUpDoneBody', params: { amount: 150_000 } })
    expect(renderMsg(ntf.body, 'en')).toBe('Rp 150,000 has been added to your loong.lat balance.')
    expect(renderMsg(ntf.body, 'id')).toBe('Rp 150.000 sudah masuk ke saldo loong.lat Anda.')
    expect(msgProblems([tx, ntf])).toEqual([])
  })

  it('pembatalan: catatan pengembalian berupa Msg', async () => {
    const { api, id } = await readyDraft()
    const plan = await api.plans.confirm(id, 'saldo')
    await api.plans.cancel(id, 'Salah alamat')
    const refund = (await api.wallet.transactions()).find((x) => x.kind === 'refund')!
    expect(refund.title).toEqual({ key: 'srv.ops.wallet.refund', params: { code: plan.code } })
    expect(renderMsg(refund.title, 'en')).toBe(`Refund for ${plan.code} (cancelled)`)
    expect(renderMsg(refund.title, 'id')).toBe(`Pengembalian ${plan.code} (dibatalkan)`)
  })

  it('galat top up membawa Msg: jumlah, cara bayar, batas menunggu, kedaluwarsa, dibatalkan, akun diblokir', async () => {
    const api = as(DINA)
    const tooSmall = await failure(api.wallet.createTopUp({ amountRp: TOPUP_MIN_RP - 1, method: 'qris' }))
    expect(tooSmall.msg).toEqual({ key: 'dom.topup.errMin', params: { min: TOPUP_MIN_RP } })
    expect((await failure(api.wallet.createTopUp({ amountRp: 50_000, method: 'bitcoin' as never }))).msg).toEqual({ key: 'srv.ops.err.chooseMethod' })

    await api.wallet.createTopUp({ amountRp: 50_000, method: 'qris' })
    await api.wallet.createTopUp({ amountRp: 50_000, method: 'qris' })
    const many = await failure(api.wallet.createTopUp({ amountRp: 50_000, method: 'qris' }))
    expect(many.msg).toEqual({ key: 'srv.ops.err.tooManyPending', params: { count: 3 } })
    expect(many.message).toBe('You still have 3 top-ups awaiting payment. Complete or cancel one of them first.')
    expect(renderMsg(many.msg!, 'id')).toBe('Masih ada 3 top up yang menunggu pembayaran. Selesaikan atau batalkan salah satunya dulu.')

    const [pending] = (await api.wallet.get()).pendingTopUps
    await api.wallet.cancelTopUp(pending!.id)
    expect((await failure(api.wallet.simulatePay(pending!.id))).msg).toEqual({ key: 'srv.ops.err.topUpCancelled' })
    expect((await failure(api.wallet.cancelTopUp(pending!.id))).msg).toEqual({ key: 'srv.ops.err.onlyPendingCancel' })

    const [other] = (await api.wallet.get()).pendingTopUps
    t += 31 * 60_000
    expect((await failure(api.wallet.simulatePay(other!.id))).msg).toEqual({ key: 'srv.ops.err.topUpExpired' })

    expect((await failure(as(FAJAR).wallet.getTopUp(other!.id))).msg).toEqual({ key: 'srv.ops.err.topUpForbidden' })
    expect((await failure(api.wallet.getTopUp('top-tidak-ada'))).msg).toEqual({ key: 'srv.ops.err.topUpNotFound' })

    server.mutate((d) => {
      d.users.find((u) => u.id === DINA.userId)!.blocked = true
    })
    expect((await failure(api.wallet.createTopUp({ amountRp: 50_000, method: 'qris' }))).msg).toEqual({ key: 'srv.ops.err.accountBlocked' })
  })

  it('saldo tidak cukup memakai kunci yang terdaftar di kedua bahasa', async () => {
    const { api, id, cost } = await readyDraft()
    server.mutate((db) => {
      db.balances[DINA.userId] = cost - 1
    })
    const err = await failure(api.plans.confirm(id, 'saldo'))
    expect(msgProblems(err.msg)).toEqual([])
    expect(renderMsg(err.msg!, 'id')).toMatch(/^Saldo tidak cukup: saldo Anda Rp [\d.]+, biaya Rp [\d.]+\. Top up dulu/)
  })

  it('pengguna yang sudah dihapus pada daftar top up memakai teks pengganti sesuai bahasa permintaan', async () => {
    server.mutate((db) => {
      db.users = db.users.filter((u) => u.id !== DINA.userId)
    })
    const env = { server, run: async <T,>(fn: (db: ReturnType<MockServer['db']>) => T) => fn(server.db()), requireRole: () => ADMIN }
    const names = (rows: { userId: string; userName: string }[]) => [...new Set(rows.filter((x) => x.userId === DINA.userId).map((x) => x.userName))]
    expect(names(await createFinanceApi(env).topUps())).toEqual(['Deleted user'])
    expect(names(await createFinanceApi({ ...env, locale: 'id' }).topUps())).toEqual(['Pengguna dihapus'])
  })
})
