import { describe, expect, it } from 'vitest'
import { ApiError, roadKm, type OptionSet, type Shipment, type Waypoint } from '@loong/domain'
import { PLACES, getPlace } from '@loong/gazetteer'
import { LOCALES, registeredMessages, renderMsg, type Msg } from '@loong/i18n'
import { makeItem } from '../seed/catalog'
import { makeVehicles } from '../seed/fleet'
import { bestSequence, computeOptionSet, objectiveFor, searchOptionSet, type SolveInput } from './solve'

const NOW = Date.parse('2026-10-20T03:00:00Z')

function wp(id: string): Waypoint {
  const p = getPlace(id)!
  return { name: p.name, address: p.address, position: p.position, placeId: p.id, contactName: 'Tes', contactPhone: '081200000000' }
}
const ship = (id: string, from: string, to: string, items = ['paket-kecil']): Shipment => ({ id, pickup: wp(from), dropoff: wp(to), items: items.map((k) => makeItem(k)) })
const input = (shipments: Shipment[]): SolveInput => ({ planId: 'PLAN-TES', shipments, schedule: { mode: 'sekarang' }, priority: { preset: 'seimbang' }, vehicles: makeVehicles(), nowMs: NOW })

const BATCH = [
  ship('a', 'kemang-village', 'plaza-senayan', ['kue']),
  ship('b', 'kemang-raya', 'senayan-city', ['sepatu']),
  ship('c', 'blok-m-square', 'pacific-place', ['dokumen']),
  ship('d', 'kemang-village', 'kota-kasablanka', ['laptop']),
]
const SIX = [...BATCH, ship('e', 'pasar-santa', 'gandaria-city', ['makanan']), ship('f', 'pondok-indah-mall', 'blok-m-square', ['kardus-sedang'])]
const ONE = [ship('a', 'kemang-village', 'plaza-senayan')]

/** Menjalankan pencarian sambil mencatat pesan progres yang dikeluarkannya. */
function search(shipments: Shipment[], effort?: 'fast' | 'thorough' | 'reviewed') {
  const g = searchOptionSet(input(shipments), effort)
  const msgs: Msg[] = []
  for (;;) {
    const r = g.next()
    if (r.done) return { set: r.value, msgs, keys: msgs.map((m) => m.key) }
    msgs.push(r.value)
  }
}

const best = (set: OptionSet) => ({
  cost: Math.min(...set.options.map((o) => o.totals.costRp)),
  km: Math.min(...set.options.map((o) => o.totals.distanceKm)),
  min: Math.min(...set.options.map((o) => o.totals.durationMin)),
})

describe('percobaan ulang urutan stop (inti pemolesan)', () => {
  /** Lima pengiriman dari gazetteer, dipilih tetap (bukan acak) supaya hasilnya stabil. */
  const group = (k: number): Shipment[] =>
    Array.from({ length: 5 }, (_, i) => ({
      id: `s${i}`,
      pickup: wp(PLACES[(k * 3 + i * 7) % PLACES.length]!.id),
      dropoff: wp(PLACES[(k * 5 + i * 11 + 3) % PLACES.length]!.id),
      items: [makeItem('paket-kecil')],
    }))

  it('tidak pernah lebih buruk daripada tanpa percobaan ulang, dan pada satu kelompok nyata memperbaiki sasaran', () => {
    const gains = [10, 34].map((k) => {
      const shipments = group(k)
      const start = shipments[0]!.pickup.position
      const obj = objectiveFor('jarak', shipments, start, roadKm)
      const plain = obj(bestSequence(shipments, start, 'jarak', roadKm))
      const polished = obj(bestSequence(shipments, start, 'jarak', roadKm, undefined, { count: 10, seed: 'polish' }))
      expect(polished).toBeLessThanOrEqual(plain + 1e-9)
      return plain - polished
    })
    expect(Math.max(...gains)).toBeGreaterThan(1e-6)
  })

  it('seed yang sama memberi urutan yang sama (deterministik)', () => {
    const shipments = group(34)
    const start = shipments[0]!.pickup.position
    const run = () => bestSequence(shipments, start, 'jarak', roadKm, undefined, { count: 10, seed: 'polish' })
    expect(run()).toEqual(run())
  })
})

