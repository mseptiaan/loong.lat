import {
  ApiError,
  centroid,
  dwellMin,
  hash01,
  haversineKm,
  isEffort,
  offsetPoint,
  roadKm,
  type Effort,
  type LatLng,
  type Method,
  type OptionSet,
  type PlanOption,
  type Preset,
  type Priority,
  type Route,
  type Schedule,
  type Shipment,
  type Totals,
  type Vehicle,
  type VehicleType,
} from '@loong/domain'
import { msg, type Msg } from '@loong/i18n'
import { createRng } from '../rng'
import { aggregateShipments, assertValid, fleetProfile, smallestFit, type FleetProfile } from './model'
import { buildRoute, hourWib, positionOf, sequenceKm, speedKmh, trafficFactor, type RoadKm, type SeqItem } from './route'

/*
 * MOCK ENGINE. Pengganti engine optimasi sungguhan: deterministik, cepat, dan cukup realistis supaya
 * keempat preset menghasilkan rencana yang benar-benar berbeda dan angka di UI konsisten.
 * Bentuk masukan/keluarannya adalah kontrak yang diharapkan dari engine produksi (Go, sendiri).
 */

export interface SolveInput {
  planId: string
  shipments: Shipment[]
  schedule: Schedule
  priority: Priority
  vehicles: Vehicle[]
  nowMs: number
  /** Jarak antar titik lewat jalan. Kosong = perkiraan haversine x faktor jalan. */
  km?: RoadKm
}

const PRESETS: Preset[] = ['termurah', 'terpendek', 'tercepat', 'seimbang']

/* ---------------- sidik jari masukan ---------------- */

function djb2(s: string): string {
  let h = 5381
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) | 0
  return (h >>> 0).toString(36)
}

/** Berubah bila lokasi/barang/jadwal/prioritas berubah, sehingga opsi lama ditandai basi. */
export function inputHash(input: Pick<SolveInput, 'shipments' | 'schedule' | 'priority'>): string {
  const core = {
    s: input.shipments.map((s) => [
      s.pickup.position.lat.toFixed(5),
      s.pickup.position.lng.toFixed(5),
      s.dropoff.position.lat.toFixed(5),
      s.dropoff.position.lng.toFixed(5),
      s.items.map((i) => [i.qty, i.weightKg, i.lengthCm, i.widthCm, i.heightCm, i.fragile]),
      s.deadline ?? null,
    ]),
    sc: [input.schedule.mode, input.schedule.window?.from, input.schedule.window?.to],
    p: [input.priority.preset],
  }
  return djb2(JSON.stringify(core))
}

/* ---------------- urutan stop dalam satu rute ---------------- */

/** Fungsi sasaran urutan stop: makin kecil makin baik. */
type SeqObjective = (seq: SeqItem[]) => number

/**
 * 'jarak'       : total jarak tempuh terpendek (paling hemat; biasanya ambil semua dulu baru antar).
 * 'antar-cepat' : paket tiba lebih awal, dengan menyisipkan antar di sela ambil (sedikit lebih jauh).
 * 'tenggat'     : meminimalkan keterlambatan terhadap batas waktu tiap pengiriman, baru jarak. Hanya dipakai bila ada batas waktu.
 */
export type OrderMode = 'jarak' | 'antar-cepat' | 'tenggat'

/** Konteks waktu untuk mode 'tenggat': kapan berangkat, kendaraan apa, dan batas waktu tiap pengiriman di grup. */
interface TimeCtx {
  startMs: number
  type: VehicleType
  deadlineMs: (number | undefined)[]
}

export function objectiveFor(mode: OrderMode, shipments: Shipment[], start: LatLng, km: RoadKm, time?: TimeCtx): SeqObjective {
  if (mode === 'jarak') return (seq) => sequenceKm(shipments, seq, start, km)
  if (mode === 'tenggat' && time) {
    return (seq) => {
      let etaMin = 0
      let cum = 0
      let lateMin = 0
      let prev = start
      for (const e of seq) {
        const p = positionOf(shipments[e.s]!, e.kind)
        const legKm = km(prev, p)
        cum += legKm
        etaMin += (legKm / speedKmh(time.type, hourWib(time.startMs + etaMin * 60_000))) * 60
        const due = time.deadlineMs[e.s]
        if (e.kind === 'antar' && due !== undefined) lateMin += Math.max(0, (time.startMs + etaMin * 60_000 - due) / 60_000)
        etaMin += dwellMin(e.kind)
        prev = p
      }
      // terlambat 1 menit lebih buruk daripada 1 km tambahan: keterlambatan menentukan urutan, jarak hanya memecah seri
      return lateMin * 1000 + cum
    }
  }
  return (seq) => {
    let cum = 0
    let sumDrop = 0
    let prev = start
    for (const e of seq) {
      const p = positionOf(shipments[e.s]!, e.kind)
      cum += km(prev, p)
      prev = p
      if (e.kind === 'antar') sumDrop += cum
    }
    // jumlah jarak sampai tiap paket tiba (proxy rata-rata waktu tiba) + sedikit penalti total jarak
    return sumDrop + 0.25 * cum * shipments.length
  }
}

