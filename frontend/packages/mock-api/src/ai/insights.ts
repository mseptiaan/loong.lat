import type { CopilotAction, Driver, Plan } from '@loong/domain'
import { formatTime, type Locale } from '@loong/i18n'
import { tAi } from '../i18n'
import type { Answer, ToolStep } from './copilot'
import { hash32 } from './vision'

const DAY = 86_400_000
const WIB_MS = 7 * 3_600_000
const ANOMALY_DELAY_MIN = 10

const toolMs = (seed: string, i: number) => 140 + (hash32(`${seed}:${i}`) % 720)
const step = (seed: string, i: number, title: string, detail: string, target?: string): ToolStep => ({ title, target, detail, ms: toolMs(seed, i) })

const isOperational = (p: Plan) => p.status !== 'draft' && p.status !== 'opsi_siap'
const isActive = (p: Plan) => p.status === 'menunggu_driver' || p.status === 'ditugaskan' || p.status === 'berjalan'

/** Keterlambatan (menit) terbesar di antara rute yang belum selesai. */
const maxDelay = (p: Plan) => Math.max(0, ...p.routes.filter((r) => r.status !== 'selesai').map((r) => r.delayMin ?? 0))
const hasProblemStop = (p: Plan) => p.routes.some((r) => r.stops.some((s) => s.status === 'bermasalah'))
const isAnomaly = (p: Plan) => isActive(p) && (maxDelay(p) >= ANOMALY_DELAY_MIN || hasProblemStop(p))

const anomalyText = (p: Plan, locale: Locale) => {
  const t = tAi(locale)
  const late = maxDelay(p) >= ANOMALY_DELAY_MIN
  const params = { code: p.code, minutes: maxDelay(p) }
  if (late && hasProblemStop(p)) return t('ai.anomaly.both', params)
  return late ? t('ai.anomaly.late', params) : t('ai.anomaly.problem', params)
}

const openPlan = (p: Plan, locale: Locale, primary = false): CopilotAction => ({
  id: `buka:${p.id}`,
  label: tAi(locale)('ai.action.open', { code: p.code }),
  kind: 'buka_plan',
  planId: p.id,
  primary,
})

/** Jam WIB bulat ("14.00" id, "14:00" en). */
const hourLabel = (h: number, locale: Locale) => formatTime(Date.UTC(2000, 0, 1, h - 7), locale)

/** Menit sejak plan terakhir berubah (dipakai sebagai "lama menunggu" untuk plan yang belum punya driver). */
const waitedMin = (p: Plan, nowMs: number) => Math.max(0, Math.round((nowMs - new Date(p.updatedAt).getTime()) / 60_000))

/** Jawaban untuk admin: apa yang perlu dikerjakan dulu, urut menurut mendesaknya. */
export function answerOps(plans: Plan[], drivers: Driver[], nowMs: number, locale: Locale): Answer {
  const t = tAi(locale)
  const active = plans.filter(isOperational).filter(isActive)
  const anomalies = active.filter(isAnomaly)
  const waiting = active.filter((p) => p.status === 'menunggu_driver').sort((a, b) => a.updatedAt.localeCompare(b.updatedAt))
  const idle = drivers.filter((d) => d.status === 'online').length

  const tools = [
    step('ops', 0, t('ai.ops.readQueue'), t('ai.ops.readQueueDetail', { active: active.length, waiting: waiting.length }), t('ai.ops.activePlans', { count: active.length })),
    step(
      'ops',
      1,
      t('ai.ops.checkAnomalies'),
      anomalies.map((p) => `${p.code}: ${anomalyText(p, locale).replace(/\*\*/g, '')}`).join('\n') || t('ai.ops.none'),
      t('ai.ops.anomalies', { count: anomalies.length }),
    ),
    step('ops', 2, t('ai.ops.checkDrivers'), t('ai.ops.checkDriversDetail', { idle }), t('ai.ops.driversReady', { count: idle })),
  ]

  if (anomalies.length === 0 && waiting.length === 0) {
    return { tools, markdown: t('ai.ops.allClear'), actions: [] }
  }

  const lines: string[] = []
  let n = 1
  for (const p of anomalies) lines.push(`${n++}. ${t('ai.ops.contactDriver', { anomaly: anomalyText(p, locale) })}`)
  for (const p of waiting) lines.push(`${n++}. ${t(idle > 0 ? 'ai.ops.waitingWithIdle' : 'ai.ops.waitingNoIdle', { code: p.code, waited: waitedMin(p, nowMs) })}`)

  return {
    tools,
    markdown: `${t('ai.ops.intro')}\n\n${lines.join('\n')}`,
    actions: [...anomalies, ...waiting].slice(0, 3).map((p, i) => openPlan(p, locale, i === 0)),
  }
}

