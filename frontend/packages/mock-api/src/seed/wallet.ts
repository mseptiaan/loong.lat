import { TOPUP_METHODS, type Plan, type TopUp, type User, type WalletTx } from '@loong/domain'
import { msg } from '@loong/i18n'
import { createRng } from '../rng'
import { newTopUp, settleLedger, topUpTitle } from '../wallet'

const MIN = 60_000
const HOUR = 60 * MIN
const iso = (ms: number) => new Date(ms).toISOString()
const roundUp = (n: number, step: number) => Math.ceil(n / step) * step

export interface SeededWallet {
  balances: Record<string, number>
  walletTx: WalletTx[]
  topups: TopUp[]
  counters: { tx: number; topup: number }
}

/**
 * Buku besar saldo yang konsisten dengan plan di seed: tiap plan yang dibayar dengan saldo menjadi pembayaran
 * (plus pengembalian bila dibatalkan), dan top up ditambahkan secukupnya sebelum pembayaran yang butuh. Saldo akhir =
 * jumlah semua transaksi. Memakai RNG sendiri agar data seed lain tidak berubah.
 */
export function seedWallet(users: User[], plans: Plan[], nowMs: number): SeededWallet {
  const rng = createRng(20261021)
  let txNo = 0
  let topNo = 0
  const balances: Record<string, number> = {}
  const walletTx: WalletTx[] = []
  const topups: TopUp[] = []

  for (const user of users) {
    const raw: Omit<WalletTx, 'balanceAfterRp'>[] = []
    const addTopUp = (atMs: number, amountRp: number) => {
      const id = `top-${++topNo}`
      const method = rng.pick([...TOPUP_METHODS])
      const created = newTopUp({ id, userId: user.id, phone: user.phone, amountRp, method, atMs: atMs - 4 * MIN })
      topups.push({ ...created, status: 'berhasil', paidAt: iso(atMs) })
      raw.push({ id: `wtx-${++txNo}`, userId: user.id, at: iso(atMs), kind: 'topup', amountRp, title: topUpTitle(method), topUpId: id })
    }

    const events = plans
      .filter((p) => p.userId === user.id && p.paymentMethod === 'saldo' && p.confirmedAt && p.totalCostRp)
      .flatMap((p) => [
        { at: Date.parse(p.confirmedAt!), delta: -p.totalCostRp!, plan: p, kind: 'pembayaran' as const },
        ...(p.status === 'dibatalkan' ? [{ at: Date.parse(p.updatedAt), delta: p.totalCostRp!, plan: p, kind: 'refund' as const }] : []),
      ])
      .sort((a, b) => a.at - b.at)

    let balance = 0
    for (const e of events) {
      if (e.delta < 0 && balance + e.delta < 0) {
        const add = roundUp(-e.delta - balance + rng.int(0, 2) * 25_000, 50_000)
        addTopUp(e.at - 20 * MIN, add)
        balance += add
      }
      balance += e.delta
      raw.push({
        id: `wtx-${++txNo}`,
        userId: user.id,
        at: iso(e.at),
        kind: e.kind,
        amountRp: e.delta,
        title: msg(e.kind === 'pembayaran' ? 'srv.ops.wallet.payment' : 'srv.ops.wallet.refund', { code: e.plan.code }),
        planId: e.plan.id,
      })
    }

    // saldo akhir cukup untuk mencoba alur demo; akun demo (Dina) sengaja lebih longgar
    const floor = user.id === 'usr-1' ? 200_000 : 50_000
    if (balance < floor) {
      const add = roundUp(floor - balance, 50_000)
      addTopUp(nowMs - 2 * HOUR * (1 + rng.int(0, 3)), add)
    }

    const settled = settleLedger(raw)
    walletTx.push(...settled.rows)
    balances[user.id] = settled.balanceRp
  }

  // akun demo punya satu top up yang masih menunggu pembayaran, agar keadaan itu terlihat di halaman saldo
  const dina = users[0]
  if (dina) {
    const id = `top-${++topNo}`
    topups.push(newTopUp({ id, userId: dina.id, phone: dina.phone, amountRp: 100_000, method: 'va_bca', atMs: nowMs - 20 * MIN }))
  }

  return { balances, walletTx, topups, counters: { tx: txNo, topup: topNo } }
}