describe('tingkat usaha: fast', () => {
  it('hasilnya sama persis dengan jalur lama; hanya `method` yang bertambah', () => {
    for (const shipments of [ONE, BATCH, SIX]) {
      const legacy = computeOptionSet(input(shipments))
      const fast = computeOptionSet(input(shipments), { effort: 'fast' })
      const { method, ...rest } = fast
      expect(rest).toEqual(legacy)
      expect(legacy.method).toBeUndefined() // seed dan pembuat plan acak tidak mengarang cara pencarian
      expect(method?.effort).toBe('fast')
    }
  })

  it('tanpa catatan progres, tanpa putaran, tanpa pemolesan, dan tidak pernah mengaku terbukti optimal', () => {
    for (const shipments of [ONE, BATCH, SIX]) {
      const { set, msgs } = search(shipments, 'fast')
      expect(msgs).toEqual([])
      expect(set.method).toEqual({ effort: 'fast', trials: set.method!.trials })
      expect(set.method!.trials).toBeGreaterThan(0)
    }
  })
})

describe('tingkat usaha: thorough', () => {
  it('mencoba lebih banyak daripada fast, lalu memoles; angkanya hitungan nyata dan tak ada klaim bukti', () => {
    for (const shipments of [BATCH, SIX]) {
      const fast = search(shipments, 'fast').set
      const { set, keys } = search(shipments, 'thorough')
      expect(set.method!.effort).toBe('thorough')
      expect(set.method!.trials).toBeGreaterThan(fast.method!.trials)
      expect(set.method!.polished).toBe(true)
      expect(set.method!.rounds).toBeUndefined()
      expect(set.method!.reviewer).toBeUndefined()
      expect(set.method!.proven).toBeUndefined()
      expect(keys).toEqual(['srv.engine.search.experiments', 'srv.engine.search.polish'])
    }
  })

  it('tidak pernah lebih buruk daripada fast di biaya, jarak, dan lama selesai terbaik', () => {
    for (const shipments of [BATCH, SIX]) {
      const fast = best(search(shipments, 'fast').set)
      const thorough = best(search(shipments, 'thorough').set)
      expect(thorough.cost).toBeLessThanOrEqual(fast.cost)
      expect(thorough.km).toBeLessThanOrEqual(fast.km + 1e-9)
      expect(thorough.min).toBeLessThanOrEqual(fast.min + 1e-9)
    }
  })

  it('pada plan enam pengiriman benar-benar menemukan sesuatu yang lebih baik daripada fast', () => {
    const fast = best(search(SIX, 'fast').set)
    const thorough = best(search(SIX, 'thorough').set)
    expect(thorough.cost < fast.cost || thorough.min < fast.min - 1e-9 || thorough.km < fast.km - 1e-9).toBe(true)
  })

  it('satu pengiriman: tidak ada yang bisa dicoba, jadi jawabannya sama dan tidak berpura-pura memoles', () => {
    const fast = search(ONE, 'fast')
    const thorough = search(ONE, 'thorough')
    expect(thorough.keys).toEqual([])
    expect(thorough.set.method).toEqual({ effort: 'thorough', trials: fast.set.method!.trials })
    expect(thorough.set.options).toEqual(fast.set.options)
  })

  it('deterministik: dua kali jalan memberi hasil dan catatan yang sama', () => {
    expect(search(SIX, 'thorough')).toEqual(search(SIX, 'thorough'))
  })
})

