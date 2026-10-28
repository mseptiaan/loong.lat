import type { LatLng, LngLat, Route } from './types'

const EARTH_KM = 6371.0088

/** Pengali jarak lurus ke jarak jalan untuk kota padat seperti Jakarta (dasar mock engine). */
export const ROAD_FACTOR = 1.28

const rad = (d: number) => (d * Math.PI) / 180
const deg = (r: number) => (r * 180) / Math.PI

export function haversineKm(a: LatLng, b: LatLng): number {
  const dLat = rad(b.lat - a.lat)
  const dLng = rad(b.lng - a.lng)
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(rad(a.lat)) * Math.cos(rad(b.lat)) * Math.sin(dLng / 2) ** 2
  return 2 * EARTH_KM * Math.asin(Math.min(1, Math.sqrt(h)))
}

/** Perkiraan jarak jalan dari jarak lurus. */
export function roadKm(a: LatLng, b: LatLng): number {
  return haversineKm(a, b) * ROAD_FACTOR
}

/** Arah dari a ke b dalam derajat (0 = utara, searah jarum jam). */
export function bearingDeg(a: LatLng, b: LatLng): number {
  const y = Math.sin(rad(b.lng - a.lng)) * Math.cos(rad(b.lat))
  const x =
    Math.cos(rad(a.lat)) * Math.sin(rad(b.lat)) - Math.sin(rad(a.lat)) * Math.cos(rad(b.lat)) * Math.cos(rad(b.lng - a.lng))
  return (deg(Math.atan2(y, x)) + 360) % 360
}

export const toLngLat = (p: LatLng): LngLat => [p.lng, p.lat]
export const toLatLng = (p: LngLat): LatLng => ({ lat: p[1], lng: p[0] })

export function centroid(points: LatLng[]): LatLng {
  if (points.length === 0) return { lat: -6.2, lng: 106.82 }
  const sum = points.reduce((acc, p) => ({ lat: acc.lat + p.lat, lng: acc.lng + p.lng }), { lat: 0, lng: 0 })
  return { lat: sum.lat / points.length, lng: sum.lng / points.length }
}

/** Kotak batas [barat, selatan, timur, utara] untuk fitBounds. */
export function boundsOf(points: LatLng[]): [LngLat, LngLat] | null {
  if (points.length === 0) return null
  let w = Infinity
  let s = Infinity
  let e = -Infinity
  let n = -Infinity
  for (const p of points) {
    w = Math.min(w, p.lng)
    e = Math.max(e, p.lng)
    s = Math.min(s, p.lat)
    n = Math.max(n, p.lat)
  }
  return [
    [w, s],
    [e, n],
  ]
}

export function polylineKm(points: LngLat[]): number {
  let total = 0
  for (let i = 1; i < points.length; i++) {
    total += haversineKm(toLatLng(points[i - 1]!), toLatLng(points[i]!))
  }
  return total
}

/** Titik pada polyline setelah menempuh `km` dari awal, beserta arah gerak. */
export function pointAlong(points: LngLat[], km: number): { position: LatLng; bearing: number } {
  if (points.length === 0) return { position: { lat: -6.2, lng: 106.82 }, bearing: 0 }
  if (points.length === 1) return { position: toLatLng(points[0]!), bearing: 0 }
  let remaining = Math.max(0, km)
  for (let i = 1; i < points.length; i++) {
    const a = toLatLng(points[i - 1]!)
    const b = toLatLng(points[i]!)
    const seg = haversineKm(a, b)
    if (remaining <= seg || i === points.length - 1) {
      const t = seg === 0 ? 1 : Math.min(1, remaining / seg)
      return {
        position: { lat: a.lat + (b.lat - a.lat) * t, lng: a.lng + (b.lng - a.lng) * t },
        bearing: bearingDeg(a, b),
      }
    }
    remaining -= seg
  }
  return { position: toLatLng(points[points.length - 1]!), bearing: 0 }
}

/** Membelah polyline di jarak `km` dari awal: [bagian yang sudah ditempuh, bagian tersisa]. Keduanya berbagi titik belah. */
export function splitPolyline(points: LngLat[], km: number): [LngLat[], LngLat[]] {
  if (points.length < 2 || km <= 0) return [points.slice(0, 1), points]
  let remaining = km
  for (let i = 1; i < points.length; i++) {
    const a = toLatLng(points[i - 1]!)
    const b = toLatLng(points[i]!)
    const seg = haversineKm(a, b)
    if (remaining <= seg) {
      const t = seg === 0 ? 1 : remaining / seg
      const cut: LngLat = [a.lng + (b.lng - a.lng) * t, a.lat + (b.lat - a.lat) * t]
      return [[...points.slice(0, i), cut], [cut, ...points.slice(i)]]
    }
    remaining -= seg
  }
  return [points, points.slice(-1)]
}

