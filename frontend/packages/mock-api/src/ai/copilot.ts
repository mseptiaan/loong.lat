import {
  RECOMMEND_MIN_SCORE,
  liveEtas,
  type CopilotAction,
  type Driver,
  type DriverSuggestion,
  type Plan,
  type PlanOption,
  type Preset,
  type Route,
  type Vehicle,
  type VehicleType,
} from '@loong/domain'
import { formatDuration, formatKm, formatRupiah, formatTime, renderMsg, type Locale, type Msg } from '@loong/i18n'
import { tAi } from '../i18n'
import { hash32 } from './vision'

export type Audience = 'customer' | 'admin'

/** Satu langkah kerja yang ditampilkan sebagai jejak alat: judul + sasaran, durasi, dan detail yang bisa dibuka. */
export interface ToolStep {
  title: string
  target?: string
  detail: string
  ms: number
}

/** Hasil siap-alir: jejak alat berurutan, jawaban markdown, dan tindakan yang boleh diterapkan pengguna. */
export interface Answer {
  tools: ToolStep[]
  markdown: string
  actions: CopilotAction[]
}

export interface AnswerContext {
  plan: Plan
  question: string
  audience: Audience
  nowMs: number
  drivers: Driver[]
  vehicles: Vehicle[]
  /** Bahasa permintaan: seluruh teks jawaban dan jejak alat dibuat dalam bahasa ini. */
  locale: Locale
  /** Saran driver terbaik untuk rute yang belum punya driver (hanya admin). */
  suggestions?: Record<string, DriverSuggestion[]>
}

type Kind = 'bandingkan' | 'telat' | 'status' | 'driver' | 'biaya' | 'cepat' | 'murah' | 'pendek' | 'jelaskan' | 'bantuan'

/**
 * Pengklasifikasi kata kunci. Memahami Indonesia dan Inggris apa pun bahasa layarnya (pengguna bisa mengetik
 * dalam salah satunya). Urutan penting: yang lebih spesifik lebih dulu.
 */
const RULES: [Kind, RegExp][] = [
  [
    'bandingkan',
    /bandingkan|beda|perbedaan|semua opsi|pilihan lain|opsi lain|dibanding|satu opsi|hanya satu|cuma satu|\b(?:compare|comparison|differences?|differ|all options|other options?|other choices|only one|just one|one option)\b/,
  ],
  ['telat', /telat|terlambat|lambat|anomali|masalah|kendala|bermasalah|\b(?:late|delays?|delayed|behind schedule|anomal(?:y|ies)|problems?|issues?|trouble)\b/],
  ['status', /kapan|\beta\b|posisi|di ?mana|status|lacak|sudah sampai|tiba|\b(?:when|where|position|location|track(?:ing)?|arrive[sd]?|arrival|how far)\b/],
  ['driver', /driver|kurir|tugaskan|siapa|assign|pengemudi|\b(?:who|courier|rider)\b/],
  ['biaya', /biaya|harga|bayar|rincian|tarif|\b(?:costs?|price|pay(?:ing|ment)?|fares?|breakdown|charges?|fees?|expensive)\b/],
  ['cepat', /cepat|buru|waktu|durasi|lama|secepat|\b(?:fast(?:er|est)?|quick(?:er|est)?|urgent|hurry|duration|how long|speed|sooner|earlier|time)\b/],
  ['murah', /murah|hemat|irit|termurah|\b(?:cheap(?:er|est)?|sav(?:e|ing|ings)|economical|budget|lowest cost|least expensive|inexpensive)\b/],
  ['pendek', /jarak|dekat|pendek|terpendek|bahan bakar|\b(?:distance|short(?:er|est)?|close(?:r|st)?|nearest|fuel|km)\b/],
  ['jelaskan', /kenapa|alasan|jelaskan|mengapa|rekomendasi|disarankan|saran|pilih|\b(?:why|reasons?|explain|recommend(?:ed|ation)?|suggest(?:ed)?|chose|choose|chosen|pick(?:ed)?)\b/],
]

