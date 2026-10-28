import {
  DEFAULT_SETTINGS,
  dwellMin,
  haversineKm,
  offsetPoint,
  type AppNotification,
  type DeliveryProof,
  type Driver,
  type Item,
  type LatLng,
  type Plan,
  type PlanEvent,
  type PlanOption,
  type Preset,
  type Route,
  type Shipment,
  type User,
  type Vehicle,
  type Waypoint,
} from '@loong/domain'
import { msg, type Msg } from '@loong/i18n'
import { PLACES, getPlace } from '@loong/gazetteer'
import { seedWallet } from './wallet'
import { DB_VERSION, type DB } from '../db'
import { rebaseStart, routesFromOption } from '../engine/instantiate'
import { computeOptionSet } from '../engine/solve'
import { createRng, type Rng } from '../rng'
import { analyzeProofPhoto } from '../ai/proof'
import { catalogItem, makeItem } from './catalog'
import { makeVehicles } from './fleet'
import { ADMIN, CONTACTS, CUSTOMERS, DRIVERS, DRIVER_PIN } from './people'
import { SAMPLE_PROOF_PHOTO, SAMPLE_PROOF_PHOTO_NAME } from './proofPhoto'

export const SCENARIO = 'jakarta'

const MIN = 60_000
const HOUR = 3_600_000
const DAY = 86_400_000
const iso = (ms: number) => new Date(ms).toISOString()

/** Awal hari (00.00 WIB) untuk sebuah waktu. */
const startOfDayWib = (ms: number) => Math.floor((ms + 7 * HOUR) / DAY) * DAY - 7 * HOUR

const ev = (n: number, at: number, text: Msg): PlanEvent => ({ id: `evt-${n}-${at.toString(36)}`, at: iso(at), text })

/* ---------- bahan acak ---------- */

const SHIPPABLE = PLACES.filter((p) => p.kind !== 'landmark')

function waypointOf(rng: Rng, placeId: string, note?: string): Waypoint {
  const p = getPlace(placeId)!
  const c = rng.pick(CONTACTS)
  return {
    placeId: p.id,
    name: p.name,
    address: p.address,
    position: p.position,
    contactName: c.name,
    contactPhone: c.phone,
    ...(note ? { note } : {}),
  }
}

/** Barang yang lazim dikirim motor (semua sisi <= 55 cm); blindvan hanya bila diminta. */
const LIGHT_ITEMS = ['dokumen', 'paket-kecil', 'sepatu', 'kue', 'laptop', 'makanan', 'kardus-sedang']

/** Maks. berat satu pengiriman ringan, agar 3 pengiriman sekaligus masih muat satu motor (20 kg). */
const LIGHT_SHIPMENT_KG = 6

function randomItems(rng: Rng, heavy: boolean): Item[] {
  if (heavy) return [makeItem(rng.pick(['rak', 'kursi', 'dispenser', 'kulkas-mini']))]
  const want = rng.int(1, 2)
  const items: Item[] = []
  let weight = 0
  for (const key of rng.shuffle(LIGHT_ITEMS)) {
    if (items.length >= want) break
    const c = catalogItem(key)
    const qty = key === 'dokumen' || key === 'paket-kecil' ? rng.int(1, 3) : 1
    if (weight + c.weightKg * qty > LIGHT_SHIPMENT_KG) continue
    items.push(makeItem(key, { qty }))
    weight += c.weightKg * qty
  }
  return items.length ? items : [makeItem('paket-kecil')]
}

