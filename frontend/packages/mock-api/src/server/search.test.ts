import { beforeEach, describe, expect, it } from 'vitest'
import { ApiError, type ApiClient, type ComputeEvent, type CopilotEvent, type OptionSet, type Session, type Shipment } from '@loong/domain'
import { getPlace } from '@loong/gazetteer'
import { formatRupiah, renderMsg, type Locale } from '@loong/i18n'
import { makeItem } from '../seed/catalog'
import { createApi } from './api'
import { MockServer, memoryRawStorage } from './server'

/** Tingkat usaha pencarian dan laporan AI di backend tiruan: sisi RPC (progres, method, report); mesinnya diuji di engine/search.test.ts. */

const T0 = Date.parse('2026-10-20T03:00:00Z')
let server: MockServer
const DINA: Session = { role: 'customer', userId: 'usr-1', name: 'Dina Pramesti' }
const OTHER: Session = { role: 'customer', userId: 'usr-2', name: 'Fajar Nugroho' }
const ADMIN: Session = { role: 'admin', userId: 'admin-1', name: 'Rani Wulandari' }
const as = (ctx: Session | null, locale: Locale = 'en'): ApiClient => createApi(server, ctx, locale)

function wp(id: string) {
  const p = getPlace(id)!
  return { name: p.name, address: p.address, position: p.position, placeId: p.id, contactName: 'Tes', contactPhone: '081200000000' }
}
const shipment = (id: string, from: string, to: string, items = ['paket-kecil']): Shipment => ({ id, pickup: wp(from), dropoff: wp(to), items: items.map((k) => makeItem(k)) })
const FOUR = [
  shipment('a', 'kemang-village', 'plaza-senayan', ['kue']),
  shipment('b', 'kemang-raya', 'senayan-city', ['sepatu']),
  shipment('c', 'blok-m-square', 'pacific-place', ['dokumen']),
  shipment('d', 'kemang-village', 'kota-kasablanka', ['laptop']),
]

async function collect<T>(it: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = []
  for await (const e of it) out.push(e)
  return out
}

async function draft(shipments = FOUR, locale: Locale = 'en') {
  const api = as(DINA, locale)
  const plan = await api.plans.createDraft()
  await api.plans.saveDraft(plan.id, { shipments, draftStep: 3 })
  return { api, id: plan.id }
}

const doneOf = (events: ComputeEvent[]): OptionSet => {
  const done = events.at(-1)
  if (done?.type !== 'done') throw new Error('hitung tidak selesai')
  return done.optionSet
}

beforeEach(() => {
  server = new MockServer({ storage: memoryRawStorage(), latency: [0, 0], realNow: () => T0 })
  server.db()
})