export function classify(question: string): Kind {
  const q = question.toLowerCase()
  return RULES.find(([, re]) => re.test(q))?.[0] ?? 'bantuan'
}

/** Pertanyaan awal yang relevan dengan keadaan plan (bukan "apa yang dipikirkan pengguna"; ini yang bisa dijawab). */
export function starterQuestions(plan: Pick<Plan, 'status' | 'optionSet' | 'routes'>, audience: Audience, locale: Locale): string[] {
  const t = tAi(locale)
  if (plan.routes.length === 0) {
    return plan.optionSet && plan.optionSet.options.length > 1
      ? [t('ai.starter.whyRecommended'), t('ai.starter.compareAll'), t('ai.starter.faster')]
      : [t('ai.starter.whyOnlyOne'), t('ai.starter.costBreakdown')]
  }
  if (audience === 'admin') return [t('ai.starter.whatToDo'), t('ai.starter.bestDriver'), t('ai.starter.whyLate')]
  return [t('ai.starter.whenArrive'), t('ai.starter.costBreakdown'), t('ai.starter.whyLate')]
}

const ms = (seed: string, i: number) => 140 + (hash32(`${seed}:${i}`) % 720)
const step = (seed: string, i: number, title: string, detail: string, target?: string): ToolStep => ({ title, target, detail, ms: ms(seed, i) })

/** Penerjemah dan pemformat terikat bahasa permintaan (server melayani banyak bahasa, jadi tidak memakai `t` global). */
function kit(locale: Locale) {
  const t = tAi(locale)
  return {
    locale,
    t,
    /** Teks `Msg` dari data plan (label opsi, alasan, rincian tarif) dalam bahasa permintaan. */
    msg: (m: Msg) => renderMsg(m, locale),
    preset: (p: Preset) => t(`dom.preset.${p}`),
    status: (s: Plan['status']) => t(`dom.planStatus.${s}`),
    vehicle: (v: VehicleType) => t(`dom.vehicleType.${v}`),
    vehicleLower: (v: VehicleType) => t(`dom.vehicleType.${v}`).toLowerCase(),
    rp: (n: number) => formatRupiah(n, locale),
    km: (n: number) => formatKm(n, locale),
    dur: (n: number) => formatDuration(n, locale),
    time: (when: string | number) => formatTime(when, locale),
  }
}
type Kit = ReturnType<typeof kit>

const cap = (s: string) => s.replace(/^(\*\*)?([a-z])/, (_m, b: string | undefined, c: string) => `${b ?? ''}${c.toUpperCase()}`)
/** Satu baris tampil sebagai kalimat biasa; beberapa baris sebagai daftar. */
const asList = (lines: string[]) => (lines.length === 1 ? cap(lines[0]!) : lines.map((l) => `- ${l}`).join('\n'))

function vehicleSummary(routes: Route[], k: Kit): string {
  const count = (t: VehicleType) => routes.filter((r) => r.vehicleType === t).length
  const parts = (['motor', 'blindvan'] as const).filter(count).map((t) => `${count(t)} ${k.vehicleLower(t)}`)
  return parts.join(' + ')
}

function chosenOption(plan: Plan): PlanOption | undefined {
  const o = plan.optionSet?.options
  return o?.find((x) => x.id === plan.selectedOptionId) ?? o?.find((x) => x.recommended) ?? o?.[0]
}

function draftPlanAction(plan: Plan, o: PlanOption, k: Kit): CopilotAction[] {
  // hanya selama pelanggan masih memilih opsi
  return plan.routes.length === 0
    ? [{ id: `pilih:${o.id}`, label: k.t('ai.action.chooseOption', { label: k.msg(o.label) }), kind: 'pilih_opsi', planId: plan.id, optionId: o.id, primary: true }] // i18n-ignore: id tindakan = kode
    : []
}