function randomShipment(rng: Rng, id: string, opts: { heavy?: boolean; near?: LatLng } = {}): Shipment {
  const from = opts.near
    ? rng.pick(SHIPPABLE.filter((p) => haversineKm(p.position, opts.near!) < 9))
    : rng.pick(SHIPPABLE)
  const candidates = SHIPPABLE.filter((p) => {
    const d = haversineKm(p.position, from.position)
    return p.id !== from.id && d >= 1.5 && d <= 16
  })
  const to = rng.pick(candidates.length ? candidates : SHIPPABLE.filter((p) => p.id !== from.id))
  return {
    id,
    pickup: waypointOf(rng, from.id),
    dropoff: waypointOf(rng, to.id, rng.chance(0.25) ? 'Titip ke satpam bila tidak ada orang' : undefined), // i18n-ignore: catatan alamat = data
    items: randomItems(rng, opts.heavy ?? false),
  }
}

export function randomShipments(rng: Rng, planKey: string, count: number, opts: { heavyFirst?: boolean } = {}): Shipment[] {
  const first = randomShipment(rng, `${planKey}-s1`, { heavy: opts.heavyFirst })
  const out = [first]
  for (let i = 2; i <= count; i++) {
    // pengiriman berikutnya di sekitar yang pertama, supaya penggabungan rute masuk akal
    out.push(randomShipment(rng, `${planKey}-s${i}`, { near: first.pickup.position }))
  }
  return out
}

/** Pengiriman yang awal dan tujuannya sama-sama berdekatan, sehingga bisa digabung dalam satu rute. */
function clusteredShipments(rng: Rng, planKey: string, count: number, heavy: boolean): Shipment[] {
  const first = randomShipment(rng, `${planKey}-s1`, { heavy })
  const out = [first]
  for (let i = 2; i <= count; i++) {
    const near = (p: LatLng) => SHIPPABLE.filter((x) => haversineKm(x.position, p) < 6)
    const from = rng.pick(near(first.pickup.position))
    const toCandidates = near(first.dropoff.position).filter((x) => x.id !== from.id)
    const to = toCandidates.length ? rng.pick(toCandidates) : getPlace(first.dropoff.placeId!)!
    out.push({ id: `${planKey}-s${i}`, pickup: waypointOf(rng, from.id), dropoff: waypointOf(rng, to.id), items: randomItems(rng, false) })
  }
  return out
}

/* ---------- pembangun plan ---------- */

interface Ctx {
  rng: Rng
  nowMs: number
  vehicles: Vehicle[]
  users: User[]
  drivers: Driver[]
  planNo: number
}

interface PlanSpec {
  user: User
  createdMs: number
  shipments: Shipment[]
  preset: Preset
  /** Hanya untuk plan terjadwal. */
  scheduledFromMs?: number
}

function newPlan(ctx: Ctx, spec: PlanSpec): { plan: Plan; chosen: PlanOption } {
  const n = ++ctx.planNo
  const id = `pln-${n}`
  const scheduled = spec.scheduledFromMs !== undefined
  const schedule: Plan['schedule'] = scheduled
    ? { mode: 'terjadwal', window: { from: iso(spec.scheduledFromMs!), to: iso(spec.scheduledFromMs! + HOUR) } }
    : { mode: 'sekarang' }
  const priority: Plan['priority'] = { preset: spec.preset }
  const optionSet = computeOptionSet({
    planId: id,
    shipments: spec.shipments,
    schedule,
    priority,
    vehicles: ctx.vehicles,
    nowMs: spec.createdMs + 90_000,
  })
  const chosen = optionSet.options.find((o) => o.recommended) ?? optionSet.options[0]!
  const plan: Plan = {
    id,
    code: `PL-${2000 + n}`,
    userId: spec.user.id,
    status: 'opsi_siap',
    schedule,
    priority,
    shipments: spec.shipments,
    optionSet,
    selectedOptionId: chosen.id,
    routes: [],
    createdAt: iso(spec.createdMs),
    updatedAt: iso(spec.createdMs + 90_000),
    events: [
      ev(n * 10 + 1, spec.createdMs, msg('srv.ops.seed.planCreated')),
      ev(n * 10 + 2, spec.createdMs + 90_000, msg('srv.ops.seed.optionsPicked', { label: chosen.label })),
    ],
    draftStep: 4,
  }
  return { plan, chosen }
}

