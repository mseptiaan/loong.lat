import type { RouteStop, StopKind, StopStatus, Waypoint } from '@loong/domain'
import { DEFAULT_LOCALE, msg, renderMsg, setLocale, type Msg } from '@loong/i18n'
import { afterEach, describe, expect, it } from 'vitest'
import { groupItemsSummary, groupPinState, groupStops } from './route'

afterEach(() => setLocale(DEFAULT_LOCALE))

const wp = (name: string, lat: number, lng: number, placeId?: string): Waypoint => ({
  name,
  address: name,
  position: { lat, lng },
  placeId,
  contactName: 'Kontak',
  contactPhone: '0812',
})

let seq = 0
const stop = (kind: StopKind, waypoint: Waypoint, extra: Partial<RouteStop> = {}): RouteStop =>
  ({ id: `st-${(seq += 1)}`, seq, kind, shipmentId: `shp-${seq}`, waypoint, status: 'menunggu', ...extra }) as RouteStop

const parcel = (count: number, kg: number) => msg('srv.engine.itemsSummary', { count, kg })
const at = wp('Toko', -6.2, 106.8, 'plc-toko')
const group = (statuses: StopStatus[]) => {
  const stops = statuses.map((status) => stop('antar', at, { status }))
  return { first: stops[0]!, stops }
}

describe('groupStops', () => {
  it('menggabung stop berurutan di tempat bernama (placeId sama)', () => {
    const kemang = wp('Kemang Village', -6.2608, 106.8133, 'plc-kemang')
    const groups = groupStops([stop('ambil', kemang), stop('ambil', kemang), stop('antar', wp('Senayan City', -6.2275, 106.8029, 'plc-sen'))])
    expect(groups.map((g) => g.stops.length)).toEqual([2, 1])
  })

  it('titik di peta dengan nama sama tetapi letak berbeda TIDAK digabung', () => {
    // rencana nyata: satu titik ambil, empat titik antar berjauhan yang semuanya bernama "Pinned location"
    const pickup = wp('Pinned location', -6.217503, 106.845477)
    const drops = [
      wp('Pinned location', -6.18106, 106.769492),
      wp('Pinned location', -6.211228, 106.766093),
      wp('Pinned location', -6.171164, 106.861985),
      wp('Pinned location', -6.260775, 106.840454),
    ]
    const stops = [...drops.map(() => stop('ambil', pickup)), ...drops.map((d) => stop('antar', d))]
    const groups = groupStops(stops)
    expect(groups.map((g) => [g.first.kind, g.stops.length])).toEqual([
      ['ambil', 4], // empat paket diambil di satu titik yang sama
      ['antar', 1],
      ['antar', 1],
      ['antar', 1],
      ['antar', 1],
    ])
  })

  it('koordinat yang sama tanpa placeId digabung, selisih beberapa meter tidak', () => {
    const a = wp('Pinned location', -6.2, 106.8)
    expect(groupStops([stop('antar', a), stop('antar', wp('Titik lain', -6.2, 106.8))]).length).toBe(1)
    expect(groupStops([stop('antar', a), stop('antar', wp('Pinned location', -6.2, 106.8002))]).length).toBe(2)
  })

  it('jenis stop berbeda tidak digabung walau tempatnya sama', () => {
    const a = wp('Toko', -6.2, 106.8, 'plc-toko')
    expect(groupStops([stop('ambil', a), stop('antar', a)]).length).toBe(2)
  })

  it('placeId berbeda memisahkan walau koordinatnya berimpit', () => {
    expect(groupStops([stop('antar', wp('A', -6.2, 106.8, 'plc-a')), stop('antar', wp('B', -6.2, 106.8, 'plc-b'))]).length).toBe(2)
  })
})

describe('groupItemsSummary', () => {
  it('menjumlahkan barang dan berat tiap paket', () => {
    const stops = [0, 1, 2, 3].map(() => stop('ambil', at, { itemsSummary: parcel(1, 0.8) }))
    const total = groupItemsSummary(stops)!
    expect(total.params).toEqual({ count: 4, kg: 3.2 })
    expect(renderMsg(total)).toBe('4 items · 3.2 kg')
    setLocale('id')
    expect(renderMsg(total)).toBe('4 barang · 3,2 kg')
  })

  it('membulatkan jumlah pecahan agar tidak muncul 0.30000000000000004', () => {
    const total = groupItemsSummary([stop('ambil', at, { itemsSummary: parcel(1, 0.1) }), stop('ambil', at, { itemsSummary: parcel(2, 0.2) })])!
    expect(total.params).toEqual({ count: 3, kg: 0.3 })
  })

  it('kosong bila ada ringkasan yang bukan buatan mesin rute atau tanpa angka', () => {
    expect(groupItemsSummary([stop('ambil', at, { itemsSummary: parcel(1, 1) }), stop('ambil', at, { itemsSummary: { key: 'dom.itemsSummary' } as Msg })])).toBeUndefined()
    expect(groupItemsSummary([stop('ambil', at, { itemsSummary: { key: 'srv.engine.itemsSummary', params: { count: 1 } } as Msg })])).toBeUndefined()
  })
})

describe('groupPinState', () => {
  it('selesai hanya bila semua stop selesai', () => {
    expect(groupPinState(group(['selesai', 'selesai']), false)).toBe('done')
    expect(groupPinState(group(['selesai', 'menunggu']), false)).toBe('default')
  })

  it('satu stop bermasalah menandai seluruh kelompok', () => {
    expect(groupPinState(group(['selesai', 'bermasalah', 'menunggu']), true)).toBe('problem')
  })

  it('stop yang sedang dituju menandai kelompoknya aktif', () => {
    expect(groupPinState(group(['menunggu', 'menunggu']), true)).toBe('active')
    expect(groupPinState(group(['menunggu']), false)).toBe('default')
  })
})
