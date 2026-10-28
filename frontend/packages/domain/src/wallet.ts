import { t, type Msg } from '@loong/i18n'
import type { ISODate } from './types'

/*
 * Saldo loong.lat (dompet pengirim). Backend asli nanti memegang buku besar ini; mock-api meniru bentuknya:
 * saldo = jumlah seluruh transaksi, tiap transaksi menyimpan saldo setelahnya.
 */

export const TOPUP_MIN_RP = 10_000
export const TOPUP_MAX_RP = 5_000_000
/** Pilihan cepat di halaman top up. */
export const TOPUP_PRESETS_RP = [50_000, 100_000, 250_000, 500_000] as const
/** Top up yang boleh menunggu pembayaran sekaligus. */
export const MAX_PENDING_TOPUPS = 3

export type TopUpMethod = 'va_bca' | 'va_mandiri' | 'va_bni' | 'va_bri' | 'qris'

export const TOPUP_METHODS: readonly TopUpMethod[] = ['va_bca', 'va_mandiri', 'va_bni', 'va_bri', 'qris']

export const TOPUP_METHOD_LABEL: Record<TopUpMethod, string> = {
  get va_bca() {
    return t('dom.topupMethod.va_bca')
  },
  get va_mandiri() {
    return t('dom.topupMethod.va_mandiri')
  },
  get va_bni() {
    return t('dom.topupMethod.va_bni')
  },
  get va_bri() {
    return t('dom.topupMethod.va_bri')
  },
  get qris() {
    return t('dom.topupMethod.qris')
  },
}

export const TOPUP_METHOD_HINT: Record<TopUpMethod, string> = {
  get va_bca() {
    return t('dom.topupHint.va_bca')
  },
  get va_mandiri() {
    return t('dom.topupHint.va_mandiri')
  },
  get va_bni() {
    return t('dom.topupHint.va_bni')
  },
  get va_bri() {
    return t('dom.topupHint.va_bri')
  },
  get qris() {
    return t('dom.topupHint.qris')
  },
}

export const topUpIsVa = (m: TopUpMethod) => m !== 'qris'

/** Berapa lama instruksi pembayaran berlaku. */
export const topUpTtlMs = (m: TopUpMethod) => (m === 'qris' ? 30 * 60_000 : 24 * 3_600_000)

export type TopUpStatus = 'menunggu' | 'berhasil' | 'kedaluwarsa' | 'dibatalkan'

export const TOPUP_STATUS_LABEL: Record<TopUpStatus, string> = {
  get menunggu() {
    return t('dom.topupStatus.menunggu')
  },
  get berhasil() {
    return t('dom.topupStatus.berhasil')
  },
  get kedaluwarsa() {
    return t('dom.topupStatus.kedaluwarsa')
  },
  get dibatalkan() {
    return t('dom.topupStatus.dibatalkan')
  },
}

export interface TopUp {
  id: string
  userId: string
  amountRp: number
  method: TopUpMethod
  status: TopUpStatus
  createdAt: ISODate
  expiresAt: ISODate
  paidAt?: ISODate
  /** Nomor virtual account, atau kode QRIS. */
  reference: string
}

export interface TopUpInput {
  amountRp: number
  method: TopUpMethod
}

export type WalletTxKind = 'topup' | 'pembayaran' | 'refund'

export interface WalletTx {
  id: string
  userId: string
  at: ISODate
  kind: WalletTxKind
  /** Positif = saldo bertambah, negatif = berkurang. */
  amountRp: number
  balanceAfterRp: number
  title: Msg
  planId?: string
  topUpId?: string
}

export interface Wallet {
  balanceRp: number
  /** Top up yang menunggu pembayaran dan belum kedaluwarsa, terbaru dulu. */
  pendingTopUps: TopUp[]
}

/** Galat untuk jumlah top up (pesan netral-bahasa), dipakai klien (saat mengetik) dan server (saat mengirim). */
export function validateTopUpAmount(amountRp: number): Msg | undefined {
  if (!Number.isFinite(amountRp) || amountRp <= 0) return { key: 'dom.topup.errEnter' }
  if (!Number.isInteger(amountRp)) return { key: 'dom.topup.errInteger' }
  if (amountRp < TOPUP_MIN_RP) return { key: 'dom.topup.errMin', params: { min: TOPUP_MIN_RP } }
  if (amountRp > TOPUP_MAX_RP) return { key: 'dom.topup.errMax', params: { max: TOPUP_MAX_RP } }
  return undefined
}

/** Pembulatan ke atas ke kelipatan 10.000 untuk saran jumlah (mis. saldo kurang Rp 12.300 -> Rp 20.000). */
export const roundUpTopUp = (amountRp: number) => Math.min(TOPUP_MAX_RP, Math.max(TOPUP_MIN_RP, Math.ceil(amountRp / 10_000) * 10_000))

/* ---------- sisi admin (Backoffice) ---------- */

export interface AdminTopUp extends TopUp {
  userName: string
  userEmail: string
}

export interface TopUpFilter {
  status?: TopUpStatus[]
  userId?: string
  method?: TopUpMethod
  /** Cocok dengan nama, email, nomor virtual account/kode, atau id top up. */
  q?: string
}

/** Ringkasan uang di dompet pengguna untuk admin. */
export interface FinanceOverview {
  /** Total saldo seluruh pengguna: dana pengguna yang masih dipegang platform. */
  totalBalanceRp: number
  usersWithBalance: number
  pendingCount: number
  pendingAmountRp: number
  /** Top up berhasil sejak tengah malam WIB (waktu virtual). */
  paidTodayRp: number
  paidTodayCount: number
  /** Top up berhasil 30 hari terakhir. */
  paid30dRp: number
  paid30dCount: number
}
