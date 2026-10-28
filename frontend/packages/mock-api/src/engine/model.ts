import { ApiError, ITEM_CATALOG, type Item, type Shipment } from '@loong/domain'
import { msg, type Msg, type Value } from '@loong/i18n'

// Perhitungan muatan ada di domain (dipakai juga oleh wizard); di sini hanya diekspor ulang agar impor engine tetap pendek.
export {
  aggregateItems,
  aggregateShipments,
  fitsVehicle,
  fleetProfile,
  smallestFit,
  type Aggregate,
  type FleetProfile,
} from '@loong/domain'

/** Nama barang untuk pesan galat: barang katalog mengikuti bahasa layar, barang ketikan pengguna apa adanya. */
const itemName = (it: Item): Value =>
  it.catalogKey && ITEM_CATALOG.some((c) => c.key === it.catalogKey) ? { key: `cat.${it.catalogKey}` } : it.name.trim() ? it.name : msg('srv.engine.itemFallback')

/** Pesan galat (netral-bahasa) bila masukan tidak bisa dihitung; null bila valid. */
export function validateShipments(shipments: Shipment[]): { message: Msg; field?: string } | null {
  if (shipments.length === 0) return { message: msg('srv.engine.needShipment'), field: 'shipments' }
  for (const [i, s] of shipments.entries()) {
    const n = i + 1
    for (const [label, w] of [
      ['pickup', s.pickup],
      ['tujuan', s.dropoff],
    ] as const) {
      const p = w.position
      if (!w.name || !Number.isFinite(p.lat) || !Number.isFinite(p.lng)) {
        return { message: msg(label === 'pickup' ? 'srv.engine.pickupMissing' : 'srv.engine.dropoffMissing', { n }), field: `shipments.${i}.${label}` }
      }
    }
    if (s.items.length === 0) return { message: msg('srv.engine.itemsMissing', { n }), field: `shipments.${i}.items` }
    for (const it of s.items) {
      if (!it.name.trim() || it.qty < 1 || !(it.weightKg > 0) || !(it.lengthCm > 0) || !(it.widthCm > 0) || !(it.heightCm > 0)) {
        return { message: msg('srv.engine.itemIncomplete', { n, item: itemName(it) }), field: `shipments.${i}.items` }
      }
    }
  }
  return null
}

export function assertValid(shipments: Shipment[]): void {
  const err = validateShipments(shipments)
  if (err) throw new ApiError('tidak_valid', err.message, { field: err.field })
}
