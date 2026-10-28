import { RECOMMEND_MIN_SCORE, type DriverSuggestion, type Plan } from '@loong/domain'
import { LOCALES, registerMessages, type Locale } from '@loong/i18n'
import { describe, expect, it } from 'vitest'
import { createSeed } from '../seed'
import { answerAboutPlan, classify, starterQuestions } from './copilot'

// alasan driver adalah Msg dari engine (srv.ops.*); tes ini memakai kunci sendiri supaya tidak bergantung pada katalog itu
registerMessages('en', { 'test.reason': '{km|km} from the pickup point' })
registerMessages('id', { 'test.reason': '{km|km} dari titik ambil' })

const NOW = Date.parse('2026-10-20T07:00:00Z')
const db = createSeed(NOW)
const waiting = db.plans.find((p) => p.status === 'menunggu_driver')!

const suggestion = (route: Plan['routes'][number], score: number): DriverSuggestion => ({
  driverId: 'drv-x',
  driverName: 'Contoh Driver',
  vehicleId: 'veh-x',
  vehicleType: route.vehicleType,
  plate: 'B 0000 XX',
  rating: 4.7,
  distanceToPickupKm: score > 50 ? 1.5 : 52,
  etaToPickupMin: score > 50 ? 4 : 88,
  fits: true,
  score,
  reasons: [{ key: 'test.reason', params: { km: score > 50 ? 1.5 : 52 } }],
})

const ask = (score: number, locale: Locale = 'id') =>
  answerAboutPlan({
    plan: waiting,
    question: 'Siapa driver terbaik?',
    audience: 'admin',
    nowMs: NOW,
    drivers: db.drivers,
    vehicles: db.vehicles,
    locale,
    suggestions: Object.fromEntries(waiting.routes.map((r) => [r.id, [suggestion(r, score)]])),
  })

describe('Copilot: saran driver', () => {
  it('kandidat kuat disebut "saran saya" dan boleh diterapkan', () => {
    const a = ask(80)
    expect(a.markdown).toContain('Saran saya')
    expect(a.actions[0]).toMatchObject({ kind: 'tugaskan_terbaik', planId: waiting.id, primary: true })
  })

  it(`kandidat di bawah skor ${RECOMMEND_MIN_SCORE} tidak dipromosikan: alasan ditunjukkan, tanpa tombol terapkan`, () => {
    const a = ask(RECOMMEND_MIN_SCORE - 1)
    expect(a.markdown).toContain('kurang cocok')
    expect(a.markdown).not.toContain('Saran saya')
    expect(a.markdown).toContain('52,0 km dari titik ambil')
    expect(a.actions).toHaveLength(0)
  })

  it('satu rute tampil sebagai kalimat biasa dan berawalan huruf besar, bukan butir', () => {
    const one = { ...waiting, routes: [waiting.routes[0]!] }
    const a = answerAboutPlan({ plan: one, question: 'siapa driver', audience: 'admin', nowMs: NOW, drivers: db.drivers, vehicles: db.vehicles, locale: 'id', suggestions: { [one.routes[0]!.id]: [suggestion(one.routes[0]!, 80)] } })
    expect(a.markdown.startsWith('- ')).toBe(false)
    expect(a.markdown[0]).toBe(a.markdown[0]!.toUpperCase())
  })
})

describe('klasifikasi pertanyaan', () => {
  it.each([
    ['Bandingkan semua opsi', 'bandingkan'],
    ['Kenapa hanya ada satu opsi?', 'bandingkan'],
    ['Kenapa terlambat?', 'telat'],
    ['Kapan paket sampai?', 'status'],
    ['Siapa driver terbaik?', 'driver'],
    ['Rincian biaya', 'biaya'],
    ['Bagaimana kalau saya ingin lebih cepat?', 'cepat'],
    ['Kenapa opsi ini yang disarankan?', 'jelaskan'],
    ['cuaca besok bagaimana?', 'bantuan'],
  ])('%s -> %s', (q, kind) => {
    expect(classify(q)).toBe(kind)
  })
})

