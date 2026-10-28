import type { FareLine, Vehicle } from '@loong/domain'
import { msg } from '@loong/i18n'

export interface BillableLoad {
  /** Jarak tertagih: dari stop pertama sampai terakhir (perjalanan menjemput driver tidak ditagih). */
  km: number
  min: number
  stops: number
  weightKg: number
}

const roundTo = (n: number, step: number) => Math.round(n / step) * step

/** Tarif satu rute: tarif dasar + jarak + waktu + stop tambahan + muatan berat, dibulatkan ke Rp 500. */
export function computeFare(vehicle: Pick<Vehicle, 'fare'>, load: BillableLoad): { lines: FareLine[]; total: number } {
  const f = vehicle.fare
  const lines: FareLine[] = [
    { label: msg('srv.engine.fare.base'), amountRp: f.base },
    { label: msg('srv.engine.fare.distance', { km: load.km }), amountRp: Math.round(load.km * f.perKm) },
    { label: msg('srv.engine.fare.time', { min: load.min }), amountRp: Math.round(load.min * f.perMin) },
  ]
  const extraStops = Math.max(0, load.stops - 2)
  if (extraStops > 0) lines.push({ label: msg('srv.engine.fare.extraStops', { count: extraStops }), amountRp: extraStops * f.perExtraStop })
  if (load.weightKg > f.surchargeAboveKg && f.surchargePerKg > 0) {
    lines.push({
      label: msg('srv.engine.fare.weight', { kg: load.weightKg, over: f.surchargeAboveKg }),
      amountRp: Math.round((load.weightKg - f.surchargeAboveKg) * f.surchargePerKg),
    })
  }
  const raw = lines.reduce((sum, l) => sum + l.amountRp, 0)
  const total = Math.max(roundTo(raw, 500), 500)
  const rounding = total - raw
  if (rounding !== 0) lines.push({ label: msg('srv.engine.fare.rounding'), amountRp: rounding })
  return { lines, total }
}