/** Narasi analitik: setiap kalimat memuat angka yang bisa ditelusuri ke daftar plan. Tidak mengubah data apa pun. */
export function buildInsights(plans: Plan[], drivers: Driver[], nowMs: number, locale: Locale): Answer {
  const t = tAi(locale)
  const all = plans.filter(isOperational)
  const inWindow = (from: number, to: number) => all.filter((p) => {
    const at = new Date(p.createdAt).getTime()
    return at >= from && at < to
  })
  const cur = inWindow(nowMs - 7 * DAY, nowMs + 1)
  const prev = inWindow(nowMs - 14 * DAY, nowMs - 7 * DAY)
  const billable = cur.filter((p) => p.status !== 'dibatalkan')
  const revenue = billable.reduce((s, p) => s + (p.totalCostRp ?? 0), 0)

  const saved = billable.reduce((s, p) => {
    const o = p.optionSet?.options.find((x) => x.id === p.selectedOptionId)
    return s + Math.max(0, o?.savings.costRp ?? 0)
  }, 0)
  const savedPct = revenue + saved > 0 ? (saved / (revenue + saved)) * 100 : 0

  const done = cur.filter((p) => p.status === 'selesai')
  const clean = done.filter((p) => !hasProblemStop(p))

  const byHour = new Map<number, number>()
  for (const p of billable) {
    const h = new Date(new Date(p.createdAt).getTime() + WIB_MS).getUTCHours()
    byHour.set(h, (byHour.get(h) ?? 0) + 1)
  }
  const peak = [...byHour.entries()].sort((a, b) => b[1] - a[1] || a[0] - b[0])[0]

  const active = all.filter(isActive)
  const anomalies = active.filter(isAnomaly)
  const waiting = active.filter((p) => p.status === 'menunggu_driver')
  const idle = drivers.filter((d) => d.status === 'online').length

  // kalimat pertama berbeda menurut tren dibanding 7 hari sebelumnya (satu kalimat satu kunci)
  const prevBillable = prev.filter((p) => p.status !== 'dibatalkan').length
  const delta = prev.length > 0 ? ((billable.length - prevBillable) / Math.max(1, prevBillable)) * 100 : undefined
  const createdKey =
    delta === undefined ? 'ai.insights.created' : Math.abs(delta) < 1 ? 'ai.insights.createdSame' : delta > 0 ? 'ai.insights.createdUp' : 'ai.insights.createdDown'

  const summary = [
    t(createdKey, { plans: t('ai.plans', { count: billable.length }), pct: Math.abs(delta ?? 0), revenue }),
    saved > 0 ? t('ai.insights.saved', { saved, pct: savedPct }) : null,
    done.length > 0 ? t('ai.insights.clean', { clean: clean.length, done: done.length, pct: (clean.length / done.length) * 100 }) : null,
    peak ? t('ai.insights.peak', { from: hourLabel(peak[0], locale), to: hourLabel((peak[0] + 1) % 24, locale), plans: t('ai.plans', { count: peak[1] }) }) : null,
  ]
    .filter((l): l is string => l !== null)
    .map((l) => `- ${l}`)

  const attention: string[] = [
    ...anomalies.map((p) => `- ${anomalyText(p, locale)}.`),
    ...(waiting.length > 0
      ? [
          `- ${
            idle === 0
              ? t('ai.insights.waitingNoIdle', { plans: t('ai.plans', { count: waiting.length }) })
              : t('ai.insights.waitingIdle', { plans: t('ai.plans', { count: waiting.length }), drivers: t('ai.insights.drivers', { count: idle }) })
          }`,
        ]
      : []),
  ]

  const advice: string[] = []
  if (waiting.length > 0 && idle === 0) advice.push(t('ai.insights.adviceNoDrivers'))
  if (anomalies.length > 0) advice.push(t('ai.insights.adviceLate'))
  if (peak) advice.push(t('ai.insights.advicePeak', { hour: hourLabel(peak[0], locale) }))

  const markdown = [
    t('ai.insights.title'),
    '',
    ...summary,
    '',
    t('ai.insights.attention'),
    '',
    ...(attention.length ? attention : [`- ${t('ai.insights.attentionNone')}`]),
    ...(advice.length ? ['', t('ai.insights.advice'), '', ...advice.map((a) => `- ${a}`)] : []),
    '',
    t('ai.insights.footer'),
  ].join('\n')

  const tools = [
    step('insights', 0, t('ai.insights.readPlans'), t('ai.insights.readPlansDetail', { total: all.length, current: cur.length, previous: prev.length }), t('ai.plans', { count: all.length })),
    step('insights', 1, t('ai.insights.calcSavings'), t('ai.insights.calcSavingsDetail', { saved, revenue })),
    step('insights', 2, t('ai.insights.checkQueue'), t('ai.insights.checkQueueDetail', { anomalies: anomalies.length, waiting: waiting.length, idle })),
    step(
      'insights',
      3,
      t('ai.insights.findPeak'),
      [...byHour.entries()]
        .sort((a, b) => b[1] - a[1])
        .slice(0, 3)
        .map(([h, c]) => `${hourLabel(h, locale)} -> ${c}`)
        .join('\n') || t('ai.insights.noData'),
    ),
  ]

  return { tools, markdown, actions: anomalies.slice(0, 3).map((p, i) => openPlan(p, locale, i === 0)) }
}
