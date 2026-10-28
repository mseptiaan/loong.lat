import type { LngLat } from '@loong/domain'

const round5 = (n: number) => Math.round(n * 1e5) / 1e5

/**
 * Douglas-Peucker pada bidang lokal (meter). Jalur jalan dari Directions sangat rapat; dibuang titik yang
 * menyimpang kurang dari `toleranceM` supaya DB (localStorage) dan render peta tetap ringan.
 * Titik pertama dan terakhir selalu dipertahankan; koordinat dibulatkan ke 5 desimal (±1 m).
 */
export function simplifyPath(points: LngLat[], toleranceM = 4): LngLat[] {
  if (points.length <= 2) return points.map(([lng, lat]) => [round5(lng), round5(lat)])
  const lat0 = points[0]![1]
  const mx = 111_320 * Math.cos((lat0 * Math.PI) / 180)
  const my = 110_540
  const xy = points.map(([lng, lat]) => [lng * mx, lat * my] as const)

  const keep = new Uint8Array(points.length)
  keep[0] = 1
  keep[points.length - 1] = 1
  const stack: [number, number][] = [[0, points.length - 1]]
  while (stack.length) {
    const [lo, hi] = stack.pop()!
    const [ax, ay] = xy[lo]!
    const [bx, by] = xy[hi]!
    const dx = bx - ax
    const dy = by - ay
    const len2 = dx * dx + dy * dy
    let far = -1
    let farD = toleranceM
    for (let i = lo + 1; i < hi; i++) {
      const [px, py] = xy[i]!
      const t = len2 === 0 ? 0 : Math.max(0, Math.min(1, ((px - ax) * dx + (py - ay) * dy) / len2))
      const d = Math.hypot(px - (ax + t * dx), py - (ay + t * dy))
      if (d > farD) {
        farD = d
        far = i
      }
    }
    if (far !== -1) {
      keep[far] = 1
      stack.push([lo, far], [far, hi])
    }
  }
  const out: LngLat[] = []
  points.forEach(([lng, lat], i) => {
    if (keep[i]) out.push([round5(lng), round5(lat)])
  })
  return out
}
