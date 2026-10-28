import {
  ApiError,
  DEFAULT_SETTINGS,
  type AiFeatures,
  type AppSettings,
  type ApiClient,
  type CopilotEvent,
  type CopilotToolTrace,
  type DriverSuggestion,
  type Plan,
  type ReadImageEvent,
  type Route,
  type Session,
} from '@loong/domain'
import { msg, type Locale, type Msg } from '@loong/i18n'
import type { DB } from '../db'
import { answerAboutPlan, type Answer, type Audience } from '../ai/copilot'
import { answerOps, buildInsights } from '../ai/insights'
import { buildOptionReport } from '../ai/report'
import { tAi } from '../i18n'
import { parseIntent } from '../ai/intent'
import { parseSpeech } from '../ai/speech'
import { MAX_IMAGE_BYTES, detectItems, hash32 } from '../ai/vision'
import { tickDispatch } from './dispatch'
import { clone, sleep, type MockServer } from './server'

/** Pengaturan dengan nilai bawaan untuk bagian yang belum ada di DB lama. */
export const settingsOf = (db: DB): AppSettings => ({
  ai: { ...DEFAULT_SETTINGS.ai, ...db.settings?.ai },
  dispatch: { ...DEFAULT_SETTINGS.dispatch, ...db.settings?.dispatch },
})

/** Hal-hal yang dipinjam dari createApi (penjaga peran, akses plan, saran driver). */
export interface AiEnv {
  server: MockServer
  requireRole: (...roles: Session['role'][]) => Session
  readablePlan: (db: DB, id: string) => Plan
  suggestFor: (db: DB, plan: Plan, route: Route) => DriverSuggestion[]
  ctx: Session | null
  /** Bahasa permintaan: teks sesaat (jawaban, hasil baca, progres) dibuat dalam bahasa ini. */
  locale: Locale
}

/* ---------------------------------- settings ---------------------------------- */

export function createSettingsApi(env: AiEnv): ApiClient['settings'] {
  const { server } = env
  return {
    get: async () => {
      await server.delay()
      env.requireRole('customer', 'admin', 'driver')
      return clone(settingsOf(server.db()))
    },
    update: async (patch) => {
      await server.delay()
      env.requireRole('admin')
      const next = server.mutate((db) => {
        const cur = settingsOf(db)
        db.settings = { ai: { ...cur.ai, ...patch.ai }, dispatch: { ...cur.dispatch, ...patch.dispatch } }
        return db.settings
      })
      server.emit({ type: 'settings.changed' })
      // penugasan otomatis baru dinyalakan: pesanan yang sudah menunggu langsung dipertimbangkan
      if (patch.dispatch?.auto) tickDispatch(server)
      return clone(next)
    },
  }
}

/* ---------------------------------- ai ---------------------------------- */

const REQUEST_ID = (seed: string) => `req-${hash32(seed).toString(36)}`