/**
 * Plan yang berjalan/ditugaskan di seed ditangani satu driver, jadi harus punya opsi satu-rute (opsi yang
 * memecah ke beberapa kendaraan meninggalkan rute tanpa driver di plan yang sudah jalan). Mencoba beberapa
 * kali dengan pengiriman berbeda; nomor plan dikembalikan bila percobaan gagal agar kode PL tetap berurutan.
 */
function newSingleRoutePlan(
  ctx: Ctx,
  base: Omit<PlanSpec, 'shipments'>,
  makeShipments: () => Shipment[],
): { plan: Plan; chosen: PlanOption } {
  for (let attempt = 0; attempt < 20; attempt++) {
    const created = newPlan(ctx, { ...base, shipments: makeShipments() })
    const single = created.plan.optionSet?.options.find((o) => o.routes.length === 1)
    if (single) return { plan: { ...created.plan, selectedOptionId: single.id }, chosen: single }
    ctx.planNo -= 1
  }
  throw new Error('seed: tidak berhasil membuat plan satu-rute setelah 20 percobaan') // i18n-ignore: galat pengembang
}

function confirm(plan: Plan, chosen: PlanOption, atMs: number, method: Plan['paymentMethod'] = 'saldo'): Plan {
  return {
    ...plan,
    status: 'menunggu_driver',
    routes: routesFromOption(plan.id, chosen),
    paymentMethod: method,
    totalCostRp: chosen.totals.costRp,
    confirmedAt: iso(atMs),
    updatedAt: iso(atMs),
    events: [...plan.events, ev(plan.events.length + 100, atMs, msg('srv.ops.seed.orderConfirmed', { cost: chosen.totals.costRp }))],
  }
}

function pickDriver(ctx: Ctx, type: Route['vehicleType'], busy: Set<string>, index = 0): { driver: Driver; vehicle: Vehicle } {
  const options = ctx.drivers
    .map((d) => ({ driver: d, vehicle: ctx.vehicles.find((v) => v.id === d.vehicleId) }))
    .filter((x): x is { driver: Driver; vehicle: Vehicle } => !!x.vehicle && x.vehicle.type === type && !busy.has(x.driver.id))
  const hit = options[index % Math.max(1, options.length)]
  if (!hit) throw new Error(`Tidak ada driver ${type} yang tersedia di seed`) // i18n-ignore: galat pengembang
  return hit
}

function assignRoute(route: Route, driver: Driver, vehicle: Vehicle, atMs: number): Route {
  return { ...route, driverId: driver.id, vehicleId: vehicle.id, status: 'ditugaskan', assignedAt: iso(atMs) }
}

/** Bukti antar seed: kode OTP penerima, atau foto contoh beserta ringkasan AI-nya. */
function proofAt(atMs: number, photo: boolean): DeliveryProof {
  if (!photo) return { kind: 'otp', at: iso(atMs) }
  return {
    kind: 'foto',
    at: iso(atMs),
    photo: SAMPLE_PROOF_PHOTO,
    analysis: analyzeProofPhoto({ name: SAMPLE_PROOF_PHOTO_NAME, size: SAMPLE_PROOF_PHOTO.length }, atMs + 20_000),
  }
}

