import { topUpIsVa, topUpTtlMs, type TopUp, type TopUpMethod, type WalletTx } from '@loong/domain'
import { msg, type Msg } from '@loong/i18n'

/* Bagian murni dari dompet tiruan (tanpa MockServer), dipakai bersama oleh seed dan server. */

const VA_PREFIX: Record<Exclude<TopUpMethod, 'qris'>, string> = {
  va_bca: '39358',
  va_mandiri: '88608',
  va_bni: '8808',
  va_bri: '26215',
}

/** Nomor virtual account (awalan bank + nomor HP, tetap sama untuk pengguna yang sama) atau kode QRIS (per top up). */
export function topUpReference(method: TopUpMethod, phone: string, topUpId: string): string {
  if (!topUpIsVa(method)) return `LOONG-${topUpId.replace(/\D/g, '').padStart(6, '0')}`
  const digits = phone.replace(/\D/g, '').replace(/^62/, '0')
  const prefix = VA_PREFIX[method as Exclude<TopUpMethod, 'qris'>]
  return `${prefix}${digits.slice(-11).padStart(11, '0')}`
}

/** Judul catatan saldo untuk top up: nama metode mengikuti bahasa layar (label domain bersarang). */
export const topUpTitle = (method: TopUpMethod): Msg => msg('srv.ops.wallet.topUp', { method: msg(`dom.topupMethod.${method}`) })

export function newTopUp(args: { id: string; userId: string; phone: string; amountRp: number; method: TopUpMethod; atMs: number }): TopUp {
  return {
    id: args.id,
    userId: args.userId,
    amountRp: args.amountRp,
    method: args.method,
    status: 'menunggu',
    createdAt: new Date(args.atMs).toISOString(),
    expiresAt: new Date(args.atMs + topUpTtlMs(args.method)).toISOString(),
    reference: topUpReference(args.method, args.phone, args.id),
  }
}

/** Isi `balanceAfterRp` untuk satu pengguna dari daftar transaksi (urut waktu); mengembalikan saldo akhir. */
export function settleLedger(txs: Omit<WalletTx, 'balanceAfterRp'>[]): { rows: WalletTx[]; balanceRp: number } {
  let balance = 0
  const rows = [...txs]
    .sort((a, b) => a.at.localeCompare(b.at) || a.id.localeCompare(b.id, undefined, { numeric: true }))
    .map((t) => {
      balance += t.amountRp
      return { ...t, balanceAfterRp: balance }
    })
  return { rows, balanceRp: balance }
}
