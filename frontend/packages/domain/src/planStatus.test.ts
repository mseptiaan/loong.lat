import { afterEach, describe, expect, it } from 'vitest'
import { setLocale } from '@loong/i18n'
import { PLAN_PENDING_LABEL, planStatusText } from './types'

afterEach(() => setLocale('en'))

describe('teks status plan', () => {
  it('tanpa tindakan tertunda memakai label status', () => {
    expect(planStatusText({ status: 'menunggu_driver' })).toBe('Finding a driver')
    expect(planStatusText({ status: 'berjalan' })).toBe('On the way')
  })

  it('pembatalan yang menunggu driver menggantikan label status, di kedua bahasa', () => {
    const p = { status: 'ditugaskan', pendingAction: 'cancel' } as const
    expect(planStatusText(p)).toBe('Cancelling…')
    setLocale('id')
    expect(planStatusText(p)).toBe('Membatalkan…')
    expect(PLAN_PENDING_LABEL.cancel).toBe('Membatalkan…')
  })
})