/** Tandai rute selesai dengan waktu aktual yang bergeser dari rencana (rata-rata sedikit telat). */
function finishRoute(rng: Rng, route: Route, opts: { problemStop?: boolean; photoProofStop?: number } = {}): Route {
  let cumulative = 0
  const stops = route.stops.map((s, i) => {
    cumulative += rng.chance(0.14) ? rng.int(7, 18) : rng.int(-2, 4)
    cumulative = Math.max(-3, cumulative)
    const arrived = new Date(s.arriveAt).getTime() + cumulative * MIN
    const completed = arrived + (dwellMin(s.kind) + rng.int(-1, 2)) * MIN
    const problem = opts.problemStop && s.kind === 'antar' && i === route.stops.length - 1
    return {
      ...s,
      status: problem ? ('bermasalah' as const) : ('selesai' as const),
      arrivedAt: iso(arrived),
      completedAt: iso(completed),
      ...(problem ? { issue: msg('dom.issue.tidak_ada_orang') } : {}),
      ...(s.kind === 'antar' && !problem ? { proof: proofAt(completed, i === opts.photoProofStop) } : {}),
    }
  })
  const last = stops.at(-1)!
  return {
    ...route,
    status: 'selesai',
    stops,
    startedAt: route.startAt,
    finishedAt: last.completedAt,
    delayMin: Math.max(0, Math.round(cumulative)),
  }
}

/** Tempatkan rute di tengah perjalanan: `done` stop selesai, sedang menuju stop berikutnya. */
function runningRoute(route: Route, nowMs: number, done: number, fraction: number, extraDelay = 0): Route {
  // 1) ETA konsisten dari posisi awal sebenarnya (jangkar sembarang), 2) suntik keterlambatan di ruas aktif
  let r = rebaseStart(route, route.startPosition, nowMs)
  if (extraDelay) {
    r = { ...r, stops: r.stops.map((s, i) => (i === done ? { ...s, legMin: s.legMin + extraDelay } : s)) }
    r = rebaseStart(r, r.startPosition, nowMs)
  }
  // 3) berapa lama rute sudah berjalan, 4) geser jangkar ke belakang sebesar itu
  const leg = r.stops[done]!.legMin
  const before = done > 0 ? r.stops[done - 1]!.etaMin + dwellMin(r.stops[done - 1]!.kind) : 0
  const startMs = nowMs - (before + fraction * leg) * MIN
  r = rebaseStart(r, r.startPosition, startMs)
  r.stops = r.stops.map((s, i) => {
    if (i < done) {
      const arrived = new Date(s.arriveAt).getTime()
      const completed = arrived + dwellMin(s.kind) * MIN
      return { ...s, status: 'selesai', arrivedAt: iso(arrived), completedAt: iso(completed), ...(s.kind === 'antar' ? { proof: proofAt(completed, false) } : {}) }
    }
    return { ...s, status: i === done ? 'menuju' : 'menunggu' }
  })
  r.status = 'berjalan'
  r.startedAt = iso(startMs)
  r.legStartedAt = iso(nowMs - fraction * leg * MIN)
  if (extraDelay) r.delayMin = extraDelay
  return r
}

/* ---------- seed utama ---------- */

