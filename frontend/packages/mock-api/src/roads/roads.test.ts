import { describe, expect, it } from 'vitest'
import { roadKm, type LatLng, type LngLat } from '@loong/domain'
import { createMapboxRoads } from './mapbox'
import { decodePolyline, encodePolyline } from './polyline'
import { matrixKm, memoryRoadCache, uniquePoints } from './provider'
import { simplifyPath } from './simplify'

const TOKEN = 'pk.token-rahasia-untuk-tes'
const A: LatLng = { lat: -6.2, lng: 106.8 }
const B: LatLng = { lat: -6.21, lng: 106.82 }
const C: LatLng = { lat: -6.25, lng: 106.83 }

const json = (body: unknown, init?: ResponseInit) => new Response(JSON.stringify(body), { status: 200, ...init })

describe('polyline', () => {
  it('encode lalu decode mengembalikan titik yang sama (presisi 5)', () => {
    const line: LngLat[] = [
      [106.8, -6.2],
      [106.80123, -6.20456],
      [106.83, -6.25],
    ]
    expect(decodePolyline(encodePolyline(line))).toEqual(line)
  })

  it('contoh baku dari spesifikasi', () => {
    // dari dokumentasi Google/Mapbox: lat,lng (38.5,-120.2) (40.7,-120.95) (43.252,-126.453)
    const line = decodePolyline('_p~iF~ps|U_ulLnnqC_mqNvxq`@')
    expect(line).toEqual([
      [-120.2, 38.5],
      [-120.95, 40.7],
      [-126.453, 43.252],
    ])
  })

  it('polyline terpotong ditolak', () => {
    expect(() => decodePolyline('_p~iF~ps|U_')).toThrow()
  })
})

describe('simplifyPath', () => {
  it('membuang titik segaris, mempertahankan ujung dan tikungan', () => {
    const line: LngLat[] = []
    for (let i = 0; i <= 50; i++) line.push([106.8 + i * 0.0001, -6.2]) // lurus ke timur ±550 m
    for (let i = 1; i <= 50; i++) line.push([106.805, -6.2 - i * 0.0001]) // lalu lurus ke selatan
    const out = simplifyPath(line)
    expect(out).toEqual([
      [106.8, -6.2],
      [106.805, -6.2],
      [106.805, -6.205],
    ])
  })

  it('menjaga simpangan dalam toleransi dan membulatkan 5 desimal', () => {
    const wiggly: LngLat[] = Array.from({ length: 200 }, (_, i) => [106.8 + i * 0.00005, -6.2 + Math.sin(i / 3) * 0.00002])
    const out = simplifyPath(wiggly, 4)
    expect(out.length).toBeLessThan(wiggly.length)
    expect(out[0]).toEqual([106.8, -6.2])
    for (const [lng, lat] of out) {
      expect(Math.round(lng * 1e5) / 1e5).toBe(lng)
      expect(Math.round(lat * 1e5) / 1e5).toBe(lat)
    }
  })
})

describe('matrixKm', () => {
  it('memakai matriks untuk titik yang dikenal dan perkiraan untuk sisanya', () => {
    const pts = uniquePoints([A, B, A])
    expect(pts).toHaveLength(2)
    const km = matrixKm(pts, [
      [0, 7.5],
      [null, 0],
    ])
    expect(km(A, B)).toBe(7.5)
    expect(km(B, A)).toBe(roadKm(B, A)) // null -> perkiraan
    expect(km(A, C)).toBe(roadKm(A, C)) // titik di luar matriks
  })
})