function bestInsert(seq: SeqItem[], s: number, obj: SeqObjective): SeqItem[] {
  let best: SeqItem[] = seq
  let bestScore = Infinity
  for (let i = 0; i <= seq.length; i++) {
    for (let j = i; j <= seq.length; j++) {
      const cand: SeqItem[] = [
        ...seq.slice(0, i),
        { s, kind: 'ambil' },
        ...seq.slice(i, j),
        { s, kind: 'antar' },
        ...seq.slice(j),
      ]
      const score = obj(cand)
      if (score < bestScore - 1e-9) {
        bestScore = score
        best = cand
      }
    }
  }
  return best
}

/** Perbaikan lokal: cabut tiap pengiriman lalu pasang ulang di posisi terbaik, sampai tak ada perbaikan. */
function improve(seq: SeqItem[], obj: SeqObjective): SeqItem[] {
  let cur = seq
  let curScore = obj(cur)
  for (let pass = 0; pass < 4; pass++) {
    let improved = false
    for (const s of new Set(cur.map((e) => e.s))) {
      const cand = bestInsert(
        cur.filter((e) => e.s !== s),
        s,
        obj,
      )
      const score = obj(cand)
      if (score < curScore - 1e-6) {
        cur = cand
        curScore = score
        improved = true
      }
    }
    if (!improved) break
  }
  return cur
}

function insertionSequence(order: number[], obj: SeqObjective): SeqItem[] {
  let seq: SeqItem[] = []
  for (const s of order) seq = bestInsert(seq, s, obj)
  return improve(seq, obj)
}

/** Semua urutan sah (ambil sebelum antar) untuk grup kecil. */
function exhaustive(m: number, obj: SeqObjective): SeqItem[] {
  let best: SeqItem[] = []
  let bestScore = Infinity
  const picked = new Array<boolean>(m).fill(false)
  const dropped = new Array<boolean>(m).fill(false)
  const cur: SeqItem[] = []
  const walk = () => {
    if (cur.length === m * 2) {
      const score = obj(cur)
      if (score < bestScore - 1e-9) {
        bestScore = score
        best = [...cur]
      }
      return
    }
    for (let s = 0; s < m; s++) {
      if (!picked[s]) {
        picked[s] = true
        cur.push({ s, kind: 'ambil' })
        walk()
        cur.pop()
        picked[s] = false
      } else if (!dropped[s]) {
        dropped[s] = true
        cur.push({ s, kind: 'antar' })
        walk()
        cur.pop()
        dropped[s] = false
      }
    }
  }
  walk()
  return best
}

/** Percobaan ulang urutan stop dari urutan awal acak (seed tetap, jadi hasilnya tetap deterministik). */
interface Restarts {
  count: number
  seed: string
}

/** Diekspor hanya untuk pengujian (engine/search.test.ts memeriksa bahwa percobaan ulang tidak pernah memperburuk dan kadang memperbaiki). */
export function bestSequence(shipments: Shipment[], start: LatLng, mode: OrderMode, km: RoadKm, time?: TimeCtx, restarts?: Restarts): SeqItem[] {
  const obj = objectiveFor(mode, shipments, start, km, time)
  const idx = shipments.map((_, i) => i)
  const byNear = [...idx].sort(
    (a, b) => haversineKm(start, shipments[a]!.pickup.position) - haversineKm(start, shipments[b]!.pickup.position),
  )
  const options = [insertionSequence(byNear, obj), insertionSequence([...byNear].reverse(), obj)]
  if (shipments.length <= 3) options.push(exhaustive(shipments.length, obj))
  // percobaan ulang hanya menambah calon; urutan bawaan tetap ada, jadi hasilnya tidak pernah lebih buruk
  if (restarts && shipments.length > 1) {
    const rng = createRng(Math.floor(hash01(restarts.seed) * 0x1_0000_0000))
    for (let i = 0; i < restarts.count; i++) options.push(insertionSequence(rng.shuffle(idx), obj))
  }
  return options.reduce((best, cand) => (obj(cand) < obj(best) - 1e-9 ? cand : best))
}

/* ---------------- partisi pengiriman ke kendaraan ---------------- */

function nominalStart(group: Shipment[], key: string): LatLng {
  const c = centroid(group.map((s) => s.pickup.position))
  return offsetPoint(c, 1.2 + hash01(key) * 1.2, hash01(key + 'x') * 360)
}

/** Titik tengah tiap pengiriman: bahan pengelompokan. */
const midpoints = (shipments: Shipment[]): LatLng[] => shipments.map((s) => centroid([s.pickup.position, s.dropoff.position]))

