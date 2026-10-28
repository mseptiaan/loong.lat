import { describe, expect, it } from 'vitest'
import { ApiError, type PlanOption, type Preset, type Shipment, type Waypoint } from '@loong/domain'
import { getPlace } from '@loong/gazetteer'
import { renderMsg, tFor } from '@loong/i18n'
import { collectMsgs, msgProblems } from '../test-msgs'
import { catalogItem, makeItem } from '../seed/catalog'
import { makeVehicles } from '../seed/fleet'
import { computeOptionSet, inputHash, type SolveInput } from './solve'

const NOW = Date.parse('2026-10-20T03:00:00Z') // 10.00 WIB, bukan jam sibuk

function wp(id: string): Waypoint {
  const p = getPlace(id)
  if (!p) throw new Error(`lokasi ${id} tidak ada`)
  return { name: p.name, address: p.address, position: p.position, placeId: p.id, contactName: 'Tes', contactPhone: '081200000000' }
}

function ship(id: string, from: string, to: string, items: string[] = ['paket-kecil']): Shipment {
  return { id, pickup: wp(from), dropoff: wp(to), items: items.map((k) => makeItem(k)) }
}

function input(shipments: Shipment[], over: Partial<SolveInput> = {}): SolveInput {
  return {
    planId: 'PLAN-TES',
    shipments,
    schedule: { mode: 'sekarang' },
    priority: { preset: 'seimbang' },
    vehicles: makeVehicles(),
    nowMs: NOW,
    ...over,
  }
}

const en = tFor('en')
const label = (o: PlanOption) => renderMsg(o.label, 'en').toLowerCase()
const presetName = (p: Preset) => en(`dom.preset.${p}`).toLowerCase()
/** Alasan opsi sebagai teks Inggris, satu per baris. */
const reasonText = (o: PlanOption) => o.reasons.map((r) => renderMsg(r, 'en')).join(' ')
const reasonKeys = (o: PlanOption) => o.reasons.flatMap((r) => collectMsgs(r)).map((m) => m.key)

// beberapa pengiriman di kawasan Jakarta Selatan yang berdekatan, sehingga penggabungan memang menghemat
const BATCH: Shipment[] = [
  ship('a', 'kemang-village', 'plaza-senayan', ['kue']),
  ship('b', 'kemang-raya', 'senayan-city', ['sepatu']),
  ship('c', 'blok-m-square', 'pacific-place', ['dokumen']),
  ship('d', 'kemang-village', 'kota-kasablanka', ['laptop']),
]

