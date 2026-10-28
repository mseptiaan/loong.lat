import type { Plan } from '@loong/domain'

/**
 * Selama sebuah plan punya `pendingAction` (pembatalan menunggu jawaban driver), layar mengambil ulang tiap beberapa detik
 * sebagai jaring pengaman: peristiwa `plans.changed` lewat SSE yang utama, tetapi bila sambungan itu terputus pada saat
 * yang salah, "Membatalkan…" tidak boleh tertinggal selamanya (penyapu backend menutup permintaan paling lambat sekitar 2 menit).
 */
export const PENDING_POLL_MS = 5000

type Pending = Pick<Plan, 'pendingAction'>

export const planPollMs = (plan: Pending | undefined): number | false => (plan?.pendingAction ? PENDING_POLL_MS : false)

export const plansPollMs = (plans: readonly Pending[] | undefined): number | false => (plans?.some((p) => p.pendingAction) ? PENDING_POLL_MS : false)