describe('tingkat usaha: reviewed', () => {
  it('ronde tinjauan oleh aturan bawaan: setiap ronde diawali "round" lalu "reviewRules", diakhiri puas atau batas ronde, lalu memoles', () => {
    const { set, keys } = search(BATCH, 'reviewed')
    const m = set.method!
    expect(m.effort).toBe('reviewed')
    expect(m.reviewer).toBe('rules') // tiruan tidak punya peninjau AI
    expect(m.polished).toBe(true)
    expect(m.proven).toBeUndefined()
    expect(m.rounds).toBeGreaterThanOrEqual(1)
    expect(m.rounds).toBeLessThanOrEqual(3)

    expect(keys.filter((k) => k === 'srv.engine.search.round')).toHaveLength(m.rounds!)
    keys.forEach((k, i) => {
      if (k === 'srv.engine.search.round') expect(keys[i + 1]).toBe('srv.engine.search.reviewRules')
    })
    expect(keys.at(-1)).toBe('srv.engine.search.polish')
    // pemolesan di akhir, bukan di ronde pertama
    expect(keys.indexOf('srv.engine.search.polish')).toBe(keys.length - 1)
  })

  it('peninjau puas setelah dua ronde berturut-turut tanpa perbaikan: pada BATCH dua kali meminta, lalu puas di ronde ketiga', () => {
    const { set, keys } = search(BATCH, 'reviewed')
    expect(set.method!.rounds).toBe(3)
    const rounds = keys.slice(0, keys.indexOf('srv.engine.search.polish'))
    expect(rounds.filter((k) => k === 'srv.engine.search.more')).toHaveLength(2)
    expect(rounds.at(-1)).toBe('srv.engine.search.satisfied')
  })

  it('lebih banyak percobaan daripada thorough, dan tidak lebih buruk', () => {
    const thorough = search(SIX, 'thorough').set
    const reviewed = search(SIX, 'reviewed').set
    expect(reviewed.method!.trials).toBeGreaterThan(thorough.method!.trials)
    const a = best(thorough)
    const b = best(reviewed)
    expect(b.cost).toBeLessThanOrEqual(a.cost)
    expect(b.km).toBeLessThanOrEqual(a.km + 1e-9)
    expect(b.min).toBeLessThanOrEqual(a.min + 1e-9)
  })

  it('satu pengiriman: satu ronde yang langsung puas dan tanpa pemolesan', () => {
    const { set, keys } = search(ONE, 'reviewed')
    expect(keys).toEqual(['srv.engine.search.round', 'srv.engine.search.reviewRules', 'srv.engine.search.satisfied'])
    expect(set.method).toEqual({ effort: 'reviewed', trials: set.method!.trials, rounds: 1, reviewer: 'rules' })
  })

  it('percobaan lanjutan menyebut jumlahnya (n dan count sama) dan tidak lebih dari tiga', () => {
    const { msgs } = search(SIX, 'reviewed')
    const more = msgs.filter((m) => m.key === 'srv.engine.search.more')
    expect(more.length).toBeGreaterThan(0)
    for (const m of more) {
      expect(m.params!.n).toBe(m.params!.count)
      expect(Number(m.params!.n)).toBeGreaterThanOrEqual(1)
      expect(Number(m.params!.n)).toBeLessThanOrEqual(3)
    }
  })
})

describe('catatan pencarian dan masukan', () => {
  it('semua pesan progres punya teks di kedua bahasa (tanpa kunci mentah)', () => {
    const keys = new Set<string>()
    for (const effort of ['thorough', 'reviewed'] as const) {
      for (const shipments of [ONE, BATCH, SIX]) for (const m of search(shipments, effort).msgs) {
        keys.add(m.key)
        for (const locale of LOCALES) {
          const text = renderMsg(m, locale)
          expect(text).not.toContain('srv.engine.search')
          expect(text).not.toMatch(/\{\w+\}/)
        }
      }
    }
    for (const key of ['experiments', 'polish', 'round', 'reviewRules', 'satisfied', 'more']) expect(keys.has(`srv.engine.search.${key}`), key).toBe(true)
    // reviewAI hanya dikeluarkan backend asli, tetapi tetap harus ada di katalog
    for (const locale of LOCALES) {
      const catalog = registeredMessages(locale)
      expect(catalog['srv.engine.search.reviewAI']).toBeTruthy()
    }
  })

  it('tingkat yang tidak dikenal ditolak dengan pesan srv.api.badEffort', () => {
    try {
      computeOptionSet(input(BATCH), { effort: 'turbo' as never })
      expect.unreachable()
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError)
      expect((err as ApiError).code).toBe('tidak_valid')
      expect((err as ApiError).msg?.key).toBe('srv.api.badEffort')
    }
  })
})