describe('Mapbox', () => {
  const polyline = encodePolyline([
    [106.8, -6.2],
    [106.81, -6.205],
    [106.82, -6.21],
  ])

  it('matriks: meminta jarak, mengubah meter ke km, mempertahankan null, dan memakai cache', async () => {
    const urls: string[] = []
    const fetch = async (url: string | URL | Request) => {
      urls.push(String(url))
      return json({ code: 'Ok', distances: [[0, 8123], [null, 0]] })
    }
    const roads = createMapboxRoads({ token: TOKEN, fetch: fetch as typeof globalThis.fetch, cache: memoryRoadCache() })
    expect(await roads.matrix([A, B])).toEqual([[0, 8.123], [null, 0]])
    expect(urls).toHaveLength(1)
    expect(urls[0]).toContain('/directions-matrix/v1/mapbox/driving/106.800000,-6.200000;106.820000,-6.210000')
    expect(urls[0]).toContain('annotations=distance')
    expect(await roads.matrix([A, B])).toEqual([[0, 8.123], [null, 0]])
    expect(urls).toHaveLength(1)
    expect(roads.status()).toMatchObject({ requests: 1, cacheHits: 1, failures: 0 })
  })

  it('jalur: meminta geometri penuh, mendekode, dan menyimpan di cache', async () => {
    const urls: string[] = []
    const fetch = async (url: string | URL | Request) => {
      urls.push(String(url))
      return json({ code: 'Ok', routes: [{ geometry: polyline }] })
    }
    const roads = createMapboxRoads({ token: TOKEN, fetch: fetch as typeof globalThis.fetch, cache: memoryRoadCache() })
    const line = await roads.path([A, B])
    expect(line?.[0]).toEqual([106.8, -6.2])
    expect(line?.at(-1)).toEqual([106.82, -6.21])
    expect(urls[0]).toContain('/directions/v5/mapbox/driving/')
    expect(urls[0]).toContain('overview=full')
    expect(urls[0]).toContain('continue_straight=false')
    await roads.path([A, B])
    expect(urls).toHaveLength(1)
  })

  it('titik berurutan yang sama digabung dan rute satu titik tidak diminta', async () => {
    let calls = 0
    const fetch = async () => (calls++, json({ code: 'Ok', routes: [{ geometry: polyline }] }))
    const roads = createMapboxRoads({ token: TOKEN, fetch: fetch as typeof globalThis.fetch })
    expect(await roads.path([A, A])).toBeNull()
    expect(calls).toBe(0)
  })

  it('lebih dari 25 titik: tidak diminta, null', async () => {
    let calls = 0
    const fetch = async () => (calls++, json({}))
    const roads = createMapboxRoads({ token: TOKEN, fetch: fetch as typeof globalThis.fetch })
    const many = Array.from({ length: 26 }, (_, i) => ({ lat: -6.2 - i * 0.001, lng: 106.8 }))
    expect(await roads.matrix(many)).toBeNull()
    expect(await roads.path(many)).toBeNull()
    expect(calls).toBe(0)
  })

  it('429: null, lalu ditahan sampai jeda lewat tanpa memukul server lagi', async () => {
    let now = 1_000_000
    let calls = 0
    const fetch = async () => (calls++, new Response('{}', { status: 429, headers: { 'Retry-After': '10' } }))
    const roads = createMapboxRoads({ token: TOKEN, fetch: fetch as typeof globalThis.fetch, now: () => now })
    expect(await roads.path([A, B])).toBeNull()
    expect(await roads.path([A, C])).toBeNull()
    expect(calls).toBe(1)
    now += 11_000
    await roads.path([A, C])
    expect(calls).toBe(2)
  })

  it('token ditolak (403) dan galat jaringan: null, pesan galat tidak memuat token', async () => {
    const denied = createMapboxRoads({ token: TOKEN, fetch: (async () => new Response('{}', { status: 403 })) as typeof globalThis.fetch })
    expect(await denied.matrix([A, B])).toBeNull()
    expect(denied.status().lastError).toMatch(/403/)
    expect(JSON.stringify(denied.status())).not.toContain(TOKEN)

    const offline = createMapboxRoads({
      token: TOKEN,
      fetch: (async () => {
        throw new TypeError(`Failed to fetch https://api.mapbox.com/?access_token=${TOKEN}`)
      }) as typeof globalThis.fetch,
    })
    expect(await offline.path([A, B])).toBeNull()
    expect(JSON.stringify(offline.status())).not.toContain(TOKEN)
    expect(offline.status().failures).toBe(1)
  })

  it('jawaban tak sesuai (code bukan Ok / bentuk salah): null', async () => {
    const roads = createMapboxRoads({ token: TOKEN, fetch: (async () => json({ code: 'NoRoute' })) as typeof globalThis.fetch })
    expect(await roads.path([A, B])).toBeNull()
    expect(await roads.matrix([A, B])).toBeNull()
  })

  it('membatasi permintaan serentak', async () => {
    let active = 0
    let peak = 0
    const fetch = async () => {
      active += 1
      peak = Math.max(peak, active)
      await new Promise((r) => setTimeout(r, 5))
      active -= 1
      return json({ code: 'Ok', routes: [{ geometry: polyline }] })
    }
    const roads = createMapboxRoads({ token: TOKEN, fetch: fetch as typeof globalThis.fetch, maxConcurrent: 2 })
    const pts = (i: number): LatLng[] => [A, { lat: -6.3 - i * 0.01, lng: 106.9 }]
    await Promise.all([0, 1, 2, 3, 4, 5].map((i) => roads.path(pts(i))))
    expect(peak).toBeLessThanOrEqual(2)
  })
})
