/** Bagian tarif yang diterima driver; sisanya biaya platform. */
export const DRIVER_SHARE = 0.7

/**
 * Pendapatan driver dari sebuah rute, dibulatkan ke Rp500. Satu aturan dipakai server tiruan (riwayat, pendapatan)
 * dan layar Driver (estimasi di kartu tugas), sehingga angka di kartu dan di riwayat selalu sama.
 */
export const driverEarningRp = (routeCostRp: number): number => Math.round((routeCostRp * DRIVER_SHARE) / 500) * 500