/** Potongan awal polyline sepanjang `fraction` (0..1) dari panjang total; untuk animasi menggambar rute. */
export function partialPolyline(points: LngLat[], fraction: number): LngLat[] {
  if (fraction >= 1) return points
  if (fraction <= 0 || points.length < 2) return points.slice(0, 1)
  return splitPolyline(points, polylineKm(points) * fraction)[0]
}

/** Pseudo-acak deterministik dari string, 0..1. */
export function hash01(input: string): number {
  let h = 2166136261
  for (let i = 0; i < input.length; i++) {
    h ^= input.charCodeAt(i)
    h = Math.imul(h, 16777619)
  }
  return ((h >>> 0) % 100000) / 100000
}

/**
 * Jalur halus antara dua titik (kurva kuadrat dengan simpangan tegak lurus yang deterministik),
 * dipakai sebagai pengganti jalan sungguhan selama fase mockup.
 */
export function smoothPath(a: LatLng, b: LatLng, seed: string, samples = 28): LngLat[] {
  const dLat = b.lat - a.lat
  const dLng = b.lng - a.lng
  const len = Math.hypot(dLat, dLng)
  const side = hash01(seed) > 0.5 ? 1 : -1
  const bend = 0.12 + hash01(seed + 'b') * 0.1
  // vektor tegak lurus
  const px = -dLat / (len || 1)
  const py = dLng / (len || 1)
  const cLat = (a.lat + b.lat) / 2 + px * len * bend * side
  const cLng = (a.lng + b.lng) / 2 + py * len * bend * side
  const out: LngLat[] = []
  for (let i = 0; i <= samples; i++) {
    const t = i / samples
    const u = 1 - t
    out.push([u * u * a.lng + 2 * u * t * cLng + t * t * b.lng, u * u * a.lat + 2 * u * t * cLat + t * t * b.lat])
  }
  return out
}

/** Geometri rute: titik awal lalu setiap stop, tiap ruas berupa jalur halus. */
export function buildRouteGeometry(route: Pick<Route, 'id' | 'startPosition' | 'stops' | 'geometry'>): LngLat[] {
  if (route.geometry && route.geometry.length > 1) return route.geometry
  const pts: LatLng[] = [route.startPosition, ...route.stops.map((s) => s.waypoint.position)]
  const out: LngLat[] = []
  for (let i = 1; i < pts.length; i++) {
    const a = pts[i - 1]!
    const b = pts[i]!
    // seed dari koordinat ujung ruas (bukan id rute) agar bentuk jalur sama di pratinjau opsi dan setelah konfirmasi
    const seg = smoothPath(a, b, `${a.lat.toFixed(4)},${a.lng.toFixed(4)}>${b.lat.toFixed(4)},${b.lng.toFixed(4)}`)
    out.push(...(i === 1 ? seg : seg.slice(1)))
  }
  return out
}

/** Titik yang digeser `km` ke arah `bearing` derajat (pendekatan datar, cukup untuk jarak pendek). */
export function offsetPoint(p: LatLng, km: number, bearing: number): LatLng {
  const dLat = (km * Math.cos(rad(bearing))) / 111.32
  const dLng = (km * Math.sin(rad(bearing))) / (111.32 * Math.cos(rad(p.lat)))
  return { lat: p.lat + dLat, lng: p.lng + dLng }
}

/** Selisih jarak (km) yang masih dianggap "sama dekat" saat memasangkan stop ke titik jalur. */
const SNAP_KM = 0.02

/** Jarak kumulatif (km) di polyline untuk tiap stop, dipakai simulator untuk menaruh stop pada jalur. */
export function stopOffsetsKm(route: Pick<Route, 'id' | 'startPosition' | 'stops' | 'geometry'>): number[] {
  const geom = buildRouteGeometry(route)
  // jarak kumulatif di tiap titik geometri
  const cum: number[] = [0]
  for (let i = 1; i < geom.length; i++) {
    cum.push(cum[i - 1]! + haversineKm(toLatLng(geom[i - 1]!), toLatLng(geom[i]!)))
  }
  // tiap stop dipasangkan ke titik geometri terdekat yang tidak mundur dari stop sebelumnya. Jalan sungguhan bisa
  // melewati dekat sebuah stop lebih dari sekali (mis. putar balik), jadi dipilih titik PERTAMA yang masih dalam
  // SNAP_KM dari yang terdekat, bukan yang kebetulan paling dekat belakangan.
  const offsets: number[] = []
  let from = 0
  for (const stop of route.stops) {
    const target = stop.waypoint.position
    const dist: number[] = []
    let nearest = Infinity
    for (let i = from; i < geom.length; i++) {
      const km = haversineKm(toLatLng(geom[i]!), target)
      dist.push(km)
      if (km < nearest) nearest = km
    }
    let best = from
    for (let i = 0; i < dist.length; i++) {
      if (dist[i]! <= nearest + SNAP_KM) {
        best = from + i
        break
      }
    }
    offsets.push(cum[best] ?? 0)
    from = best
  }
  return offsets
}