/** k-means deterministik pada titik tengah tiap pengiriman (inisialisasi titik terjauh, mulai dari pengiriman `first`). */
function cluster(shipments: Shipment[], k: number, first = 0): number[][] {
  const pts = midpoints(shipments)
  const centers: LatLng[] = [pts[first]!]
  while (centers.length < k) {
    let far = pts[0]!
    let farKm = -1
    for (const p of pts) {
      const d = Math.min(...centers.map((c) => haversineKm(c, p)))
      if (d > farKm) {
        farKm = d
        far = p
      }
    }
    centers.push(far)
  }
  let groups: number[][] = []
  for (let iter = 0; iter < 8; iter++) {
    groups = centers.map(() => [])
    pts.forEach((p, i) => {
      let bi = 0
      let bd = Infinity
      centers.forEach((c, ci) => {
        const d = haversineKm(c, p)
        if (d < bd) {
          bd = d
          bi = ci
        }
      })
      groups[bi]!.push(i)
    })
    groups.forEach((g, ci) => {
      if (g.length) centers[ci] = centroid(g.map((i) => pts[i]!))
    })
  }
  return groups.filter((g) => g.length > 0)
}

/** Pengelompokan bertahap: gabungkan dua kelompok yang titik tengahnya paling dekat sampai tersisa k. */
function agglomerative(shipments: Shipment[], k: number): number[][] {
  const pts = midpoints(shipments)
  let groups: number[][] = pts.map((_, i) => [i])
  const center = (g: number[]) => centroid(g.map((i) => pts[i]!))
  while (groups.length > k) {
    let bestA = 0
    let bestB = 1
    let bestKm = Infinity
    for (let a = 0; a < groups.length; a++) {
      for (let b = a + 1; b < groups.length; b++) {
        const d = haversineKm(center(groups[a]!), center(groups[b]!))
        if (d < bestKm - 1e-9) {
          bestKm = d
          bestA = a
          bestB = b
        }
      }
    }
    const merged = [...groups[bestA]!, ...groups[bestB]!].sort((x, y) => x - y)
    groups = groups.filter((_, i) => i !== bestA && i !== bestB).concat([merged])
  }
  return groups
}

/** Kunci pengelompokan tanpa memandang urutan kelompok, untuk membuang yang kembar. */
const groupingKey = (partition: number[][]): string => JSON.stringify(partition.map((g) => [...g].sort((a, b) => a - b)).sort((a, b) => a[0]! - b[0]!))

function partitions(shipments: Shipment[], fleet: FleetProfile): number[][][] {
  const n = shipments.length
  const all = shipments.map((_, i) => i)
  const out: number[][][] = [[all]]
  if (n >= 2) {
    // pisah menurut tipe kendaraan yang dibutuhkan masing-masing
    const motor: number[] = []
    const van: number[] = []
    shipments.forEach((s, i) => (smallestFit(aggregateShipments([s]), fleet) === 'motor' ? motor : van).push(i))
    if (motor.length && van.length) out.push([motor, van])
    for (const k of [2, 3]) if (n >= k) out.push(cluster(shipments, k))
    // paralel penuh hanya untuk pengiriman sedikit; di atas itu terlalu banyak kendaraan
    if (n <= 3) out.push(all.map((i) => [i]))
  }
  return out
}

/**
 * Cara pengelompokan tambahan untuk tingkat "thorough": k-means yang dimulai dari tiap pengiriman, pengelompokan bertahap,
 * dan k lebih besar. Hanya yang belum ada di `partitions()` (dan tidak kembar) yang dikembalikan.
 */
function morePartitions(shipments: Shipment[], known: number[][][]): number[][][] {
  const n = shipments.length
  if (n < 2) return []
  const seen = new Set(known.map(groupingKey))
  const out: number[][][] = []
  const offer = (p: number[][]) => {
    if (p.length < 2 || seen.has(groupingKey(p))) return
    seen.add(groupingKey(p))
    out.push(p)
  }
  for (let k = 2; k <= Math.min(n, 5); k++) {
    offer(agglomerative(shipments, k))
    for (let first = 0; first < n; first++) offer(cluster(shipments, k, first))
  }
  return out
}

/* ---------------- kandidat ---------------- */

interface GroupPlan {
  idx: number[]
  type: VehicleType
  seq: SeqItem[]
}

interface Candidate {
  groups: GroupPlan[]
  routes: Route[]
  totals: Totals
  lastArriveMs: number
  signature: string
  mode: OrderMode
  /** Keterlambatan terbesar terhadap batas waktu pengiriman (ms; <= 0 = semua terpenuhi). -Infinity bila tak ada batas waktu. */
  lateMs: number
}

