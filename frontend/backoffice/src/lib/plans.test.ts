import { setLocale, type Locale } from '@loong/i18n'
import type { PlanSummary } from '@loong/domain'
import { afterEach, describe, expect, it } from 'vitest'
import { actionKind, actionText, needsAction, routeText, sortQueue } from './plans'

const NOW = Date.parse('2026-01-01T10:00:00Z')

const plan = (over: Partial<PlanSummary>): PlanSummary =>
  ({
    status: 'menunggu_driver',
    hasAnomaly: false,
    delayMin: 0,
    problemStops: 0,
    routesWithoutDriver: 0,
    scheduleMode: 'sekarang',
    updatedAt: '2026-01-01T09:52:00Z',
    shipments: 1,
    fromLabel: 'Kemang',
    toLabel: 'Senayan',
    ...over,
  }) as PlanSummary

const as = <T>(locale: Locale, fn: () => T) => {
  setLocale(locale)
  return fn()
}

describe('teks antrean mengikuti bahasa saat dipanggil', () => {
  afterEach(() => setLocale('en'))

  it('anomali: telat dan stop bermasalah', () => {
    const p = plan({ status: 'berjalan', hasAnomaly: true, delayMin: 12, problemStops: 2 })
    expect(as('en', () => actionText(p, NOW))).toBe('Late by 12 min · 2 problem stops')
    expect(as('id', () => actionText(p, NOW))).toBe('Terlambat 12 mnt · 2 stop bermasalah')
    expect(as('id', () => actionText({ ...p, problemStops: 1, delayMin: 0 }, NOW))).toBe('Stop bermasalah')
    expect(as('id', () => actionText({ ...p, problemStops: 0, delayMin: 0 }, NOW))).toBe('Ada anomali')
  })

  it('menunggu driver: lama menunggu, jumlah rute, dan jadwal nanti', () => {
    const p = plan({ routesWithoutDriver: 2 })
    expect(as('en', () => actionText(p, NOW))).toBe('Waiting 8 min · 2 routes')
    expect(as('id', () => actionText(p, NOW))).toBe('Menunggu 8 mnt · 2 rute')
    const later = plan({ routesWithoutDriver: 1, scheduleMode: 'terjadwal', startAt: '2026-01-01T14:00:00Z' })
    expect(as('en', () => actionText(later, NOW))).toBe('Scheduled for later')
    expect(as('id', () => actionText(later, NOW))).toBe('Dijadwalkan nanti')
  })

  it('ringkasan rute', () => {
    expect(as('en', () => routeText(plan({ shipments: 2 })))).toBe('Kemang → Senayan +1')
    expect(as('en', () => routeText(plan({ fromLabel: '', toLabel: '' })))).toBe('No location yet')
    expect(as('id', () => routeText(plan({ fromLabel: '', toLabel: '' })))).toBe('Belum ada lokasi')
  })
})

describe('plan yang sedang dibatalkan (pendingAction)', () => {
  afterEach(() => setLocale('en'))

  it('tidak masuk antrean "perlu tindakan", sekalipun belum ada driver atau ada anomali', () => {
    const noDriver = plan({ routesWithoutDriver: 1 })
    expect(actionKind(noDriver)).toBe('tanpa_driver')
    expect(actionKind({ ...noDriver, pendingAction: 'cancel' })).toBeUndefined()
    const late = plan({ status: 'berjalan', hasAnomaly: true, delayMin: 20 })
    expect(needsAction({ ...late, pendingAction: 'cancel' })).toBe(false)
    expect(actionText({ ...noDriver, pendingAction: 'cancel' }, NOW)).toBeUndefined()
  })

  it('urutan antrean memperlakukannya seperti plan biasa yang tidak butuh tindakan', () => {
    const a = plan({ id: 'a', routesWithoutDriver: 1, pendingAction: 'cancel', updatedAt: '2026-01-01T09:00:00Z' } as Partial<PlanSummary>)
    const b = plan({ id: 'b', routesWithoutDriver: 1, updatedAt: '2026-01-01T09:30:00Z' } as Partial<PlanSummary>)
    expect(sortQueue([a, b], NOW).map((p) => p.id)).toEqual(['b', 'a'])
  })
})
