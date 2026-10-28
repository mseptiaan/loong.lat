import { describe, expect, it } from 'vitest'
import type { OptionSet, PlanOption, Preset, Schedule, Shipment, Vehicle, Priority } from '@loong/domain'
import { renderMsg, tFor } from '@loong/i18n'
import { msgProblems } from '../test-msgs'
import { computeOptionSet, type SolveInput } from './solve'

/*
 * Paritas dengan engine produksi (Go, backend/). Backend menulis pasangan berkas per skenario di
 * backend/testdata/parity: permintaan kuotasi dan OptionSet hasil hitungannya. Tes ini memeriksa bahwa hasil
 * itu memenuhi aturan yang diandalkan layar wizard (tipe, urutan, tarif, pesan terdaftar di katalog) dan bahwa
 * engine produksi tidak pernah kalah dari mock yang digantikannya. Berkas dibuat ulang dengan
 * `UPDATE_PARITY=1 go test ./services/optimizer/internal/quote -run Parity` di backend/.
 */

interface QuoteRequest {
  planId: string
  shipments: Shipment[]
  schedule: Schedule
  priority: Priority
  vehicles: Vehicle[]
  now: string
}

const files = import.meta.glob<string>('../../../../../backend/testdata/parity/*.json', { eager: true, query: '?raw', import: 'default' })
const parse = <T>(name: string): T => {
  const key = Object.keys(files).find((f) => f.endsWith(`/${name}`))
  if (!key) throw new Error(`berkas paritas tidak ada: ${name}`)
  return JSON.parse(files[key]!) as T
}
const scenarios = [...new Set(Object.keys(files).map((f) => f.split('/').pop()!.replace(/\.(request|optionset)\.json$/, '')))].sort()

const en = tFor('en')
const label = (o: PlanOption) => renderMsg(o.label, 'en').toLowerCase()
const presetName = (p: Preset) => en(`dom.preset.${p}`).toLowerCase()

/** Aturan yang diandalkan wizard, apa pun mesin yang menghitungnya. */
function invariants(set: OptionSet, shipments: Shipment[]): string[] {
  const bad: string[] = []
  if (set.options.length === 0) return ['tidak ada opsi']
  if (set.options.filter((o) => o.recommended).length !== 1) bad.push('harus tepat satu opsi yang direkomendasikan')
  if (!set.options[0]!.recommended) bad.push('opsi yang direkomendasikan harus pertama')
  for (const o of set.options) {
    const served = new Set<string>()
    let cost = 0
    for (const r of o.routes) {
      const fare = r.fare.reduce((s, l) => s + l.amountRp, 0)
      if (fare !== r.costRp) bad.push(`${o.id}: baris tarif ${fare} != biaya ${r.costRp}`)
      cost += r.costRp
      const picked = new Set<string>()
      let prev = -Infinity
      r.stops.forEach((s, i) => {
        if (s.seq !== i + 1) bad.push(`${r.id}: seq ${s.seq} di posisi ${i + 1}`)
        const at = Date.parse(s.arriveAt)
        if (at < prev) bad.push(`${r.id}: ETA mundur di stop ${s.seq}`)
        prev = at
        if (s.kind === 'ambil') picked.add(s.shipmentId)
        else if (!picked.has(s.shipmentId)) bad.push(`${r.id}: ${s.shipmentId} diantar sebelum diambil`)
        else served.add(s.shipmentId)
      })
    }
    if (served.size !== shipments.length) bad.push(`${o.id}: hanya ${served.size} dari ${shipments.length} pengiriman terlayani`)
    if (o.totals.costRp !== cost) bad.push(`${o.id}: total ${o.totals.costRp} != jumlah rute ${cost}`)
    if (o.savings.costRp !== set.baseline.costRp - o.totals.costRp) bad.push(`${o.id}: hemat tidak cocok dengan baseline`)
    if (o.reasons.length === 0) bad.push(`${o.id}: tanpa alasan`)
  }
  return bad
}

describe('paritas dengan engine produksi (backend Go)', () => {
  it('ada skenario untuk diperiksa', () => {
    expect(scenarios.length).toBeGreaterThanOrEqual(5)
  })

  for (const name of scenarios) {
    describe(name, () => {
      const req = parse<QuoteRequest>(`${name}.request.json`)
      const go = parse<OptionSet>(`${name}.optionset.json`)
      const mock = computeOptionSet({
        planId: req.planId,
        shipments: req.shipments,
        schedule: req.schedule,
        priority: req.priority,
        vehicles: req.vehicles,
        nowMs: Date.parse(req.now),
      } satisfies SolveInput)

      it('memenuhi aturan wizard', () => {
        expect(invariants(go, req.shipments)).toEqual([])
      })

      it('semua pesan terdaftar di katalog EN dan ID dengan parameter lengkap', () => {
        expect(msgProblems(go)).toEqual([])
      })

      it('tiap preset menang di metriknya dan tidak ada opsi yang dikalahkan opsi lain', () => {
        const winner = (p: Preset) => go.options.find((o) => label(o).includes(presetName(p)) || o.label.key === 'srv.engine.label.best')
        const cheapest = winner('termurah')!
        const shortest = winner('terpendek')!
        for (const o of go.options) {
          expect(cheapest.totals.costRp).toBeLessThanOrEqual(o.totals.costRp)
          expect(shortest.totals.distanceKm).toBeLessThanOrEqual(o.totals.distanceKm + 1e-9)
          for (const other of go.options) {
            if (other === o) continue
            const m = (x: PlanOption) => [x.totals.costRp, x.totals.distanceKm, x.totals.durationMin, x.totals.avgDeliveryMin]
            const dominated = m(o).every((v, i) => m(other)[i]! <= v + 1e-9) && m(o).some((v, i) => m(other)[i]! < v - 1e-9)
            expect(dominated, `${label(o)} dikalahkan ${label(other)}`).toBe(false)
          }
        }
      })

      it('tidak pernah lebih buruk dari mock: biaya dan jarak terbaik', () => {
        const best = (set: OptionSet, f: (o: PlanOption) => number) => Math.min(...set.options.map(f))
        // satu langkah pembulatan Rp 500 dan 0,1 km diizinkan: kedua mesin membangun waktu per ruas dengan cara yang sama,
        // tetapi mesin produksi mengoptimalkan dengan kecepatan tetap pada jam berangkat
        expect(best(go, (o) => o.totals.costRp)).toBeLessThanOrEqual(best(mock, (o) => o.totals.costRp) + 500)
        expect(best(go, (o) => o.totals.distanceKm)).toBeLessThanOrEqual(best(mock, (o) => o.totals.distanceKm) + 0.1)
      })

      it('baseline "kirim terpisah" sama dengan baseline mock (tarif dan jarak dihitung dengan rumus yang sama)', () => {
        expect(Math.abs(go.baseline.costRp - mock.baseline.costRp)).toBeLessThanOrEqual(500 * req.shipments.length)
        expect(Math.abs(go.baseline.distanceKm - mock.baseline.distanceKm)).toBeLessThanOrEqual(0.1 * req.shipments.length)
      })
    })
  }
})
