import { describe, expect, it } from 'vitest'
import { PENDING_POLL_MS, planPollMs, plansPollMs } from './pending'

describe('ambil ulang selama plan menunggu tindakan', () => {
  it('satu plan: hanya saat pendingAction terisi', () => {
    expect(planPollMs(undefined)).toBe(false)
    expect(planPollMs({})).toBe(false)
    expect(planPollMs({ pendingAction: 'cancel' })).toBe(PENDING_POLL_MS)
  })

  it('daftar plan: cukup satu baris yang menunggu; daftar kosong atau belum ada tidak mengambil ulang', () => {
    expect(plansPollMs(undefined)).toBe(false)
    expect(plansPollMs([])).toBe(false)
    expect(plansPollMs([{}, {}])).toBe(false)
    expect(plansPollMs([{}, { pendingAction: 'cancel' }])).toBe(PENDING_POLL_MS)
  })
})
