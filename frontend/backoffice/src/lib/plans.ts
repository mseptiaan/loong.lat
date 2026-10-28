import type { PlanStatus, PlanSummary } from '@loong/domain'
import { t } from '../i18n'

/** Plan yang masih berjalan di sisi operasional (sudah dikonfirmasi pelanggan, belum selesai/batal). */
export const ACTIVE_STATUSES: PlanStatus[] = ['menunggu_driver', 'ditugaskan', 'berjalan']
/** Draft dan opsi yang belum dikonfirmasi adalah urusan pelanggan, bukan antrean operasional. */
export const CUSTOMER_SIDE: PlanStatus[] = ['draft', 'opsi_siap']

export const isActive = (p: Pick<PlanSummary, 'status'>) => ACTIVE_STATUSES.includes(p.status)
export const isOperational = (p: Pick<PlanSummary, 'status'>) => !CUSTOMER_SIDE.includes(p.status)

export type ActionKind = 'anomali' | 'tanpa_driver'

/** Alasan sebuah plan perlu tindakan dispatcher, atau undefined bila tidak. Anomali menang atas "belum ada driver"; plan yang sedang dibatalkan tidak perlu tindakan. */
export function actionKind(p: PlanSummary): ActionKind | undefined {
  if (!isActive(p)) return undefined
  // sedang dibatalkan: tidak ada yang bisa dilakukan dispatcher selain menunggu jawaban driver
  if (p.pendingAction) return undefined
  if (p.hasAnomaly) return 'anomali'
  if (p.status === 'menunggu_driver' && p.routesWithoutDriver > 0) return 'tanpa_driver'
  return undefined
}

export const needsAction = (p: PlanSummary) => actionKind(p) !== undefined

/**
 * Sub-baris yang menjelaskan MENGAPA plan ada di antrean: jenis anomali (telat atau stop bermasalah) dan lama menunggu.
 * Status "Mencari driver" sudah tampil di kolom status, jadi di sini tidak diulang dengan kata lain.
 */
export function actionText(p: PlanSummary, nowMs: number): string | undefined {
  const kind = actionKind(p)
  if (kind === 'anomali') {
    const parts = [
      p.delayMin >= 10 ? t('bo.plans.actionDelayed', { min: p.delayMin }) : null,
      p.problemStops > 1 ? t('bo.plans.actionProblemStops', { count: p.problemStops }) : p.problemStops > 0 ? t('bo.plans.actionProblemStop') : null,
    ].filter(Boolean)
    return parts.join(' · ') || t('bo.plans.actionAnomaly')
  }
  if (kind === 'tanpa_driver') {
    const waited = Math.max(0, Math.round((nowMs - new Date(p.updatedAt).getTime()) / 60_000))
    const scheduledLater = p.scheduleMode === 'terjadwal' && p.startAt && new Date(p.startAt).getTime() > nowMs + 60 * 60_000
    const parts = [scheduledLater ? t('bo.plans.actionScheduledLater') : t('bo.plans.actionWaiting', { min: waited })]
    if (p.routesWithoutDriver > 1) parts.push(t('bo.plans.actionRoutes', { count: p.routesWithoutDriver }))
    return parts.join(' · ')
  }
  return undefined
}

/**
 * Urutan antrean (angka kecil = lebih atas): anomali, lalu yang menunggu driver dan harus berangkat sekarang
 * (paling lama menunggu dulu), lalu yang dijadwalkan nanti, lalu yang berjalan, ditugaskan, dan sisanya.
 */
export function queueRank(p: PlanSummary, nowMs: number): number {
  const kind = actionKind(p)
  if (kind === 'anomali') return 0
  if (kind === 'tanpa_driver') {
    const startsSoon = p.scheduleMode === 'sekarang' || (p.startAt ? new Date(p.startAt).getTime() <= nowMs + 60 * 60_000 : true)
    return startsSoon ? 1 : 2
  }
  if (p.status === 'berjalan') return 3
  if (p.status === 'ditugaskan') return 4
  return 5
}

export function sortQueue(plans: PlanSummary[], nowMs: number): PlanSummary[] {
  return [...plans].sort((a, b) => {
    const r = queueRank(a, nowMs) - queueRank(b, nowMs)
    if (r !== 0) return r
    // dalam peringkat yang sama, yang paling lama tidak tersentuh di atas
    return a.updatedAt.localeCompare(b.updatedAt)
  })
}

/** "Kemang → Senayan +1" untuk baris tabel: tujuan terakhir diringkas bila lebih dari satu pengiriman. */
export function routeText(p: Pick<PlanSummary, 'fromLabel' | 'toLabel' | 'shipments'>): string {
  if (!p.fromLabel && !p.toLabel) return t('bo.plans.noLocation')
  const base = `${p.fromLabel || '—'} → ${p.toLabel || '—'}`
  return p.shipments > 1 ? `${base} +${p.shipments - 1}` : base
}
