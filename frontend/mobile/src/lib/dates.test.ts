import { setLocale } from '@loong/i18n'
import { afterEach, describe, expect, it } from 'vitest'
import { dayKeyOf, dayLabel, weekdayShort } from './dates'

describe('tanggal Driver mengikuti bahasa', () => {
  afterEach(() => setLocale('en'))

  it('nama hari singkat', () => {
    setLocale('en')
    expect(weekdayShort('2026-10-19')).toBe('Mon')
    setLocale('id')
    expect(weekdayShort('2026-10-19')).toBe('Sen')
  })

  it('label hari: hari ini, kemarin, atau tanggal', () => {
    const now = new Date('2026-10-21T05:00:00Z').getTime()
    const today = '2026-10-21T03:20:00Z'
    const yesterday = '2026-10-20T03:20:00Z'
    const earlier = '2026-10-10T03:20:00Z'
    setLocale('en')
    expect([dayLabel(today, now), dayLabel(yesterday, now), dayLabel(earlier, now)]).toEqual(['Today', 'Yesterday', '10 Oct'])
    setLocale('id')
    expect([dayLabel(today, now), dayLabel(yesterday, now), dayLabel(earlier, now)]).toEqual(['Hari ini', 'Kemarin', '10 Okt'])
  })

  it('kunci hari tidak bergantung bahasa', () => {
    setLocale('id')
    expect(dayKeyOf('2026-10-21T03:20:00Z')).toBe('2026-10-21')
  })
})
