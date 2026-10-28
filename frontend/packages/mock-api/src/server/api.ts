import {
  ApiError,
  DEFAULT_EFFORT,
  driverEarningRp,
  isEffort,
  routePositionAt,
  virtualNowOf,
  type ActiveRouteLive,
  type ApiClient,
  type ComputeEvent,
  type DemoState,
  type Driver,
  type DriverEarnings,
  type DriverHistoryItem,
  type DriverRoute,
  type DriverSuggestion,
  type OptionSet,
  type Plan,
  type PlanSummary,
  type Route,
  type Session,
  type StopProof,
  type User,
  type UserDetail,
  type Vehicle,
} from '@loong/domain'
import { describePoint, nearestPlace, searchPlaces } from '@loong/gazetteer'
import { DEFAULT_LOCALE, msg, renderMsg, type Locale, type Msg } from '@loong/i18n'
import type { DB } from '../db'
import { analyzeProofPhoto, isProofPhoto } from '../ai/proof'
import { assertValid } from '../engine/model'
import { rebaseStart, routesFromOption } from '../engine/instantiate'
import type { RoadKm } from '../engine/route'
import { computeOptionSet, inputHash, searchOptionSet } from '../engine/solve'
import { matrixKm, uniquePoints } from '../roads/provider'
import { assignRoute, autoDispatchOn, dispatchPlan, pickDriver, suggestForRoute, tickDispatch, unassignRoute } from './dispatch'
import { attachRoadPaths } from './roads'
import { balanceOf, chargePlan, createFinanceApi, createWalletApi, refundPlan } from './wallet'
import { tAi } from '../i18n'
import { createRng } from '../rng'
import { ADMIN, DRIVER_PIN } from '../seed/people'
import { randomShipments, SCENARIO } from '../seed'
import { createAiApi, createSettingsApi } from './ai'
import { arriveStop, autopilotStep, closeStop, findPlan, findRoute, findStop, openIndex, syncPlanStatus, touchPlan } from './ops'
import { clone, sleep, type MockServer } from './server'
import { summarize, userSummary } from './summary'

/** PIN driver: tepat 6 digit (aturan yang sama dengan backend). */
const PIN_PATTERN = /^\d{6}$/

const MIN = 60_000
const iso = (ms: number) => new Date(ms).toISOString()
const digits = (s: string) => s.replace(/\D/g, '')
const normPhone = (s: string) => {
  const d = digits(s)
  return d.startsWith('62') ? `0${d.slice(2)}` : d
}

/** Jeda tiruan per catatan pencarian (ms): lebih pendek daripada backend asli, tetapi tetap terbaca di layar. */
const SEARCH_PAUSE_MS: Readonly<Record<string, number>> = {
  'srv.engine.search.experiments': 1100,
  'srv.engine.search.polish': 900,
  'srv.engine.search.round': 350,
  'srv.engine.search.reviewAI': 1000,
  'srv.engine.search.reviewRules': 1000,
  'srv.engine.search.satisfied': 350,
  'srv.engine.search.more': 450,
}

const unauthorized = () => new ApiError('ditolak', msg('srv.api.signInFirst'), { status: 401 })

function need<T>(value: T | undefined | null, message: Msg): T {
  if (value === undefined || value === null) throw new ApiError('tidak_ditemukan', message)
  return value
}

/**
 * Membuat ApiClient yang terikat pada satu pemanggil (`ctx` = sesi; null bila belum masuk).
 * Dalam produksi, `ctx` datang dari token di header; di sini dikirim oleh klien RPC.
 */