const applyBestDriver = (plan: Plan, k: Kit): CopilotAction => ({ id: `tugaskan:${plan.id}`, label: k.t('ai.action.applyBestDriver'), kind: 'tugaskan_terbaik', planId: plan.id, primary: true })

/* ---------------------------------- jawaban per jenis pertanyaan ---------------------------------- */

function explain(ctx: AnswerContext, k: Kit): Answer {
  const { plan } = ctx
  const { t } = k
  const set = plan.optionSet
  const o = chosenOption(plan)
  if (!set || !o) return noOptions(plan, k)
  const alt = set.options.filter((x) => x.id !== o.id)
  const faster = alt.filter((x) => x.totals.avgDeliveryMin < o.totals.avgDeliveryMin - 3).sort((a, b) => a.totals.avgDeliveryMin - b.totals.avgDeliveryMin)[0]
  const cheaper = alt.filter((x) => x.totals.costRp < o.totals.costRp - 500).sort((a, b) => a.totals.costRp - b.totals.costRp)[0]

  // alasan dari engine sudah memuat biaya, penghematan, durasi, kendaraan, dan urutan stop: tampilkan apa adanya (terlacak),
  // lalu tambahkan hanya angka yang belum ada di sana
  const lines = [
    t(o.recommended ? 'ai.copilot.explain.recommended' : 'ai.copilot.explain.chosen', { label: k.msg(o.label), priority: k.preset(plan.priority.preset) }),
    '',
    ...o.reasons.map((r) => `- ${k.msg(r)}`),
    `- ${t('ai.copilot.explain.summary', { distance: o.totals.distanceKm, vehicles: vehicleSummary(o.routes, k), avg: o.totals.avgDeliveryMin })}`,
  ]
  if (faster) {
    lines.push('', t('ai.copilot.explain.faster', { label: k.msg(faster.label), minutes: o.totals.avgDeliveryMin - faster.totals.avgDeliveryMin, extra: Math.max(0, faster.totals.costRp - o.totals.costRp) }))
  }
  if (cheaper) {
    lines.push('', t('ai.copilot.explain.cheaper', { label: k.msg(cheaper.label), saving: o.totals.costRp - cheaper.totals.costRp, minutes: Math.max(0, cheaper.totals.avgDeliveryMin - o.totals.avgDeliveryMin) }))
  }

  return {
    tools: [
      step(plan.id, 0, t('ai.tool.readPlan'), t('ai.copilot.explain.readPlanDetail', { code: plan.code, priority: k.preset(plan.priority.preset), count: plan.shipments.length }), plan.code),
      step(
        plan.id,
        1,
        t('ai.tool.readOptions'),
        t('ai.copilot.explain.readOptionsDetail', { options: set.options.map((x) => k.msg(x.label)).join(', '), cost: set.baseline.costRp, distance: set.baseline.distanceKm }),
        t('ai.optionsTarget', { count: set.options.length }),
      ),
      step(plan.id, 2, t('ai.copilot.explain.traceReasons'), o.reasons.map((r) => `- ${k.msg(r)}`).join('\n'), k.msg(o.label)),
    ],
    markdown: lines.join('\n'),
    actions: [],
  }
}

