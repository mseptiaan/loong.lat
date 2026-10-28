import { afterEach, describe, expect, it } from 'vitest'
import { itemFromCatalog, type Shipment, type Vehicle, type Waypoint } from '@loong/domain'
import { getPlace } from '@loong/gazetteer'
import { getLocale, renderMsg, setLocale } from '@loong/i18n'
import { makeVehicles } from '@loong/mock-api'
import { STEPS, emptyShipment, fitInfo, validateItems, validateRoute, withPlace } from './model'

const NOW = Date.parse('2026-10-20T03:00:00Z')
const vehicles: Vehicle[] = makeVehicles()

function placed(from: string, to: string): Shipment {
  const base = emptyShipment({ name: 'Dina', phone: '081311220001' })
  const ref = (id: string) => {
    const p = getPlace(id)!
    return { placeId: p.id, name: p.name, address: p.address, position: p.position }
  }
  const dropoff: Waypoint = { ...withPlace(base.dropoff, ref(to)), contactName: 'Bu Ratna', contactPhone: '081290010001' }
  return { ...base, pickup: withPlace(base.pickup, ref(from)), dropoff }
}

describe('validateRoute', () => {
  it('pengiriman kosong: tiap lokasi diminta dipilih, kontak penerima diminta diisi', () => {
    const e = validateRoute([emptyShipment({ name: 'Dina', phone: '081311220001' })], { mode: 'sekarang' }, NOW)
    expect(e['s0.pickup']).toMatch(/pickup/)
    expect(e['s0.dropoff']).toMatch(/drop-off/)
    expect(e['s0.dropoffName']).toBeDefined()
    expect(e['s0.pickupName']).toBeUndefined()
  })

  it('lengkap: tanpa galat', () => {
    expect(validateRoute([placed('kemang-village', 'plaza-senayan')], { mode: 'sekarang' }, NOW)).toEqual({})
  })

  it('tujuan sama dengan pengambilan ditolak', () => {
    const e = validateRoute([placed('kemang-village', 'kemang-village')], { mode: 'sekarang' }, NOW)
    expect(e['s0.dropoff']).toMatch(/same as the pickup/)
  })

  it('jadwal terlalu dekat atau di luar rentang ditolak', () => {
    const s = [placed('kemang-village', 'plaza-senayan')]
    const soon = new Date(NOW + 5 * 60_000).toISOString()
    expect(validateRoute(s, { mode: 'terjadwal', window: { from: soon, to: soon } }, NOW).schedule).toMatch(/20 minutes/)
    const far = new Date(NOW + 20 * 86_400_000).toISOString()
    expect(validateRoute(s, { mode: 'terjadwal', window: { from: far, to: far } }, NOW).schedule).toMatch(/14 days/)
    expect(validateRoute(s, { mode: 'terjadwal', window: undefined }, NOW).schedule).toMatch(/date/)
  })

  it('nomor HP pendek ditolak untuk pengambilan dan penerima', () => {
    const s = placed('kemang-village', 'plaza-senayan')
    s.pickup.contactPhone = '0812'
    s.dropoff.contactPhone = '12'
    const e = validateRoute([s], { mode: 'sekarang' }, NOW)
    expect(e['s0.pickupPhone']).toBeDefined()
    expect(e['s0.dropoffPhone']).toBeDefined()
  })
})

describe('validateItems & fitInfo', () => {
  const item = (key: string, qty = 1) => itemFromCatalog(key, { id: `i-${key}`, qty })

  it('tanpa barang diminta menambah; barang cacat ditunjuk per kolom', () => {
    const s = placed('kemang-village', 'plaza-senayan')
    expect(validateItems([s], vehicles)['s0.items']).toBeDefined()
    s.items = [{ ...item('paket-kecil'), name: ' ', weightKg: 0 }]
    const e = validateItems([s], vehicles)
    expect(e['s0.i0.name']).toBeDefined()
    expect(e['s0.i0.weight']).toBeDefined()
  })

  it('barang ringan muat motor; barang besar butuh blindvan dan alasannya jelas', () => {
    const light = fitInfo([item('dokumen', 2)], vehicles)
    expect(light.type).toBe('motor')
    expect(light.reason).toBeUndefined()
    const heavy = fitInfo([item('rak')], vehicles)
    expect(heavy.type).toBe('blindvan')
    expect(renderMsg(heavy.reason!, 'en')).toMatch(/exceeds/)
  })

  it('barang melebihi semua kendaraan ditolak dengan penjelasan', () => {
    const s = placed('kemang-village', 'plaza-senayan')
    s.items = [item('kulkas-mini', 40)]
    expect(fitInfo(s.items, vehicles).type).toBeUndefined()
    expect(validateItems([s], vehicles)['s0.fit']).toMatch(/exceed the capacity of all vehicles \(.+\)/)
  })
})