function evaluate(
  input: SolveInput,
  fleet: FleetProfile,
  partition: number[][],
  startMs: number,
  tag: string,
  mode: OrderMode = 'jarak',
  restarts?: Restarts,
): Candidate | null {
  const groups: GroupPlan[] = []
  const routes: Route[] = []
  let r = 0
  for (const idx of partition) {
    const members = idx.map((i) => input.shipments[i]!)
    const type = smallestFit(aggregateShipments(members), fleet)
    if (!type) return null
    const vehicle = fleet[type]!
    const start = nominalStart(members, `${input.planId}:${idx.join(',')}`)
    const km = input.km ?? roadKm
    const time: TimeCtx = { startMs, type, deadlineMs: members.map((m) => (m.deadline ? Date.parse(m.deadline) : undefined)) }
    const seq = bestSequence(members, start, mode, km, time, restarts)
    groups.push({ idx, type, seq })
    r += 1
    routes.push(
      buildRoute({
        id: `${input.planId}:${tag}:r${r}`,
        planId: input.planId,
        vehicle,
        shipments: members,
        seq,
        start,
        startMs,
        km,
      }),
    )
  }
  const totals = totalsOf(routes)
  const lastArriveMs = Math.max(
    ...routes.map((rt) => new Date(rt.stops.at(-1)!.arriveAt).getTime()),
  )
  const signature = JSON.stringify(groups.map((g) => [g.type, g.idx, g.seq.map((e) => `${e.s}${e.kind[1]}`)]).sort())
  const lateMs = Math.max(-Infinity, ...deadlineRows(routes, input.shipments).map((r) => r.arriveMs - r.dueMs))
  return { groups, routes, totals, lastArriveMs, signature, mode, lateMs }
}

function totalsOf(routes: Route[]): Totals {
  const drops = routes.flatMap((r) => r.stops.filter((s) => s.kind === 'antar'))
  return {
    costRp: routes.reduce((s, r) => s + r.costRp, 0),
    distanceKm: Math.round(routes.reduce((s, r) => s + r.distanceKm, 0) * 10) / 10,
    durationMin: Math.round(Math.max(...routes.map((r) => r.durationMin)) * 10) / 10,
    avgDeliveryMin: Math.round((drops.reduce((s, d) => s + d.etaMin, 0) / Math.max(1, drops.length)) * 10) / 10,
    stops: routes.reduce((s, r) => s + r.stops.length, 0),
    motor: routes.filter((r) => r.vehicleType === 'motor').length,
    blindvan: routes.filter((r) => r.vehicleType === 'blindvan').length,
  }
}

/* ---------------- penyusunan opsi ---------------- */

/** "1 motor + 1 blindvan" sebagai Msg bersarang (urutan kata dan jamak mengikuti bahasa layar). */
function describeFleet(t: Totals): Msg {
  const motor = t.motor ? msg('srv.engine.fleet.motor', { count: t.motor }) : undefined
  const van = t.blindvan ? msg('srv.engine.fleet.blindvan', { count: t.blindvan }) : undefined
  if (motor && van) return msg('srv.engine.fleet.both', { motor, van })
  return motor ?? van ?? msg('srv.engine.fleet.motor', { count: 0 })
}

const presetMsg = (p: Preset): Msg => msg(`dom.preset.${p}`)

/** Label opsi: satu preset, gabungan beberapa ("Seimbang · Termurah"), atau "Rute terbaik" bila semua preset sama. */
function optionLabel(presets: Preset[]): Msg {
  if (presets.length === PRESETS.length) return msg('srv.engine.label.best')
  const [a, b, c] = presets.map(presetMsg)
  if (a && b && c) return msg('srv.engine.label.three', { a, b, c })
  if (a && b) return msg('srv.engine.label.two', { a, b })
  return a ?? msg('srv.engine.label.best')
}

/** Menyambung Msg menjadi rantai bersarang (`join(a, join(b, c))`) tanpa menyambung teks terjemahan. */
const chain = (items: Msg[], join: (a: Msg, b: Msg) => Msg): Msg => items.slice(0, -1).reduceRight((acc, m) => join(m, acc), items.at(-1)!)

function startOf(input: SolveInput): number {
  const lead = 6 * 60_000
  if (input.schedule.mode === 'terjadwal' && input.schedule.window) {
    return Math.max(input.nowMs + 10 * 60_000, new Date(input.schedule.window.from).getTime())
  }
  return input.nowMs + lead
}

/** Calon-calon dalam satu pencarian: unik menurut tanda tangan, dalam urutan masuk. */
type Pool = Map<string, Candidate>