function compare(ctx: AnswerContext, k: Kit): Answer {
  const { plan } = ctx
  const { t } = k
  const set = plan.optionSet
  if (!set) return noOptions(plan, k)
  const chosen = chosenOption(plan)
  if (set.options.length === 1) {
    const o = set.options[0]!
    return {
      tools: [step(plan.id, 0, t('ai.tool.readOptions'), t('ai.copilot.compare.singleDetail', { label: k.msg(o.label) }), t('ai.optionsTarget', { count: 1 }))],
      markdown: `${t('ai.copilot.compare.single', { label: k.msg(o.label), cost: o.totals.costRp, distance: o.totals.distanceKm, duration: o.totals.durationMin })}\n\n${t('ai.copilot.compare.singleHint')}`,
      actions: [],
    }
  }
  const rows = set.options.map(
    (o) =>
      `| ${k.msg(o.label)}${o.id === chosen?.id ? ` ${t('ai.copilot.compare.chosen')}` : ''} | ${k.rp(o.totals.costRp)} | ${k.km(o.totals.distanceKm)} | ${k.dur(o.totals.durationMin)} | ${k.dur(o.totals.avgDeliveryMin)} | ${vehicleSummary(o.routes, k)} |`,
  )
  const by = (f: (o: PlanOption) => number) => k.msg([...set.options].sort((a, b) => f(a) - f(b))[0]!.label)
  const cheapest = by((o) => o.totals.costRp)
  const shortest = by((o) => o.totals.distanceKm)
  const fastest = by((o) => o.totals.avgDeliveryMin)
  return {
    tools: [
      step(plan.id, 0, t('ai.tool.readOptions'), t('ai.tool.optionCount', { count: set.options.length }), t('ai.optionsTarget', { count: set.options.length })),
      step(plan.id, 1, t('ai.copilot.compare.compareTitle'), t('ai.copilot.compare.compareDetail', { cheapest, shortest, fastest })),
    ],
    markdown: [t('ai.copilot.compare.header'), ...rows, '', t('ai.copilot.compare.summary', { cheapest, shortest, fastest })].join('\n'),
    actions: [],
  }
}

function extreme(ctx: AnswerContext, k: Kit, kind: 'cepat' | 'murah' | 'pendek'): Answer {
  const { plan } = ctx
  const { t } = k
  const set = plan.optionSet
  const cur = chosenOption(plan)
  if (!set || !cur) return noOptions(plan, k)
  const metric = {
    cepat: { f: (o: PlanOption) => o.totals.avgDeliveryMin, unit: k.dur },
    murah: { f: (o: PlanOption) => o.totals.costRp, unit: k.rp },
    pendek: { f: (o: PlanOption) => o.totals.distanceKm, unit: k.km },
  }[kind]
  const best = [...set.options].sort((a, b) => metric.f(a) - metric.f(b))[0]!
  const tools = [
    step(plan.id, 0, t('ai.tool.readOptions'), t('ai.tool.optionCount', { count: set.options.length }), t('ai.optionsTarget', { count: set.options.length })),
    step(plan.id, 1, t(`ai.copilot.extreme.sortBy.${kind}`), set.options.map((o) => `${k.msg(o.label)}: ${metric.unit(metric.f(o))}`).join('\n')),
  ]

  if (best.id === cur.id) {
    return { tools, markdown: t(`ai.copilot.extreme.alreadyBest.${kind}`, { label: k.msg(cur.label), value: metric.f(cur) }), actions: [] }
  }
  const costDiff = best.totals.costRp - cur.totals.costRp
  const timeDiff = cur.totals.avgDeliveryMin - best.totals.avgDeliveryMin
  const km = cur.totals.distanceKm - best.totals.distanceKm
  const gain = t(`ai.copilot.extreme.gain.${kind}`, { value: kind === 'cepat' ? timeDiff : kind === 'pendek' ? km : -costDiff })
  const price =
    kind === 'murah'
      ? t(best.totals.avgDeliveryMin > cur.totals.avgDeliveryMin ? 'ai.copilot.extreme.price.later' : 'ai.copilot.extreme.price.sooner', {
          value: Math.abs(best.totals.avgDeliveryMin - cur.totals.avgDeliveryMin),
        })
      : costDiff > 0
        ? t('ai.copilot.extreme.price.more', { value: costDiff })
        : costDiff < 0
          ? t('ai.copilot.extreme.price.less', { value: -costDiff })
          : t('ai.copilot.extreme.price.same')
  return {
    tools,
    markdown: `${t(`ai.copilot.extreme.best.${kind}`, { best: k.msg(best.label), gain, price, current: k.msg(cur.label) })}\n\n${t('ai.copilot.extreme.numbers', { cost: best.totals.costRp, distance: best.totals.distanceKm, duration: best.totals.durationMin })}`,
    actions: draftPlanAction(plan, best, k),
  }
}

