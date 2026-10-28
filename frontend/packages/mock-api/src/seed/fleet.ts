import { DEFAULT_CAPACITY, DEFAULT_FARE, type Vehicle, type VehicleType } from '@loong/domain'

const VEHICLES: [VehicleType, string, string][] = [
  ['motor', 'B 3412 KJR', 'Honda Vario 160'],
  ['motor', 'B 6620 UFD', 'Yamaha NMAX'],
  ['motor', 'B 4187 SRA', 'Honda BeAT'],
  ['motor', 'B 5233 LMN', 'Honda Vario 125'],
  ['motor', 'B 2918 RFT', 'Yamaha Aerox'],
  ['blindvan', 'B 9087 TXA', 'Daihatsu Gran Max Blind Van'],
  ['blindvan', 'B 9341 KDB', 'Suzuki Carry Blind Van'],
  ['blindvan', 'B 9560 PQC', 'Mitsubishi L300 Box'],
]

export function makeVehicles(): Vehicle[] {
  return VEHICLES.map(([type, plate, model], i) => ({
    id: `veh-${i + 1}`,
    type,
    plate,
    model,
    ...DEFAULT_CAPACITY[type],
    fare: { ...DEFAULT_FARE[type] },
    status: 'aktif',
  }))
}
