import { DEFAULT_SETTINGS, isEffort, type Method, type Report } from '@loong/domain'
import type { Msg } from '@loong/i18n'

/*
 * Jawaban backend Go yang bentuknya lebih longgar daripada tipe di `@loong/domain`: slice kosong yang nil menjadi `null`,
 * angka nol atau waktu nol bisa dihilangkan (`omitempty`/`omitzero`), dan waktu nol menjadi "0001-01-01T00:00:00Z".
 * Layar menganggap tipe itu benar (mis. `profile.savedAddresses.length`), jadi bentuknya dirapikan SEKALI di perbatasan.
 * Hanya metode yang jawabannya perlu dirapikan ada di sini; metode lain sudah mengikuti tipe.
 */

type Rec = Record<string, unknown>

const isRec = (v: unknown): v is Rec => typeof v === 'object' && v !== null && !Array.isArray(v)

/** `null` (slice nil di Go) menjadi daftar kosong. */
const list = (v: unknown): unknown[] => (Array.isArray(v) ? v : [])

const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0)

const str = (v: unknown): string => (typeof v === 'string' ? v : '')

/** Waktu nol milik Go ("0001-01-01T00:00:00Z") berarti "belum pernah". */
const time = (v: unknown): string | undefined => (typeof v === 'string' && v !== '' && !v.startsWith('0001-01-01') ? v : undefined)

/** Menyalin `o` dengan `key` diganti nilai rapi; `undefined` membuang kuncinya supaya `?.` dan `in` di layar jujur. */
function set(o: Rec, key: string, value: unknown): Rec {
  const next = { ...o }
  if (value === undefined) delete next[key]
  else next[key] = value
  return next
}

const user = (v: unknown): unknown => {
  if (!isRec(v)) return v
  return { ...v, name: str(v.name), email: str(v.email), phone: str(v.phone), blocked: v.blocked === true, savedAddresses: list(v.savedAddresses) }
}

const userSummary = (v: unknown): unknown => {
  if (!isRec(v)) return v
  const base = {
    ...v,
    name: str(v.name),
    email: str(v.email),
    phone: str(v.phone),
    blocked: v.blocked === true, // `false` yang dihilangkan harus tetap false: filter "aktif" membandingkan dengan ===
    plansCount: num(v.plansCount),
    completedCount: num(v.completedCount),
    totalSpendRp: num(v.totalSpendRp),
    balanceRp: num(v.balanceRp),
  }
  return set(base, 'lastPlanAt', time(v.lastPlanAt))
}

const topUp = (v: unknown): unknown => {
  if (!isRec(v)) return v
  const base = { ...v, reference: str(v.reference), userName: str(v.userName) || str(v.userId), userEmail: str(v.userEmail) }
  return set(base, 'paidAt', time(v.paidAt))
}

/** Bagian pengaturan: saklar yang tidak ada (false yang dihilangkan) berarti mati; bagian yang tidak ada sama sekali memakai bawaan. */
const flags = (section: unknown, defaults: Record<string, boolean>): Record<string, boolean> =>
  isRec(section) ? Object.fromEntries(Object.keys(defaults).map((k) => [k, section[k] === true])) : { ...defaults }

const settings = (v: unknown): unknown => {
  const o = isRec(v) ? v : {}
  return { ...o, ai: flags(o.ai, { ...DEFAULT_SETTINGS.ai }), dispatch: flags(o.dispatch, { ...DEFAULT_SETTINGS.dispatch }) }
}

/** Pesan netral-bahasa yang sah: `key` tidak kosong; `params` hanya dipakai bila berupa objek. Selain itu dibuang. */
const msgOf = (v: unknown): Msg | undefined => {
  if (!isRec(v) || typeof v.key !== 'string' || v.key === '') return undefined
  return isRec(v.params) ? { key: v.key, params: v.params as Msg['params'] } : { key: v.key }
}

/**
 * Cara pencarian opsi. Tingkat yang tidak dikenal membuang seluruh `method` (layar tidak boleh menebak); bagian yang
 * tidak terjadi (`omitempty` di Go: nol, false, kosong) tetap tidak ada, sehingga ringkasan di layar hanya menyebut yang nyata.
 */
