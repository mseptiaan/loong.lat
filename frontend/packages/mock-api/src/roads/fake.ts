import { haversineKm, type LngLat } from '@loong/domain'
import type { RoadProvider } from './provider'

/**
 * Penyedia jalan palsu untuk tes (tanpa jaringan). Jarak = haversine x `factor` (sengaja beda dari faktor
 * perkiraan bawaan agar terlihat di angka); jalur = siku-siku (lurus bujur lalu lintang) sehingga jelas bukan garis halus.
 */
export function createFakeRoads(opts: { factor?: number; fail?: boolean } = {}) {
  const factor = opts.factor ?? 2
  const calls = { matrix: 0, path: 0 }
  const provider: RoadProvider = {
    async matrix(points) {
      calls.matrix += 1
      if (opts.fail) return null
      return points.map((a) => points.map((b) => haversineKm(a, b) * factor))
    },
    async path(points) {
      calls.path += 1
      if (opts.fail) return null
      const out: LngLat[] = [[points[0]!.lng, points[0]!.lat]]
      for (let i = 1; i < points.length; i++) {
        const a = points[i - 1]!
        const b = points[i]!
        out.push([b.lng, a.lat], [b.lng, b.lat])
      }
      return out
    },
    status: () => ({ provider: 'Palsu', requests: calls.matrix + calls.path, cacheHits: 0, failures: 0 }),
  }
  return { provider, calls }
}