export function createSeed(nowMs: number, scenario = SCENARIO): DB {
  const rng = createRng(20261020)
  const vehicles = makeVehicles()

  const users: User[] = CUSTOMERS.map((c, i) => ({
    id: `usr-${i + 1}`,
    name: c.name,
    email: c.email,
    phone: c.phone,
    createdAt: iso(nowMs - (60 - i * 4) * DAY),
    blocked: false,
    savedAddresses: [],
  }))
  const credentials: Record<string, string> = Object.fromEntries(users.map((u) => [u.id, 'demo1234']))

  // alamat tersimpan untuk akun demo
  const dina = users[0]!
  const home = getPlace('kemang-raya')!
  const shop = getPlace('pasar-mayestik')!
  dina.savedAddresses = [
    {
      id: 'adr-1',
      label: 'Rumah',
      place: { name: home.name, address: home.address, position: home.position, placeId: home.id },
      contactName: dina.name,
      contactPhone: dina.phone,
    },
    {
      id: 'adr-2',
      label: 'Toko',
      place: { name: shop.name, address: shop.address, position: shop.position, placeId: shop.id },
      contactName: 'Bu Marlina',
      contactPhone: '081290010009',
    },
  ]

  const vehicleForDriver = (i: number) => (i < vehicles.length ? vehicles[i] : undefined)
  const drivers: Driver[] = DRIVERS.map((p, i) => {
    const v = vehicleForDriver(i)
    const spot = rng.pick(SHIPPABLE.filter((x) => x.kind === 'mall' || x.kind === 'stasiun' || x.kind === 'kantor'))
    return {
      id: `drv-${i + 1}`,
      name: p.name,
      phone: p.phone,
      rating: Math.round((4.55 + rng.next() * 0.4) * 100) / 100,
      vehicleId: v?.id,
      status: i === 8 ? 'offline' : 'online',
      position: offsetPoint(spot.position, rng.next() * 0.8, rng.next() * 360),
      lastSeen: iso(nowMs - rng.int(0, 4) * MIN),
      joinedAt: iso(nowMs - rng.int(90, 400) * DAY),
      completedTrips: rng.int(120, 900),
    }
  })
  drivers.forEach((d) => {
    const v = vehicles.find((x) => x.id === d.vehicleId)
    if (v) v.driverId = d.id
  })

  const ctx: Ctx = { rng, nowMs, vehicles, users, drivers, planNo: 0 }
  const plans: Plan[] = []
  const notifications: AppNotification[] = []
  let notifNo = 0
  const notify = (audience: AppNotification['audience'], atMs: number, title: Msg, body: Msg, planId?: string, read = false) =>
    notifications.push({ id: `ntf-${++notifNo}`, audience, at: iso(atMs), title, body, planId, read })

  const busy = new Set<string>()

  /* --- riwayat selesai (14 hari) --- */
  const history: { day: number; hour: number; shipments: number; preset: Preset; user: number }[] = []
  // jumlah plan selesai per hari mundur (kemarin, 2 hari lalu, ...): akhir pekan lebih sepi, hari terbaru lebih ramai
  const PER_DAY = [4, 1, 2, 3, 3, 2, 2, 2, 1, 1, 1, 2, 1, 1]
  const dayOfPlan = PER_DAY.flatMap((n, i) => Array.from({ length: n }, () => i + 1))
  for (let i = 0; i < 26; i++) {
    history.push({
      day: dayOfPlan[i]!,
      hour: rng.int(8, 16),
      shipments: rng.pick([1, 1, 2, 2, 3, 3, 4]),
      preset: rng.pick(['termurah', 'seimbang', 'seimbang', 'tercepat', 'terpendek'] as const),
      user: i < 4 ? 0 : rng.int(0, users.length - 1),
    })
  }
  // plan paling lama dibuat lebih dulu agar kode PL naik menurut waktu
  history.sort((a, b) => b.day - a.day || a.hour - b.hour)
  history.forEach((h, i) => {
    const createdMs = startOfDayWib(nowMs - h.day * DAY) + h.hour * HOUR + rng.int(0, 59) * MIN
    const user = users[h.user]!
    // plan multi-pengiriman berasal dari titik yang berdekatan (pengguna menggabungkan paket yang searah), sehingga
    // penggabungan rute memang menghemat seperti yang dijanjikan produk
    const shipments = h.shipments > 1 ? clusteredShipments(rng, `h${i}`, h.shipments, i === 7) : randomShipments(rng, `h${i}`, h.shipments, { heavyFirst: i === 7 })
    const { plan, chosen } = newPlan(ctx, { user, createdMs, shipments, preset: h.preset })
    let p = confirm(plan, chosen, createdMs + 4 * MIN, rng.pick(['saldo', 'transfer', 'tunai'] as const))
    const routes = p.routes.map((r, ri) => {
      const { driver, vehicle } = pickDriver(ctx, r.vehicleType, new Set(), rng.int(0, 5) + ri)
      const startMs = createdMs + 10 * MIN
      const based = rebaseStart(assignRoute(r, driver, vehicle, createdMs + 6 * MIN), driver.position ?? r.startPosition, startMs)
      // plan terbaru (PL-2026): antar pertama di rute pertama dibuktikan dengan foto penerima, sisanya kode OTP
      const photoProofStop = i === history.length - 1 && ri === 0 ? based.stops.findIndex((s) => s.kind === 'antar') : undefined
      return finishRoute(rng, based, { problemStop: i === 11 && ri === 0, photoProofStop })
    })
    const finishedMs = Math.max(...routes.map((r) => new Date(r.finishedAt!).getTime()))
    p = {
      ...p,
      status: 'selesai',
      routes,
      finishedAt: iso(finishedMs),
      updatedAt: iso(finishedMs),
      events: [
        ...p.events,
        ev(p.events.length + 101, createdMs + 6 * MIN, msg('srv.ops.seed.driverAssigned', { drivers: routes.map((r) => ctx.drivers.find((d) => d.id === r.driverId)?.name).join(', ') })),
        ev(p.events.length + 102, finishedMs, msg('srv.ops.event.allDone')),
      ],
    }
    plans.push(p)
  })

  /* --- dibatalkan --- */
  for (let i = 0; i < 2; i++) {
    const createdMs = nowMs - (3 + i * 4) * DAY + 10 * HOUR
    const { plan, chosen } = newPlan(ctx, {
      user: users[2 + i]!,
      createdMs,
      shipments: randomShipments(rng, `c${i}`, 2),
      preset: 'termurah',
    })
    const p = confirm(plan, chosen, createdMs + 3 * MIN)
    plans.push({
      ...p,
      status: 'dibatalkan',
      cancelReason: i === 0 ? 'Berubah pikiran' : 'Salah memilih lokasi', // i18n-ignore: isian pengguna = data
      updatedAt: iso(createdMs + 9 * MIN),
      events: [...p.events, ev(p.events.length + 100, createdMs + 9 * MIN, msg('srv.ops.seed.cancelledBySender'))],
    })
  }

  /* --- sedang berjalan: dua motor (yang pertama Budi, akun demo; yang kedua telat) dan satu blindvan --- */
  const running: { user: number; shipments: number; heavy: boolean; done: number; frac: number; delay: number; agoMin: number }[] = [
    { user: 0, shipments: 2, heavy: false, done: 2, frac: 0.45, delay: 0, agoMin: 38 },
    { user: 3, shipments: 3, heavy: false, done: 3, frac: 0.3, delay: 14, agoMin: 46 },
    { user: 5, shipments: 1, heavy: true, done: 1, frac: 0.6, delay: 0, agoMin: 34 },
  ]
  running.forEach((r, i) => {
    const createdMs = nowMs - r.agoMin * MIN
    const user = users[r.user]!
    const { plan, chosen } = newSingleRoutePlan(ctx, { user, createdMs, preset: 'termurah' }, () =>
      clusteredShipments(rng, `r${i}`, r.shipments, r.heavy),
    )
    let p = confirm(plan, chosen, createdMs + 3 * MIN)
    // driver dipilih menurut tipe kendaraan rute: motor pertama = Budi (demo), blindvan pertama = Gilang
    const { driver, vehicle } = pickDriver(ctx, p.routes[0]!.vehicleType, busy)
    const first = assignRoute(p.routes[0]!, driver, vehicle, createdMs + 6 * MIN)
    const done = Math.min(r.done, first.stops.length - 1)
    const live = runningRoute({ ...first, startPosition: driver.position ?? first.startPosition }, nowMs, done, r.frac, r.delay)
    p = {
      ...p,
      status: 'berjalan',
      routes: [live, ...p.routes.slice(1)],
      updatedAt: iso(nowMs - 2 * MIN),
      events: [
        ...p.events,
        ev(p.events.length + 101, createdMs + 6 * MIN, msg('srv.ops.seed.driverAssigned', { drivers: driver.name })),
        ev(p.events.length + 102, new Date(live.startedAt!).getTime(), msg('srv.ops.seed.departed')),
        ...live.stops
          .filter((s) => s.status === 'selesai')
          .map((s, k) =>
            ev(
              p.events.length + 110 + k,
              new Date(s.completedAt!).getTime(),
              msg(s.kind === 'ambil' ? 'srv.ops.event.pickupDone' : 'srv.ops.event.dropoffDone', { place: s.waypoint.name }),
            ),
          ),
      ],
    }
    driver.status = 'bertugas'
    busy.add(driver.id)
    plans.push(p)
    notify(`customer:${user.id}`, nowMs - 12 * MIN, msg('srv.ops.seed.onTheWayTitle'), msg('srv.ops.seed.onTheWayBody', { driver: driver.name, code: p.code }), p.id, true)
    if (r.delay) notify('admin', nowMs - 6 * MIN, msg('srv.ops.seed.delayTitle', { code: p.code }), msg('srv.ops.seed.delayBody', { driver: driver.name, count: r.delay }), p.id)
  })

  /* --- ditugaskan: satu motor dan satu blindvan --- */
  const assigned: { user: number; shipments: number; heavy: boolean; agoMin: number }[] = [
    { user: 4, shipments: 2, heavy: false, agoMin: 14 },
    { user: 6, shipments: 1, heavy: true, agoMin: 17 },
  ]
  assigned.forEach((a, i) => {
    const createdMs = nowMs - a.agoMin * MIN
    const user = users[a.user]!
    const { plan, chosen } = newSingleRoutePlan(ctx, { user, createdMs, preset: 'seimbang' }, () =>
      clusteredShipments(rng, `a${i}`, a.shipments, a.heavy),
    )
    let p = confirm(plan, chosen, createdMs + 2 * MIN)
    const { driver, vehicle } = pickDriver(ctx, p.routes[0]!.vehicleType, busy)
    const first = p.routes[0]!
    const near = offsetPoint(first.stops[0]!.waypoint.position, 1.6 + rng.next(), rng.next() * 360)
    driver.position = near
    const route = rebaseStart(assignRoute(first, driver, vehicle, nowMs - 4 * MIN), near, nowMs + 3 * MIN)
    p = {
      ...p,
      status: 'ditugaskan',
      routes: [route, ...p.routes.slice(1)],
      updatedAt: iso(nowMs - 4 * MIN),
      events: [...p.events, ev(p.events.length + 101, nowMs - 4 * MIN, msg('srv.ops.seed.driverAssigned', { drivers: driver.name }))],
    }
    driver.status = 'bertugas'
    busy.add(driver.id)
    plans.push(p)
    notify(`customer:${user.id}`, nowMs - 4 * MIN, msg('srv.ops.ntf.driverAssignedTitle'), msg('srv.ops.seed.assignedBody', { driver: driver.name, code: p.code }), p.id)
  })

  /* --- antrean menunggu driver (4), satu terjadwal --- */
  const queue: { user: number; shipments: number; preset: Preset; agoMin: number; scheduleInMin?: number }[] = [
    { user: 0, shipments: 3, preset: 'seimbang', agoMin: 2 },
    { user: 7, shipments: 2, preset: 'termurah', agoMin: 6 },
    { user: 8, shipments: 1, preset: 'tercepat', agoMin: 9 },
    { user: 9, shipments: 2, preset: 'seimbang', agoMin: 12, scheduleInMin: 100 },
  ]
  queue.forEach((q, i) => {
    const createdMs = nowMs - q.agoMin * MIN
    const user = users[q.user]!
    const { plan, chosen } = newPlan(ctx, {
      user,
      createdMs,
      shipments: randomShipments(rng, `q${i}`, q.shipments),
      preset: q.preset,
      ...(q.scheduleInMin ? { scheduledFromMs: nowMs + q.scheduleInMin * MIN } : {}),
    })
    const p = confirm(plan, chosen, createdMs + 1.5 * MIN)
    plans.push(p)
    notify('admin', createdMs + 2 * MIN, msg('srv.ops.seed.queueTitle', { code: p.code }), msg('srv.ops.seed.queueBody', { customer: user.name, count: p.routes.length }), p.id, q.agoMin > 8)
  })

  /* --- draft & opsi siap milik akun demo --- */
  {
    const n = ++ctx.planNo
    const from = waypointOf(rng, 'kemang-village')
    const to = waypointOf(rng, 'plaza-senayan')
    plans.push({
      id: `pln-${n}`,
      code: `PL-${2000 + n}`,
      userId: dina.id,
      status: 'draft',
      schedule: { mode: 'sekarang' },
      priority: { preset: 'seimbang' },
      shipments: [{ id: `d${n}-s1`, pickup: from, dropoff: to, items: [] }],
      routes: [],
      createdAt: iso(nowMs - 25 * MIN),
      updatedAt: iso(nowMs - 22 * MIN),
      events: [ev(n * 10 + 1, nowMs - 25 * MIN, msg('srv.ops.seed.draftCreated'))],
      draftStep: 2,
    })
  }
  {
    const { plan } = newPlan(ctx, {
      user: dina,
      createdMs: nowMs - 70 * MIN,
      shipments: randomShipments(rng, 'o1', 3),
      preset: 'seimbang',
    })
    plans.push({ ...plan, selectedOptionId: undefined, draftStep: 3, updatedAt: iso(nowMs - 65 * MIN) })
  }

  notify(`driver:${drivers[0]!.id}`, nowMs - 38 * MIN, msg('srv.ops.ntf.newJobTitle'), msg('srv.ops.seed.jobBody', { count: 2 }), plans.find((p) => p.status === 'berjalan')?.id, true)
  notify('admin', nowMs - 3 * HOUR, msg('srv.ops.seed.summaryTitle'), msg('srv.ops.seed.summaryBody', { count: 31, pct: 94 }), undefined, true)

  // Driver yang menganggur ditempatkan dekat titik ambil plan yang menunggu, satu driver per plan (sesuai jenis kendaraan),
  // dimulai dari plan yang harus berangkat sekarang dan paling lama menunggu. Hasilnya demo penugasan punya kandidat kuat
  // untuk antrean teratas; driver lain tetap tersebar di kota.
  const waiting = plans
    .filter((p) => p.status === 'menunggu_driver')
    .sort((a, b) => Number(a.schedule.mode === 'terjadwal') - Number(b.schedule.mode === 'terjadwal') || a.createdAt.localeCompare(b.createdAt))
  const idle = drivers.filter((x) => x.status === 'online')
  const placed = new Set<string>()
  for (const plan of waiting) {
    const route = plan.routes[0]
    if (!route) continue
    const d = idle.find((x) => !placed.has(x.id) && vehicles.find((v) => v.id === x.vehicleId)?.type === route.vehicleType)
    if (!d) continue
    placed.add(d.id)
    d.position = offsetPoint(route.stops[0]!.waypoint.position, 0.6 + rng.next() * 2.2, rng.next() * 360)
  }

  const wallet = seedWallet(users, plans, nowMs)

  return {
    version: DB_VERSION,
    rev: 1,
    scenario,
    seededAt: iso(nowMs),
    clock: { speed: 1, baseReal: nowMs, baseVirtual: nowMs },
    fault: 'none',
    autopilot: false,
    settings: structuredClone(DEFAULT_SETTINGS),
    counters: { plan: ctx.planNo, notification: notifNo, ...wallet.counters },
    users,
    credentials: { ...credentials, [ADMIN.id]: ADMIN.password, ...Object.fromEntries(drivers.map((d) => [d.id, DRIVER_PIN])) },
    drivers,
    vehicles,
    plans,
    notifications,
    balances: wallet.balances,
    walletTx: wallet.walletTx,
    topups: wallet.topups,
  }
}
