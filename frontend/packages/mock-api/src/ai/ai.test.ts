import { LOW_CONFIDENCE } from '@loong/domain'
import type { Locale } from '@loong/i18n'
import { describe, expect, it } from 'vitest'
import { parseIntent } from './intent'
import { parseSpeech } from './speech'
import { detectItems } from './vision'

const file = (name: string, size = 120_000) => ({ name, type: 'image/jpeg', size })

describe('baca gambar (tiruan)', () => {
  it('deterministik: berkas yang sama menghasilkan barang yang sama', () => {
    expect(detectItems(file('IMG_2041.jpg'), 'en')).toEqual(detectItems(file('IMG_2041.jpg'), 'en'))
  })

  it('nama berkas yang jelas menentukan barang utama dengan keyakinan tinggi', () => {
    const [first] = detectItems(file('foto-laptop.jpg'), 'en')
    expect(first!.key).toBe('laptop')
    expect(first!.qty).toBe(1) // barang mudah pecah tidak ditebak berjumlah banyak
    expect(first!.fragile).toBe(true)
    expect(first!.confidence).toBeGreaterThanOrEqual(0.95)
  })

  it('selalu menghasilkan barang yang bisa dihitung engine', () => {
    for (let i = 0; i < 60; i++) {
      const items = detectItems(file(`IMG_${1000 + i}.jpg`, 50_000 + i * 997), 'en')
      expect(items.length).toBeGreaterThanOrEqual(1)
      expect(items.length).toBeLessThanOrEqual(3)
      for (const it of items) {
        expect(it.qty).toBeGreaterThanOrEqual(1)
        expect(it.weightKg).toBeGreaterThan(0)
        expect(it.lengthCm * it.widthCm * it.heightCm).toBeGreaterThan(0)
        expect(it.confidence).toBeGreaterThan(0)
        expect(it.confidence).toBeLessThan(1)
      }
      // barang utama selalu yakin
      expect(items[0]!.confidence).toBeGreaterThanOrEqual(0.88)
    }
  })

  it('bervariasi antar berkas dan sesekali memunculkan barang berkeyakinan rendah ("periksa")', () => {
    const results = Array.from({ length: 40 }, (_, i) => detectItems(file(`IMG_${2000 + i}.jpg`, 80_000 + i * 1301), 'en'))
    expect(new Set(results.map((r) => r[0]!.key)).size).toBeGreaterThan(4)
    expect(results.some((r) => r.some((it) => it.confidence < LOW_CONFIDENCE))).toBe(true)
  })
})

describe('ucapan -> barang (tiruan)', () => {
  it('mengurai beberapa barang, jumlah kata, dan mudah pecah', () => {
    const r = parseSpeech('Dua kardus sedang, satu laptop mudah pecah, total sekitar delapan kilo', 'id')
    expect(r.items.map((i) => [i.key, i.qty, i.fragile])).toEqual([
      ['kardus-sedang', 2, false],
      ['laptop', 1, true],
    ])
    expect(r.matches).toHaveLength(2)
    expect(r.leftover).toBe('')
  })

  it('jumlah angka dan kata hubung "dan"', () => {
    const r = parseSpeech('satu galon dan 2 dokumen', 'id')
    expect(r.items.map((i) => [i.key, i.qty])).toEqual([
      ['galon', 1],
      ['dokumen', 2],
    ])
  })

  it('jumlah setelah nama barang ("kardus besar dua buah")', () => {
    const r = parseSpeech('kardus besar dua buah', 'id')
    expect(r.items).toHaveLength(1)
    expect(r.items[0]).toMatchObject({ key: 'kardus-besar', qty: 2 })
  })

  it('frasa panjang menang atas kata pendek ("kardus besar" bukan "kardus")', () => {
    expect(parseSpeech('satu kardus besar', 'id').items[0]!.key).toBe('kardus-besar')
  })

  it('berat yang disebut untuk satu barang dipakai sebagai berat satuan', () => {
    const r = parseSpeech('kue ulang tahun 2 kilo', 'id')
    expect(r.items[0]).toMatchObject({ key: 'kue', qty: 1, weightKg: 2 })
  })

  it('berat total untuk satu jenis barang dibagi rata per satuan', () => {
    const r = parseSpeech('dua kardus sedang, total sekitar delapan kilo', 'id')
    expect(r.items[0]).toMatchObject({ key: 'kardus-sedang', qty: 2, weightKg: 4 })
  })

  it('menggabungkan jenis yang sama', () => {
    const r = parseSpeech('satu kardus sedang lalu dua kardus sedang', 'id')
    expect(r.items).toHaveLength(1)
    expect(r.items[0]!.qty).toBe(3)
  })

  it('bagian yang tidak dipahami dikembalikan, tidak ditebak', () => {
    const r = parseSpeech('tolong antar secepatnya', 'id')
    expect(r.items).toHaveLength(0)
    expect(r.leftover).toContain('antar')
  })
})