function status(ctx: AnswerContext, k: Kit): Answer {
  const { plan, nowMs, audience } = ctx
  const { t } = k
  if (plan.status === 'dibatalkan') {
    return {
      tools: [step(plan.id, 0, t('ai.tool.readPlan'), t('ai.tool.statusOnly', { status: k.status(plan.status) }), plan.code)],
      markdown: plan.cancelReason ? t('ai.copilot.status.cancelledReason', { code: plan.code, reason: plan.cancelReason }) : t('ai.copilot.status.cancelled', { code: plan.code }),
      actions: [],
    }
  }
  if (plan.routes.length === 0) return notConfirmed(plan, k)

  const lines: string[] = []
  const tools: ToolStep[] = [step(plan.id, 0, t('ai.tool.readPlan'), t('ai.copilot.status.readPlanDetail', { code: plan.code, status: k.status(plan.status), count: plan.routes.length }), plan.code)]
  plan.routes.forEach((r, i) => {
    const driver = ctx.drivers.find((d) => d.id === r.driverId)
    const head = plan.routes.length > 1 ? `${t('ai.route.headVehicle', { n: i + 1, vehicle: k.vehicleLower(r.vehicleType) })}: ` : ''
    const done = r.stops.filter((s) => s.status === 'selesai' || s.status === 'bermasalah').length
    if (r.status === 'selesai') {
      const stops = t('ai.stops', { count: r.stops.length })
      lines.push(head + (r.finishedAt ? t('ai.copilot.status.doneAt', { time: r.finishedAt, stops }) : t('ai.copilot.status.done', { stops })))
    } else if (r.status === 'berjalan') {
      const etas = liveEtas(r, nowMs)
      const next = r.stops.find((s) => s.status !== 'selesai' && s.status !== 'bermasalah')
      const last = etas.at(-1)
      tools.push(
        step(
          plan.id,
          1 + i,
          t('ai.copilot.status.recalcEta'),
          t('ai.copilot.status.recalcDetail', { route: r.id, done, total: r.stops.length, eta: last ? k.time(last.etaAt) : '-' }),
          driver?.name,
        ),
      )
      const progress = t('ai.copilot.status.progress', { done, count: r.stops.length })
      let line = driver ? t('ai.copilot.status.runningNamed', { driver: driver.name, progress }) : t('ai.copilot.status.runningUnnamed', { progress })
      if (next && etas[0]) line += ` ${t(next.kind === 'ambil' ? 'ai.copilot.status.nextPickup' : 'ai.copilot.status.nextDropoff', { place: next.waypoint.name, time: etas[0].etaAt })}` // i18n-ignore: nilai enum stop
      if (last) line += ` ${t('ai.copilot.status.allDone', { time: last.etaAt })}`
      lines.push(head + line)
    } else if (r.status === 'ditugaskan') {
      lines.push(head + t('ai.copilot.status.assigned', { driver: driver?.name ?? t('ai.driverFallback'), time: r.startAt }))
    } else {
      const top = ctx.suggestions?.[r.id]?.find((s) => s.fits)
      lines.push(`${head}${t('ai.copilot.status.searching')}${audience === 'admin' && top ? ` ${t('ai.copilot.status.bestCandidate', { driver: top.driverName, distance: top.distanceToPickupKm })}` : ''}`)
    }
  })
  const actions: CopilotAction[] = audience === 'admin' && plan.routes.some((r) => r.status === 'menunggu_driver' && !r.driverId) ? [applyBestDriver(plan, k)] : []
  return { tools, markdown: asList(lines), actions }
}