/** Kandidat pemenang tiap preset di antara `pool`; bila ada yang memenuhi semua batas waktu, hanya itu yang dipertimbangkan. */
function chooseWinners(pool: Pool, hasDeadlines: boolean, baselineCand: Candidate): Record<Preset, Candidate> {
  let list = [...pool.values()]
  if (list.length === 0) list = [baselineCand]

  // batas waktu: bila ada opsi yang memenuhi SEMUA batas waktu pengiriman, hanya itu yang dipertimbangkan.
  // Bila tidak ada, semua tetap ditampilkan dan alasan tiap opsi menyebut pengiriman mana yang terlambat.
  if (hasDeadlines) {
    const ok = list.filter((c) => c.lateMs <= 0)
    if (ok.length) list = ok
  }

  const by = (cmp: (a: Candidate, b: Candidate) => number) => list.reduce((a, b) => (cmp(b, a) < 0 ? b : a))
  // biaya sama -> pilih yang lebih pendek lalu yang paketnya tiba lebih awal, supaya "termurah" tidak pernah
  // dikalahkan opsi lain yang harganya sama tetapi lebih baik di metrik lain
  const cheapest = by(
    (a, b) =>
      a.totals.costRp - b.totals.costRp ||
      a.totals.distanceKm - b.totals.distanceKm ||
      a.totals.avgDeliveryMin - b.totals.avgDeliveryMin ||
      a.routes.length - b.routes.length,
  )
  const shortest = by(
    (a, b) =>
      a.totals.distanceKm - b.totals.distanceKm ||
      a.totals.costRp - b.totals.costRp ||
      a.totals.avgDeliveryMin - b.totals.avgDeliveryMin,
  )
  const fastest = by(
    (a, b) =>
      a.totals.durationMin - b.totals.durationMin ||
      a.totals.avgDeliveryMin - b.totals.avgDeliveryMin ||
      a.totals.costRp - b.totals.costRp,
  )
  // seimbang: biaya paling penting, lalu seberapa cepat paket tiba (rata-rata), sisanya jarak dan rute terlama
  const norm = (v: number, min: number, max: number) => (max - min < 1e-9 ? 0 : (v - min) / (max - min))
  const mm = (f: (c: Candidate) => number) => [Math.min(...list.map(f)), Math.max(...list.map(f))] as const
  const [cMin, cMax] = mm((c) => c.totals.costRp)
  const [dMin, dMax] = mm((c) => c.totals.distanceKm)
  const [tMin, tMax] = mm((c) => c.totals.durationMin)
  const [vMin, vMax] = mm((c) => c.totals.avgDeliveryMin)
  const balancedScore = (c: Candidate) =>
    0.4 * norm(c.totals.costRp, cMin, cMax) +
    0.3 * norm(c.totals.avgDeliveryMin, vMin, vMax) +
    0.15 * norm(c.totals.distanceKm, dMin, dMax) +
    0.15 * norm(c.totals.durationMin, tMin, tMax)
  const balanced = by((a, b) => balancedScore(a) - balancedScore(b))
  return { termurah: cheapest, terpendek: shortest, tercepat: fastest, seimbang: balanced }
}

/** Menyusun opsi dari pemenang tiap preset: menggabungkan preset yang memilih kandidat sama, lalu alasan dan penghematan. */
function assemble(a: {
  input: SolveInput
  pool: Pool
  hasDeadlines: boolean
  baselineCand: Candidate
  startMs: number
  n: number
}): OptionSet {
  const { input, baselineCand, startMs, n } = a
  const baseline = baselineCand.totals
  const chosen = chooseWinners(a.pool, a.hasDeadlines, baselineCand)

  // gabungkan preset yang memilih kandidat sama
  const merged = new Map<string, { cand: Candidate; presets: Preset[] }>()
  for (const p of PRESETS) {
    const c = chosen[p]
    const entry = merged.get(c.signature)
    if (entry) entry.presets.push(p)
    else merged.set(c.signature, { cand: c, presets: [p] })
  }

  const single = [...a.pool.values()].find((c) => c.routes.length === 1)
  const options: PlanOption[] = [...merged.values()].map(({ cand, presets }) => {
    const primary = presets.includes(input.priority.preset) ? input.priority.preset : presets[0]!
    const label = optionLabel(presets)
    const routes = cand.routes.map((rt, i) => renameRoute(rt, `${input.planId}:${primary}:r${i + 1}`))
    const totals = cand.totals
    const savingsCost = baseline.costRp - totals.costRp
    return {
      id: `${input.planId}:${primary}`,
      preset: primary,
      label,
      recommended: presets.includes(input.priority.preset),
      headline: msg('srv.engine.headline', { fleet: describeFleet(totals), count: totals.stops, time: cand.lastArriveMs }),
      routes,
      totals,
      savings: {
        costRp: savingsCost,
        pct: baseline.costRp ? Math.round((savingsCost / baseline.costRp) * 100) : 0,
        distanceKm: Math.round((baseline.distanceKm - totals.distanceKm) * 10) / 10,
      },
      reasons: reasonsFor({ input, cand, baseline, single, presets, startMs, n }),
    } satisfies PlanOption
  })

  // rekomendasi pertama, sisanya urut preset
  options.sort((x, y) => Number(y.recommended) - Number(x.recommended) || PRESETS.indexOf(x.preset) - PRESETS.indexOf(y.preset))

  return {
    computedAt: new Date(input.nowMs).toISOString(),
    baseline,
    options,
    inputHash: inputHash(input),
  }
}

