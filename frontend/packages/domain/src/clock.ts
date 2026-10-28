import type { ClockState } from './api'

/**
 * Jam virtual: waktu nyata yang bisa dipercepat/dimajukan lewat Panel Demo (1x, 10x, 60x, atau loncat menit).
 * Seluruh waktu tersimpan di backend tiruan (legStartedAt, arriveAt, ...) memakai jam ini, dan posisi kendaraan
 * adalah fungsi murni dari jam ini (lihat routePositionAt).
 */
export function virtualNowOf(clock: ClockState, realNow: number = Date.now()): number {
  return clock.baseVirtual + (realNow - clock.baseReal) * clock.speed
}
