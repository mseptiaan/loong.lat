import {
  aggregateItems,
  aggregateShipments,
  fleetProfile,
  smallestFit,
  whyNotFit,
  type Aggregate,
  type Item,
  type PlaceRef,
  type Schedule,
  type Shipment,
  type Vehicle,
  type VehicleType,
  type Waypoint,
} from '@loong/domain'
import type { Msg } from '@loong/i18n'
import { t } from '../../i18n'
import { scheduleError } from '../../lib/schedule'

/** `label` adalah getter: dibaca saat render sehingga mengikuti bahasa aktif. */
export const STEPS = [
  { n: 1, get label() { return t('dash.wizard.stepRoute') } },
  { n: 2, get label() { return t('dash.wizard.stepItems') } },
  { n: 3, get label() { return t('dash.wizard.stepOptions') } },
  { n: 4, get label() { return t('dash.wizard.stepReview') } },
] as const

export const MAX_SHIPMENTS = 6

export const uid = (prefix: string) => `${prefix}-${Math.random().toString(36).slice(2, 9)}`

/** Waypoint belum dipilih: nama kosong (engine & server memperlakukannya sebagai "belum dipilih"). */
export const isSet = (w: Waypoint) => !!w.name

export const withPlace = (w: Waypoint, p: PlaceRef | undefined): Waypoint =>
  p
    ? { ...w, name: p.name, address: p.address, position: p.position, placeId: p.placeId }
    : { ...w, name: '', address: '', position: { lat: 0, lng: 0 }, placeId: undefined }

export const asPlaceRef = (w: Waypoint): PlaceRef | undefined =>
  isSet(w) ? { name: w.name, address: w.address, position: w.position, placeId: w.placeId } : undefined

export function emptyShipment(contact: { name: string; phone: string }): Shipment {
  const blank = (name: string, phone: string): Waypoint => ({
    name: '',
    address: '',
    position: { lat: 0, lng: 0 },
    contactName: name,
    contactPhone: phone,
  })
  return { id: uid('shp'), pickup: blank(contact.name, contact.phone), dropoff: blank('', ''), items: [] }
}

export function blankItem(): Item {
  return { id: uid('itm'), name: '', qty: 1, weightKg: 1, lengthCm: 20, widthCm: 20, heightCm: 10, fragile: false, source: 'manual' }
}

export type FieldErrors = Record<string, string>

const phoneOk = (v: string) => v.replace(/\D/g, '').length >= 9

/** Kapan driver diperkirakan berangkat: jam jadwal bila terjadwal, selain itu sebentar lagi. Batas waktu dihitung dari sini. */
export const departureMs = (schedule: Schedule, nowMs: number) =>
  schedule.mode === 'terjadwal' && schedule.window ? Date.parse(schedule.window.from) : nowMs + 6 * 60_000

/** Jeda minimal antara berangkat dan batas waktu tiba. */
export const MIN_DEADLINE_GAP_MS = 15 * 60_000

export function validateRoute(shipments: Shipment[], schedule: Schedule, nowMs: number): FieldErrors {
  const e: FieldErrors = {}
  shipments.forEach((s, i) => {
    if (!isSet(s.pickup)) e[`s${i}.pickup`] = t('dash.wizard.errPickup')
    if (!isSet(s.dropoff)) e[`s${i}.dropoff`] = t('dash.wizard.errDropoff')
    if (isSet(s.pickup) && isSet(s.dropoff) && s.pickup.position.lat === s.dropoff.position.lat && s.pickup.position.lng === s.dropoff.position.lng) {
      e[`s${i}.dropoff`] = t('dash.wizard.errSameSpot')
    }
    if (!s.pickup.contactName.trim()) e[`s${i}.pickupName`] = t('dash.wizard.errPickupName')
    if (!phoneOk(s.pickup.contactPhone)) e[`s${i}.pickupPhone`] = t('dash.wizard.errPickupPhone')
    if (!s.dropoff.contactName.trim()) e[`s${i}.dropoffName`] = t('dash.wizard.errDropoffName')
    if (!phoneOk(s.dropoff.contactPhone)) e[`s${i}.dropoffPhone`] = t('dash.wizard.errDropoffPhone')
    if (s.deadline && Date.parse(s.deadline) < departureMs(schedule, nowMs) + MIN_DEADLINE_GAP_MS) {
      e[`s${i}.deadline`] = t('dash.wizard.errDeadline')
    }
  })
  const sch = scheduleError(schedule, nowMs)
  if (sch) e.schedule = sch
  return e
}

export interface FitInfo {
  agg: Aggregate
  /** Tipe kendaraan terkecil yang muat untuk SELURUH barang di satu rute; undefined = tidak muat di mana pun. */
  type?: VehicleType
  /** Alasan tidak muat di motor (bila perlu blindvan atau tidak muat sama sekali); pesan netral-bahasa, tampilkan dengan renderMsg. */
  reason?: Msg
  /** Armada tidak diketahui di sisi klien (backend asli): tidak ada kesimpulan muat atau tidak muat; backend yang memutuskan saat menghitung opsi. */
  unknown?: boolean
}

export function fitInfo(items: Item[], vehicles: Vehicle[] | undefined): FitInfo {
  const agg = aggregateItems(items)
  if (!vehicles) return { agg, unknown: true }
  const fleet = fleetProfile(vehicles)
  const type = smallestFit(agg, fleet)
  const reason = fleet.motor && (type !== 'motor') ? (whyNotFit(agg, fleet.motor) ?? undefined) : undefined
  return { agg, type, reason }
}

/** Satu pengiriman yang barangnya melebihi kapasitas SEMUA kendaraan tak bisa dihitung; gabungan antar pengiriman boleh dipecah. */
export function validateItems(shipments: Shipment[], vehicles: Vehicle[] | undefined): FieldErrors {
  const e: FieldErrors = {}
  // tanpa data armada (backend asli) aturan muat tidak diperiksa di sini: backend menolak muatan yang tak muat di mana pun saat opsi dihitung
  const fleet = vehicles ? fleetProfile(vehicles) : undefined
  shipments.forEach((s, i) => {
    if (s.items.length === 0) {
      e[`s${i}.items`] = t('dash.wizard.errNoItems')
      return
    }
    s.items.forEach((it, j) => {
      if (!it.name.trim()) e[`s${i}.i${j}.name`] = t('dash.wizard.errItemName')
      if (!(it.weightKg > 0)) e[`s${i}.i${j}.weight`] = t('dash.wizard.errWeight')
      if (!(it.lengthCm > 0) || !(it.widthCm > 0) || !(it.heightCm > 0)) e[`s${i}.i${j}.dims`] = t('dash.wizard.errDims')
    })
    if (!fleet) return
    const agg = aggregateShipments([s])
    if (!smallestFit(agg, fleet)) {
      const why = fleet.blindvan ? whyNotFit(agg, fleet.blindvan) : null
      e[`s${i}.fit`] = why ? t('dash.wizard.errFitWhy', { why }) : t('dash.wizard.errFit')
    }
  })
  return e
}

/** Titik-titik yang sudah dipilih, untuk pratinjau peta langkah 1. */
export function pickedPoints(shipments: Shipment[]) {
  return shipments.flatMap((s, i) => [
    ...(isSet(s.pickup) ? [{ key: `${s.id}:p`, n: i + 1, kind: 'ambil' as const, position: s.pickup.position }] : []),
    ...(isSet(s.dropoff) ? [{ key: `${s.id}:d`, n: i + 1, kind: 'antar' as const, position: s.dropoff.position }] : []),
  ])
}