function late(ctx: AnswerContext, k: Kit): Answer {
  const { plan, audience, nowMs } = ctx
  const { t } = k
  if (plan.routes.length === 0) return notConfirmed(plan, k)
  const tools: ToolStep[] = [step(plan.id, 0, t('ai.tool.readPlan'), t('ai.tool.planRoutes', { code: plan.code, count: plan.routes.length }), plan.code)]
  const issues: string[] = []
  const advice: string[] = []
  plan.routes.forEach((r, i) => {
    const driver = ctx.drivers.find((d) => d.id === r.driverId)
    const delay = r.delayMin ?? 0
    const label = plan.routes.length > 1 ? t('ai.copilot.late.routeN', { n: i + 1 }) : t('ai.copilot.late.thisRoute')
    if (delay >= 1 && r.status !== 'selesai') {
      const next = liveEtas(r, nowMs)[0]
      const stop = r.stops.find((s) => s.status !== 'selesai' && s.status !== 'bermasalah')
      tools.push(
        step(plan.id, 1 + i, t('ai.copilot.late.checkSchedule'), t('ai.copilot.late.checkDetail', { route: r.id, delay, stop: stop?.waypoint.name ?? '-' }), driver?.name),
      )
      issues.push(
        `- ${
          stop && next
            ? t('ai.copilot.late.delayedEta', { label, count: delay, stop: stop.waypoint.name, eta: next.etaAt, planned: stop.arriveAt })
            : t('ai.copilot.late.delayed', { label, count: delay })
        }`,
      )
      if (audience === 'admin' && driver) advice.push(t('ai.copilot.late.adviceDriver', { driver: driver.name, phone: driver.phone }))
    }
    for (const s of r.stops.filter((x) => x.status === 'bermasalah')) {
      const place = s.waypoint.name
      issues.push(`- ${s.issue ? t('ai.copilot.late.problemStopIssue', { seq: s.seq, place, issue: k.msg(s.issue) }) : t('ai.copilot.late.problemStop', { seq: s.seq, place })}`)
      if (audience === 'admin') advice.push(t('ai.copilot.late.adviceStop', { seq: s.seq, contact: s.waypoint.contactName, phone: s.waypoint.contactPhone }))
    }
  })
  if (issues.length === 0) {
    return { tools, markdown: t('ai.copilot.late.none'), actions: [] }
  }
  if (audience === 'customer') advice.push(t('ai.copilot.late.adviceCustomer'))
  return { tools, markdown: [...issues, '', ...[...new Set(advice)].map((a) => `- ${a}`)].join('\n'), actions: [] }
}

function driverAnswer(ctx: AnswerContext, k: Kit): Answer {
  const { plan, audience } = ctx
  const { t } = k
  if (plan.routes.length === 0) return notConfirmed(plan, k)
  const tools: ToolStep[] = [step(plan.id, 0, t('ai.tool.readPlan'), t('ai.tool.planRoutes', { code: plan.code, count: plan.routes.length }), plan.code)]
  const lines: string[] = []
  let canAssign = false
  plan.routes.forEach((r, i) => {
    const driver = ctx.drivers.find((d) => d.id === r.driverId)
    const vehicle = ctx.vehicles.find((v) => v.id === r.vehicleId)
    const head = plan.routes.length > 1 ? `${t('ai.route.head', { n: i + 1 })}: ` : ''
    if (driver) {
      lines.push(
        head +
          t('ai.copilot.driver.assigned', {
            driver: driver.name,
            vehicle: vehicle ? `${vehicle.model} · ${vehicle.plate}` : k.vehicle(r.vehicleType),
            rating: driver.rating,
            count: driver.completedTrips,
          }),
      )
    } else {
      const ranked = [...(ctx.suggestions?.[r.id] ?? [])].sort((a, b) => Number(b.fits) - Number(a.fits) || b.score - a.score)
      const top = ranked[0]
      tools.push(
        step(
          plan.id,
          1 + i,
          t('ai.copilot.driver.rateCandidates'),
          ranked.map((s) => t('ai.copilot.driver.candidateLine', { driver: s.driverName, score: Math.round(s.score), distance: s.distanceToPickupKm })).join('\n') || t('ai.copilot.driver.noCandidates'),
          t('ai.copilot.driver.candidates', { count: ranked.length }),
        ),
      )
      if (audience === 'admin' && top && top.score >= RECOMMEND_MIN_SCORE) {
        canAssign = true
        lines.push(head + t('ai.copilot.driver.suggest', { driver: top.driverName, plate: top.plate, reasons: top.reasons.map((m) => k.msg(m)).join('; ') }))
      } else if (audience === 'admin' && top) {
        // kandidat terbaik pun lemah: jangan disebut saran; tunjukkan alasannya dan biarkan admin memutuskan
        lines.push(head + t('ai.copilot.driver.weak', { driver: top.driverName, plate: top.plate, reason: top.reasons[0] ? k.msg(top.reasons[0]) : '' }))
      } else {
        lines.push(head + t(audience === 'admin' ? 'ai.copilot.driver.noneAdmin' : 'ai.copilot.driver.noneCustomer'))
      }
    }
  })
  return { tools, markdown: asList(lines), actions: canAssign ? [applyBestDriver(plan, k)] : [] }
}