describe('maksud -> prioritas (tiruan)', () => {
  // 20 Okt 2026, 14.00 WIB
  const NOW = Date.UTC(2026, 9, 20, 7, 0)

  it('"paling hemat tapi sampai sebelum jam 5" -> termurah + batas 17.00 hari ini', () => {
    const r = parseIntent('paling hemat tapi sampai sebelum jam 5', NOW, 'id')
    const preset = r.chips.find((c) => c.kind === 'prioritas')
    const deadline = r.chips.find((c) => c.kind === 'batas_waktu')
    expect(preset?.preset).toBe('termurah')
    expect(deadline?.deadline).toBe(new Date(Date.UTC(2026, 9, 20, 10, 0)).toISOString())
    expect(deadline?.label).toBe('Tiba sebelum 17.00')
  })

  it('"pukul 4 sore" -> 16.00; jam dengan menit', () => {
    expect(parseIntent('harus tiba paling lambat pukul 4 sore', NOW, 'id').chips[0]!.deadline).toBe(new Date(Date.UTC(2026, 9, 20, 9, 0)).toISOString())
    expect(parseIntent('maksimal jam 17.30', NOW, 'id').chips[0]!.deadline).toBe(new Date(Date.UTC(2026, 9, 20, 10, 30)).toISOString())
  })

  it('batas yang sudah lewat hari ini dipindah ke besok', () => {
    const r = parseIntent('sebelum jam 1 siang', NOW, 'id')
    expect(r.chips[0]!.deadline).toBe(new Date(Date.UTC(2026, 9, 21, 6, 0)).toISOString())
  })

  it('kata bertentangan -> seimbang dengan alasan, bukan menebak satu sisi', () => {
    const r = parseIntent('yang penting cepat tapi juga hemat', NOW, 'id')
    const p = r.chips.find((c) => c.kind === 'prioritas')!
    expect(p.preset).toBe('seimbang')
    expect(p.label).toContain('bertentangan')
  })

  it('kata penguat ("paling", "sekali") bukan bagian yang tidak dipahami', () => {
    expect(parseIntent('paling hemat tapi sampai sebelum jam 5', NOW, 'id').leftover).toBe('')
  })

  it('tanpa kata kunci -> tanpa chip; sisa kalimat dikembalikan', () => {
    const r = parseIntent('tolong hati-hati ya', NOW, 'id')
    expect(r.chips).toHaveLength(0)
    expect(r.leftover).toContain('hati-hati')
  })
})

/* ---------- dua bahasa: masukan Inggris dan Indonesia dipahami sama, keluaran mengikuti bahasa permintaan ---------- */

describe('ucapan: Inggris dan Indonesia diklasifikasi sama', () => {
  const shape = (transcript: string, locale: Locale = 'en') => {
    const r = parseSpeech(transcript, locale)
    return { items: r.items.map((i) => [i.key, i.qty, i.fragile, i.weightKg]), leftover: r.leftover }
  }

  it.each([
    ['two medium boxes, one fragile laptop, about eight kilos in total', 'dua kardus sedang, satu laptop mudah pecah, total sekitar delapan kilo'],
    ['two boxes of shoes and a laptop', 'dua kotak sepatu dan satu laptop'],
    ['one birthday cake and three documents', 'satu kue ulang tahun dan tiga dokumen'],
    ['one water jug and two food boxes', 'satu galon dan dua boks makanan'],
    ['a large box, 2 chairs', 'satu kardus besar, 2 kursi'],
    ['birthday cake 2 kilos', 'kue ulang tahun 2 kilo'],
    ['one and a half kilos of documents', 'dokumen satu setengah kilo'],
  ])('"%s" = "%s"', (en, id) => {
    const a = shape(en)
    expect(a.items.length).toBeGreaterThan(0)
    expect(a).toEqual(shape(id))
    expect(a.leftover).toBe('')
  })

  it('contoh dari brief: kotak sepatu dua buah dan laptop satu', () => {
    expect(shape('two boxes of shoes and a laptop').items).toEqual([
      ['sepatu', 2, false, 1.2],
      ['laptop', 1, true, 2.8],
    ])
  })

  it('kata wadah kalah dari barang yang disebut; wadah saja tetap kardus', () => {
    expect(parseSpeech('a box of shoes', 'en').items.map((i) => i.key)).toEqual(['sepatu'])
    expect(parseSpeech('three boxes', 'en').items.map((i) => [i.key, i.qty])).toEqual([['kardus-sedang', 3]])
    expect(parseSpeech('dua dus', 'id').items.map((i) => [i.key, i.qty])).toEqual([['kardus-sedang', 2]])
  })

  it('bagian yang tidak dipahami dikembalikan, juga dalam bahasa Inggris', () => {
    const r = parseSpeech('please deliver it asap', 'en')
    expect(r.items).toHaveLength(0)
    expect(r.leftover).toContain('deliver')
  })

  it('nama barang mengikuti bahasa permintaan, bukan bahasa ucapan', () => {
    expect(parseSpeech('two boxes of shoes', 'en').items[0]!.name).toBe('Shoebox')
    expect(parseSpeech('two boxes of shoes', 'id').items[0]!.name).toBe('Kotak sepatu')
    expect(parseSpeech('dua kotak sepatu', 'en').items[0]!.name).toBe('Shoebox')
  })
})

