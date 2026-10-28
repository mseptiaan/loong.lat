import {
  ApiError,
  MAX_PENDING_TOPUPS,
  TOPUP_METHODS,
  validateTopUpAmount,
  type AdminTopUp,
  type ApiClient,
  type FinanceOverview,
  type Plan,
  type Session,
  type TopUp,
  type WalletTx,
  type WalletTxKind,
} from '@loong/domain'
import { DEFAULT_LOCALE, msg, tFor, type Locale, type Msg } from '@loong/i18n'
import type { DB } from '../db'
import { newTopUp, topUpTitle } from '../wallet'
import type { MockServer } from './server'

const iso = (ms: number) => new Date(ms).toISOString()

export const balanceOf = (db: DB, userId: string) => db.balances[userId] ?? 0

/** Mencatat satu transaksi dan memperbarui saldo. Satu-satunya jalan saldo berubah, jadi saldo selalu = jumlah transaksi. */
export function postTx(
  db: DB,
  args: { userId: string; atMs: number; kind: WalletTxKind; amountRp: number; title: Msg; planId?: string; topUpId?: string },
): WalletTx {
  const balanceAfterRp = balanceOf(db, args.userId) + args.amountRp
  if (balanceAfterRp < 0) throw new ApiError('tidak_valid', msg('srv.ops.err.insufficientBalance'), { field: 'paymentMethod' })
  db.balances[args.userId] = balanceAfterRp
  db.counters.tx += 1
  const tx: WalletTx = {
    id: `wtx-${db.counters.tx}`,
    userId: args.userId,
    at: iso(args.atMs),
    kind: args.kind,
    amountRp: args.amountRp,
    balanceAfterRp,
    title: args.title,
    ...(args.planId ? { planId: args.planId } : {}),
    ...(args.topUpId ? { topUpId: args.topUpId } : {}),
  }
  db.walletTx.push(tx)
  return tx
}

/** Memotong saldo untuk pesanan. Melempar galat yang jelas (dan tidak mengubah apa pun) bila saldo kurang. */
export function chargePlan(db: DB, plan: Plan, costRp: number, atMs: number): void {
  const balance = balanceOf(db, plan.userId)
  if (balance < costRp) {
    throw new ApiError('tidak_valid', msg('srv.ops.err.insufficientBalanceDetail', { balance, cost: costRp }), { field: 'paymentMethod' })
  }
  postTx(db, { userId: plan.userId, atMs, kind: 'pembayaran', amountRp: -costRp, title: msg('srv.ops.wallet.payment', { code: plan.code }), planId: plan.id })
}

/** Mengembalikan pembayaran saldo penuh untuk plan yang dibatalkan. Hanya bila memang dibayar saldo dan belum dikembalikan. */
export function refundPlan(db: DB, plan: Plan, atMs: number): void {
  const paid = db.walletTx.find((t) => t.planId === plan.id && t.kind === 'pembayaran')
  if (!paid || db.walletTx.some((t) => t.planId === plan.id && t.kind === 'refund')) return
  postTx(db, { userId: plan.userId, atMs, kind: 'refund', amountRp: -paid.amountRp, title: msg('srv.ops.wallet.refund', { code: plan.code }), planId: plan.id })
}

/** Top up yang melewati batas waktu ditandai kedaluwarsa (dilakukan saat dibaca, seperti penyedia pembayaran sungguhan). */
export function sweepExpiredTopUps(server: MockServer): DB {
  const now = server.now()
  if (server.db().topups.some((t) => t.status === 'menunggu' && Date.parse(t.expiresAt) <= now)) {
    server.mutate((db) => {
      for (const t of db.topups) if (t.status === 'menunggu' && Date.parse(t.expiresAt) <= now) t.status = 'kedaluwarsa'
    })
    server.emit({ type: 'wallet.changed' })
  }
  return server.db()
}

