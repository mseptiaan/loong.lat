import {
  ApiError,
  DEFAULT_EFFORT,
  EFFORTS,
  formatList,
  type ComputeEvent,
  type ComputeOptions,
  type CopilotEvent,
  type Effort,
  type Method,
  type OptionSet,
  type Plan,
  type Report,
} from '@loong/domain'
import { LOCALES, LOCALE_NAME, getLocale, msg, renderMsg, resolveTemplate, type Locale, type Msg } from '@loong/i18n'
import { t } from '../../i18n'

/*
 * Logika langkah 3 yang bukan tampilan: pilihan kedalaman pencarian, baris progres bertahap, satu kalimat tentang cara opsi
 * ditemukan, dan keadaan ringkasan AI. Dipisah dari komponen supaya bisa diuji di Vitest (tanpa DOM), seperti model.ts.
 */

/* ======================================================================== pilihan kedalaman pencarian */

/** `chosen` = yang dipilih di layar; `applied` = tingkat yang menghasilkan opsi yang sedang tampil. */
export interface SearchPick {
  chosen: Effort
  applied: Effort
}

/** Opsi yang sudah ada menentukan pilihan awal (data lama tanpa `method` dianggap `fast`); tanpa opsi: bawaan. */
export function initialPick(set: OptionSet | null | undefined): SearchPick {
  const applied = set?.method?.effort ?? DEFAULT_EFFORT
  return { chosen: applied, applied }
}

/** Mengganti pilihan; nilai di luar tiga tingkat diabaikan. */
export function choose(pick: SearchPick, effort: Effort): SearchPick {
  return EFFORTS.includes(effort) && effort !== pick.chosen ? { ...pick, chosen: effort } : pick
}

/** Pencarian selesai dengan `effort`: layar kembali selaras (tombol "cari lagi" hilang). */
export const settled = (effort: Effort): SearchPick => ({ chosen: effort, applied: effort })

/** Tombol "cari lagi" hanya muncul bila pilihan berbeda dari yang menghasilkan opsi yang tampil. */
export const needsSearch = (pick: SearchPick): boolean => pick.chosen !== pick.applied

/** Teks satu pilihan. Pilihan "reviewed" tetap ditawarkan saat AI dimatikan, tetapi keterangannya jujur: peninjau bawaan yang bekerja. */
export function effortText(effort: Effort, aiOn: boolean): { label: string; caption: string; time: string } {
  switch (effort) {
    case 'fast':
      return { label: t('dash.wizard.depth.fast'), caption: t('dash.wizard.depth.fastCaption'), time: t('dash.wizard.depth.fastTime') }
    case 'thorough':
      return { label: t('dash.wizard.depth.thorough'), caption: t('dash.wizard.depth.thoroughCaption'), time: t('dash.wizard.depth.thoroughTime') }
    case 'reviewed':
      return {
        label: t('dash.wizard.depth.reviewed'),
        caption: t(aiOn ? 'dash.wizard.depth.reviewedCaption' : 'dash.wizard.depth.reviewedCaptionRules'),
        time: t('dash.wizard.depth.reviewedTime'),
      }
  }
}

/** Keterangan di kartu progres: makin dalam makin lama. */
export function calcCopy(effort: Effort): { title: string; hint: string } {
  switch (effort) {
    case 'fast':
      return { title: t('dash.wizard.calcTitle'), hint: t('dash.wizard.calcHint') }
    case 'thorough':
      return { title: t('dash.wizard.calcTitleDeep'), hint: t('dash.wizard.calcHintThorough') }
    case 'reviewed':
      return { title: t('dash.wizard.calcTitleDeep'), hint: t('dash.wizard.calcHintReviewed') }
  }
}

/* ======================================================================== progres bertahap */

/** Empat tahap besar di layar; sub-langkah di dalam satu tahap mengulang `step` yang sama. */
export const STAGES = 4

type ProgressEvent = Extract<ComputeEvent, { type: 'progress' }>

/** Baris progres per tahap: indeks 0 = tahap 1. Baris pertama sebuah tahap adalah judulnya, sisanya sub-langkah. */
export interface ProgressState {
  step: number
  total: number
  stages: string[][]
}

export const initialProgress = (): ProgressState => ({ step: 0, total: STAGES, stages: [] })

/** `{n}` dari backend juga dipakai sebagai `count` agar jamak ("1 attempt", "3 attempts") mengikuti katalog. */
const withCount = (m: Msg): Msg => (m.params && typeof m.params.n === 'number' && m.params.count === undefined ? { ...m, params: { ...m.params, count: m.params.n } } : m)

