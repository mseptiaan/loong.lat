import type { LngLat } from '@loong/domain'

/*
 * Format "encoded polyline" (presisi 5 = ±1 m). Dipakai untuk membaca jawaban Directions dan untuk
 * menyimpan jalur di cache lokal dengan ukuran kecil (±5 byte per titik).
 */

const FACTOR = 1e5

/** Membaca polyline ke pasangan [lng, lat] (format aslinya lat,lng). */
export function decodePolyline(encoded: string): LngLat[] {
  const out: LngLat[] = []
  let index = 0
  let lat = 0
  let lng = 0
  const next = (): number => {
    let result = 0
    let shift = 0
    let byte: number
    do {
      if (index >= encoded.length) throw new Error('polyline terpotong')
      byte = encoded.charCodeAt(index++) - 63
      result |= (byte & 0x1f) << shift
      shift += 5
    } while (byte >= 0x20)
    return result & 1 ? ~(result >> 1) : result >> 1
  }
  while (index < encoded.length) {
    lat += next()
    lng += next()
    out.push([lng / FACTOR, lat / FACTOR])
  }
  return out
}

export function encodePolyline(points: LngLat[]): string {
  let out = ''
  let prevLat = 0
  let prevLng = 0
  const put = (delta: number) => {
    let v = delta < 0 ? ~(delta << 1) : delta << 1
    while (v >= 0x20) {
      out += String.fromCharCode((0x20 | (v & 0x1f)) + 63)
      v >>= 5
    }
    out += String.fromCharCode(v + 63)
  }
  for (const [lng, lat] of points) {
    const la = Math.round(lat * FACTOR)
    const ln = Math.round(lng * FACTOR)
    put(la - prevLat)
    put(ln - prevLng)
    prevLat = la
    prevLng = ln
  }
  return out
}
