import type { FareConfig, Vehicle, VehicleType } from './types'

/** Tarif bawaan per tipe kendaraan; dipakai seed dan sebagai nilai awal form kendaraan baru di Backoffice. */
export const DEFAULT_FARE: Record<VehicleType, FareConfig> = {
  motor: { base: 6000, perKm: 1800, perMin: 150, perExtraStop: 2500, surchargePerKg: 800, surchargeAboveKg: 10 },
  blindvan: { base: 45000, perKm: 4500, perMin: 500, perExtraStop: 8000, surchargePerKg: 150, surchargeAboveKg: 300 },
}

/** Kapasitas bawaan per tipe kendaraan. */
export const DEFAULT_CAPACITY: Record<VehicleType, Pick<Vehicle, 'maxWeightKg' | 'maxVolumeM3' | 'maxDimensionCm'>> = {
  motor: { maxWeightKg: 20, maxVolumeM3: 0.12, maxDimensionCm: 55 },
  blindvan: { maxWeightKg: 500, maxVolumeM3: 3.2, maxDimensionCm: 190 },
}