describe('computeOptions menurut tingkat usaha', () => {
  it('tanpa argumen sama dengan fast: empat tahap, tanpa pesan, method fast', async () => {
    const { api, id } = await draft()
    const events = await collect(api.plans.computeOptions(id))
    const progress = events.filter((e) => e.type === 'progress')
    expect(progress.map((e) => e.step)).toEqual([1, 2, 3, 4])
    expect(progress.every((e) => e.msg === undefined)).toBe(true)
    const set = doneOf(events)
    expect(set.method).toMatchObject({ effort: 'fast', roadSource: 'estimate' })
    expect(set.method!.trials).toBeGreaterThan(0)
    expect(set.method!.polished).toBeUndefined()
    expect(set.method!.proven).toBeUndefined()

    const explicit = doneOf(await collect(api.plans.computeOptions(id, { effort: 'fast' })))
    expect(explicit.options).toEqual(set.options)
  })

  it('thorough: baris pencarian memakai step tahap 3 dan membawa msg; label = msg dirender', async () => {
    const { api, id } = await draft()
    const events = await collect(api.plans.computeOptions(id, { effort: 'thorough' }))
    const progress = events.filter((e): e is Extract<ComputeEvent, { type: 'progress' }> => e.type === 'progress')
    const withMsg = progress.filter((e) => e.msg)
    expect(withMsg.map((e) => e.msg!.key)).toEqual(['srv.engine.search.experiments', 'srv.engine.search.polish'])
    expect(withMsg.every((e) => e.step === 3)).toBe(true)
    // tahap besar tetap 1..4 berurutan; sub-langkah hanya mengulang step 3
    expect([...new Set(progress.map((e) => e.step))]).toEqual([1, 2, 3, 4])
    expect(progress.every((e, i) => i === 0 || e.step >= progress[i - 1]!.step)).toBe(true)
    for (const e of progress) {
      expect(e.label.length).toBeGreaterThan(0)
      if (e.msg) expect(e.label).toBe(renderMsg(e.msg, 'en'))
    }
    const set = doneOf(events)
    expect(set.method).toMatchObject({ effort: 'thorough', polished: true })
    expect(set.method!.reviewer).toBeUndefined()
  })

  it('reviewed: ronde dan peninjau aturan; label mengikuti bahasa permintaan', async () => {
    const { api, id } = await draft(FOUR, 'id')
    const events = await collect(api.plans.computeOptions(id, { effort: 'reviewed' }))
    const lines = events.filter((e): e is Extract<ComputeEvent, { type: 'progress' }> => e.type === 'progress' && !!e.msg)
    expect(lines.map((e) => e.msg!.key)).toContain('srv.engine.search.round')
    expect(lines.map((e) => e.msg!.key)).toContain('srv.engine.search.reviewRules')
    for (const e of lines) expect(e.label).toBe(renderMsg(e.msg!, 'id'))
    expect(lines.find((e) => e.msg!.key === 'srv.engine.search.round')!.label).toMatch(/^Ronde tinjauan \d$/)
    const set = doneOf(events)
    expect(set.method).toMatchObject({ effort: 'reviewed', reviewer: 'rules', polished: true })
    expect(set.method!.rounds).toBeGreaterThanOrEqual(1)
  })

  it('menghitung ulang menggantikan opsi, menghapus opsi terpilih dan laporan lama', async () => {
    const { api, id } = await draft()
    const first = doneOf(await collect(api.plans.computeOptions(id)))
    await api.plans.chooseOption(id, first.options[0]!.id)
    await collect(api.ai.explainOptions(id))
    let plan = await api.plans.get(id)
    expect(plan.selectedOptionId).toBeDefined()
    expect(plan.optionSet?.report).toBeDefined()

    const second = doneOf(await collect(api.plans.computeOptions(id, { effort: 'thorough' })))
    plan = await api.plans.get(id)
    expect(plan.optionSet?.method?.effort).toBe('thorough')
    expect(plan.optionSet).toEqual(second)
    expect(plan.selectedOptionId).toBeUndefined()
    expect(plan.optionSet?.report).toBeUndefined()
    expect(plan.status).toBe('opsi_siap')
  })

  it('tingkat yang tidak dikenal ditolak: tidak_valid, kolom effort, srv.api.badEffort', async () => {
    const { api, id } = await draft()
    const err = await collect(api.plans.computeOptions(id, { effort: 'turbo' as never })).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ code: 'tidak_valid', field: 'effort', msg: { key: 'srv.api.badEffort' } })
  })

  it('plan yang sudah dikonfirmasi tidak bisa dihitung ulang lewat tingkat mana pun', async () => {
    const { api, id } = await draft()
    const set = doneOf(await collect(api.plans.computeOptions(id)))
    await api.plans.chooseOption(id, set.options[0]!.id)
    await api.plans.confirm(id, 'saldo')
    await expect(collect(api.plans.computeOptions(id, { effort: 'thorough' }))).rejects.toMatchObject({ code: 'bentrok' })
  })
})