export interface WalletEnv {
  /** Bahasa permintaan: dipakai untuk teks turunan yang dikirim sebagai string (mis. nama pengguna yang sudah dihapus). Bawaan Inggris. */
  locale?: Locale
  server: MockServer
  run: <T>(fn: (db: DB) => T) => Promise<T>
  requireRole: (...roles: Session['role'][]) => Session
}

export function createWalletApi(env: WalletEnv): ApiClient['wallet'] {
  const { server, run, requireRole } = env
  const me = () => requireRole('customer')
  const sweep = () => sweepExpiredTopUps(server)

  const ownTopUp = (db: DB, id: string, userId: string): TopUp => {
    const t = db.topups.find((x) => x.id === id)
    if (!t) throw new ApiError('tidak_ditemukan', msg('srv.ops.err.topUpNotFound'))
    if (t.userId !== userId) throw new ApiError('ditolak', msg('srv.ops.err.topUpForbidden'))
    return t
  }

  return {
    get: () =>
      run(() => {
        const s = me()
        const db = sweep()
        return {
          balanceRp: balanceOf(db, s.userId),
          pendingTopUps: db.topups.filter((t) => t.userId === s.userId && t.status === 'menunggu').sort((a, b) => b.createdAt.localeCompare(a.createdAt)),
        }
      }),

    transactions: () =>
      run(() => {
        const s = me()
        return server
          .db()
          .walletTx.filter((t) => t.userId === s.userId)
          .sort((a, b) => b.at.localeCompare(a.at) || b.id.localeCompare(a.id, undefined, { numeric: true }))
          .slice(0, 200)
      }),

    createTopUp: (input) =>
      run(() => {
        const s = me()
        const amountError = validateTopUpAmount(input.amountRp)
        if (amountError) throw new ApiError('tidak_valid', amountError, { field: 'amountRp' })
        if (!TOPUP_METHODS.includes(input.method)) throw new ApiError('tidak_valid', msg('srv.ops.err.chooseMethod'), { field: 'method' })
        const top = server.mutate((db) => {
          const user = db.users.find((u) => u.id === s.userId)
          if (!user) throw new ApiError('tidak_ditemukan', msg('srv.ops.err.userNotFound'))
          if (user.blocked) throw new ApiError('ditolak', msg('srv.ops.err.accountBlocked'))
          const now = server.now()
          const pending = db.topups.filter((t) => t.userId === s.userId && t.status === 'menunggu' && Date.parse(t.expiresAt) > now)
          if (pending.length >= MAX_PENDING_TOPUPS) {
            throw new ApiError('bentrok', msg('srv.ops.err.tooManyPending', { count: MAX_PENDING_TOPUPS }))
          }
          db.counters.topup += 1
          const t = newTopUp({ id: `top-${db.counters.topup}`, userId: s.userId, phone: user.phone, amountRp: input.amountRp, method: input.method, atMs: now })
          db.topups.push(t)
          return t
        })
        server.emit({ type: 'wallet.changed' })
        return top
      }),

    getTopUp: (id) =>
      run(() => {
        const s = me()
        return ownTopUp(sweep(), id, s.userId)
      }),

    simulatePay: (id) =>
      run(() => {
        const s = me()
        const current = ownTopUp(sweep(), id, s.userId)
        if (current.status === 'berhasil') return current // idempoten: bayar dua kali tidak menambah saldo dua kali
        if (current.status !== 'menunggu') {
          throw new ApiError('bentrok', msg(current.status === 'kedaluwarsa' ? 'srv.ops.err.topUpExpired' : 'srv.ops.err.topUpCancelled'))
        }
        const paid = server.mutate((db) => {
          const t = ownTopUp(db, id, s.userId)
          const now = server.now()
          t.status = 'berhasil'
          t.paidAt = iso(now)
          postTx(db, { userId: s.userId, atMs: now, kind: 'topup', amountRp: t.amountRp, title: topUpTitle(t.method), topUpId: t.id })
          server.notify(db, `customer:${s.userId}`, msg('srv.ops.wallet.topUpDoneTitle'), msg('srv.ops.wallet.topUpDoneBody', { amount: t.amountRp }))
          return t
        })
        server.emit({ type: 'wallet.changed' })
        server.emit({ type: 'notifications.changed' })
        return paid
      }),

    cancelTopUp: (id) =>
      run(() => {
        const s = me()
        const current = ownTopUp(sweep(), id, s.userId)
        if (current.status !== 'menunggu') throw new ApiError('bentrok', msg('srv.ops.err.onlyPendingCancel'))
        const t = server.mutate((db) => {
          const x = ownTopUp(db, id, s.userId)
          x.status = 'dibatalkan'
          return x
        })
        server.emit({ type: 'wallet.changed' })
        return t
      }),
  }
}