describe('armada tidak diketahui di klien (backend asli)', () => {
  const item = (key: string, qty = 1) => itemFromCatalog(key, { id: `i-${key}`, qty })

  it('tidak ada kesimpulan muat atau tidak muat: backend yang memutuskan saat opsi dihitung', () => {
    const s = placed('kemang-village', 'plaza-senayan')
    s.items = [item('kulkas-mini', 40)] // melebihi semua kendaraan bila armadanya dikenal
    const fit = fitInfo(s.items, undefined)
    expect(fit.unknown).toBe(true)
    expect(fit.type).toBeUndefined()
    expect(fit.reason).toBeUndefined()
    expect(fit.agg.itemCount).toBe(40) // ringkasan jumlah dan berat tetap ada
    expect(validateItems([s], undefined)['s0.fit']).toBeUndefined()
  })

  it('aturan lain tetap berlaku: barang kosong, nama kosong, berat nol', () => {
    const s = placed('kemang-village', 'plaza-senayan')
    expect(validateItems([s], undefined)['s0.items']).toBeDefined()
    s.items = [{ ...item('paket-kecil'), name: ' ', weightKg: 0 }]
    const e = validateItems([s], undefined)
    expect(e['s0.i0.name']).toBeDefined()
    expect(e['s0.i0.weight']).toBeDefined()
  })

  it('armada yang dikenal tetap memutuskan seperti biasa', () => {
    const s = placed('kemang-village', 'plaza-senayan')
    s.items = [item('kulkas-mini', 40)]
    expect(fitInfo(s.items, vehicles).unknown).toBeUndefined()
    expect(validateItems([s], vehicles)['s0.fit']).toBeDefined()
  })
})

describe('batas waktu per pengiriman', () => {
  const ship = (deadline?: string): Shipment => ({ ...placed('kemang-village', 'plaza-senayan'), ...(deadline ? { deadline } : {}) })

  it('tanpa batas waktu sah; terlalu dekat dengan jam berangkat ditolak; 15 menit setelah berangkat cukup', () => {
    expect(validateRoute([ship()], { mode: 'sekarang' }, NOW)['s0.deadline']).toBeUndefined()
    const tooSoon = new Date(NOW + 10 * 60_000).toISOString()
    expect(validateRoute([ship(tooSoon)], { mode: 'sekarang' }, NOW)['s0.deadline']).toMatch(/too close/)
    const ok = new Date(NOW + 6 * 60_000 + 15 * 60_000).toISOString() // berangkat = sekarang + 6 menit
    expect(validateRoute([ship(ok)], { mode: 'sekarang' }, NOW)['s0.deadline']).toBeUndefined()
  })

  it('tiap pengiriman dinilai sendiri dan jam berangkat mengikuti jadwal', () => {
    const later = NOW + 5 * 3_600_000
    const schedule = { mode: 'terjadwal' as const, window: { from: new Date(later).toISOString(), to: new Date(later + 3_600_000).toISOString() } }
    const errors = validateRoute([ship(new Date(later + 30 * 60_000).toISOString()), ship(new Date(later - 60 * 60_000).toISOString())], schedule, NOW)
    expect(errors['s0.deadline']).toBeUndefined()
    expect(errors['s1.deadline']).toBeDefined()
  })
})

describe('bahasa', () => {
  const before = getLocale()
  afterEach(() => setLocale(before))

  it('langkah wizard punya label di kedua bahasa dan mengikuti bahasa aktif', () => {
    setLocale('en')
    expect(STEPS.map((x) => x.label)).toEqual(['Route & schedule', 'Items', 'Choose an option', 'Review & confirm'])
    setLocale('id')
    expect(STEPS.map((x) => x.label)).toEqual(['Rute & jadwal', 'Barang', 'Pilih opsi', 'Tinjau & konfirmasi'])
  })

  it('galat validasi memakai bahasa saat validasi dijalankan', () => {
    const blank = [emptyShipment({ name: 'Dina', phone: '081311220001' })]
    setLocale('id')
    const id = validateRoute(blank, { mode: 'sekarang' }, NOW)
    expect(id['s0.pickup']).toBe('Pilih lokasi pengambilan.')
    expect(id['s0.dropoffName']).toBe('Isi nama penerima.')
    setLocale('en')
    expect(validateRoute(blank, { mode: 'sekarang' }, NOW)['s0.pickup']).toBe('Choose a pickup location.')
  })

  it('alasan tidak muat adalah pesan netral-bahasa yang dirender sesuai bahasa', () => {
    const heavy = fitInfo([itemFromCatalog('rak', { id: 'i-rak' })], vehicles)
    expect(heavy.reason?.key).toMatch(/^dom\.fit\./)
    expect(renderMsg(heavy.reason!, 'id')).toMatch(/melebihi/)
  })
})