describe('computeOptionSet', () => {
  it('satu pengiriman kecil: satu opsi, memakai motor', () => {
    const set = computeOptionSet(input([ship('a', 'kemang-village', 'plaza-senayan')]))
    expect(set.options).toHaveLength(1)
    const [opt] = set.options
    expect(opt!.routes).toHaveLength(1)
    expect(opt!.routes[0]!.vehicleType).toBe('motor')
    expect(opt!.recommended).toBe(true)
    // semua preset jatuh ke rute yang sama, jadi labelnya bukan satu preset saja
    expect(opt!.label).toEqual({ key: 'srv.engine.label.best' })
    // satu pengiriman = baseline itu sendiri
    expect(opt!.savings.costRp).toBe(0)
  })

  it('beberapa pengiriman: opsi berbeda dan tiap preset menang di metriknya', () => {
    const set = computeOptionSet(input(BATCH))
    expect(set.options.length).toBeGreaterThanOrEqual(2)
    const cost = (o: (typeof set.options)[number]) => o.totals.costRp
    const km = (o: (typeof set.options)[number]) => o.totals.distanceKm
    const min = (o: (typeof set.options)[number]) => o.totals.durationMin

    const preset = (p: Preset) => set.options.find((o) => label(o).includes(presetName(p)))
    const termurah = preset('termurah')!
    const terpendek = preset('terpendek')!
    const tercepat = preset('tercepat')!
    expect(termurah).toBeDefined()
    for (const o of set.options) {
      expect(cost(termurah)).toBeLessThanOrEqual(cost(o))
      expect(km(terpendek)).toBeLessThanOrEqual(km(o) + 1e-9)
      expect(min(tercepat)).toBeLessThanOrEqual(min(o) + 1e-9)
    }
  })

  it('tidak ada opsi yang kalah di semua metrik dari opsi lain (tidak ada opsi yang tak berguna)', () => {
    const SIX: Shipment[] = [
      ...BATCH,
      ship('e', 'pasar-santa', 'gandaria-city', ['makanan']),
      ship('f', 'pondok-indah-mall', 'blok-m-square', ['kardus-sedang']),
    ]
    const scenarios: Shipment[][] = [
      BATCH,
      SIX,
      BATCH.slice(0, 2),
      [ship('x', 'stasiun-depok', 'margo-city', ['dokumen']), ship('y', 'bintaro-sektor-9', 'margo-city', ['laptop']), ship('z', 'stasiun-depok', 'kemang-village', ['kue'])],
      [ship('a', 'kemang-village', 'plaza-senayan', ['rak']), ship('b', 'kemang-raya', 'senayan-city', ['sepatu'])],
    ]
    for (const shipments of scenarios) {
      const { options } = computeOptionSet(input(shipments))
      const m = (o: (typeof options)[number]) => [o.totals.costRp, o.totals.distanceKm, o.totals.durationMin, o.totals.avgDeliveryMin]
      for (const a of options) {
        for (const b of options) {
          if (a === b) continue
          const dominated = m(a).every((v, i) => m(b)[i]! <= v + 1e-9) && m(a).some((v, i) => m(b)[i]! < v - 1e-9)
          expect(dominated, `${label(a)} dikalahkan ${label(b)}`).toBe(false)
        }
      }
    }
  })

  it('menggabungkan pengiriman berdekatan lebih murah daripada kirim terpisah', () => {
    const set = computeOptionSet(input(BATCH))
    const cheapest = set.options.reduce((a, b) => (a.totals.costRp <= b.totals.costRp ? a : b))
    expect(cheapest.totals.costRp).toBeLessThan(set.baseline.costRp)
    expect(cheapest.savings.pct).toBeGreaterThan(0)
    expect(cheapest.savings.costRp).toBe(set.baseline.costRp - cheapest.totals.costRp)
  })

  it('tepat satu opsi direkomendasikan dan cocok dengan preset pilihan user', () => {
    for (const preset of ['termurah', 'terpendek', 'tercepat', 'seimbang'] as const) {
      const set = computeOptionSet(input(BATCH, { priority: { preset } }))
      const rec = set.options.filter((o) => o.recommended)
      expect(rec).toHaveLength(1)
      expect(label(rec[0]!)).toContain(presetName(preset))
      expect(set.options[0]!.recommended).toBe(true)
    }
  })

  it('barang besar memaksa blindvan dan alasannya tercatat', () => {
    const set = computeOptionSet(input([ship('a', 'kemang-village', 'plaza-senayan', ['rak'])]))
    const route = set.options[0]!.routes[0]!
    expect(route.vehicleType).toBe('blindvan')
    expect(reasonKeys(set.options[0]!)).toContain('srv.engine.reason.vanDim')
    expect(reasonText(set.options[0]!)).toMatch(/Using a blind van because the largest item \(120 cm\)/)
  })

  it('barang melebihi kapasitas semua kendaraan ditolak dengan galat tidak_valid', () => {
    const huge = makeItem('kulkas-mini', { qty: 40 }) // 880 kg
    const s: Shipment = { ...ship('a', 'kemang-village', 'plaza-senayan'), items: [huge] }
    expect(() => computeOptionSet(input([s]))).toThrow(ApiError)
    try {
      computeOptionSet(input([s]))
    } catch (e) {
      expect((e as ApiError).code).toBe('tidak_valid')
      expect((e as ApiError).msg).toEqual({ key: 'srv.engine.tooBig' })
      expect((e as ApiError).message).toMatch(/exceeds the capacity of every vehicle/)
    }
  })

  it('masukan tidak lengkap ditolak dengan pesan yang menunjuk kolomnya', () => {
    const s = ship('a', 'kemang-village', 'plaza-senayan')
    s.items = []
    try {
      computeOptionSet(input([s]))
      expect.unreachable()
    } catch (e) {
      expect((e as ApiError).code).toBe('tidak_valid')
      expect((e as ApiError).field).toBe('shipments.0.items')
      expect((e as ApiError).msg).toEqual({ key: 'srv.engine.itemsMissing', params: { n: 1 } })
      expect((e as ApiError).message).toBe('Shipment 1: add at least one item.')
    }
  })

  it('rincian tarif berjumlah sama dengan biaya rute dan biaya dibulatkan Rp 500', () => {
    const set = computeOptionSet(input(BATCH))
    for (const o of set.options) {
      for (const r of o.routes) {
        expect(r.fare.reduce((s, l) => s + l.amountRp, 0)).toBe(r.costRp)
        expect(r.costRp % 500).toBe(0)
      }
      expect(o.totals.costRp).toBe(o.routes.reduce((s, r) => s + r.costRp, 0))
    }
  })

  it('urutan stop selalu ambil sebelum antar untuk tiap pengiriman, dan ETA menaik', () => {
    const set = computeOptionSet(input(BATCH))
    for (const o of set.options) {
      for (const r of o.routes) {
        const seen = new Set<string>()
        let last = -1
        for (const s of r.stops) {
          if (s.kind === 'antar') expect(seen.has(s.shipmentId)).toBe(true)
          else seen.add(s.shipmentId)
          expect(s.etaMin).toBeGreaterThan(last)
          last = s.etaMin
        }
        expect(r.stops).toHaveLength(r.stops.filter((s) => s.kind === 'ambil').length * 2)
      }
      // setiap pengiriman muncul tepat sekali sebagai ambil dan sekali sebagai antar
      const all = o.routes.flatMap((r) => r.stops)
      for (const s of BATCH) {
        expect(all.filter((x) => x.shipmentId === s.id && x.kind === 'ambil')).toHaveLength(1)
        expect(all.filter((x) => x.shipmentId === s.id && x.kind === 'antar')).toHaveLength(1)
      }
    }
  })

  it('deterministik: masukan sama menghasilkan keluaran sama', () => {
    const a = computeOptionSet(input(BATCH))
    const b = computeOptionSet(input(BATCH))
    expect(JSON.stringify(a)).toBe(JSON.stringify(b))
  })

  it('jam sibuk membuat perjalanan lebih lama dan ditandai di alasan', () => {
    const rush = Date.parse('2026-10-20T01:00:00Z') // 08.00 WIB
    const calm = computeOptionSet(input(BATCH))
    const busy = computeOptionSet(input(BATCH, { nowMs: rush }))
    const pick = (s: typeof calm) => s.options.find((o) => o.preset === 'seimbang') ?? s.options[0]!
    expect(pick(busy).totals.durationMin).toBeGreaterThan(pick(calm).totals.durationMin)
    expect(reasonKeys(pick(busy))).toContain('srv.engine.reason.rush')
    expect(reasonText(pick(busy))).toMatch(/rush hour/)
  })

  describe('batas waktu per pengiriman', () => {
    const arrivalOf = (set: ReturnType<typeof computeOptionSet>['options'][number], shipmentId: string) =>
      Date.parse(set.routes.flatMap((r) => r.stops).find((st) => st.shipmentId === shipmentId && st.kind === 'antar')!.arriveAt)
    const withDeadline = (id: string, ms: number) => BATCH.map((s) => (s.id === id ? { ...s, deadline: new Date(ms).toISOString() } : s))

    it('batas waktu yang mustahil dicatat sebagai alasan, bukan galat', () => {
      const set = computeOptionSet(input(withDeadline('a', NOW + 5 * 60_000)))
      expect(set.options.length).toBeGreaterThan(0)
      for (const o of set.options) expect(reasonText(o)).toMatch(/Deadline missed: Plaza Senayan arrives \d\d:\d\d \(deadline \d\d:\d\d\)/)
    })

    it('hanya opsi yang memenuhi semua batas waktu yang dipertimbangkan, dan alasannya menyebut waktunya', () => {
      const free = computeOptionSet(input(BATCH))
      // pengiriman yang paling bervariasi waktu tibanya antar opsi
      const spread = BATCH.map((s) => {
        const times = free.options.map((o) => arrivalOf(o, s.id))
        return { id: s.id, min: Math.min(...times), max: Math.max(...times) }
      }).sort((x, y) => y.max - y.min - (x.max - x.min))[0]!
      expect(spread.max - spread.min).toBeGreaterThan(60_000) // ada opsi yang jelas lebih cepat untuk pengiriman ini

      const due = spread.min + 60_000
      const set = computeOptionSet(input(withDeadline(spread.id, due)))
      for (const o of set.options) {
        expect(arrivalOf(o, spread.id)).toBeLessThanOrEqual(due)
        expect(reasonKeys(o)).toContain('srv.engine.reason.deadlineMet')
      }
    })

    it('urutan stop mengejar batas waktu yang lebih ketat daripada yang dicapai urutan terpendek atau antar-cepat', () => {
      const three = [
        ship('s0', 'monas', 'pondok-indah-mall'),
        ship('s1', 'senayan-city', 'lippo-mall-puri'),
        ship('s2', 'pasar-senen', 'kebon-jeruk'),
      ]
      // planId ikut menentukan titik awal sementara, jadi skenario ini terikat pada id 'P'
      const opts = { planId: 'P', priority: { preset: 'termurah' as const } }
      const free = computeOptionSet(input(three, opts))
      const best = Math.min(...free.options.map((o) => arrivalOf(o, 's0')))
      // 3 menit lebih awal dari opsi mana pun tanpa batas waktu: hanya urutan yang mengutamakan s0 yang bisa memenuhi
      const due = best - 3 * 60_000
      const set = computeOptionSet(input(three.map((s) => (s.id === 's0' ? { ...s, deadline: new Date(due).toISOString() } : s)), opts))
      for (const o of set.options) {
        expect(arrivalOf(o, 's0')).toBeLessThanOrEqual(due)
        expect(reasonText(o)).toMatch(/Deadline met: Pondok Indah Mall arrives/)
      }
    })

    it('inputHash ikut berubah saat batas waktu berubah', () => {
      const base = inputHash(input(BATCH))
      expect(inputHash(input(withDeadline('a', NOW + 3_600_000)))).not.toBe(base)
      expect(inputHash(input(withDeadline('a', NOW + 3_600_000)))).not.toBe(inputHash(input(withDeadline('a', NOW + 7_200_000))))
    })
  })

  it('inputHash berubah bila barang berubah dan stabil bila tidak', () => {
    const base = inputHash(input(BATCH))
    expect(inputHash(input(BATCH))).toBe(base)
    const changed = BATCH.map((s, i) => (i === 0 ? { ...s, items: [makeItem('galon')] } : s))
    expect(inputHash(input(changed))).not.toBe(base)
  })

  it('katalog: dimensi barang besar benar-benar melebihi batas motor', () => {
    const motor = makeVehicles()[0]!
    expect(catalogItem('rak').lengthCm).toBeGreaterThan(motor.maxDimensionCm)
  })
  describe('teks netral-bahasa', () => {
    const rich = [
      ship('a', 'kemang-village', 'plaza-senayan', ['rak']),
      ship('b', 'kemang-raya', 'senayan-city', ['sepatu']),
      ship('c', 'blok-m-square', 'pacific-place', ['dokumen']),
    ]
    const withDeadline = rich.map((s, i) => (i === 0 ? { ...s, deadline: new Date(NOW + 5 * 60_000).toISOString() } : s))
    const scenarios = [
      [ship('a', 'kemang-village', 'plaza-senayan')],
      BATCH,
      rich,
      withDeadline,
    ]

    it('semua kunci srv.engine.* yang dihasilkan ada di katalog Inggris dan Indonesia dengan parameter lengkap', () => {
      for (const shipments of scenarios) {
        for (const nowMs of [NOW, Date.parse('2026-10-20T01:00:00Z')]) {
          const set = computeOptionSet(input(shipments, { nowMs }))
          expect(msgProblems(set)).toEqual([])
          expect(collectMsgs(set).length).toBeGreaterThan(0)
        }
      }
    })

    it('rincian tarif memakai kunci tarif dan parameter mentah, bukan teks berformat', () => {
      const set = computeOptionSet(input(BATCH))
      const lines = set.options.flatMap((o) => o.routes).flatMap((r) => r.fare)
      expect(lines[0]!.label).toEqual({ key: 'srv.engine.fare.base' })
      for (const l of lines) expect(l.label.key).toMatch(/^srv\.engine\.fare\./)
      const dist = lines.find((l) => l.label.key === 'srv.engine.fare.distance')!
      expect(typeof dist.label.params!.km).toBe('number')
      expect(renderMsg(dist.label, 'en')).toMatch(/^Distance \d+\.\d km$/)
      expect(renderMsg(dist.label, 'id')).toMatch(/^Jarak \d+,\d km$/)
    })

    it('headline dan label mengikuti bahasa layar saat dirender', () => {
      const set = computeOptionSet(input(BATCH))
      for (const o of set.options) {
        expect(renderMsg(o.headline, 'en')).toMatch(/^\d+ (motorbikes?|blind vans?)( \+ \d+ (motorbikes?|blind vans?))? · \d+ stops · arrives \d\d:\d\d$/)
        expect(renderMsg(o.headline, 'id')).toMatch(/^\d+ (motor|blindvan)( \+ \d+ (motor|blindvan))? · \d+ stop · tiba \d\d\.\d\d$/)
      }
      const single = computeOptionSet(input([ship('a', 'kemang-village', 'plaza-senayan')]))
      expect(renderMsg(single.options[0]!.label, 'id')).toBe('Rute terbaik')
      expect(renderMsg(single.options[0]!.label, 'en')).toBe('Best route')
    })

    it('stop pada rute membawa catalogKey barang dan rincian sebagai Msg dom.stopItem.detail', () => {
      const set = computeOptionSet(input([ship('a', 'kemang-village', 'plaza-senayan', ['kue'])]))
      const stop = set.options[0]!.routes[0]!.stops[0]!
      expect(stop.items![0]).toMatchObject({ catalogKey: 'kue', detail: { key: 'dom.stopItem.detail' } })
      expect(msgProblems(stop)).toEqual([])
      expect(stop.itemsSummary.key).toBe('srv.engine.itemsSummary')
    })
  })
})
