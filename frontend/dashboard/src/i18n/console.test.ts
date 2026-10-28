import { getLocale, setLocale } from '@loong/i18n'
import { afterEach, describe, expect, it } from 'vitest'
import { t } from '.'

describe('teks console: jamak dan format', () => {
  const before = getLocale()
  afterEach(() => setLocale(before))

  it('Inggris membedakan tunggal dan jamak', () => {
    setLocale('en')
    expect(t('dash.plans.parcels', { count: 1 })).toBe('1 parcel')
    expect(t('dash.plans.parcels', { count: 3 })).toBe('3 parcels')
    expect(t('dash.plans.progressStops', { done: 1, count: 1 })).toBe('1 of 1 stop')
    expect(t('dash.plans.progressStops', { done: 2, count: 5 })).toBe('2 of 5 stops')
    expect(t('dash.plans.stopsDoneOf', { done: 3, count: 5 })).toBe('3 of 5 stops completed')
    expect(t('dash.plans.itemsTotal', { count: 1, weight: 2 })).toBe('Total 1 item · 2 kg')
    expect(t('dash.wallet.pendingCount', { count: 2 })).toBe('2 top-ups awaiting payment')
    expect(t('dash.home.savedMonthHint', { count: 1 })).toBe('from 1 completed delivery, compared with sending separately')
  })

  it('Indonesia memakai teks lama', () => {
    setLocale('id')
    expect(t('dash.plans.parcels', { count: 1 })).toBe('1 paket')
    expect(t('dash.plans.progressStops', { done: 2, count: 5 })).toBe('2 dari 5 stop')
    expect(t('dash.plans.stopsDoneOf', { done: 3, count: 5 })).toBe('3 stop selesai dari 5')
    expect(t('dash.plans.itemsTotal', { count: 4, weight: 12 })).toBe('Total 4 barang · 12 kg')
    expect(t('dash.wallet.pendingCount', { count: 1 })).toBe('1 top up menunggu pembayaran')
  })

  it('angka dan tanggal mengikuti bahasa', () => {
    setLocale('en')
    expect(t('dash.wallet.balanceAfter', { amount: 1_250_000 })).toBe('Balance Rp 1,250,000')
    expect(t('dash.wallet.amountPlaceholder', { example: 150_000 })).toBe('e.g. 150,000')
    expect(t('dash.app.authProofText', { count: 1280 })).toContain('1,280 deliveries')
    setLocale('id')
    expect(t('dash.wallet.balanceAfter', { amount: 1_250_000 })).toBe('Saldo Rp 1.250.000')
    expect(t('dash.wallet.amountPlaceholder', { example: 150_000 })).toBe('mis. 150.000')
    expect(t('dash.app.authProofText', { count: 1280 })).toContain('1.280 pengiriman')
  })

  it('kalimat waktu tunggu tidak menyisakan "expired left"', () => {
    setLocale('en')
    expect(t('dash.wallet.pendingMeta', { method: 'QRIS', remaining: '12 min' })).toBe('QRIS · 12 min left')
    expect(t('dash.wallet.pendingMetaExpired', { method: 'QRIS' })).toBe('QRIS · expired')
    setLocale('id')
    expect(t('dash.wallet.pendingMeta', { method: 'QRIS', remaining: '12 mnt' })).toBe('QRIS · sisa waktu 12 mnt')
    expect(t('dash.wallet.pendingMetaExpired', { method: 'QRIS' })).toBe('QRIS · sisa waktu habis')
  })
})