export function createApi(server: MockServer, ctx: Session | null, locale: Locale = DEFAULT_LOCALE): ApiClient {
  /** Teks sesaat (label progres, kata pengganti) dalam bahasa permintaan; server melayani banyak bahasa, jadi bukan `t` global. */
  const tx = tAi(locale)
  /* ----- pembungkus: latensi, kegagalan tiruan, salinan hasil ----- */
  async function run<T>(fn: (db: DB) => T): Promise<T> {
    await server.delay()
    const fault = server.db().fault
    if (fault === 'offline') throw new ApiError('jaringan', msg('srv.api.offline'))
    if (fault === 'error') throw new ApiError('layanan', msg('srv.api.serviceError'))
    if (fault === 'slow') await sleep(2600)
    return clone(fn(server.db()))
  }
  /** Seperti run, tetapi tidak terpengaruh kegagalan tiruan (kontrol Panel Demo). */
  async function runDemo<T>(fn: () => T): Promise<T> {
    return clone(fn())
  }
  const emptyIfFault = <T>(db: DB, rows: T[]): T[] => (db.fault === 'empty' ? [] : rows)

  const requireRole = (...roles: Session['role'][]): Session => {
    if (!ctx) throw unauthorized()
    if (!roles.includes(ctx.role)) throw new ApiError('ditolak', msg('srv.api.roleDenied'))
    return ctx
  }

  const audienceOf = (): `customer:${string}` | 'admin' | `driver:${string}` => {
    const s = requireRole('customer', 'admin', 'driver')
    return s.role === 'admin' ? 'admin' : s.role === 'driver' ? `driver:${s.userId}` : `customer:${s.userId}`
  }

  /** Plan yang boleh dibaca pemanggil. */
  const readablePlan = (db: DB, id: string): Plan => {
    const s = requireRole('customer', 'admin', 'driver')
    const plan = findPlan(db, id)
    if (s.role === 'admin') return plan
    if (s.role === 'customer' && plan.userId === s.userId) return plan
    if (s.role === 'driver' && plan.routes.some((r) => r.driverId === s.userId)) return plan
    throw new ApiError('ditolak', msg('srv.api.planNoAccess'))
  }

  const ownDraft = (db: DB, id: string): Plan => {
    const s = requireRole('customer')
    const plan = findPlan(db, id)
    if (plan.userId !== s.userId) throw new ApiError('ditolak', msg('srv.api.planNoAccess'))
    return plan
  }

  const changed = (planId?: string) => server.emit({ type: 'plans.changed', planId })

  /* ----- agregat dan kecocokan driver (mesin penugasan ada di dispatch.ts) ----- */
  const suggestFor = (db: DB, plan: Plan, route: Route): DriverSuggestion[] => suggestForRoute(server, db, plan, route)
  const assignInternal = (db: DB, plan: Plan, route: Route, driver: Driver) => assignRoute(server, db, plan, route, driver, { mode: 'manual' })
  const unassignInternal = (db: DB, plan: Plan, route: Route) => unassignRoute(server, db, plan, route)

  /* =====================================================================================
   * auth
   * ===================================================================================== */
  const auth: ApiClient['auth'] = {
    session: () => run(() => ctx),
    loginCustomer: (email, password) =>
      run((db) => {
        const user = db.users.find((u) => u.email.toLowerCase() === email.trim().toLowerCase())
        if (!user || db.credentials[user.id] !== password) {
          throw new ApiError('tidak_valid', msg('srv.api.badCredentials'))
        }
        if (user.blocked) throw new ApiError('ditolak', msg('srv.api.accountBlocked'))
        return { role: 'customer', userId: user.id, name: user.name } satisfies Session
      }),
    register: (input) =>
      run((db) => {
        const email = input.email.trim().toLowerCase()
        if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) throw new ApiError('tidak_valid', msg('srv.api.emailInvalid'), { field: 'email' })
        if (db.users.some((u) => u.email.toLowerCase() === email)) {
          throw new ApiError('bentrok', msg('srv.api.emailTaken'), { field: 'email' })
        }
        if (normPhone(input.phone).length < 9) throw new ApiError('tidak_valid', msg('srv.api.phoneIncomplete'), { field: 'phone' })
        if (input.password.length < 8) throw new ApiError('tidak_valid', msg('srv.api.passwordShort'), { field: 'password' })
        const user = server.mutate((d) => {
          const u: User = {
            id: `usr-${d.users.length + 1}-${server.now().toString(36)}`,
            name: input.name.trim(),
            email,
            phone: normPhone(input.phone),
            createdAt: iso(server.now()),
            blocked: false,
            savedAddresses: [],
          }
          d.users.push(u)
          d.credentials[u.id] = input.password
          server.notify(d, 'admin', msg('srv.api.notif.newUser.title'), msg('srv.api.notif.newUser.body', { name: u.name }))
          return u
        })
        server.emit({ type: 'users.changed' })
        server.emit({ type: 'notifications.changed' })
        return { role: 'customer', userId: user.id, name: user.name } satisfies Session
      }),
    loginAdmin: (email, password) =>
      run(() => {
        if (email.trim().toLowerCase() !== ADMIN.email || password !== ADMIN.password) {
          throw new ApiError('tidak_valid', msg('srv.api.badCredentials'))
        }
        return { role: 'admin', userId: ADMIN.id, name: ADMIN.name } satisfies Session
      }),
    loginDriver: (phone, pin) =>
      run((db) => {
        const driver = db.drivers.find((d) => normPhone(d.phone) === normPhone(phone))
        if (!driver || pin !== (db.credentials[driver.id] ?? DRIVER_PIN)) throw new ApiError('tidak_valid', msg('srv.api.driverBadCredentials'))
        return { role: 'driver', userId: driver.id, name: driver.name } satisfies Session
      }),
    logout: () => run(() => undefined),
  }

  /* =====================================================================================
   * lokasi
   * ===================================================================================== */
  const places: ApiClient['places'] = {
    search: (query, limit = 8) => run(() => searchPlaces(query, limit)),
    describe: (position) =>
      run(() => {
        const { place, distanceKm } = nearestPlace(position)
        if (distanceKm < 0.12) return { name: place.name, address: place.address, placeId: place.id }
        return describePoint(position, locale)
      }),
  }

  /* =====================================================================================
   * akun & notifikasi
   * ===================================================================================== */
  const customerRecord = (db: DB): User => {
    const s = requireRole('customer')
    return need(
      db.users.find((u) => u.id === s.userId),
      msg('srv.api.userNotFound'),
    )
  }

  const account: ApiClient['account'] = {
    profile: () => run((db) => customerRecord(db)),
    update: (patch) =>
      run(() => {
        const s = requireRole('customer')
        const user = server.mutate((db) => {
          const u = need(db.users.find((x) => x.id === s.userId), msg('srv.api.userNotFound'))
          if (patch.email) {
            const email = patch.email.trim().toLowerCase()
            if (db.users.some((x) => x.id !== u.id && x.email.toLowerCase() === email)) {
              throw new ApiError('bentrok', msg('srv.api.emailUsed'), { field: 'email' })
            }
            u.email = email
          }
          if (patch.name !== undefined) {
            if (!patch.name.trim()) throw new ApiError('tidak_valid', msg('srv.api.nameEmpty'), { field: 'name' })
            u.name = patch.name.trim()
          }
          if (patch.phone !== undefined) {
            if (normPhone(patch.phone).length < 9) throw new ApiError('tidak_valid', msg('srv.api.phoneIncomplete'), { field: 'phone' })
            u.phone = normPhone(patch.phone)
          }
          return u
        })
        server.emit({ type: 'users.changed' })
        return user
      }),
    addresses: {
      add: (input) =>
        run(() => {
          const s = requireRole('customer')
          if (!input.label.trim()) throw new ApiError('tidak_valid', msg('srv.api.addressLabelRequired'), { field: 'label' })
          const created = server.mutate((db) => {
            const u = need(db.users.find((x) => x.id === s.userId), msg('srv.api.userNotFound'))
            const adr = { ...input, id: `adr-${u.savedAddresses.length + 1}-${server.now().toString(36)}` }
            u.savedAddresses.push(adr)
            return adr
          })
          server.emit({ type: 'users.changed' })
          return created
        }),
      update: (id, patch) =>
        run(() => {
          const s = requireRole('customer')
          const updated = server.mutate((db) => {
            const u = need(db.users.find((x) => x.id === s.userId), msg('srv.api.userNotFound'))
            const adr = need(u.savedAddresses.find((a) => a.id === id), msg('srv.api.addressNotFound'))
            Object.assign(adr, patch)
            return adr
          })
          server.emit({ type: 'users.changed' })
          return updated
        }),
      remove: (id) =>
        run(() => {
          const s = requireRole('customer')
          server.mutate((db) => {
            const u = need(db.users.find((x) => x.id === s.userId), msg('srv.api.userNotFound'))
            u.savedAddresses = u.savedAddresses.filter((a) => a.id !== id)
          })
          server.emit({ type: 'users.changed' })
        }),
    },
  }

  const notifications: ApiClient['notifications'] = {
    list: () =>
      run((db) => {
        const aud = audienceOf()
        return emptyIfFault(
          db,
          db.notifications
            .filter((n) => n.audience === aud)
            .sort((a, b) => b.at.localeCompare(a.at))
            .slice(0, 50),
        )
      }),
    markRead: (ids) =>
      run(() => {
        const aud = audienceOf()
        server.mutate((db) => {
          for (const n of db.notifications) if (n.audience === aud && (!ids || ids.includes(n.id))) n.read = true
        })
        server.emit({ type: 'notifications.changed' })
      }),
  }

  /* =====================================================================================
   * plan
   * ===================================================================================== */
  const plans: ApiClient['plans'] = {
    list: (filter = {}) =>
      run((db) => {
        const s = requireRole('customer', 'admin', 'driver')
        let rows = db.plans.filter((p) => {
          if (s.role === 'customer') return p.userId === s.userId
          if (s.role === 'driver') return p.routes.some((r) => r.driverId === s.userId)
          return p.status !== 'draft' && p.status !== 'opsi_siap'
        })
        if (filter.userId) rows = rows.filter((p) => p.userId === filter.userId)
        if (filter.status?.length) rows = rows.filter((p) => filter.status!.includes(p.status))
        if (filter.from) rows = rows.filter((p) => p.createdAt >= filter.from!)
        if (filter.to) rows = rows.filter((p) => p.createdAt <= filter.to!)
        let summaries: PlanSummary[] = rows.map((p) => summarize(p, db, locale))
        if (filter.needsDriver) summaries = summaries.filter((p) => p.routesWithoutDriver > 0)
        if (filter.vehicleType) summaries = summaries.filter((p) => p.vehicleTypes.includes(filter.vehicleType!))
        if (filter.q?.trim()) {
          const q = filter.q.trim().toLowerCase()
          summaries = summaries.filter((p) =>
            [p.code, p.customerName, p.fromLabel, p.toLabel, ...p.driverNames].some((t) => t.toLowerCase().includes(q)),
          )
        }
        summaries.sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
        return emptyIfFault(db, summaries)
      }),

    get: (id) => run((db) => readablePlan(db, id)),

    createDraft: () =>
      run(() => {
        const s = requireRole('customer')
        const plan = server.mutate((db) => {
          db.counters.plan += 1
          const n = db.counters.plan
          const nowMs = server.now()
          const p: Plan = {
            id: `pln-${n}`,
            code: `PL-${2000 + n}`,
            userId: s.userId,
            status: 'draft',
            schedule: { mode: 'sekarang' },
            priority: { preset: 'seimbang' },
            shipments: [],
            routes: [],
            createdAt: iso(nowMs),
            updatedAt: iso(nowMs),
            events: [{ id: `evt-1-${nowMs.toString(36)}`, at: iso(nowMs), text: msg('srv.api.event.draftCreated') }],
            draftStep: 1,
          }
          db.plans.push(p)
          return p
        })
        changed(plan.id)
        return plan
      }),

    saveDraft: (id, patch) =>
      run(() => {
        const plan = server.mutate((db) => {
          const p = ownDraft(db, id)
          if (p.status !== 'draft' && p.status !== 'opsi_siap') {
            throw new ApiError('bentrok', msg('srv.api.planLocked'))
          }
          if (patch.shipments) p.shipments = patch.shipments
          if (patch.schedule) p.schedule = patch.schedule
          if (patch.priority) p.priority = patch.priority
          if (patch.draftStep !== undefined) p.draftStep = patch.draftStep
          // opsi lama basi bila masukan berubah
          if (p.optionSet && p.optionSet.inputHash !== inputHash(p)) {
            delete p.optionSet
            delete p.selectedOptionId
            p.status = 'draft'
          }
          touchPlan(p, server.now())
          return p
        })
        changed(plan.id)
        return plan
      }),

    computeOptions: (id, opts) =>
      (async function* (): AsyncGenerator<ComputeEvent> {
        const fault = server.db().fault
        if (fault === 'offline') throw new ApiError('jaringan', msg('srv.api.offline'))
        if (fault === 'error') throw new ApiError('layanan', msg('srv.api.routeEngineError'))
        const effort = opts?.effort ?? DEFAULT_EFFORT
        if (!isEffort(effort)) throw new ApiError('tidak_valid', msg('srv.api.badEffort'), { field: 'effort' })
        const db0 = server.db()
        const plan0 = ownDraft(db0, id)
        if (plan0.status !== 'draft' && plan0.status !== 'opsi_siap') {
          throw new ApiError('bentrok', msg('srv.api.planAlreadyConfirmed'))
        }
        assertValid(plan0.shipments)
        const n = plan0.shipments.length
        const pointCount = n * 2
        const roads = server.options.roads
        const total = roads ? 5 : 4
        let step = 0
        const progress = (label: string): ComputeEvent => ({ type: 'progress', step: (step += 1), total, label })
        const pause = (ms: number) => (server.options.latency[1] > 0 ? sleep(ms) : Promise.resolve())

        yield progress(tx('ai.compute.readPoints', { count: pointCount }))
        await pause(450)

        // jarak antar titik: lewat jalan sungguhan bila ada penyedia, selain itu perkiraan; engine tetap milik kita
        yield progress(roads ? tx('ai.compute.measureRoads') : tx('ai.compute.estimateDistances'))
        let km: RoadKm | undefined
        if (roads) {
          const points = uniquePoints(plan0.shipments.flatMap((s) => [s.pickup.position, s.dropoff.position]))
          const [matrix] = await Promise.all([roads.matrix(points), pause(750)])
          if (matrix) km = matrixKm(points, matrix)
        } else {
          await pause(750)
        }

        yield progress(tx('ai.compute.combine', { count: n }))
        await pause(effort === 'fast' ? 800 : 400)

        // "thorough" dan "reviewed": pencarian yang lebih keras, dengan catatan di dalam tahap 3 (step yang sama).
        // Jedanya lebih pendek daripada backend asli, tetapi hitungannya nyata (lihat searchOptionSet).
        let searched: OptionSet | undefined
        if (effort !== 'fast') {
          const latest = ownDraft(server.db(), id)
          const search = searchOptionSet(
            { planId: latest.id, shipments: latest.shipments, schedule: latest.schedule, priority: latest.priority, vehicles: server.db().vehicles, nowMs: server.now(), km },
            effort,
          )
          for (;;) {
            const next = search.next()
            if (next.done) {
              searched = next.value
              break
            }
            yield { type: 'progress', step, total, label: renderMsg(next.value, locale), msg: next.value }
            await pause(SEARCH_PAUSE_MS[next.value.key] ?? 400)
          }
        }

        yield progress(tx('ai.compute.compare'))
        await pause(550)

        const computed = server.mutate((db) => {
          const p = ownDraft(db, id)
          let set: OptionSet
          if (searched) {
            // masukan berubah selagi mencari: hasil ini sudah basi
            if (searched.inputHash !== inputHash(p)) throw new ApiError('bentrok', msg('srv.api.optionsStale'))
            set = searched
          } else {
            set = computeOptionSet(
              {
                planId: p.id,
                shipments: p.shipments,
                schedule: p.schedule,
                priority: p.priority,
                vehicles: db.vehicles,
                nowMs: server.now(),
                km,
              },
              { effort },
            )
          }
          if (set.method) set.method.roadSource = km ? 'mapbox' : 'estimate'
          p.optionSet = set
          delete p.selectedOptionId
          p.status = 'opsi_siap'
          p.draftStep = Math.max(p.draftStep, 3)
          touchPlan(p, server.now())
          return set
        })

        // jalur jalan untuk digambar; gagal = tetap memakai garis halus, opsi dan angkanya tidak berubah
        let optionSet = computed
        if (roads) {
          yield progress(tx('ai.compute.drawRoutes'))
          const drawn = clone(computed)
          if ((await attachRoadPaths(drawn, roads)) > 0) {
            optionSet = server.mutate((db) => {
              const p = db.plans.find((x) => x.id === id)
              if (p?.optionSet?.inputHash === drawn.inputHash) p.optionSet = drawn
              return drawn
            })
          }
        }
        changed(id)
        yield { type: 'done', optionSet: clone(optionSet) }
      })(),

    chooseOption: (id, optionId) =>
      run(() => {
        const plan = server.mutate((db) => {
          const p = ownDraft(db, id)
          const opt = p.optionSet?.options.find((o) => o.id === optionId)
          if (!opt) throw new ApiError('tidak_valid', msg('srv.api.optionNotFound'), { field: 'optionId' })
          p.selectedOptionId = optionId
          touchPlan(p, server.now())
          return p
        })
        changed(id)
        return plan
      }),

    confirm: (id, paymentMethod) =>
      run(() => {
        const plan = server.mutate((db) => {
          const p = ownDraft(db, id)
          if (p.status !== 'opsi_siap') throw new ApiError('bentrok', msg('srv.api.planNotReady'))
          const set = p.optionSet
          const opt = set?.options.find((o) => o.id === p.selectedOptionId)
          if (!set || !opt) throw new ApiError('tidak_valid', msg('srv.api.pickOption'), { field: 'optionId' })
          if (set.inputHash !== inputHash(p)) throw new ApiError('bentrok', msg('srv.api.optionsStale'))
          const user = need(db.users.find((u) => u.id === p.userId), msg('srv.api.userNotFound'))
          const nowMs = server.now()
          // bayar dulu: saldo kurang melempar galat sebelum plan berubah apa pun
          if (paymentMethod === 'saldo') chargePlan(db, p, opt.totals.costRp, nowMs)
          p.routes = routesFromOption(p.id, opt)
          p.status = 'menunggu_driver'
          p.paymentMethod = paymentMethod
          p.totalCostRp = opt.totals.costRp
          p.confirmedAt = iso(nowMs)
          p.draftStep = 4
          touchPlan(p, nowMs, msg('srv.api.event.confirmed', { amount: opt.totals.costRp, option: opt.label }))
          server.notify(db, `customer:${p.userId}`, msg('srv.api.notif.confirmed.title'), msg('srv.api.notif.confirmed.body', { code: p.code }), p.id)
          // penugasan otomatis: sekarang -> driver terdekat yang tersedia; terjadwal -> driver yang kosong pada jamnya
          if (autoDispatchOn(db)) dispatchPlan(server, db, p)
          if (p.routes.some((r) => !r.driverId)) server.notify(db, 'admin', msg('srv.api.notif.newPlan.title', { code: p.code }), msg('srv.api.notif.newPlan.body', { name: user.name, count: p.routes.length }), p.id)
          return p
        })
        changed(id)
        server.emit({ type: 'fleet.changed' })
        server.emit({ type: 'wallet.changed' })
        server.emit({ type: 'notifications.changed' })
        return plan
      }),

    cancel: (id, reason) =>
      run(() => {
        const s = requireRole('customer', 'admin')
        const plan = server.mutate((db) => {
          const p = s.role === 'admin' ? findPlan(db, id) : ownDraft(db, id)
          if (p.status === 'berjalan') throw new ApiError('bentrok', msg('srv.api.cancelRunning'))
          if (p.status === 'selesai' || p.status === 'dibatalkan') throw new ApiError('bentrok', msg('srv.api.planInactive'))
          const nowMs = server.now()
          for (const r of p.routes) {
            const d = db.drivers.find((x) => x.id === r.driverId)
            if (d && d.status === 'bertugas') {
              d.status = 'online'
              server.notify(
                db,
                `driver:${d.id}`,
                msg('srv.api.notif.taskCancelled.title'),
                reason ? msg('srv.api.notif.taskCancelled.bodyReason', { code: p.code, reason }) : msg('srv.api.notif.taskCancelled.body', { code: p.code }),
                p.id,
              )
            }
          }
          p.status = 'dibatalkan'
          // alasan bawaan disimpan sebagai teks (`cancelReason` bertipe string) dalam bahasa permintaan
          p.cancelReason = reason?.trim() || tx(s.role === 'admin' ? 'srv.api.cancelledByAdmin' : 'srv.api.cancelledBySender')
          touchPlan(p, nowMs, reason ? msg('srv.api.event.cancelledReason', { reason }) : msg('srv.api.event.cancelled'))
          if (p.confirmedAt) server.notify(db, 'admin', msg('srv.api.notif.cancelled.title', { code: p.code }), msg('srv.api.notif.cancelled.body', { name: s.name }), p.id)
          if (p.paymentMethod === 'saldo') refundPlan(db, p, nowMs)
          return p
        })
        changed(id)
        server.emit({ type: 'wallet.changed' })
        server.emit({ type: 'fleet.changed' })
        server.emit({ type: 'notifications.changed' })
        return plan
      }),

    reschedule: (id, schedule) =>
      run(() => {
        const plan = server.mutate((db) => {
          const p = ownDraft(db, id)
          if (p.status !== 'menunggu_driver' && p.status !== 'ditugaskan') {
            throw new ApiError('bentrok', msg('srv.api.rescheduleLate'))
          }
          if (schedule.mode === 'terjadwal') {
            const from = schedule.window ? new Date(schedule.window.from).getTime() : NaN
            if (!Number.isFinite(from) || from < server.now() + 20 * MIN) {
              throw new ApiError('tidak_valid', msg('srv.api.scheduleTooSoon'), { field: 'schedule' })
            }
          }
          p.schedule = schedule
          const nowMs = server.now()
          const startMs = schedule.mode === 'terjadwal' ? new Date(schedule.window!.from).getTime() : nowMs + 6 * MIN
          p.routes = p.routes.map((r) => {
            const driver = db.drivers.find((d) => d.id === r.driverId)
            return { ...r, ...rebaseStart(r, driver?.position ?? r.startPosition, startMs) }
          })
          touchPlan(p, nowMs, schedule.mode === 'terjadwal' ? msg('srv.api.event.rescheduled', { time: startMs }) : msg('srv.api.event.rescheduledNow'))
          return p
        })
        changed(id)
        return plan
      }),

    remove: (id) =>
      run(() => {
        server.mutate((db) => {
          const p = ownDraft(db, id)
          if (p.status !== 'draft' && p.status !== 'opsi_siap') throw new ApiError('bentrok', msg('srv.api.draftOnlyDelete'))
          db.plans = db.plans.filter((x) => x.id !== id)
        })
        changed(id)
      }),
  }

  /* =====================================================================================
   * armada
   * ===================================================================================== */
  const validateVehicle = (db: DB, input: Partial<Vehicle>, selfId?: string) => {
    if (input.plate !== undefined) {
      const plate = input.plate.trim().toUpperCase().replace(/\s+/g, ' ')
      if (!/^[A-Z]{1,2} \d{1,4} [A-Z]{1,3}$/.test(plate)) {
        throw new ApiError('tidak_valid', msg('srv.api.plateInvalid'), { field: 'plate' })
      }
      if (db.vehicles.some((v) => v.id !== selfId && v.plate === plate)) {
        throw new ApiError('bentrok', msg('srv.api.plateTaken', { plate }), { field: 'plate' })
      }
      input.plate = plate
    }
    for (const [key, label] of [
      ['maxWeightKg', msg('srv.api.field.maxWeightKg')],
      ['maxVolumeM3', msg('srv.api.field.maxVolumeM3')],
      ['maxDimensionCm', msg('srv.api.field.maxDimensionCm')],
    ] as const) {
      if (input[key] !== undefined && !(input[key]! > 0)) throw new ApiError('tidak_valid', msg('srv.api.mustBePositive', { field: label }), { field: key })
    }
    if (input.model !== undefined && !input.model.trim()) throw new ApiError('tidak_valid', msg('srv.api.modelRequired'), { field: 'model' })
    if (input.fare) {
      for (const [k, v] of Object.entries(input.fare)) {
        if (!(v >= 0)) throw new ApiError('tidak_valid', msg('srv.api.fareNegative'), { field: `fare.${k}` })
      }
    }
  }

  const fleet: ApiClient['fleet'] = {
    vehicles: {
      list: () => run((db) => emptyIfFault(db, [...db.vehicles])),
      create: (input) =>
        run(() => {
          requireRole('admin')
          const v = server.mutate((db) => {
            const data = { ...input }
            validateVehicle(db, data)
            const vehicle: Vehicle = { ...data, id: `veh-${db.vehicles.length + 1}-${server.now().toString(36)}` }
            db.vehicles.push(vehicle)
            return vehicle
          })
          server.emit({ type: 'fleet.changed' })
          return v
        }),
      update: (id, patch) =>
        run(() => {
          requireRole('admin')
          const v = server.mutate((db) => {
            const vehicle = need(db.vehicles.find((x) => x.id === id), msg('srv.api.vehicleNotFound'))
            const data = { ...patch }
            validateVehicle(db, data, id)
            const inUse = db.plans.flatMap((p) => p.routes).some((r) => r.vehicleId === id && (r.status === 'ditugaskan' || r.status === 'berjalan'))
            if (inUse && (data.status === 'servis' || data.status === 'nonaktif' || (data.type && data.type !== vehicle.type))) {
              throw new ApiError('bentrok', msg('srv.api.vehicleInUseChange'), { field: 'status' })
            }
            Object.assign(vehicle, data)
            return vehicle
          })
          server.emit({ type: 'fleet.changed' })
          return v
        }),
      remove: (id) =>
        run(() => {
          requireRole('admin')
          server.mutate((db) => {
            const vehicle = need(db.vehicles.find((x) => x.id === id), msg('srv.api.vehicleNotFound'))
            const inUse = db.plans.flatMap((p) => p.routes).some((r) => r.vehicleId === id && (r.status === 'ditugaskan' || r.status === 'berjalan'))
            if (inUse) throw new ApiError('bentrok', msg('srv.api.vehicleInUse', { plate: vehicle.plate }))
            for (const d of db.drivers) if (d.vehicleId === id) delete d.vehicleId
            db.vehicles = db.vehicles.filter((x) => x.id !== id)
          })
          server.emit({ type: 'fleet.changed' })
        }),
    },
    drivers: {
      list: () => run((db) => emptyIfFault(db, [...db.drivers])),
      create: (input) =>
        run(() => {
          requireRole('admin')
          const d = server.mutate((db) => {
            if (!input.name.trim()) throw new ApiError('tidak_valid', msg('srv.api.driverNameRequired'), { field: 'name' })
            const phone = normPhone(input.phone)
            if (phone.length < 9) throw new ApiError('tidak_valid', msg('srv.api.phoneIncomplete'), { field: 'phone' })
            if (db.drivers.some((x) => normPhone(x.phone) === phone)) {
              throw new ApiError('bentrok', msg('srv.api.phoneTakenDriver'), { field: 'phone' })
            }
            if (input.pin !== undefined && !PIN_PATTERN.test(input.pin)) throw new ApiError('tidak_valid', msg('srv.api.pinInvalid'), { field: 'pin' })
            if (input.vehicleId && db.drivers.some((x) => x.vehicleId === input.vehicleId)) {
              throw new ApiError('bentrok', msg('srv.api.vehicleTaken'), { field: 'vehicleId' })
            }
            const driver: Driver = {
              id: `drv-${db.drivers.length + 1}-${server.now().toString(36)}`,
              name: input.name.trim(),
              phone,
              rating: 5,
              vehicleId: input.vehicleId,
              status: 'offline',
              joinedAt: iso(server.now()),
              completedTrips: 0,
            }
            db.drivers.push(driver)
            db.credentials[driver.id] = input.pin ?? DRIVER_PIN
            if (input.vehicleId) {
              const v = db.vehicles.find((x) => x.id === input.vehicleId)
              if (v) v.driverId = driver.id
            }
            return driver
          })
          server.emit({ type: 'fleet.changed' })
          return d
        }),
      update: (id, patch) =>
        run(() => {
          requireRole('admin')
          const d = server.mutate((db) => {
            const driver = need(db.drivers.find((x) => x.id === id), msg('srv.api.driverNotFound'))
            if (patch.name !== undefined) {
              if (!patch.name.trim()) throw new ApiError('tidak_valid', msg('srv.api.driverNameRequired'), { field: 'name' })
              driver.name = patch.name.trim()
            }
            if (patch.phone !== undefined) {
              const phone = normPhone(patch.phone)
              if (phone.length < 9) throw new ApiError('tidak_valid', msg('srv.api.phoneIncomplete'), { field: 'phone' })
              if (db.drivers.some((x) => x.id !== id && normPhone(x.phone) === phone)) {
                throw new ApiError('bentrok', msg('srv.api.phoneUsedDriver'), { field: 'phone' })
              }
              driver.phone = phone
            }
            if (patch.pin) {
              if (!PIN_PATTERN.test(patch.pin)) throw new ApiError('tidak_valid', msg('srv.api.pinInvalid'), { field: 'pin' })
              db.credentials[id] = patch.pin
            }
            if (patch.vehicleId !== undefined) {
              if (driver.status === 'bertugas') throw new ApiError('bentrok', msg('srv.api.driverOnDutyVehicle'), { field: 'vehicleId' })
              if (patch.vehicleId && db.drivers.some((x) => x.id !== id && x.vehicleId === patch.vehicleId)) {
                throw new ApiError('bentrok', msg('srv.api.vehicleTaken'), { field: 'vehicleId' })
              }
              for (const v of db.vehicles) if (v.driverId === id) delete v.driverId
              driver.vehicleId = patch.vehicleId || undefined
              const nv = db.vehicles.find((x) => x.id === patch.vehicleId)
              if (nv) nv.driverId = id
            }
            if (patch.status !== undefined) {
              if (driver.status === 'bertugas' || patch.status === 'bertugas') throw new ApiError('bentrok', msg('srv.api.statusAuto'), { field: 'status' })
              driver.status = patch.status
            }
            return driver
          })
          server.emit({ type: 'fleet.changed' })
          return d
        }),
    },
  }

  /* =====================================================================================
   * pengguna (admin)
   * ===================================================================================== */
  const users: ApiClient['users'] = {
    list: (q) =>
      run((db) => {
        requireRole('admin')
        let rows = db.users.map((u) => userSummary(u, db))
        if (q?.trim()) {
          const needle = q.trim().toLowerCase()
          rows = rows.filter((u) => [u.name, u.email, u.phone].some((t) => t.toLowerCase().includes(needle)))
        }
        return emptyIfFault(db, rows.sort((a, b) => b.totalSpendRp - a.totalSpendRp))
      }),
    get: (id) =>
      run((db): UserDetail => {
        requireRole('admin')
        const user = need(db.users.find((u) => u.id === id), msg('srv.api.userNotFound'))
        return {
          user,
          summary: userSummary(user, db),
          plans: db.plans
            .filter((p) => p.userId === id && p.status !== 'draft' && p.status !== 'opsi_siap')
            .map((p) => summarize(p, db, locale))
            .sort((a, b) => b.createdAt.localeCompare(a.createdAt)),
        }
      }),
    setBlocked: (id, blocked) =>
      run(() => {
        requireRole('admin')
        const summary = server.mutate((db) => {
          const user = need(db.users.find((u) => u.id === id), msg('srv.api.userNotFound'))
          user.blocked = blocked
          return userSummary(user, db)
        })
        server.emit({ type: 'users.changed' })
        return summary
      }),
  }

  /* =====================================================================================
   * dispatch (admin)
   * ===================================================================================== */
  const dispatch: ApiClient['dispatch'] = {
    suggest: (planId, routeId) =>
      run((db) => {
        requireRole('admin')
        const plan = findPlan(db, planId)
        const route = findRoute(plan, routeId)
        return suggestFor(db, plan, route)
      }),
    assign: (planId, routeId, driverId) =>
      run(() => {
        requireRole('admin')
        const plan = server.mutate((db) => {
          const p = findPlan(db, planId)
          const r = findRoute(p, routeId)
          const d = need(db.drivers.find((x) => x.id === driverId), msg('srv.api.driverNotFound'))
          assignInternal(db, p, r, d)
          return p
        })
        changed(planId)
        server.emit({ type: 'fleet.changed' })
        server.emit({ type: 'notifications.changed' })
        return plan
      }),
    unassign: (planId, routeId) =>
      run(() => {
        requireRole('admin')
        const plan = server.mutate((db) => {
          const p = findPlan(db, planId)
          unassignInternal(db, p, findRoute(p, routeId))
          return p
        })
        changed(planId)
        server.emit({ type: 'fleet.changed' })
        return plan
      }),
    autoAssign: (planId) =>
      run(() => {
        requireRole('admin')
        const plan = server.mutate((db) => {
          const p = findPlan(db, planId)
          const open = p.routes.filter((r) => r.status === 'menunggu_driver')
          if (open.length === 0) throw new ApiError('bentrok', msg('srv.api.allRoutesHaveDriver'))
          for (const route of open) {
            const pick = pickDriver(server, db, p, route)
            if (!pick) {
              throw new ApiError('bentrok', msg('srv.api.noDriverForRoute', { vehicle: msg(`srv.api.vehicleLower.${route.vehicleType}`), route: route.id.split(':r')[1] ?? '' }))
            }
            assignRoute(server, db, p, route, pick.driver, { mode: 'otomatis', reason: pick.reasons[0] })
          }
          return p
        })
        changed(planId)
        server.emit({ type: 'fleet.changed' })
        server.emit({ type: 'notifications.changed' })
        return plan
      }),
  }

  /* =====================================================================================
   * driver
   * ===================================================================================== */
  const me = (db: DB): Driver => {
    const s = requireRole('driver')
    return need(db.drivers.find((d) => d.id === s.userId), msg('srv.api.driverNotFound'))
  }

  const myRoute = (db: DB, planId: string, routeId: string) => {
    const driver = me(db)
    const plan = findPlan(db, planId)
    const route = findRoute(plan, routeId)
    if (route.driverId !== driver.id) throw new ApiError('ditolak', msg('srv.api.notYourRoute'))
    return { driver, plan, route }
  }

  const historyItems = (db: DB, driverId: string): DriverHistoryItem[] =>
    db.plans
      .flatMap((p) => p.routes.filter((r) => r.driverId === driverId && r.status === 'selesai').map((r) => ({ p, r })))
      .map(({ p, r }) => ({
        planId: p.id,
        planCode: p.code,
        routeId: r.id,
        finishedAt: r.finishedAt ?? p.updatedAt,
        stops: r.stops.length,
        distanceKm: r.distanceKm,
        durationMin: r.durationMin,
        earningsRp: driverEarningRp(r.costRp),
      }))
      .sort((a, b) => b.finishedAt.localeCompare(a.finishedAt))

  const driver: ApiClient['driver'] = {
    me: () =>
      run((db) => {
        const d = me(db)
        return { driver: d, vehicle: db.vehicles.find((v) => v.id === d.vehicleId) }
      }),
    setOnline: (online) =>
      run(() => {
        const result = server.mutate((db) => {
          const d = me(db)
          if (d.status === 'bertugas') throw new ApiError('bentrok', msg('srv.api.finishActiveFirst'))
          if (online && !d.vehicleId) throw new ApiError('bentrok', msg('srv.api.noVehicle'))
          // offline: tugas yang belum dimulai dikembalikan ke antrean (dan ditugaskan ulang ke driver lain)
          const released: string[] = []
          if (!online) {
            for (const p of db.plans) {
              for (const r of p.routes) {
                if (r.driverId !== d.id || r.status !== 'ditugaskan') continue
                unassignRoute(server, db, p, r, msg('srv.api.event.driverOfflineReleased', { name: d.name }))
                released.push(p.id)
              }
            }
            if (released.length) server.notify(db, 'admin', msg('srv.api.notif.released.title'), msg('srv.api.notif.released.body', { name: d.name, count: released.length }))
          }
          d.status = online ? 'online' : 'offline'
          d.lastSeen = server.nowIso()
          for (const id of new Set(released)) server.emit({ type: 'plans.changed', planId: id })
          return d
        })
        server.emit({ type: 'fleet.changed' })
        // driver yang baru online langsung dipertimbangkan untuk pesanan yang menunggu
        if (online) tickDispatch(server)
        return result
      }),
    routes: () =>
      run((db): DriverRoute[] => {
        const d = me(db)
        return emptyIfFault(
          db,
          db.plans.flatMap((p) =>
            p.routes
              .filter((r) => r.driverId === d.id && (r.status === 'ditugaskan' || r.status === 'berjalan'))
              .map((route) => ({
                planId: p.id,
                planCode: p.code,
                customerName: db.users.find((u) => u.id === p.userId)?.name ?? tx('srv.api.userFallback'),
                route,
              })),
          ),
        )
      }),
    startRoute: (planId, routeId) =>
      run(() => {
        const plan = server.mutate((db) => {
          const { driver: d, plan: p, route } = myRoute(db, planId, routeId)
          if (route.status !== 'ditugaskan') throw new ApiError('bentrok', msg('srv.api.routeCannotStart'))
          const nowMs = server.now()
          const windowFrom = p.schedule.mode === 'terjadwal' && p.schedule.window ? new Date(p.schedule.window.from).getTime() : 0
          if (windowFrom && nowMs < windowFrom - 45 * MIN) {
            throw new ApiError('bentrok', msg('srv.api.tooEarly', { time: windowFrom }))
          }
          Object.assign(route, rebaseStart(route, d.position ?? route.startPosition, nowMs))
          route.status = 'berjalan'
          route.startedAt = iso(nowMs)
          route.legStartedAt = iso(nowMs)
          d.status = 'bertugas'
          route.stops[0]!.status = 'menuju'
          syncPlanStatus(p)
          touchPlan(p, nowMs, msg('srv.api.event.driverDeparted', { name: d.name }))
          server.notify(
            db,
            `customer:${p.userId}`,
            msg('srv.api.notif.driverEnRoute.title'),
            msg('srv.api.notif.driverEnRoute.body', { name: d.name, place: route.stops[0]!.waypoint.name, code: p.code }),
            p.id,
          )
          return p
        })
        changed(planId)
        server.emit({ type: 'notifications.changed' })
        return plan
      }),
    arrive: (planId, routeId, stopId) =>
      run(() => {
        const plan = server.mutate((db) => {
          const { plan: p, route } = myRoute(db, planId, routeId)
          if (route.status !== 'berjalan') throw new ApiError('bentrok', msg('srv.api.startRouteFirst'))
          const stop = findStop(route, stopId)
          const idx = openIndex(route)
          if (route.stops[idx]?.id !== stop.id) throw new ApiError('bentrok', msg('srv.api.finishPreviousStop'))
          if (stop.status !== 'menuju') throw new ApiError('bentrok', msg('srv.api.stopAlreadyArrived'))
          arriveStop(server, db, p, route, stop)
          return p
        })
        changed(planId)
        server.emit({ type: 'notifications.changed' })
        return plan
      }),
    complete: (planId, routeId, stopId, proof?: StopProof) =>
      run(() => {
        const plan = server.mutate((db) => {
          const { plan: p, route } = myRoute(db, planId, routeId)
          const stop = findStop(route, stopId)
          if (stop.status !== 'tiba') throw new ApiError('bentrok', msg('srv.api.markArrivedFirst'))
          if (stop.kind === 'antar') {
            if (!proof || proof.kind === 'tanpa') {
              throw new ApiError('tidak_valid', msg('srv.api.proofRequired'), { field: 'proof' })
            }
            if (proof.kind === 'otp' && proof.value?.trim() !== stop.deliveryCode) {
              throw new ApiError('tidak_valid', msg('srv.api.otpMismatch'), { field: 'proof' })
            }
            if (proof.kind === 'foto' && !isProofPhoto(proof.photo)) {
              throw new ApiError('tidak_valid', msg('srv.api.proofPhotoInvalid'), { field: 'proof' })
            }
            const at = iso(server.now())
            stop.proof =
              proof.kind === 'foto'
                ? { kind: 'foto', at, photo: proof.photo, analysis: analyzeProofPhoto({ name: proof.value ?? '', size: proof.photo!.length }, server.now()) }
                : { kind: 'otp', at }
          }
          closeStop(server, db, p, route, stop, 'selesai')
          return p
        })
        changed(planId)
        server.emit({ type: 'fleet.changed' })
        server.emit({ type: 'notifications.changed' })
        return plan
      }),
    reportIssue: (planId, routeId, stopId, issue, note) =>
      run(() => {
        const plan = server.mutate((db) => {
          const { plan: p, route } = myRoute(db, planId, routeId)
          const stop = findStop(route, stopId)
          const idx = openIndex(route)
          if (route.status !== 'berjalan' || route.stops[idx]?.id !== stop.id) throw new ApiError('bentrok', msg('srv.api.issueOnlyActive'))
          if (issue === 'lainnya' && !note?.trim()) throw new ApiError('tidak_valid', msg('srv.api.issueNoteRequired'), { field: 'note' })
          closeStop(server, db, p, route, stop, 'bermasalah', { kind: issue, note: note?.trim() || undefined })
          return p
        })
        changed(planId)
        server.emit({ type: 'fleet.changed' })
        server.emit({ type: 'notifications.changed' })
        return plan
      }),
    history: () => run((db) => emptyIfFault(db, historyItems(db, me(db).id))),
    earnings: () =>
      run((db): DriverEarnings => {
        const items = historyItems(db, me(db).id)
        const nowMs = server.now()
        const dayOf = (ms: number) => new Date(ms + 7 * 3_600_000).toISOString().slice(0, 10)
        const today = dayOf(nowMs)
        const byDayMap = new Map<string, { amountRp: number; trips: number }>()
        for (let i = 6; i >= 0; i--) byDayMap.set(dayOf(nowMs - i * 86_400_000), { amountRp: 0, trips: 0 })
        for (const it of items) {
          const row = byDayMap.get(dayOf(new Date(it.finishedAt).getTime()))
          if (row) {
            row.amountRp += it.earningsRp
            row.trips += 1
          }
        }
        const byDay = [...byDayMap.entries()].map(([date, v]) => ({ date, ...v }))
        const todayRow = byDayMap.get(today) ?? { amountRp: 0, trips: 0 }
        return {
          todayRp: todayRow.amountRp,
          tripsToday: todayRow.trips,
          weekRp: byDay.reduce((s, d) => s + d.amountRp, 0),
          tripsWeek: byDay.reduce((s, d) => s + d.trips, 0),
          byDay,
        }
      }),
  }

  /* =====================================================================================
   * live
   * ===================================================================================== */
  const live: ApiClient['live'] = {
    routes: () =>
      run((db): ActiveRouteLive[] => {
        requireRole('admin')
        return emptyIfFault(
          db,
          db.plans.flatMap((p) =>
            p.routes
              .filter((r) => r.status === 'ditugaskan' || r.status === 'berjalan')
              .map((route) => ({
                planId: p.id,
                planCode: p.code,
                customerName: db.users.find((u) => u.id === p.userId)?.name ?? tx('srv.api.userFallback'),
                driverName: db.drivers.find((d) => d.id === route.driverId)?.name,
                route,
              })),
          ),
        )
      }),
    idleDrivers: () =>
      run((db) => {
        requireRole('admin')
        return emptyIfFault(db, db.drivers.filter((d) => d.status === 'online' && d.position))
      }),
  }

  /* =====================================================================================
   * demo
   * ===================================================================================== */
  const demoState = (db: DB): DemoState => ({
    clock: db.clock,
    now: virtualNowOf(db.clock, server.realNow()),
    fault: db.fault,
    autopilot: db.autopilot,
    seededAt: db.seededAt,
    scenario: db.scenario,
  })

  const demo: ApiClient['demo'] = {
    state: () => runDemo(() => demoState(server.db())),
    reset: (scenario = SCENARIO) =>
      runDemo(() => {
        server.reseed(scenario)
        server.emit({ type: 'data.reset' })
      }),
    setSpeed: (speed) =>
      runDemo(() => {
        if (![1, 5, 10, 30, 60, 120].includes(speed)) throw new ApiError('tidak_valid', msg('srv.api.speedUnsupported'))
        server.mutate((db) => {
          const nowV = virtualNowOf(db.clock, server.realNow())
          db.clock = { speed, baseReal: server.realNow(), baseVirtual: nowV }
        })
        server.emit({ type: 'clock.changed' })
      }),
    advance: (minutes) =>
      runDemo(() => {
        server.mutate((db) => {
          db.clock = { ...db.clock, baseVirtual: db.clock.baseVirtual + minutes * MIN }
        })
        server.emit({ type: 'clock.changed' })
        server.emit({ type: 'plans.changed' })
      }),
    setAutopilot: (on) =>
      runDemo(() => {
        server.mutate((db) => {
          db.autopilot = on
        })
        server.emit({ type: 'demo.changed' })
      }),
    setFault: (mode) =>
      runDemo(() => {
        server.mutate((db) => {
          db.fault = mode
        })
        server.emit({ type: 'demo.changed' })
        server.emit({ type: 'plans.changed' })
        server.emit({ type: 'fleet.changed' })
        server.emit({ type: 'notifications.changed' })
      }),
    triggerDelay: (routeId, minutes) =>
      runDemo(() => {
        const result = server.mutate((db) => {
          const plan = routeId
            ? need(db.plans.find((p) => p.routes.some((r) => r.id === routeId)), msg('srv.api.routeNotFound'))
            : need(
                db.plans.find((p) => p.routes.some((r) => r.status === 'berjalan' && !r.delayMin)) ?? db.plans.find((p) => p.routes.some((r) => r.status === 'berjalan')),
                msg('srv.api.noRunningRoute'),
              )
          const route = routeId
            ? findRoute(plan, routeId)
            : need(plan.routes.find((r) => r.status === 'berjalan' && !r.delayMin) ?? plan.routes.find((r) => r.status === 'berjalan'), msg('srv.api.noRunningRoute'))
          if (route.status !== 'berjalan') throw new ApiError('bentrok', msg('srv.api.delayOnlyRunning'))
          const idx = openIndex(route)
          const stop = route.stops[idx]!
          stop.legMin += minutes
          route.delayMin = (route.delayMin ?? 0) + minutes
          // ETA stop yang tersisa mundur sejumlah keterlambatan
          route.stops = route.stops.map((s, i) => (i >= idx ? { ...s, arriveAt: iso(new Date(s.arriveAt).getTime() + minutes * MIN) } : s))
          const d = db.drivers.find((x) => x.id === route.driverId)
          touchPlan(plan, server.now(), msg('srv.api.event.delayInjected', { minutes }))
          server.notify(db, 'admin', msg('srv.api.notif.delayAdmin.title', { code: plan.code }), msg('srv.api.notif.delayAdmin.body', { name: d?.name ?? msg('srv.api.driverFallback'), count: route.delayMin }), plan.id)
          server.notify(db, `customer:${plan.userId}`, msg('srv.api.notif.delayCustomer.title'), msg('srv.api.notif.delayCustomer.body', { code: plan.code, count: minutes }), plan.id)
          return { planId: plan.id, planCode: plan.code }
        })
        changed(result.planId)
        server.emit({ type: 'notifications.changed' })
        return { planCode: result.planCode }
      }),
    spawnPlan: () =>
      run(() => {
        const plan = server.mutate((db) => {
          const rng = createRng(server.now() & 0x7fffffff)
          const user = rng.pick(db.users.filter((u) => !u.blocked))
          db.counters.plan += 1
          const n = db.counters.plan
          const nowMs = server.now()
          const shipments = randomShipments(rng, `x${n}`, rng.int(1, 3))
          const preset = rng.pick(['termurah', 'seimbang', 'tercepat', 'terpendek'] as const)
          const optionSet = computeOptionSet({
            planId: `pln-${n}`,
            shipments,
            schedule: { mode: 'sekarang' },
            priority: { preset },
            vehicles: db.vehicles,
            nowMs,
          })
          const chosen = optionSet.options.find((o) => o.recommended) ?? optionSet.options[0]!
          const p: Plan = {
            id: `pln-${n}`,
            code: `PL-${2000 + n}`,
            userId: user.id,
            status: 'menunggu_driver',
            schedule: { mode: 'sekarang' },
            priority: { preset },
            shipments,
            optionSet,
            selectedOptionId: chosen.id,
            routes: routesFromOption(`pln-${n}`, chosen),
            // plan buatan generator memakai saldo hanya bila cukup, supaya saldo pengguna tidak pernah negatif
            paymentMethod: balanceOf(db, user.id) >= chosen.totals.costRp ? 'saldo' : 'transfer',
            totalCostRp: chosen.totals.costRp,
            createdAt: iso(nowMs),
            updatedAt: iso(nowMs),
            confirmedAt: iso(nowMs),
            events: [
              { id: `evt-1-${nowMs.toString(36)}`, at: iso(nowMs), text: msg('srv.api.event.planCreated') },
              { id: `evt-2-${nowMs.toString(36)}`, at: iso(nowMs), text: msg('srv.api.event.confirmed', { amount: chosen.totals.costRp, option: chosen.label }) },
            ],
            draftStep: 4,
          }
          db.plans.push(p)
          if (p.paymentMethod === 'saldo') chargePlan(db, p, chosen.totals.costRp, nowMs)
          server.notify(db, `customer:${user.id}`, msg('srv.api.notif.confirmed.title'), msg('srv.api.notif.confirmed.body', { code: p.code }), p.id)
          if (autoDispatchOn(db)) dispatchPlan(server, db, p)
          if (p.routes.some((r) => !r.driverId)) server.notify(db, 'admin', msg('srv.api.notif.newPlan.title', { code: p.code }), msg('srv.api.notif.newPlan.body', { name: user.name, count: p.routes.length }), p.id)
          return p
        })
        changed(plan.id)
        server.emit({ type: 'fleet.changed' })
        server.emit({ type: 'wallet.changed' })
        server.emit({ type: 'notifications.changed' })
        return plan
      }),
  }

  /* =====================================================================================
   * peristiwa
   * ===================================================================================== */
  const events: ApiClient['events'] = {
    subscribe: (listener) => server.subscribe(listener),
  }

  /* =====================================================================================
   * pengaturan bersama dan fitur AI
   * ===================================================================================== */
  const aiEnv = { server, ctx, locale, requireRole, readablePlan, suggestFor }
  const settings = createSettingsApi(aiEnv)
  const ai = createAiApi(aiEnv)
  const wallet = createWalletApi({ server, run, requireRole, locale })
  const finance = createFinanceApi({ server, run, requireRole, locale })

  return { auth, places, account, wallet, finance, notifications, plans, fleet, users, dispatch, driver, live, demo, settings, ai, events }
}

/**
 * Satu langkah autopilot untuk semua rute yang sedang berjalan (dipanggil berkala oleh host api.<domain>).
 * Mengembalikan jumlah rute yang berubah.
 */
export function tickAutopilot(server: MockServer): number {
  if (!server.db().autopilot) return 0
  let changedCount = 0
  const ids: string[] = []
  server.mutate((db) => {
    const nowMs = server.now()
    for (const plan of db.plans) {
      for (const route of plan.routes) {
        if (route.status !== 'berjalan') continue
        const pos = routePositionAt(route, nowMs)
        if (autopilotStep(server, db, plan, route, pos.legFraction)) {
          changedCount += 1
          ids.push(plan.id)
        }
      }
    }
  })
  if (changedCount > 0) {
    for (const id of new Set(ids)) server.emit({ type: 'plans.changed', planId: id })
    server.emit({ type: 'fleet.changed' })
    server.emit({ type: 'notifications.changed' })
  }
  return changedCount
}