const DAY = 86_400_000
const startOfDayWib = (ms: number) => Math.floor((ms + 7 * 3_600_000) / DAY) * DAY - 7 * 3_600_000

/** Sisi admin: ringkasan saldo, semua top up, dan riwayat saldo per pengguna. */
export function createFinanceApi(env: WalletEnv): ApiClient['finance'] {
  const { server, run, requireRole } = env
  const deletedUser = () => tFor(env.locale ?? DEFAULT_LOCALE)('srv.ops.wallet.deletedUser')
  const admin = () => requireRole('admin')

  return {
    overview: () =>
      run(() => {
        admin()
        const db = sweepExpiredTopUps(server)
        const now = server.now()
        const today = startOfDayWib(now)
        const paid = db.topups.filter((t) => t.status === 'berhasil' && t.paidAt)
        const sum = (rows: { amountRp: number }[]) => rows.reduce((s, t) => s + t.amountRp, 0)
        const paidToday = paid.filter((t) => Date.parse(t.paidAt!) >= today)
        const paid30 = paid.filter((t) => Date.parse(t.paidAt!) >= now - 30 * DAY)
        const pending = db.topups.filter((t) => t.status === 'menunggu')
        const balances = db.users.map((u) => balanceOf(db, u.id))
        return {
          totalBalanceRp: balances.reduce((s, b) => s + b, 0),
          usersWithBalance: balances.filter((b) => b > 0).length,
          pendingCount: pending.length,
          pendingAmountRp: sum(pending),
          paidTodayRp: sum(paidToday),
          paidTodayCount: paidToday.length,
          paid30dRp: sum(paid30),
          paid30dCount: paid30.length,
        } satisfies FinanceOverview
      }),

    topUps: (filter) =>
      run(() => {
        admin()
        const db = sweepExpiredTopUps(server)
        const needle = filter?.q?.trim().toLowerCase()
        const users = new Map(db.users.map((u) => [u.id, u]))
        return db.topups
          .filter((t) => !filter?.status?.length || filter.status.includes(t.status))
          .filter((t) => !filter?.userId || t.userId === filter.userId)
          .filter((t) => !filter?.method || t.method === filter.method)
          .map((t): AdminTopUp => ({ ...t, userName: users.get(t.userId)?.name ?? deletedUser(), userEmail: users.get(t.userId)?.email ?? '' }))
          .filter((t) => !needle || [t.userName, t.userEmail, t.reference, t.id].some((x) => x.toLowerCase().includes(needle)))
          .sort((a, b) => b.createdAt.localeCompare(a.createdAt) || b.id.localeCompare(a.id, undefined, { numeric: true }))
      }),

    transactions: (userId) =>
      run(() => {
        admin()
        const db = server.db()
        if (!db.users.some((u) => u.id === userId)) throw new ApiError('tidak_ditemukan', msg('srv.ops.err.userNotFound'))
        return db.walletTx
          .filter((t) => t.userId === userId)
          .sort((a, b) => b.at.localeCompare(a.at) || b.id.localeCompare(a.id, undefined, { numeric: true }))
          .slice(0, 200)
      }),
  }
}