/* ---------------- pencarian: tiga tingkat usaha ---------------- */

/** Berapa percobaan ulang urutan stop per pemolesan dan per percobaan lanjutan peninjau. */
const POLISH_RESTARTS = 10
const ROUND_RESTARTS = 6
/** Ronde tinjauan paling banyak, dan berapa percobaan lanjutan paling banyak per ronde (sama dengan batas peninjau di backend). */
const MAX_ROUNDS = 3
const MAX_ASKS = 3
/** Peninjau puas bila dua ronde berturut-turut memperbaiki sasaran kurang dari ini. */
const CALM_GAIN = 0.01

/** Satu angka untuk "seberapa baik jawaban terbaik": biaya, jarak, dan lama pemenang tiap ukuran, dibanding kirim terpisah. Makin kecil makin baik. */
function objectiveOf(winners: Record<Preset, Candidate>, baseline: Totals): number {
  const ratio = (v: number, base: number) => v / Math.max(1e-9, base)
  return (
    ratio(winners.termurah.totals.costRp, baseline.costRp) +
    ratio(winners.terpendek.totals.distanceKm, baseline.distanceKm) +
    ratio(winners.tercepat.totals.durationMin, baseline.durationMin)
  )
}

/**
 * Pencarian opsi dalam tiga tingkat usaha. Setiap `yield` adalah catatan progres (pesan netral-bahasa) SEBELUM langkah yang
 * dijelaskannya dijalankan, dan nilai kembaliannya adalah OptionSet. Semua angka `method` adalah hitungan nyata dari yang
 * dijalankan di sini (`trials` = jumlah `evaluate`); tiruan tidak membuktikan optimal, jadi `proven` tidak pernah diisi.
 *
 *  - tanpa `effort`: jalur lama (tanpa `method`), dipakai seed dan pembuat plan acak.
 *  - `fast`: tiap preset memilih dari kandidat dasar (partisi x gaya urutan); hasil sama dengan jalur lama.
 *  - `thorough`: ditambah pengelompokan lain (k-means dari tiap pengiriman, bertahap, k lebih besar), lalu dipoles: urutan
 *    stop pemenang dicoba ulang dari urutan acak.
 *  - `reviewed`: tanpa pemolesan di awal, lalu hingga 3 ronde: peninjau (aturan bawaan) meminta percobaan ulang pada pemenang
 *    atau menyatakan puas; pemolesan di akhir.
 * Kandidat dasar `fast` selalu ikut, dan percobaan hanya menambah calon, jadi hasil tidak pernah lebih buruk daripada `fast`.
 */