export function createAiApi(env: AiEnv): ApiClient['ai'] {
  const { server, locale } = env
  const t = tAi(locale)
  const live = () => server.options.latency[1] > 0

  /** Kegagalan tiruan yang sama dengan metode lain; AI juga harus bisa diuji lewat Panel Demo. */
  async function guard(feature: keyof AiFeatures | undefined, label: Msg) {
    const db = server.db()
    if (db.fault === 'offline') throw new ApiError('jaringan', msg('srv.api.offline'))
    if (db.fault === 'error') throw new ApiError('layanan', msg('srv.api.aiError', { feature: label }))
    if (feature && !(db.settings?.ai ?? DEFAULT_SETTINGS.ai)[feature]) throw new ApiError('ditolak', msg('srv.api.aiDisabled', { feature: label }))
    if (db.fault === 'slow' && live()) await sleep(2600)
  }

  /** Mengalirkan jawaban: jejak alat (berjalan -> selesai), potongan teks per kata, lalu tindakan yang diusulkan. */
  async function* stream(answer: Answer, requestSeed: string): AsyncGenerator<CopilotEvent> {
    for (const [i, tool] of answer.tools.entries()) {
      const trace: CopilotToolTrace = { id: `t${i}`, title: tool.title, target: tool.target, status: 'running' }
      yield { type: 'tool', trace }
      // jeda dibatasi agar tidak terasa lambat, tetapi durasi yang ditampilkan tetap angka tiruan yang stabil
      if (live()) await sleep(Math.min(260, tool.ms * 0.35))
      yield { type: 'tool', trace: { ...trace, status: 'done', ms: tool.ms, detail: tool.detail } }
    }
    for (const chunk of answer.markdown.match(/\S+\s*|\n+/g) ?? []) {
      yield { type: 'token', text: chunk }
      if (live()) await sleep(chunk.includes('\n') ? 8 : 16)
    }
    yield { type: 'done', actions: answer.actions, requestId: REQUEST_ID(requestSeed + answer.markdown) }
  }

  const audienceOf = (): Audience => (env.ctx?.role === 'admin' ? 'admin' : 'customer')

  return {
    readImage: (input) =>
      (async function* (): AsyncGenerator<ReadImageEvent> {
        env.requireRole('customer')
        await guard('vision', msg('srv.api.feature.vision'))
        if (!input.type.startsWith('image/')) throw new ApiError('tidak_valid', msg('srv.api.fileNotImage'), { field: 'file' })
        if (input.size > MAX_IMAGE_BYTES) throw new ApiError('tidak_valid', msg('srv.api.imageTooLarge'), { field: 'file' })
        if (input.size === 0) throw new ApiError('tidak_valid', msg('srv.api.fileEmpty'), { field: 'file' })

        const steps = [t('ai.vision.step1'), t('ai.vision.step2'), t('ai.vision.step3'), t('ai.vision.step4')]
        const pauses = [350, 700, 650, 450]
        for (const [i, label] of steps.entries()) {
          yield { type: 'progress', step: i + 1, total: steps.length, label }
          if (live()) await sleep(pauses[i]!)
        }
        const items = detectItems(input, locale)
        const low = items.filter((i) => i.confidence < 0.7).length
        const base = t('ai.vision.summary', {
          types: t('ai.vision.types', { count: items.length }),
          pieces: t('ai.vision.pieces', { count: items.reduce((s, i) => s + i.qty, 0) }),
        })
        const summary = low ? `${base} ${t('ai.vision.lowConfidence', { count: low })}` : base
        yield { type: 'done', items, summary }
      })(),

    // tiruan tidak punya model suara: transkripsi hanya ada di backend asli (layar memakai Web Speech API peramban)
    transcribe: async () => {
      await server.delay()
      env.requireRole('customer')
      await guard('voice', msg('srv.api.feature.voice'))
      throw new ApiError('tidak_ditemukan', msg('srv.api.notAvailableYet'))
    },

    parseSpeech: async (transcript) => {
      await server.delay()
      env.requireRole('customer')
      await guard('voice', msg('srv.api.feature.voice'))
      if (!transcript.trim()) throw new ApiError('tidak_valid', msg('srv.api.noSpeech'), { field: 'transcript' })
      return clone(parseSpeech(transcript, locale))
    },

    parseIntent: async (text) => {
      await server.delay()
      env.requireRole('customer')
      await guard('intent', msg('srv.api.feature.intent'))
      if (!text.trim()) throw new ApiError('tidak_valid', msg('srv.api.intentEmpty'), { field: 'text' })
      return clone(parseIntent(text, server.now(), locale))
    },

    copilot: (scope, question) =>
      (async function* (): AsyncGenerator<CopilotEvent> {
        env.requireRole('customer', 'admin')
        await guard('copilot', msg('srv.api.feature.copilot'))
        if (!question.trim()) throw new ApiError('tidak_valid', msg('srv.api.questionEmpty'), { field: 'question' })
        const db = server.db()
        if (scope.kind === 'ops') {
          env.requireRole('admin')
          yield* stream(answerOps(db.plans, db.drivers, server.now(), locale), `ops:${question}`)
          return
        }
        const plan = env.readablePlan(db, scope.planId)
        const audience = audienceOf()
        const suggestions =
          audience === 'admin'
            ? Object.fromEntries(plan.routes.filter((r) => !r.driverId && r.status === 'menunggu_driver').map((r) => [r.id, env.suggestFor(db, plan, r)]))
            : undefined
        const answer = answerAboutPlan({ plan, question, audience, nowMs: server.now(), drivers: db.drivers, vehicles: db.vehicles, locale, suggestions })
        yield* stream(answer, `${plan.id}:${question}`)
      })(),

    insights: () =>
      (async function* (): AsyncGenerator<CopilotEvent> {
        env.requireRole('admin')
        await guard('copilot', msg('srv.api.feature.insights'))
        const db = server.db()
        yield* stream(buildInsights(db.plans, db.drivers, server.now(), locale), 'insights')
      })(),

    // Seperti backend asli: teks utuh sebagai satu `token` lalu `done` tanpa tindakan, dan disimpan sebagai `optionSet.report`.
    explainOptions: (planId) =>
      (async function* (): AsyncGenerator<CopilotEvent> {
        env.requireRole('customer')
        await guard('copilot', msg('srv.api.feature.report'))
        env.readablePlan(server.db(), planId)
        if (live()) await sleep(900)
        const set = env.readablePlan(server.db(), planId).optionSet
        if (!set || set.options.length === 0) throw new ApiError('tidak_valid', msg('srv.ai.report.noOptions'))
        const text = buildOptionReport(set, locale)
        server.mutate((db) => {
          const p = db.plans.find((x) => x.id === planId)
          // opsi yang berubah selagi laporan ditulis tidak boleh mewarisi laporan lama
          if (p?.optionSet && p.optionSet.inputHash === set.inputHash) p.optionSet.report = { text, locale, at: new Date(server.now()).toISOString() }
        })
        server.emit({ type: 'plans.changed', planId })
        yield { type: 'token', text }
        yield { type: 'done', actions: [], requestId: REQUEST_ID(`${planId}:${text}`) }
      })(),
  }
}