describe('maksud: Inggris dan Indonesia diklasifikasi sama', () => {
  const NOW = Date.UTC(2026, 9, 20, 7, 0) // 14.00 WIB
  const at = (h: number, m = 0, day = 20) => new Date(Date.UTC(2026, 9, day, h - 7, m)).toISOString()
  const shape = (text: string, locale: Locale = 'en') => {
    const r = parseIntent(text, NOW, locale)
    return {
      preset: r.chips.find((c) => c.kind === 'prioritas')?.preset,
      deadline: r.chips.find((c) => c.kind === 'batas_waktu')?.deadline,
      leftover: r.leftover,
    }
  }

  it.each([
    ['cheapest but arrive before 5 pm', 'paling hemat tapi sampai sebelum jam 5', 'termurah', at(17)],
    ['cheapest, by 5pm', 'paling hemat, tiba sebelum jam 5 sore', 'termurah', at(17)],
    ['cheapest before 17:00', 'paling hemat sebelum 17.00', 'termurah', at(17)],
    ['as fast as possible, no later than 4:30 pm', 'secepatnya, paling lambat pukul 4.30 sore', 'tercepat', at(16, 30)],
    ['fastest, before 1 pm', 'tercepat, sebelum jam 1 siang', 'tercepat', at(13, 0, 21)], // 13.00 sudah lewat: besok
    ['shortest route, arrive at 5 pm', 'terpendek, tiba jam 5 sore', 'terpendek', at(17)],
    ['i need it balanced, deadline 6 pm', 'seimbang saja, batas jam 6 sore', 'seimbang', at(18)],
  ])('"%s" = "%s"', (en, id, preset, deadline) => {
    expect(shape(en)).toEqual({ preset, deadline, leftover: '' })
    expect(shape(id)).toEqual({ preset, deadline, leftover: '' })
  })

  it('"am" dan "in the morning" berarti pagi; tanpa keterangan jam 1-6 dianggap sore', () => {
    expect(shape('fastest, before 9 am').deadline).toBe(at(9, 0, 21))
    expect(shape('fastest, by 10 in the morning').deadline).toBe(at(10, 0, 21))
    expect(shape('cheapest, before 5').deadline).toBe(at(17))
  })

  it('"within 2 hours" dan jumlah barang bukan batas waktu', () => {
    expect(shape('cheapest but arrive within 2 hours').deadline).toBeUndefined()
    expect(shape('cheapest, 5 boxes before the weekend').deadline).toBeUndefined()
  })

  it('kata bertentangan dalam bahasa Inggris -> seimbang dengan alasan', () => {
    const r = parseIntent('i need it fast but also cheap', NOW, 'en')
    const p = r.chips.find((c) => c.kind === 'prioritas')!
    expect(p.preset).toBe('seimbang')
    expect(p.label).toBe('Priority: Balanced (some words contradict each other)')
    expect(parseIntent('yang penting cepat tapi juga hemat', NOW, 'id').chips[0]!.label).toBe('Prioritas: Seimbang (ada kata yang saling bertentangan)')
  })

  it('label chip mengikuti bahasa permintaan, apa pun bahasa kalimatnya', () => {
    const labels = (text: string, locale: Locale) => parseIntent(text, NOW, locale).chips.map((c) => c.label)
    expect(labels('cheapest but arrive before 5 pm', 'en')).toEqual(['Priority: Cheapest', 'Arrive before 17:00'])
    expect(labels('cheapest but arrive before 5 pm', 'id')).toEqual(['Prioritas: Termurah', 'Tiba sebelum 17.00'])
    expect(labels('paling hemat tapi sampai sebelum jam 5', 'en')).toEqual(['Priority: Cheapest', 'Arrive before 17:00'])
  })

  it('tanpa kata kunci -> tanpa chip, juga dalam bahasa Inggris', () => {
    const r = parseIntent('please be careful', NOW, 'en')
    expect(r.chips).toHaveLength(0)
    expect(r.leftover).toContain('careful')
  })
})
