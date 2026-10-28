import type {
  AppNotification,
  AppSettings,
  ClockState,
  Driver,
  FaultMode,
  ISODate,
  Plan,
  TopUp,
  User,
  Vehicle,
  WalletTx,
} from '@loong/domain'

/**
 * 11: teks yang tersimpan (peristiwa plan, notifikasi, rincian tarif, alasan opsi, catatan saldo) kini objek `Msg`
 * (`{ key, params }`) dan bukan string Indonesia, jadi data versi lama harus di-seed ulang.
 * 12: stop antar yang selesai menyimpan `proof` (kode OTP atau foto + ringkasan AI).
 */
export const DB_VERSION = 12

/** Seluruh state backend tiruan. Disimpan sebagai satu JSON di localStorage origin `api.`. */
export interface DB {
  version: typeof DB_VERSION
  /** Naik tiap mutasi; dipakai untuk mendeteksi perubahan dari tab lain. */
  rev: number
  scenario: string
  seededAt: ISODate
  clock: ClockState
  fault: FaultMode
  autopilot: boolean
  settings: AppSettings
  counters: { plan: number; notification: number; tx: number; topup: number }
  users: User[]
  /** userId -> kata sandi (hanya tiruan; jangan pernah begini di produksi). */
  credentials: Record<string, string>
  drivers: Driver[]
  vehicles: Vehicle[]
  plans: Plan[]
  notifications: AppNotification[]
  /** userId -> saldo (sama dengan jumlah `walletTx` pengguna itu). */
  balances: Record<string, number>
  /** Buku besar saldo semua pengguna, urut waktu. */
  walletTx: WalletTx[]
  topups: TopUp[]
}
