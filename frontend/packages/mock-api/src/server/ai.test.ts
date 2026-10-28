import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError, RECOMMEND_MIN_SCORE, type ApiClient, type ComputeEvent, type CopilotEvent, type Session, type Shipment } from '@loong/domain'
import { getPlace } from '@loong/gazetteer'
import { LOCALES, formatRupiah, renderMsg, type Locale } from '@loong/i18n'
import { makeItem } from '../seed/catalog'
import { createApi } from './api'
import { MockServer, memoryRawStorage } from './server'

const T0 = Date.parse('2026-10-20T03:00:00Z') // 10.00 WIB
let server: MockServer
const DINA: Session = { role: 'customer', userId: 'usr-1', name: 'Dina Pramesti' }
const OTHER: Session = { role: 'customer', userId: 'usr-2', name: 'Fajar Nugroho' }
const ADMIN: Session = { role: 'admin', userId: 'admin-1', name: 'Rani Wulandari' }
const BUDI: Session = { role: 'driver', userId: 'drv-1', name: 'Budi Santoso' }
const as = (ctx: Session | null, locale: Locale = 'en'): ApiClient => createApi(server, ctx, locale)

function wp(id: string) {
  const p = getPlace(id)!
  return { name: p.name, address: p.address, position: p.position, placeId: p.id, contactName: 'Tes', contactPhone: '081200000000' }
}
const shipment = (id: string, from: string, to: string, items = ['paket-kecil']): Shipment => ({ id, pickup: wp(from), dropoff: wp(to), items: items.map((k) => makeItem(k)) })

async function collect<T>(it: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = []
  for await (const e of it) out.push(e)
  return out
}

/** Menjalankan Copilot dan merangkum aliran menjadi teks + jejak + tindakan. */
async function ask(api: ApiClient, scope: Parameters<ApiClient['ai']['copilot']>[0], q: string) {
  const events = await collect(api.ai.copilot(scope, q))
  const text = events.flatMap((e) => (e.type === 'token' ? [e.text] : [])).join('')
  const done = events.at(-1)
  return { events, text, actions: done?.type === 'done' ? done.actions : [], traces: events.filter((e): e is Extract<CopilotEvent, { type: 'tool' }> => e.type === 'tool') }
}

async function draftWithOptions(ctx: Session = DINA, shipments = [shipment('s1', 'kemang-village', 'plaza-senayan'), shipment('s2', 'kemang-raya', 'senayan-city')], locale: Locale = 'en') {
  const api = as(ctx, locale)
  const draft = await api.plans.createDraft()
  await api.plans.saveDraft(draft.id, { shipments, draftStep: 3 })
  const events = await collect<ComputeEvent>(api.plans.computeOptions(draft.id))
  const done = events.at(-1)
  if (done?.type !== 'done') throw new Error('hitung tidak selesai')
  return { api, id: draft.id, set: done.optionSet }
}

beforeEach(() => {
  server = new MockServer({ storage: memoryRawStorage(), latency: [0, 0], realNow: () => T0 })
  server.db()
})