export function* searchOptionSet(input: SolveInput, effort?: Effort): Generator<Msg, OptionSet, void> {
  assertValid(input.shipments)
  const fleet = fleetProfile(input.vehicles)
  if (!fleet.motor && !fleet.blindvan) {
    throw new ApiError('layanan', msg('srv.engine.noVehicles'))
  }
  const startMs = startOf(input)
  const n = input.shipments.length

  // baseline: tiap pengiriman dikirim sendiri-sendiri
  const baselineCand = evaluate(input, fleet, input.shipments.map((_, i) => [i]), startMs, 'base')
  if (!baselineCand) {
    throw new ApiError('tidak_valid', msg('srv.engine.tooBig'), {
      field: 'shipments',
    })
  }
  const baseline = baselineCand.totals

  // kumpulkan kandidat unik
  const hasDeadlines = input.shipments.some((s) => s.deadline)
  const modes: OrderMode[] = hasDeadlines ? ['jarak', 'antar-cepat', 'tenggat'] : ['jarak', 'antar-cepat']
  const pool: Pool = new Map()
  let trials = 0
  const run = (partition: number[][], tag: string, mode: OrderMode, restarts?: Restarts) => {
    trials += 1
    const c = evaluate(input, fleet, partition, startMs, tag, mode, restarts)
    if (c && !pool.has(c.signature)) pool.set(c.signature, c)
  }
  // gaya urutan per partisi: jarak terpendek vs antar lebih awal (hanya relevan bila >1 pengiriman/rute),
  // ditambah urutan yang mengejar batas waktu bila ada pengiriman yang punya batas waktu
  const base = partitions(input.shipments, fleet)
  base.forEach((p, i) => {
    for (const mode of modes) run(p, `c${i}${MODE_TAG[mode]}`, mode)
  })

  const assembleNow = () => assemble({ input, pool, hasDeadlines, baselineCand, startMs, n })
  const winners = () => chooseWinners(pool, hasDeadlines, baselineCand)
  const distinctWinners = (): Candidate[] => [...new Map(Object.values(winners()).map((c) => [c.signature, c])).values()]
  const partitionOf = (c: Candidate) => c.groups.map((g) => g.idx)
  // urutan stop hanya bisa diperbaiki pada rute dengan lebih dari satu pengiriman; selain itu tidak ada yang dicoba ulang
  const reworkable = (): Candidate[] => distinctWinners().filter((c) => c.groups.some((g) => g.idx.length > 1))

  if (effort === undefined || effort === 'fast') {
    const set = assembleNow()
    if (effort) set.method = { effort, trials }
    return set
  }

  // tingkat "thorough" dan "reviewed": pengelompokan lain
  const extra = morePartitions(input.shipments, base)
  if (extra.length) {
    const grouped = base.length + extra.length
    yield msg('srv.engine.search.experiments', { n: grouped, count: grouped })
    extra.forEach((p, i) => {
      for (const mode of modes) run(p, `x${i}${MODE_TAG[mode]}`, mode)
    })
  }

  let rounds = 0
  if (effort === 'reviewed') {
    let prev = objectiveOf(winners(), baseline)
    let calm = 0
    for (let round = 1; round <= MAX_ROUNDS; round++) {
      rounds = round
      yield msg('srv.engine.search.round', { round })
      yield msg('srv.engine.search.reviewRules')
      const searchable = reworkable()
      if (calm >= 2 || searchable.length === 0) {
        yield msg('srv.engine.search.satisfied')
        break
      }
      const asks = Math.min(MAX_ASKS, searchable.length)
      yield msg('srv.engine.search.more', { n: asks, count: asks })
      searchable.slice(0, asks).forEach((c, i) => run(partitionOf(c), `r${round}${i}`, c.mode, { count: ROUND_RESTARTS, seed: `${input.planId}:round${round}:${i}` }))
      const now = objectiveOf(winners(), baseline)
      calm = (prev - now) / Math.max(1e-9, prev) < CALM_GAIN ? calm + 1 : 0
      prev = now
    }
  }

  // pemolesan: urutan stop tiap pemenang dicoba ulang dari urutan acak; bawaan tetap ada
  const polishable = reworkable()
  if (polishable.length) {
    yield msg('srv.engine.search.polish')
    polishable.forEach((c, i) => run(partitionOf(c), `p${i}`, c.mode, { count: POLISH_RESTARTS, seed: `${input.planId}:polish:${i}` }))
  }

  const set = assembleNow()
  const method: Method = { effort, trials }
  if (polishable.length) method.polished = true
  if (rounds > 0) method.rounds = rounds
  if (effort === 'reviewed') method.reviewer = 'rules'
  set.method = method
  return set
}

/** Menjalankan pencarian sampai selesai tanpa catatan progres (cara paling sederhana memanggil engine). */
export function computeOptionSet(input: SolveInput, opts: { effort?: Effort } = {}): OptionSet {
  if (opts.effort !== undefined && !isEffort(opts.effort)) throw new ApiError('tidak_valid', msg('srv.api.badEffort'), { field: 'effort' })
  const search = searchOptionSet(input, opts.effort)
  for (;;) {
    const step = search.next()
    if (step.done) return step.value
  }
}

const MODE_TAG: Record<OrderMode, string> = { jarak: 'j', 'antar-cepat': 'a', tenggat: 't' }

interface DeadlineRow {
  name: string
  arriveMs: number
  dueMs: number
}

/** Kapan tiap pengiriman yang punya batas waktu tiba di titik antarnya, pada rute-rute ini. */
function deadlineRows(routes: Route[], shipments: Shipment[]): DeadlineRow[] {
  const drops = routes.flatMap((r) => r.stops).filter((st) => st.kind === 'antar')
  return shipments.flatMap((s) => {
    const stop = s.deadline ? drops.find((st) => st.shipmentId === s.id) : undefined
    return s.deadline && stop ? [{ name: s.dropoff.name, arriveMs: Date.parse(stop.arriveAt), dueMs: Date.parse(s.deadline) }] : []
  })
}

function deadlineReasons(routes: Route[], shipments: Shipment[]): Msg[] {
  const rows = deadlineRows(routes, shipments)
  if (!rows.length) return []
  // hari berbeda (WIB): jam saja akan terbaca "tiba 22.05 (batas 17.00)" untuk batas besok
  const wibDay = (ms: number) => new Date(ms + 7 * 3_600_000).toISOString().slice(0, 10)
  const show = (r: DeadlineRow): Msg =>
    msg(wibDay(r.arriveMs) === wibDay(r.dueMs) ? 'srv.engine.deadline.row' : 'srv.engine.deadline.rowDated', { place: r.name, arrive: r.arriveMs, due: r.dueMs })
  const list = (items: DeadlineRow[]) => chain(items.map(show), (a, b) => msg('srv.engine.deadline.join', { a, b }))
  const late = rows.filter((r) => r.arriveMs > r.dueMs)
  return late.length
    ? [msg('srv.engine.reason.deadlineLate', { count: late.length, rows: list(late) })]
    : [msg('srv.engine.reason.deadlineMet', { count: rows.length, rows: list(rows) })]
}