function cost(ctx: AnswerContext, k: Kit): Answer {
  const { plan } = ctx
  const { t } = k
  const routes = plan.routes.length ? plan.routes : (chosenOption(plan)?.routes ?? [])
  if (routes.length === 0) return noOptions(plan, k)
  const lines: string[] = []
  for (const [i, r] of routes.entries()) {
    if (routes.length > 1) lines.push(t('ai.route.headVehicle', { n: i + 1, vehicle: k.vehicleLower(r.vehicleType) }))
    for (const l of r.fare) lines.push(`- ${k.msg(l.label)}: ${k.rp(l.amountRp)}`)
    if (routes.length > 1) lines.push(`- ${t('ai.copilot.cost.subtotal', { amount: r.costRp })}`, '')
  }
  const total = routes.reduce((s, r) => s + r.costRp, 0)
  lines.push(t('ai.copilot.cost.total', { amount: total }))
  return {
    tools: [step(plan.id, 0, t('ai.copilot.cost.readFare'), t('ai.copilot.cost.readFareDetail', { count: routes.length, total }), plan.code)],
    markdown: lines.join('\n'),
    actions: [],
  }
}

function help(ctx: AnswerContext, k: Kit): Answer {
  const { t } = k
  const qs = starterQuestions(ctx.plan, ctx.audience, ctx.locale)
  return {
    tools: [],
    markdown: `${t('ai.copilot.help.intro')}\n\n${t('ai.copilot.help.tryAsking')}\n${qs.map((q) => `- ${q}`).join('\n')}`,
    actions: [],
  }
}

const noOptions = (plan: Plan, k: Kit): Answer => ({
  tools: [step(plan.id, 0, k.t('ai.tool.readPlan'), k.t('ai.copilot.noOptions.detail', { code: plan.code }), plan.code)],
  markdown: k.t('ai.copilot.noOptions.text'),
  actions: [],
})

const notConfirmed = (plan: Plan, k: Kit): Answer => ({
  tools: [step(plan.id, 0, k.t('ai.tool.readPlan'), k.t('ai.tool.statusOnly', { status: k.status(plan.status) }), plan.code)],
  markdown: k.t('ai.copilot.notConfirmed.text'),
  actions: [],
})

export function answerAboutPlan(ctx: AnswerContext): Answer {
  const k = kit(ctx.locale)
  switch (classify(ctx.question)) {
    case 'bandingkan':
      return compare(ctx, k)
    case 'telat':
      return late(ctx, k)
    case 'status':
      return status(ctx, k)
    case 'driver':
      return driverAnswer(ctx, k)
    case 'biaya':
      return cost(ctx, k)
    case 'cepat':
      return extreme(ctx, k, 'cepat')
    case 'murah':
      return extreme(ctx, k, 'murah')
    case 'pendek':
      return extreme(ctx, k, 'pendek')
    case 'jelaskan':
      return explain(ctx, k)
    default:
      return help(ctx, k)
  }
}