/**
 * Teks satu baris progres: `msg` didahulukan (dirender dalam bahasa layar); kunci yang tidak dikenal katalog (backend lebih baru
 * daripada layar) jatuh ke `label`, bukan menampilkan kunci mentah.
 */
export function progressText(e: ProgressEvent): string {
  if (e.msg) {
    const m = withCount(e.msg)
    const count = typeof m.params?.count === 'number' ? m.params.count : undefined
    if (resolveTemplate(getLocale(), m.key, count) !== undefined) return renderMsg(m)
  }
  return e.label
}

/** Menambahkan satu peristiwa ke tahapnya; baris kembar berturut-turut dan baris kosong dibuang. */
export function applyProgress(state: ProgressState, e: ProgressEvent): ProgressState {
  const text = progressText(e).trim()
  const index = Math.max(0, Math.trunc(e.step) - 1)
  const stages = state.stages.map((lines) => [...lines])
  while (stages.length <= index) stages.push([])
  if (text && stages[index]!.at(-1) !== text) stages[index]!.push(text)
  return { step: Math.max(state.step, Math.trunc(e.step)), total: e.total > 0 ? e.total : state.total, stages }
}

/* ======================================================================== menjalankan pencarian */

export interface SearchDeps {
  /** Menyimpan draft terbaru dan mengembalikan id plan. */
  ensureSaved(): Promise<string>
  getPlan(id: string): Promise<Plan>
  compute(id: string, opts: ComputeOptions): AsyncIterable<ComputeEvent>
}

export type SearchOutcome =
  /** Plan sudah punya opsi dan tidak diminta menghitung ulang: dipakai apa adanya (masuk lagi ke langkah ini). */
  | { kind: 'reused'; set: OptionSet }
  /** Opsi baru dari perhitungan: menggantikan yang lama dan menghapus pilihan di backend. */
  | { kind: 'computed'; set: OptionSet }
  /** `current()` berubah menjadi false (komponen ditutup atau pencarian lain dimulai): hasil dibuang. */
  | { kind: 'cancelled' }

/**
 * Satu kali pencarian opsi. `force` adalah jalur hitung ulang yang eksplisit ("Cari lagi"): opsi yang sudah ada TIDAK dipakai, dan
 * `plans.computeOptions` dijalankan dengan `effort` yang dipilih. Tanpa `force`, opsi yang sudah tersimpan di plan dipakai lebih dulu,
 * dan hanya bila belum ada, opsi dihitung (itulah perhitungan otomatis saat masuk langkah 3, dengan tingkat bawaan).
 * Galat dilempar ke pemanggil; aliran yang berakhir tanpa hasil juga galat (layar tidak boleh menunggu selamanya).
 */
export async function searchOptions(
  deps: SearchDeps,
  effort: Effort,
  force: boolean,
  hooks: { current(): boolean; onProgress(progress: ProgressState): void },
): Promise<SearchOutcome> {
  const id = await deps.ensureSaved()
  if (!force) {
    const existing = await deps.getPlan(id)
    if (!hooks.current()) return { kind: 'cancelled' }
    if (existing.optionSet) return { kind: 'reused', set: existing.optionSet }
  }
  let progress = initialProgress()
  for await (const e of deps.compute(id, { effort })) {
    if (!hooks.current()) return { kind: 'cancelled' }
    if (e.type === 'progress') {
      progress = applyProgress(progress, e)
      hooks.onProgress(progress)
    } else {
      return { kind: 'computed', set: e.optionSet }
    }
  }
  if (!hooks.current()) return { kind: 'cancelled' }
  throw new ApiError('layanan', msg('srv.api.serviceError'))
}

/* ======================================================================== cara opsi ditemukan */

/** Bagian-bagian kalimat dari `Method`; yang tidak ada dihilangkan, dan "optimal" hanya bila `proven`. */
export function methodParts(method: Method | undefined): string[] {
  if (!method) return []
  const parts: string[] = []
  if (method.trials > 0) parts.push(t('dash.wizard.method.trials', { count: method.trials }))
  if (method.rounds && method.rounds > 0) parts.push(t('dash.wizard.method.rounds', { count: method.rounds }))
  if (method.polished) parts.push(t('dash.wizard.method.polished'))
  if (method.reviewer === 'ai') parts.push(t('dash.wizard.method.reviewerAi'))
  else if (method.reviewer === 'rules') parts.push(t('dash.wizard.method.reviewerRules'))
  if (method.proven) parts.push(t('dash.wizard.method.proven'))
  if (method.roadSource === 'mapbox' || method.roadSource === 'mapbox-cache') parts.push(t('dash.wizard.method.roadsReal'))
  else if (method.roadSource === 'estimate') parts.push(t('dash.wizard.method.roadsEstimate'))
  return parts
}