describe('ai.explainOptions', () => {
  const textOf = (events: CopilotEvent[]) => events.flatMap((e) => (e.type === 'token' ? [e.text] : [])).join('')

  it('satu token berisi seluruh teks lalu done tanpa tindakan; disimpan sebagai optionSet.report', async () => {
    const { api, id } = await draft()
    await collect(api.plans.computeOptions(id))
    const events = await collect(api.ai.explainOptions(id))
    expect(events.map((e) => e.type)).toEqual(['token', 'done'])
    const done = events[1]!
    expect(done).toMatchObject({ type: 'done', actions: [] })
    const text = textOf(events)
    expect(text.length).toBeGreaterThan(40)

    const report = (await api.plans.get(id)).optionSet?.report
    expect(report).toMatchObject({ text, locale: 'en' })
    expect(Number.isNaN(Date.parse(report!.at))).toBe(false)
  })

  it('deterministik, dan tidak memuat Markdown (teks biasa)', async () => {
    const { api, id } = await draft()
    await collect(api.plans.computeOptions(id))
    const a = textOf(await collect(api.ai.explainOptions(id)))
    const b = textOf(await collect(api.ai.explainOptions(id)))
    expect(a).toBe(b)
    expect(a).not.toMatch(/[*#`<>]/)
  })

  it('setiap nominal rupiah di teks berasal dari angka opsi; tidak mengarang', async () => {
    const { api, id } = await draft()
    const set = doneOf(await collect(api.plans.computeOptions(id, { effort: 'thorough' })))
    const text = textOf(await collect(api.ai.explainOptions(id)))
    const allowed = new Set<string>([formatRupiah(set.baseline.costRp, 'en')])
    for (const o of set.options) {
      allowed.add(formatRupiah(o.totals.costRp, 'en'))
      allowed.add(formatRupiah(Math.abs(o.savings.costRp), 'en'))
    }
    const amounts = text.match(/Rp \d{1,3}(?:,\d{3})*/g) ?? []
    expect(amounts.length).toBeGreaterThan(1)
    for (const a of amounts) expect(allowed.has(a), a).toBe(true)
    // yang disebut direkomendasikan memang opsi yang direkomendasikan
    const main = set.options.find((o) => o.recommended)!
    expect(text).toContain(renderMsg(main.label, 'en'))
    expect(text).toContain(formatRupiah(main.totals.costRp, 'en'))
  })

  it('jujur soal bukti: tiruan tidak pernah membuktikan optimal, jadi teks menyebut "not proven optimal"', async () => {
    const { api, id } = await draft()
    for (const effort of ['fast', 'thorough', 'reviewed'] as const) {
      await collect(api.plans.computeOptions(id, { effort }))
      const text = textOf(await collect(api.ai.explainOptions(id)))
      expect(text).toContain('not proven optimal')
      expect(text).not.toContain('confirmed they are optimal')
    }
  })

  it('mengikuti bahasa permintaan dan mencatatnya di report.locale', async () => {
    const { api, id } = await draft(FOUR, 'id')
    await collect(api.plans.computeOptions(id))
    const text = textOf(await collect(api.ai.explainOptions(id)))
    expect(text).toContain('Ada ')
    expect(text).toContain('belum terbukti optimal')
    expect((await api.plans.get(id)).optionSet?.report?.locale).toBe('id')
  })

  it('belum ada opsi: tidak_valid dengan srv.ai.report.noOptions', async () => {
    const { api, id } = await draft()
    const err = await collect(api.ai.explainOptions(id)).catch((e: unknown) => e)
    expect(err).toMatchObject({ code: 'tidak_valid', msg: { key: 'srv.ai.report.noOptions' } })
  })

  it('hanya pemilik plan (pengirim): pengirim lain, admin, dan tamu ditolak', async () => {
    const { api, id } = await draft()
    await collect(api.plans.computeOptions(id))
    await expect(collect(as(OTHER).ai.explainOptions(id))).rejects.toMatchObject({ code: 'ditolak' })
    await expect(collect(as(ADMIN).ai.explainOptions(id))).rejects.toMatchObject({ code: 'ditolak' })
    await expect(collect(as(null).ai.explainOptions(id))).rejects.toMatchObject({ code: 'ditolak' })
  })

  it('dimatikan admin atau gangguan tiruan: ditolak atau galat layanan, plan tidak diubah', async () => {
    const { api, id } = await draft()
    await collect(api.plans.computeOptions(id))
    await as(ADMIN).settings.update({ ai: { copilot: false } })
    await expect(collect(api.ai.explainOptions(id))).rejects.toMatchObject({ code: 'ditolak' })
    await as(ADMIN).settings.update({ ai: { copilot: true } })
    server.mutate((db) => {
      db.fault = 'error'
    })
    await expect(collect(api.ai.explainOptions(id))).rejects.toMatchObject({ code: 'layanan' })
    server.mutate((db) => {
      db.fault = 'none'
    })
    expect((await api.plans.get(id)).optionSet?.report).toBeUndefined()
  })
})