describe('Copilot: saran driver dalam bahasa Inggris', () => {
  it('kandidat kuat dan lemah, alasan dirender dalam bahasa permintaan', () => {
    const strong = ask(80, 'en')
    expect(strong.markdown).toContain('My suggestion is')
    expect(strong.markdown).toContain('1.5 km from the pickup point')
    expect(strong.actions[0]!.label).toBe('Apply the best driver suggestion')
    const weak = ask(RECOMMEND_MIN_SCORE - 1, 'en')
    expect(weak.markdown).toContain('a poor fit')
    expect(weak.markdown).not.toContain('My suggestion is')
    expect(weak.markdown).toContain('52.0 km from the pickup point')
    expect(weak.actions).toHaveLength(0)
  })
})

/**
 * Pengguna boleh mengetik dalam bahasa apa pun, terlepas dari bahasa layar: pertanyaan Inggris dan Indonesia
 * yang setara harus jatuh ke jenis yang sama.
 */
describe('klasifikasi dua bahasa', () => {
  it.each([
    ['Why was this option recommended?', 'Kenapa opsi ini disarankan?', 'jelaskan'],
    ['Compare all options', 'Bandingkan semua opsi', 'bandingkan'],
    ['Why is there only one option?', 'Kenapa hanya ada satu opsi?', 'bandingkan'],
    ['What if I want it faster?', 'Bagaimana kalau saya ingin lebih cepat?', 'cepat'],
    ['Which one is the cheapest?', 'Yang mana paling murah?', 'murah'],
    ['Which option has the shortest distance?', 'Opsi mana yang jaraknya paling dekat?', 'pendek'],
    ['Cost breakdown', 'Rincian biaya', 'biaya'],
    ['How much is the fare?', 'Berapa tarifnya?', 'biaya'],
    ['When will the parcel arrive?', 'Kapan paket sampai?', 'status'],
    ['Where is my parcel?', 'Paket saya di mana?', 'status'],
    ['Who is the best driver?', 'Siapa driver terbaik?', 'driver'],
    ['Why is it late?', 'Kenapa terlambat?', 'telat'],
    ['Is there any delay or problem?', 'Ada kendala atau masalah?', 'telat'],
    ["What's the weather like tomorrow?", 'cuaca besok bagaimana?', 'bantuan'],
    ['What do I need to do?', 'Apa yang perlu saya lakukan?', 'bantuan'],
  ])('%s = %s -> %s', (en, id, kind) => {
    expect(classify(en)).toBe(kind)
    expect(classify(id)).toBe(kind)
  })

  it('pertanyaan awal tiap bahasa jatuh ke jenis yang sama', () => {
    const plans: [Pick<Plan, 'status' | 'optionSet' | 'routes'>, 'customer' | 'admin'][] = [
      [{ status: 'opsi_siap', routes: [], optionSet: { options: [{}, {}] } as Plan['optionSet'] }, 'customer'],
      [{ status: 'opsi_siap', routes: [], optionSet: { options: [{}] } as Plan['optionSet'] }, 'customer'],
      [waiting, 'admin'],
      [waiting, 'customer'],
    ]
    for (const [plan, audience] of plans) {
      const kinds = LOCALES.map((l) => starterQuestions(plan, audience, l).map(classify))
      expect(kinds[0]).toEqual(kinds[1])
      expect(kinds[0]!.length).toBeGreaterThan(0)
    }
  })

  it('pertanyaan Indonesia pada layar Inggris dijawab dalam bahasa Inggris, dan sebaliknya', () => {
    const answer = (question: string, locale: Locale) =>
      answerAboutPlan({ plan: waiting, question, audience: 'customer', nowMs: NOW, drivers: db.drivers, vehicles: db.vehicles, locale }).markdown
    expect(answer('Kapan paket sampai?', 'en')).toMatch(/driver|looking|assigned|way|finished/i)
    expect(answer('Kapan paket sampai?', 'en')).not.toMatch(/sedang|belum|dicarikan/)
    expect(answer('When will the parcel arrive?', 'id')).toMatch(/dicarikan|ditugaskan|sedang di jalan|selesai/)
    expect(answer('When will the parcel arrive?', 'id')).not.toMatch(/looking for|is on the way/)
  })
})