function renameRoute(route: Route, id: string): Route {
  return { ...route, id, stops: route.stops.map((s, i) => ({ ...s, id: `${id}:s${i + 1}` })) }
}

/* ---------------- alasan yang bisa ditelusuri ---------------- */

function reasonsFor(a: {
  input: SolveInput
  cand: Candidate
  baseline: Totals
  single?: Candidate
  presets: Preset[]
  startMs: number
  n: number
}): Msg[] {
  const { input, cand, baseline } = a
  const out: Msg[] = []
  const fleet = fleetProfile(input.vehicles)

  cand.groups.forEach((g, i) => {
    const members = g.idx.map((k) => input.shipments[k]!)
    const agg = aggregateShipments(members)
    let text: Msg
    if (g.type === 'blindvan' && fleet.motor) {
      const m = fleet.motor
      text =
        agg.maxDimCm > m.maxDimensionCm
          ? msg('srv.engine.reason.vanDim', { dim: agg.maxDimCm, max: m.maxDimensionCm })
          : agg.weightKg > m.maxWeightKg
            ? msg('srv.engine.reason.vanWeight', { kg: agg.weightKg, max: m.maxWeightKg })
            : msg('srv.engine.reason.vanVolume', { m3: agg.volumeM3 })
    } else {
      text = msg(g.type === 'motor' ? 'srv.engine.reason.fitsMotor' : 'srv.engine.reason.fitsVan', { count: agg.itemCount, kg: agg.weightKg })
    }
    out.push(cand.groups.length > 1 ? msg('srv.engine.reason.route', { n: i + 1, text }) : text)
  })

  const saveKm = baseline.distanceKm - cand.totals.distanceKm
  if (cand.routes.length < a.n && saveKm > 0.2) {
    out.push(
      msg('srv.engine.reason.merge', {
        shipments: a.n,
        count: cand.routes.length,
        km: cand.totals.distanceKm,
        base: baseline.distanceKm,
        pct: Math.round((saveKm / baseline.distanceKm) * 100),
      }),
    )
  }

  const saving = baseline.costRp - cand.totals.costRp
  if (saving > 0) {
    out.push(msg('srv.engine.reason.costSaves', { cost: cand.totals.costRp, saving, pct: Math.round((saving / baseline.costRp) * 100) }))
  } else if (saving < 0) {
    out.push(msg('srv.engine.reason.costMore', { cost: cand.totals.costRp, extra: -saving }))
  } else {
    out.push(msg('srv.engine.reason.costSame', { cost: cand.totals.costRp }))
  }

  if (cand.routes.length > 1) {
    const count = cand.routes.length
    const duration = cand.totals.durationMin
    out.push(a.single ? msg('srv.engine.reason.splitVs', { count, duration, single: a.single.totals.durationMin }) : msg('srv.engine.reason.split', { count, duration }))
  } else {
    out.push(msg('srv.engine.reason.single', { duration: cand.totals.durationMin }))
  }

  if (a.n > 1) {
    const pickupsFirst = cand.routes.every((r) => {
      const firstDrop = r.stops.findIndex((s) => s.kind === 'antar')
      return firstDrop === -1 || r.stops.slice(firstDrop).every((s) => s.kind === 'antar')
    })
    out.push(msg(pickupsFirst ? 'srv.engine.reason.pickupsFirst' : 'srv.engine.reason.interleaved', { avg: cand.totals.avgDeliveryMin }))
  }

  const factor = trafficFactor(hourWib(a.startMs))
  if (factor < 0.7) {
    out.push(msg('srv.engine.reason.rush', { time: a.startMs, pct: Math.round((1 - factor) * 100) }))
  }

  const first = cand.routes[0]!
  const steps = first.stops
    .slice(0, 6)
    .map((s) => msg(s.kind === 'ambil' ? 'srv.engine.seq.pickup' : 'srv.engine.seq.dropoff', { place: s.waypoint.name }))
  const joined = chain(steps, (x, y) => msg('srv.engine.seq.then', { a: x, b: y }))
  out.push(msg(cand.routes.length > 1 ? 'srv.engine.reason.orderRoute1' : 'srv.engine.reason.order', { steps: first.stops.length > 6 ? msg('srv.engine.seq.more', { a: joined }) : joined }))

  out.push(...deadlineReasons(cand.routes, input.shipments))
  return out
}
