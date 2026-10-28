import type { Shipment } from '@loong/domain'

const norm = (s: string) =>
  s
    .toLowerCase()
    .normalize('NFD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[^a-z0-9]+/g, ' ')
    .trim()

/** Pengiriman yang tempat antarnya cocok dengan tempat yang disebut pelanggan (nama atau alamat, dua arah). */
export function shipmentsForPlace(shipments: Shipment[], place: string | undefined): number[] {
  const p = place ? norm(place) : ''
  if (!p) return []
  return shipments.flatMap((s, i) => {
    const name = norm(s.dropoff.name)
    const address = norm(s.dropoff.address)
    return (name && (name.includes(p) || p.includes(name))) || (address && address.includes(p)) ? [i] : []
  })
}