describe('pengaturan fitur AI', () => {
  it('bawaan menyala; hanya admin yang boleh mengubah; peristiwa dipancarkan', async () => {
    expect((await as(DINA).settings.get()).ai).toEqual({ vision: true, voice: true, intent: true, copilot: true })
    await expect(as(DINA).settings.update({ ai: { voice: false } })).rejects.toMatchObject({ code: 'ditolak' })
    const events: string[] = []
    server.subscribe((e) => events.push(e.type))
    const next = await as(ADMIN).settings.update({ ai: { voice: false } })
    expect(next.ai.voice).toBe(false)
    expect(next.ai.vision).toBe(true) // tidak mengubah yang lain
    expect(events).toContain('settings.changed')
    expect((await as(DINA).settings.get()).ai.voice).toBe(false)
  })

  it('fitur yang dimatikan memberi pesan yang menunjuk jalan keluar manual', async () => {
    await as(ADMIN).settings.update({ ai: { vision: false, voice: false, intent: false, copilot: false } })
    await expect(collect(as(DINA).ai.readImage({ name: 'a.jpg', type: 'image/jpeg', size: 1000 }))).rejects.toThrow('Fill it in manually')
    await expect(as(DINA).ai.parseSpeech('satu galon')).rejects.toThrow('turned off')
    await expect(as(DINA).ai.parseIntent('hemat')).rejects.toBeInstanceOf(ApiError)
    const { id } = await draftWithOptions()
    await expect(ask(as(DINA), { kind: 'plan', planId: id }, 'kenapa?')).rejects.toThrow('turned off')
  })

  it('pesan galat membawa Msg dengan nama fitur bersarang; dirender per bahasa', async () => {
    await as(ADMIN).settings.update({ ai: { voice: false } })
    const err = await as(DINA).ai.parseSpeech('satu galon').catch((e: ApiError) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect((err as ApiError).msg).toEqual({ key: 'srv.api.aiDisabled', params: { feature: { key: 'srv.api.feature.voice' } } })
    expect(renderMsg((err as ApiError).msg!, 'en')).toBe('Voice input has been turned off by the admin. Fill it in manually for now.')
    expect(renderMsg((err as ApiError).msg!, 'id')).toBe('Input suara sedang dimatikan oleh admin. Isi manual dulu.')
  })
})

describe('baca gambar, suara, dan maksud lewat API', () => {
  it('readImage mengalir bertahap lalu menghasilkan barang; menolak berkas yang salah', async () => {
    const events = await collect(as(DINA).ai.readImage({ name: 'foto-kue.jpg', type: 'image/jpeg', size: 90_000 }))
    expect(events.filter((e) => e.type === 'progress').map((e) => (e.type === 'progress' ? e.step : 0))).toEqual([1, 2, 3, 4])
    const done = events.at(-1)
    expect(done?.type).toBe('done')
    if (done?.type === 'done') {
      expect(done.items[0]!.key).toBe('kue')
      expect(done.summary).toMatch(/^Detected \d+ (type|types) of items? \(\d+ pieces?\)\./)
    }
    await expect(collect(as(DINA).ai.readImage({ name: 'a.pdf', type: 'application/pdf', size: 1000 }))).rejects.toMatchObject({ field: 'file' })
    await expect(collect(as(DINA).ai.readImage({ name: 'a.jpg', type: 'image/jpeg', size: 9 * 1024 * 1024 }))).rejects.toThrow('8 MB')
    await expect(collect(as(DINA).ai.readImage({ name: 'a.jpg', type: 'image/jpeg', size: 0 }))).rejects.toMatchObject({ msg: { key: 'srv.api.fileEmpty' } })
    await expect(collect(as(ADMIN).ai.readImage({ name: 'a.jpg', type: 'image/jpeg', size: 1000 }))).rejects.toMatchObject({ code: 'ditolak' })
  })

  it('parseSpeech dan parseIntent memakai jam virtual; masukan kosong ditolak per kolom', async () => {
    const speech = await as(DINA).ai.parseSpeech('dua kardus sedang dan satu laptop')
    expect(speech.items.map((i) => i.key)).toEqual(['kardus-sedang', 'laptop'])
    await expect(as(DINA).ai.parseSpeech('   ')).rejects.toMatchObject({ field: 'transcript' })
    const intent = await as(DINA).ai.parseIntent('paling hemat tapi sampai sebelum jam 5')
    expect(intent.chips.map((c) => c.kind)).toEqual(['prioritas', 'batas_waktu'])
    // jam virtual 10.00 WIB -> 17.00 WIB hari yang sama = 10.00 UTC
    expect(intent.chips[1]!.deadline).toBe('2026-10-20T10:00:00.000Z')
  })

  it('transcribe: tiruan tidak bisa mentranskripsi, jadi menolak "belum tersedia" (layar memakai Web Speech API)', async () => {
    const audio = { type: 'audio/wav', size: 4_000, data: new ArrayBuffer(8) }
    await expect(as(DINA).ai.transcribe(audio)).rejects.toMatchObject({ code: 'tidak_ditemukan', msg: { key: 'srv.api.notAvailableYet' } })
    await expect(as(ADMIN).ai.transcribe(audio)).rejects.toMatchObject({ code: 'ditolak' }) // peran tetap diperiksa
  })

  it('transcribe mengikuti saklar suara dari admin dan mode kegagalan tiruan', async () => {
    const audio = { type: 'audio/wav', size: 4_000 }
    await as(ADMIN).settings.update({ ai: { voice: false } })
    await expect(as(DINA).ai.transcribe(audio)).rejects.toMatchObject({ code: 'ditolak', msg: { key: 'srv.api.aiDisabled' } })
    await as(ADMIN).settings.update({ ai: { voice: true } })
    await as(ADMIN).demo.setFault('offline')
    await expect(as(DINA).ai.transcribe(audio)).rejects.toMatchObject({ code: 'jaringan' })
  })
})

/** Teks yang dicari tiap bahasa (jawaban Copilot dibuat server dalam bahasa permintaan). */
const WORDS: Record<Locale, Record<string, string>> = {
  en: {
    why: 'Why was this option recommended?',
    compare: 'Compare all options',
    faster: 'What if I want it faster?',
    whyOne: 'Why is there only one option?',
    offTopic: "What's the weather like tomorrow?",
    when: 'When will the parcel arrive?',
    bestDriver: 'Who is the best driver?',
    late: 'Why is it late?',
    todo: 'What do I need to do?',
    optionHeader: 'Option |',
    alreadyFastest: 'already the fastest',
    onlyOne: 'Only **one** option',
    nothingToCombine: 'nothing to combine',
    cannotAnswer: "can't answer that question",
    onTheWay: 'is on the way',
    etaTitle: 'estimated arrival',
    suggestion: 'My suggestion is',
    lateWord: 'late',
    contact: 'Contact',
    nothingToDo: "don't need to do anything",
    order: 'Here is the order I suggest',
    summary: 'Summary of the last 7 days',
    noChange: 'does not change any data',
  },
  id: {
    why: 'Kenapa opsi ini yang disarankan?',
    compare: 'Bandingkan semua opsi',
    faster: 'Bagaimana kalau saya ingin lebih cepat?',
    whyOne: 'Kenapa hanya ada satu opsi?',
    offTopic: 'cuaca besok bagaimana?',
    when: 'Kapan paket sampai?',
    bestDriver: 'Siapa driver terbaik?',
    late: 'Kenapa terlambat?',
    todo: 'Apa yang perlu saya lakukan?',
    optionHeader: 'Opsi |',
    alreadyFastest: 'sudah yang paling cepat',
    onlyOne: 'Hanya ada **satu** opsi',
    nothingToCombine: 'tidak ada yang bisa digabung',
    cannotAnswer: 'belum bisa saya jawab',
    onTheWay: 'sedang di jalan',
    etaTitle: 'perkiraan tiba',
    suggestion: 'Saran saya',
    lateWord: 'terlambat',
    contact: 'Hubungi',
    nothingToDo: 'tidak perlu melakukan apa pun',
    order: 'Urutan yang saya sarankan',
    summary: 'Ringkasan 7 hari terakhir',
    noChange: 'tidak mengubah data apa pun',
  },
}

describe.each(LOCALES)('Copilot (%s)', (locale) => {
  const w = WORDS[locale]
  const asL = (ctx: Session | null) => as(ctx, locale)

  it('menjelaskan opsi dengan angka yang sama persis dengan kartu opsi', async () => {
    const { api, id, set } = await draftWithOptions(DINA, undefined, locale)
    const rec = set.options.find((o) => o.recommended) ?? set.options[0]!
    const { text, traces, events } = await ask(api, { kind: 'plan', planId: id }, w.why!)
    expect(text).toContain(renderMsg(rec.label, locale))
    expect(text).toContain(formatRupiah(rec.totals.costRp, locale))
    for (const r of rec.reasons) expect(text).toContain(renderMsg(r, locale))
    // angka biaya muncul sekali (dari alasan engine), tidak diulang oleh Copilot
    expect(text.split(formatRupiah(rec.totals.costRp, locale)).length - 1).toBe(1)
    // jejak alat: tiap alat muncul sebagai running lalu done, berurutan, dengan durasi dan detail
    const done = traces.filter((t) => t.trace.status === 'done')
    expect(done.length).toBeGreaterThanOrEqual(2)
    for (const t of done) {
      expect(t.trace.ms).toBeGreaterThan(0)
      expect(t.trace.detail).toBeTruthy()
    }
    expect(traces[0]!.trace.status).toBe('running')
    // urutan peristiwa: semua alat selesai sebelum token pertama; `done` terakhir
    const firstToken = events.findIndex((e) => e.type === 'token')
    expect(events.slice(0, firstToken).every((e) => e.type === 'tool')).toBe(true)
    expect(events.at(-1)?.type).toBe('done')
  })

  it('perbandingan memuat satu baris per opsi dan angka tiap opsi', async () => {
    const { api, id, set } = await draftWithOptions(DINA, undefined, locale)
    const { text } = await ask(api, { kind: 'plan', planId: id }, w.compare!)
    const rows = text.split('\n').filter((l) => l.startsWith('|') && !l.includes('---') && !l.includes(w.optionHeader!))
    expect(rows).toHaveLength(set.options.length)
    for (const o of set.options) expect(text).toContain(formatRupiah(o.totals.costRp, locale))
  })

  it('"lebih cepat" mengusulkan opsi tercepat sebagai tindakan; pilihan tidak otomatis diterapkan', async () => {
    const { api, id, set } = await draftWithOptions(DINA, undefined, locale)
    const fastest = [...set.options].sort((a, b) => a.totals.avgDeliveryMin - b.totals.avgDeliveryMin)[0]!
    const chosen = set.options.find((o) => o.recommended) ?? set.options[0]!
    const { text, actions } = await ask(api, { kind: 'plan', planId: id }, w.faster!)
    if (fastest.id === chosen.id) {
      expect(text).toContain(w.alreadyFastest)
      expect(actions).toHaveLength(0)
    } else {
      expect(text).toContain(renderMsg(fastest.label, locale))
      expect(actions[0]).toMatchObject({ kind: 'pilih_opsi', optionId: fastest.id })
      expect(actions[0]!.label).toBe(`${locale === 'en' ? 'Choose' : 'Pilih'} ${renderMsg(fastest.label, locale)}`)
    }
    // Copilot tidak mengubah data: opsi terpilih di server tetap kosong
    expect((await api.plans.get(id)).selectedOptionId).toBeUndefined()
  })

  it('"kenapa hanya satu opsi?" dijawab dengan alasan tidak ada yang bisa digabung', async () => {
    const { api, id, set } = await draftWithOptions(DINA, [shipment('s1', 'kemang-village', 'plaza-senayan')], locale)
    expect(set.options).toHaveLength(1)
    const { text } = await ask(api, { kind: 'plan', planId: id }, w.whyOne!)
    expect(text).toContain(w.onlyOne)
    expect(text).toContain(w.nothingToCombine)
  })

  it('pertanyaan di luar kemampuan dijawab jujur dengan contoh yang bisa dijawab', async () => {
    const { api, id } = await draftWithOptions(DINA, undefined, locale)
    const { text } = await ask(api, { kind: 'plan', planId: id }, w.offTopic!)
    expect(text).toContain(w.cannotAnswer)
    expect(text).toContain(w.compare)
  })

  it('pengirim tidak bisa bertanya tentang plan orang lain', async () => {
    const { id } = await draftWithOptions(DINA, undefined, locale)
    await expect(ask(asL(OTHER), { kind: 'plan', planId: id }, 'status?')).rejects.toMatchObject({ code: 'ditolak' })
    await expect(ask(asL(BUDI), { kind: 'plan', planId: id }, 'status?')).rejects.toMatchObject({ code: 'ditolak' })
  })

  it('admin: plan berjalan -> status memakai posisi driver; plan menunggu -> saran driver + tindakan terapkan', async () => {
    const admin = asL(ADMIN)
    const plans = await admin.plans.list({})
    const running = plans.find((p) => p.status === 'berjalan')!
    const status = await ask(admin, { kind: 'plan', planId: running.id }, w.when!)
    expect(status.text).toContain(w.onTheWay)
    expect(status.text).toMatch(locale === 'id' ? /\d{2}\.\d{2}/ : /\d{2}:\d{2}/) // jam tiba, format sesuai bahasa
    expect(status.traces.some((t) => t.trace.title.toLowerCase().includes(w.etaTitle!))).toBe(true)

    // plan menunggu: seed menaruh driver idle di dekat antrean teratas, jadi ada kandidat kuat
    const scored = await Promise.all(
      plans
        .filter((p) => p.status === 'menunggu_driver')
        .map(async (p) => {
          const plan = await admin.plans.get(p.id)
          const suggestions = await admin.dispatch.suggest(p.id, plan.routes[0]!.id)
          return { id: p.id, best: Math.max(0, ...suggestions.filter((x) => x.fits).map((x) => x.score)) }
        }),
    )
    const strong = scored.find((x) => x.best >= RECOMMEND_MIN_SCORE)!
    const good = await ask(admin, { kind: 'plan', planId: strong.id }, w.bestDriver!)
    expect(good.text).toContain(w.suggestion)
    expect(good.actions[0]).toMatchObject({ kind: 'tugaskan_terbaik', planId: strong.id })
    // tetap hanya usulan: rute belum punya driver
    expect((await admin.plans.get(strong.id)).routes.some((r) => !r.driverId)).toBe(true)
  })

  it('keterlambatan: menyebut menit, ETA baru, dan saran berbeda untuk admin vs pelanggan', async () => {
    const admin = asL(ADMIN)
    const target = (await admin.demo.triggerDelay(undefined, 15)).planCode
    const summary = (await admin.plans.list({})).find((p) => p.code === target)!
    const adminAnswer = await ask(admin, { kind: 'plan', planId: summary.id }, w.late!)
    expect(adminAnswer.text).toContain(w.lateWord)
    expect(adminAnswer.text).toContain('15')
    expect(adminAnswer.text).toContain(w.contact)
    const customer = asL({ role: 'customer', userId: summary.userId, name: 'x' })
    const customerAnswer = await ask(customer, { kind: 'plan', planId: summary.id }, w.late!)
    expect(customerAnswer.text).toContain(w.nothingToDo)
    expect(customerAnswer.text).not.toContain(w.contact)
  })

  it('ops dan insights hanya untuk admin; insights memuat angka yang bisa ditelusuri', async () => {
    await expect(ask(asL(DINA), { kind: 'ops' }, w.todo!)).rejects.toMatchObject({ code: 'ditolak' })
    await expect(collect(asL(DINA).ai.insights())).rejects.toMatchObject({ code: 'ditolak' })

    const admin = asL(ADMIN)
    const ops = await ask(admin, { kind: 'ops' }, w.todo!)
    expect(ops.text).toContain(w.order)
    expect(ops.actions.every((a) => a.kind === 'buka_plan' && a.planId)).toBe(true)

    const events = await collect(admin.ai.insights())
    const text = events.flatMap((e) => (e.type === 'token' ? [e.text] : [])).join('')
    expect(text).toContain(w.summary)
    expect(text).toContain(w.noChange)
    const count = (await admin.plans.list({})).filter((p) => !['draft', 'opsi_siap'].includes(p.status))
    // jumlah plan 7 hari terakhir pada narasi = jumlah plan dibuat dalam 7 hari di daftar (selain dibatalkan)
    const week = count.filter((p) => p.status !== 'dibatalkan' && new Date(p.createdAt).getTime() >= T0 - 7 * 86_400_000).length
    expect(text).toMatch(new RegExp(`\\*\\*${week} plans?\\*\\*`))
  })
})

describe('Copilot: bahasa pertanyaan tidak menentukan bahasa jawaban', () => {
  it('pertanyaan Indonesia pada layar Inggris dijawab Inggris; pertanyaan Inggris pada layar Indonesia dijawab Indonesia', async () => {
    const { api, id } = await draftWithOptions(DINA, undefined, 'en')
    const en = await ask(api, { kind: 'plan', planId: id }, WORDS.id!.compare!)
    expect(en.text).toContain('Option |')
    expect(en.text).not.toContain('Opsi |')
    const idApi = as(DINA, 'id')
    const idAnswer = await ask(idApi, { kind: 'plan', planId: id }, WORDS.en!.compare!)
    expect(idAnswer.text).toContain('Opsi |')
    expect(idAnswer.text).not.toContain('Option |')
  })
})

describe('Teks AI dalam bahasa permintaan', () => {
  it('progres dan ringkasan baca foto, nama barang hasil ucapan, dan label maksud', async () => {
    const read = async (locale: Locale) => {
      const events = await collect(as(DINA, locale).ai.readImage({ name: 'foto-kue.jpg', type: 'image/jpeg', size: 90_000 }))
      return {
        labels: events.flatMap((e) => (e.type === 'progress' ? [e.label] : [])),
        summary: events.flatMap((e) => (e.type === 'done' ? [e.summary] : []))[0]!,
        names: events.flatMap((e) => (e.type === 'done' ? e.items.map((i) => i.name) : [])),
      }
    }
    const en = await read('en')
    const id = await read('id')
    expect(en.labels).toEqual(['Reading the photo', 'Recognizing items', 'Estimating weight and size', 'Matching against the item list'])
    expect(id.labels).toEqual(['Membaca foto', 'Mengenali barang', 'Memperkirakan berat dan ukuran', 'Mencocokkan dengan daftar barang'])
    expect(en.summary).toMatch(/^Detected /)
    expect(id.summary).toMatch(/^Terdeteksi /)
    expect(en.names[0]).toBe('Birthday cake')
    expect(id.names[0]).toBe('Kue ulang tahun')

    const speech = (locale: Locale) => as(DINA, locale).ai.parseSpeech('two boxes of shoes and a laptop')
    expect((await speech('en')).items.map((i) => [i.key, i.qty, i.name])).toEqual([
      ['sepatu', 2, 'Shoebox'],
      ['laptop', 1, 'Laptop (boxed)'],
    ])
    expect((await speech('id')).items.map((i) => i.name)).toEqual(['Kotak sepatu', 'Laptop (dalam kardus)'])

    const intent = (locale: Locale) => as(DINA, locale).ai.parseIntent('cheapest but arrive before 5 pm')
    expect((await intent('en')).chips.map((c) => c.label)).toEqual(['Priority: Cheapest', 'Arrive before 17:00'])
    expect((await intent('id')).chips.map((c) => c.label)).toEqual(['Prioritas: Termurah', 'Tiba sebelum 17.00'])
  })
})

/** Setiap kunci yang dipakai harus terdaftar di kedua bahasa: kunci hilang membuat `translate` memperingatkan dan mengembalikan kuncinya. */
describe('semua kunci terdaftar', () => {
  const warn = vi.spyOn(console, 'warn').mockImplementation(() => {})
  afterEach(() => warn.mockClear())

  it.each(LOCALES)('jawaban dan jejak alat (%s) tanpa kunci mentah atau parameter hilang', async (locale) => {
    warn.mockClear()
    const w = WORDS[locale]
    const admin = as(ADMIN, locale)
    const texts: string[] = []
    const collectAnswer = async (api: ApiClient, scope: Parameters<ApiClient['ai']['copilot']>[0], q: string) => {
      const r = await ask(api, scope, q)
      texts.push(r.text, ...r.traces.flatMap((t) => [t.trace.title, t.trace.target ?? '', t.trace.detail ?? '']), ...r.actions.map((a) => a.label))
    }
    // semua jenis pertanyaan pada plan yang berjalan, menunggu, dan batal
    const plans = await admin.plans.list({})
    const target = [plans.find((p) => p.status === 'berjalan'), plans.find((p) => p.status === 'menunggu_driver'), plans.find((p) => p.status === 'selesai'), plans.find((p) => p.status === 'dibatalkan')]
    await admin.demo.triggerDelay(undefined, 15)
    const questions = [w.why, w.compare, w.faster, w.whyOne, w.offTopic, w.when, w.bestDriver, w.late, 'cheapest', 'termurah', 'jarak terpendek', 'rincian biaya', 'cost breakdown']
    for (const p of target) if (p) for (const q of questions) await collectAnswer(admin, { kind: 'plan', planId: p.id }, q!)
    await collectAnswer(admin, { kind: 'ops' }, w.todo!)
    const insights = await collect(admin.ai.insights())
    texts.push(
      ...insights.flatMap((e) => (e.type === 'token' ? [e.text] : e.type === 'tool' ? [e.trace.title, e.trace.target ?? '', e.trace.detail ?? ''] : e.type === 'done' ? e.actions.map((a) => a.label) : [])),
    )
    // draft dengan opsi (pelanggan)
    const { api, id } = await draftWithOptions(DINA, undefined, locale)
    for (const q of questions) await collectAnswer(api, { kind: 'plan', planId: id }, q!)
    const single = await draftWithOptions(DINA, [shipment('s1', 'kemang-village', 'plaza-senayan')], locale)
    for (const q of [w.whyOne, w.compare, 'cheapest']) await collectAnswer(single.api, { kind: 'plan', planId: single.id }, q!)

    expect(texts.length).toBeGreaterThan(50)
    for (const text of texts) expect(text).not.toMatch(/\b(?:ai|srv|dom|fmt|cat)\.[A-Za-z]+(?:\.[A-Za-z_]+)*\b/)
    expect(warn).not.toHaveBeenCalled()
  })
})

describe('kegagalan tiruan berlaku juga untuk AI', () => {
  it('offline dan error menghasilkan galat berkode; selalu ada jalan keluar manual di pesan', async () => {
    await as(null).demo.setFault('offline')
    await expect(as(DINA).ai.parseIntent('hemat')).rejects.toMatchObject({ code: 'jaringan' })
    await as(null).demo.setFault('error')
    await expect(as(DINA).ai.parseSpeech('satu galon')).rejects.toMatchObject({ code: 'layanan' })
    await expect(as(DINA).ai.parseSpeech('satu galon')).rejects.toThrow('fill it in manually')
  })
})
