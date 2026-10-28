import type { Shipment } from '@loong/domain'
import { describe, expect, it } from 'vitest'
import { shipmentsForPlace } from './intent'

const ship = (name: string, address: string) =>
  ({ dropoff: { name, address, position: { lat: 0, lng: 0 }, contactName: '', contactPhone: '' } }) as unknown as Shipment

const two = [ship('Plaza Senayan', 'Plaza Senayan, Jakarta Selatan, Jakarta'), ship('Kota Kasablanka', 'Kota Kasablanka, Tebet, Jakarta')]

describe('shipmentsForPlace', () => {
  it('memilih pengiriman yang tempat antarnya disebut, tanpa peka huruf besar dan tanda baca', () => {
    expect(shipmentsForPlace(two, 'kota kasablanka')).toEqual([1])
    expect(shipmentsForPlace(two, 'Kasablanka')).toEqual([1])
    expect(shipmentsForPlace(two, 'Tebet')).toEqual([1])
  })

  it('tanpa tempat atau tanpa yang cocok: kosong, jadi pemanggil memasang ke semua pengiriman', () => {
    expect(shipmentsForPlace(two, undefined)).toEqual([])
    expect(shipmentsForPlace(two, '  ')).toEqual([])
    expect(shipmentsForPlace(two, 'Bandung')).toEqual([])
  })

  it('kawasan yang dipakai beberapa pengiriman cocok ke semuanya', () => {
    expect(shipmentsForPlace(two, 'Jakarta')).toEqual([0, 1])
  })
})
