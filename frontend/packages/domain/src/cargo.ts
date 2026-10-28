import type { Msg } from '@loong/i18n'
import type { Item, Shipment, Vehicle, VehicleType } from './types'

/**
 * Agregat muatan dan kecocokan kendaraan. Murni dan kecil, dipakai bersama oleh wizard (umpan balik instan
 * "muat di motor?") dan mesin tiruan. Backend asli menghitung ulang dengan aturan yang sama.
 */
export interface Aggregate {
  weightKg: number
  volumeM3: number
  /** Sisi terpanjang di antara semua barang (cm). */
  maxDimCm: number
  itemCount: number
  fragile: boolean
}

export function aggregateItems(items: Item[]): Aggregate {
  let weightKg = 0
  let volumeM3 = 0
  let maxDimCm = 0
  let itemCount = 0
  let fragile = false
  for (const it of items) {
    weightKg += it.qty * it.weightKg
    volumeM3 += (it.qty * it.lengthCm * it.widthCm * it.heightCm) / 1_000_000
    maxDimCm = Math.max(maxDimCm, it.lengthCm, it.widthCm, it.heightCm)
    itemCount += it.qty
    fragile ||= it.fragile
  }
  return { weightKg, volumeM3, maxDimCm, itemCount, fragile }
}

export function aggregateShipments(shipments: Shipment[]): Aggregate {
  return aggregateItems(shipments.flatMap((s) => s.items))
}

type Capacity = Pick<Vehicle, 'maxWeightKg' | 'maxVolumeM3' | 'maxDimensionCm'>

export function fitsVehicle(a: Aggregate, v: Capacity): boolean {
  return a.weightKg <= v.maxWeightKg && a.volumeM3 <= v.maxVolumeM3 && a.maxDimCm <= v.maxDimensionCm
}

/** Satu kendaraan "wakil" per tipe (yang aktif dan paling murah tarif dasarnya) sebagai acuan tarif & kapasitas. */
export type FleetProfile = Partial<Record<VehicleType, Vehicle>>

export function fleetProfile(vehicles: Vehicle[]): FleetProfile {
  const profile: FleetProfile = {}
  for (const v of vehicles) {
    if (v.status !== 'aktif') continue
    const cur = profile[v.type]
    if (!cur || v.fare.base < cur.fare.base) profile[v.type] = v
  }
  return profile
}

/** Tipe terkecil yang muat untuk agregat ini, atau undefined bila tidak ada yang muat. */
export function smallestFit(a: Aggregate, fleet: FleetProfile): VehicleType | undefined {
  if (fleet.motor && fitsVehicle(a, fleet.motor)) return 'motor'
  if (fleet.blindvan && fitsVehicle(a, fleet.blindvan)) return 'blindvan'
  return undefined
}

/** Alasan singkat sebuah muatan tidak muat di kendaraan tertentu (pesan netral-bahasa), atau null bila muat. */
export function whyNotFit(a: Aggregate, v: Capacity): Msg | null {
  if (a.maxDimCm > v.maxDimensionCm) return { key: 'dom.fit.dimension', params: { dim: a.maxDimCm, max: v.maxDimensionCm } }
  if (a.weightKg > v.maxWeightKg) return { key: 'dom.fit.weight', params: { kg: a.weightKg, max: v.maxWeightKg } }
  if (a.volumeM3 > v.maxVolumeM3) return { key: 'dom.fit.volume', params: { m3: a.volumeM3, max: v.maxVolumeM3 } }
  return null
}