/** Satu kalimat tenang di bawah opsi, atau null bila tidak ada yang bisa dikatakan dengan jujur. */
export function methodLine(method: Method | undefined): string | null {
  const parts = methodParts(method)
  return parts.length ? t('dash.wizard.method.line', { parts: formatList(parts) }) : null
}

/* ======================================================================== ringkasan AI */

export type ReportProblemKind = 'noOptions' | 'unverified' | 'failed'

export interface ReportProblem {
  kind: ReportProblemKind
  message: string
}

/** Galat ringkasan: dua kunci khusus dari backend dibedakan dari galat AI umum (mati, dimatikan admin, jaringan). */
export function reportProblem(err: unknown): ReportProblem {
  const key = err instanceof ApiError ? err.msg?.key : undefined
  const message = err instanceof ApiError ? (err.msg ? renderMsg(err.msg) : err.message) : err instanceof Error ? err.message : t('shell.error.generic')
  if (key === 'srv.ai.report.noOptions') return { kind: 'noOptions', message }
  if (key === 'srv.ai.report.unverified') return { kind: 'unverified', message }
  return { kind: 'failed', message }
}

/** `previous` menjaga ringkasan lama tetap terbaca selagi dibuat ulang, dan bila pembuatan ulang gagal. */
export type ReportState =
  | { kind: 'idle' }
  | { kind: 'loading'; previous?: Report }
  | { kind: 'ready'; report: Report }
  | { kind: 'error'; problem: ReportProblem; previous?: Report }

export type ReportAction = { type: 'start' } | { type: 'done'; report: Report } | { type: 'fail'; error: unknown }

/** Ringkasan yang sudah tersimpan di OptionSet langsung tampil. */
export const initialReport = (report: Report | undefined): ReportState => (report ? { kind: 'ready', report } : { kind: 'idle' })

const reportOf = (s: ReportState): Report | undefined => (s.kind === 'ready' ? s.report : s.kind === 'loading' || s.kind === 'error' ? s.previous : undefined)

export function reportReducer(state: ReportState, action: ReportAction): ReportState {
  switch (action.type) {
    case 'start':
      return state.kind === 'loading' ? state : { kind: 'loading', previous: reportOf(state) }
    case 'done':
      return { kind: 'ready', report: action.report }
    case 'fail':
      return { kind: 'error', problem: reportProblem(action.error), previous: reportOf(state) }
  }
}

/**
 * Membaca aliran `ai.explainOptions`: server mengirim seluruh teks sebagai satu `token` (sudah diperiksa terhadap angka), tetapi
 * beberapa `token` tetap digabung. Mengembalikan null bila `current()` berubah menjadi false di tengah jalan (komponen ditutup atau
 * dijalankan ulang): keluar dari `for await` menutup aliran. Teks kosong bukan ringkasan.
 */
export async function readReport(
  stream: AsyncIterable<CopilotEvent>,
  locale: Locale,
  opts: { current?: () => boolean; now?: () => Date } = {},
): Promise<Report | null> {
  const current = opts.current ?? (() => true)
  let text = ''
  for await (const e of stream) {
    if (!current()) return null
    if (e.type === 'token') text += e.text
  }
  if (!current()) return null
  if (text.trim() === '') throw new ApiError('layanan', msg('srv.api.serviceError'))
  return { text, locale, at: (opts.now ?? (() => new Date()))().toISOString() }
}

/** Paragraf teks biasa (pemisah baris kosong); tidak pernah diurai sebagai HTML atau Markdown. */
export const reportParagraphs = (text: string): string[] =>
  text
    .split(/\n\s*\n/)
    .map((p) => p.trim())
    .filter(Boolean)

/** Nama bahasa ringkasan; kode yang tidak dikenal ditulis apa adanya (huruf besar), kosong bila tidak ada. */
export function reportLanguage(locale: string): string {
  if ((LOCALES as readonly string[]).includes(locale)) return LOCALE_NAME[locale as Locale]
  return locale.trim().toUpperCase()
}
