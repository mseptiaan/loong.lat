import type { LatLng, VehicleType } from '@loong/domain'

/** Nomor lokal ("0812...") atau sudah internasional -> digit saja dengan kode negara 62 (format wa.me). */
export function toIntlPhone(phone: string): string {
  const digits = phone.replace(/\D/g, '')
  if (digits.startsWith('62')) return digits
  return `62${digits.replace(/^0/, '')}`
}

export const telUrl = (phone: string) => `tel:+${toIntlPhone(phone)}`

export const whatsappUrl = (phone: string, text?: string) => `https://wa.me/${toIntlPhone(phone)}${text ? `?text=${encodeURIComponent(text)}` : ''}`

/** Google Maps memahami moda motor ("two-wheeler"); blindvan memakai mobil. */
export const googleMapsUrl = (to: LatLng, vehicle: VehicleType) =>
  `https://www.google.com/maps/dir/?api=1&destination=${to.lat},${to.lng}&travelmode=${vehicle === 'motor' ? 'two-wheeler' : 'driving'}`

export const wazeUrl = (to: LatLng) => `https://waze.com/ul?ll=${to.lat},${to.lng}&navigate=yes`

/** Buka tautan luar di tab baru tanpa memberi akses `window.opener`. */
export const openExternal = (url: string) => window.open(url, '_blank', 'noopener,noreferrer')