export const methodOf = (v: unknown): Method | undefined => {
  if (!isRec(v) || !isEffort(v.effort)) return undefined
  const m: Method = { effort: v.effort, trials: Math.max(0, Math.trunc(num(v.trials))) }
  const rounds = Math.trunc(num(v.rounds))
  if (rounds > 0) m.rounds = rounds
  if (v.polished === true) m.polished = true
  if (v.reviewer === 'ai' || v.reviewer === 'rules') m.reviewer = v.reviewer
  if (v.proven === true) m.proven = true
  if (typeof v.roadSource === 'string' && v.roadSource !== '') m.roadSource = v.roadSource
  return m
}

/** Laporan AI: tanpa teks berarti tidak ada laporan. Waktu nol atau hilang menjadi ''. */
export const reportOf = (v: unknown): Report | undefined => {
  if (!isRec(v) || typeof v.text !== 'string' || v.text.trim() === '') return undefined
  return { text: v.text, locale: str(v.locale), at: time(v.at) ?? '' }
}

const optionSet = (v: unknown): unknown => {
  if (!isRec(v)) return v
  return set(set(v, 'method', methodOf(v.method)), 'report', reportOf(v.report))
}

/** Plan dengan optionSet yang dirapikan; `optionSet: null` berarti belum ada. */
const plan = (v: unknown): unknown => {
  if (!isRec(v) || !('optionSet' in v)) return v
  return set(v, 'optionSet', isRec(v.optionSet) ? optionSet(v.optionSet) : undefined)
}

const FINANCE_NUMBERS = [
  'totalBalanceRp', 'usersWithBalance', 'pendingCount', 'pendingAmountRp', 'paidTodayRp', 'paidTodayCount', 'paid30dRp', 'paid30dCount',
] as const

const normalizers: Readonly<Record<string, (v: unknown) => unknown>> = {
  'account.profile': user,
  'account.update': user,
  'users.list': (v) => list(v).map(userSummary),
  'users.setBlocked': userSummary,
  'users.get': (v) => {
    if (!isRec(v)) return v
    return { ...v, user: user(v.user), summary: userSummary(v.summary), plans: list(v.plans) }
  },
  'finance.overview': (v) => {
    const o = isRec(v) ? v : {}
    return { ...o, ...Object.fromEntries(FINANCE_NUMBERS.map((k) => [k, num(o[k])])) }
  },
  'finance.topUps': (v) => list(v).map(topUp),
  'finance.transactions': list,
  'notifications.list': list,
  'settings.get': settings,
  'settings.update': settings,
  'plans.get': plan,
  'plans.createDraft': plan,
  'plans.saveDraft': plan,
  'plans.chooseOption': plan,
  'plans.confirm': plan,
  'plans.cancel': plan,
  'plans.reschedule': plan,
}

/** Merapikan jawaban sebuah metode; metode tanpa aturan dikembalikan apa adanya. */
export function normalizeResult(method: string, value: unknown): unknown {
  return normalizers[method]?.(value) ?? value
}

/** Satu peristiwa aliran. Hanya yang bentuknya longgar dirapikan; yang tak dikenal lewat apa adanya. */
function normalizeEvent(method: string, event: unknown): unknown {
  if (!isRec(event)) return event
  switch (method) {
    case 'plans.computeOptions':
      if (event.type === 'progress') {
        // `msg` lebih diutamakan daripada `label` di layar; `label` tetap string supaya cadangan selalu ada
        return set({ ...event, step: num(event.step), total: num(event.total), label: str(event.label) }, 'msg', msgOf(event.msg))
      }
      if (event.type === 'done') return { ...event, optionSet: optionSet(event.optionSet) }
      return event
    case 'ai.copilot':
    case 'ai.insights':
    case 'ai.explainOptions':
      // `actions` kosong di Go bisa menjadi `null`
      return event.type === 'done' ? { ...event, actions: list(event.actions) } : event
    default:
      return event
  }
}

/** Membungkus aliran backend: tiap peristiwa dirapikan; berhenti lebih awal ikut menutup aliran asalnya. */
export async function* normalizeStream(method: string, source: AsyncIterable<unknown>): AsyncGenerator<unknown> {
  for await (const event of source) yield normalizeEvent(method, event)
}
