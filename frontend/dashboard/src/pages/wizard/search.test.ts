import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, EFFORTS, type ComputeEvent, type ComputeOptions, type CopilotEvent, type Effort, type Method, type OptionSet, type Plan, type Report } from '@loong/domain'
import { getLocale, msg, setLocale } from '@loong/i18n'
import {
  STAGES,
  applyProgress,
  calcCopy,
  choose,
  effortText,
  initialPick,
  initialProgress,
  initialReport,
  methodLine,
  methodParts,
  needsSearch,
  progressText,
  readReport,
  searchOptions,
  reportLanguage,
  reportParagraphs,
  reportProblem,
  reportReducer,
  settled,
  type ProgressState,
  type ReportState,
  type SearchDeps,
} from './search'

const startLocale = getLocale()
afterEach(() => setLocale(startLocale))

const set = (method?: Method): OptionSet => ({ computedAt: '2026-10-21T03:00:00Z', baseline: {} as never, options: [], inputHash: 'h', ...(method ? { method } : {}) })

type Progress = Extract<ComputeEvent, { type: 'progress' }>
const progress = (over: Partial<Progress> & { step: number }): Progress => ({ type: 'progress', total: 4, label: '', ...over })

/* ================================================================== pilihan kedalaman */

describe('pilihan kedalaman pencarian', () => {
  it('awal: tingkat bawaan fast; opsi yang sudah ada menentukan pilihan awal (tanpa method = fast)', () => {
    expect(initialPick(null)).toEqual({ chosen: 'fast', applied: 'fast' })
    expect(initialPick(undefined)).toEqual({ chosen: 'fast', applied: 'fast' })
    expect(initialPick(set())).toEqual({ chosen: 'fast', applied: 'fast' })
    expect(initialPick(set({ effort: 'thorough', trials: 9 }))).toEqual({ chosen: 'thorough', applied: 'thorough' })
    expect(initialPick(set({ effort: 'reviewed', trials: 9 }))).toEqual({ chosen: 'reviewed', applied: 'reviewed' })
  })

  it('"Cari lagi" hanya ada bila pilihan berbeda dari yang menghasilkan opsi yang tampil', () => {
    let pick = initialPick(null)
    expect(needsSearch(pick)).toBe(false)
    pick = choose(pick, 'thorough')
    expect(pick).toEqual({ chosen: 'thorough', applied: 'fast' })
    expect(needsSearch(pick)).toBe(true)
    // kembali ke yang tampil: tidak perlu mencari lagi
    expect(needsSearch(choose(pick, 'fast'))).toBe(false)
    // pindah ke tingkat lain tetap perlu
    expect(needsSearch(choose(pick, 'reviewed'))).toBe(true)
  })

  it('pencarian selesai menyamakan keduanya; memilih lagi yang lain memunculkan tombol lagi', () => {
    let pick = choose(initialPick(null), 'reviewed')
    expect(needsSearch(pick)).toBe(true)
    pick = settled('reviewed')
    expect(pick).toEqual({ chosen: 'reviewed', applied: 'reviewed' })
    expect(needsSearch(pick)).toBe(false)
    expect(needsSearch(choose(pick, 'fast'))).toBe(true)
  })

  it('memilih yang sama atau yang tidak dikenal tidak mengubah apa pun (objek yang sama)', () => {
    const pick = initialPick(null)
    expect(choose(pick, 'fast')).toBe(pick)
    expect(choose(pick, 'turbo' as Effort)).toBe(pick)
  })

  it('tidak mengubah pilihan lama (murni)', () => {
    const pick = initialPick(null)
    choose(pick, 'thorough')
    expect(pick).toEqual({ chosen: 'fast', applied: 'fast' })
  })

  it('tiga pilihan dengan nama, keterangan, dan perkiraan waktu di kedua bahasa; perkiraan tidak menjanjikan angka pasti', () => {
    for (const locale of ['en', 'id'] as const) {
      setLocale(locale)
      for (const effort of EFFORTS) {
        for (const aiOn of [true, false]) {
          const text = effortText(effort, aiOn)
          expect(text.label.length, `${locale} ${effort}`).toBeGreaterThan(2)
          expect(text.caption.length).toBeGreaterThan(10)
          expect(text.time.length).toBeGreaterThan(5)
          expect(text.time, 'tanpa angka pasti').not.toMatch(/\d/)
        }
      }
    }
  })

  it('keterangan mengikuti brief: fast "Best answer in seconds"; reviewed menyebut peninjau AI, atau peninjau bawaan bila AI mati', () => {
    setLocale('en')
    expect(effortText('fast', true).caption).toBe('Best answer in seconds')
    expect(effortText('thorough', true).caption).toBe('Tries several ways of grouping the stops and polishes the best answer')
    expect(effortText('reviewed', true).label).toBe('Thorough with AI review')
    expect(effortText('reviewed', true).caption).toBe('Adds review rounds where an AI reviewer checks the routes and asks for more attempts')
    const off = effortText('reviewed', false).caption
    expect(off).toMatch(/built-in reviewer/)
    expect(off).not.toMatch(/an AI reviewer/)
    // pilihan tetap ditawarkan saat AI mati: labelnya sama
    expect(effortText('reviewed', false).label).toBe(effortText('reviewed', true).label)
    // AI tidak memengaruhi dua tingkat lainnya
    expect(effortText('fast', false)).toEqual(effortText('fast', true))
    expect(effortText('thorough', false)).toEqual(effortText('thorough', true))
  })

  it('kartu progres: makin dalam makin lama dan judulnya berbeda dari fast', () => {
    setLocale('en')
    expect(calcCopy('fast').hint).toBe('Usually only 3-5 seconds.')
    expect(calcCopy('thorough').title).not.toBe(calcCopy('fast').title)
    expect(calcCopy('thorough').hint).not.toBe(calcCopy('reviewed').hint)
  })
})

/* ================================================================== progres bertahap */

describe('progres: tahap dan sub-langkah', () => {
  it('baris pertama sebuah tahap adalah judulnya; msg berikutnya di step yang sama menjadi sub-langkah tahap 3', () => {
    setLocale('en')
    let s = initialProgress()
    expect(s).toEqual({ step: 0, total: STAGES, stages: [] })
    s = applyProgress(s, progress({ step: 1, label: 'Reading 4 locations' }))
    s = applyProgress(s, progress({ step: 2, label: 'Estimating distances' }))
    s = applyProgress(s, progress({ step: 3, label: 'Assembling combinations' }))
    s = applyProgress(s, progress({ step: 3, msg: msg('srv.engine.search.experiments', { n: 8, count: 8 }) }))
    s = applyProgress(s, progress({ step: 3, msg: msg('srv.engine.search.polish') }))
    s = applyProgress(s, progress({ step: 4, label: 'Comparing' }))
    expect(s.step).toBe(4)
    expect(s.stages[0]).toEqual(['Reading 4 locations'])
    expect(s.stages[2]).toEqual(['Assembling combinations', 'Trying 8 ways of grouping the stops', 'Polishing the best routes'])
    expect(s.stages[3]).toEqual(['Comparing'])
  })

  it('msg didahulukan atas label; label dipakai bila msg tidak dikenal katalog (backend lebih baru), bukan kunci mentah', () => {
    setLocale('en')
    expect(progressText(progress({ step: 3, label: 'label lama', msg: msg('srv.engine.search.satisfied') }))).toBe('The review is satisfied')
    const unknown = { key: 'srv.engine.search.fromTheFuture' }
    expect(progressText(progress({ step: 3, label: 'Cadangan', msg: unknown }))).toBe('Cadangan')
    expect(progressText(progress({ step: 3, label: 'Hanya label' }))).toBe('Hanya label')
    // tanpa label sama sekali: kosong, dan barisnya dibuang
    expect(progressText(progress({ step: 3, msg: unknown }))).toBe('')
    const s = applyProgress(initialProgress(), progress({ step: 3, msg: unknown }))
    expect(s.stages[2]).toEqual([])
    expect(s.step).toBe(3)
  })

  it('jamak dari {n} backend: 1 "way", 3 "ways", dan "attempt(s)" pada percobaan lanjutan; Indonesia mengikuti', () => {
    setLocale('en')
    const exp = (n: number) => progressText(progress({ step: 3, msg: { key: 'srv.engine.search.experiments', params: { n } } }))
    const more = (n: number) => progressText(progress({ step: 3, msg: { key: 'srv.engine.search.more', params: { n } } }))
    expect(exp(1)).toBe('Trying 1 way of grouping the stops')
    expect(exp(8)).toBe('Trying 8 ways of grouping the stops')
    expect(more(1)).toBe('The review asks for 1 more attempt')
    expect(more(3)).toBe('The review asks for 3 more attempts')
    expect(progressText(progress({ step: 3, msg: { key: 'srv.engine.search.round', params: { round: 2 } } }))).toBe('Review round 2')
    setLocale('id')
    expect(exp(8)).toBe('Mencoba 8 cara mengelompokkan stop')
    expect(more(2)).toBe('Peninjau meminta 2 percobaan lagi')
  })

  it('baris kembar berturut-turut dibuang; yang sama tetapi tidak berurutan tetap ditulis', () => {
    setLocale('en')
    let s = initialProgress()
    s = applyProgress(s, progress({ step: 3, label: 'A' }))
    s = applyProgress(s, progress({ step: 3, label: 'A' }))
    s = applyProgress(s, progress({ step: 3, label: 'B' }))
    s = applyProgress(s, progress({ step: 3, label: 'A' }))
    expect(s.stages[2]).toEqual(['A', 'B', 'A'])
  })

  it('tahap tidak pernah mundur, total mengikuti peristiwa, dan keadaan lama tidak diubah', () => {
    setLocale('en')
    const before = applyProgress(initialProgress(), progress({ step: 3, label: 'tiga', total: 5 }))
    const after = applyProgress(before, progress({ step: 2, label: 'dua', total: 5 }))
    expect(after.step).toBe(3)
    expect(after.total).toBe(5)
    expect(before.stages[1]).toEqual([]) // tidak termutasi
    expect(after.stages[1]).toEqual(['dua'])
    // total 0 dari backend tidak menimpa yang sudah diketahui
    expect(applyProgress(after, progress({ step: 3, label: 'x', total: 0 })).total).toBe(5)
  })
})

/* ================================================================== menjalankan pencarian */

describe('searchOptions: jalur otomatis dan jalur hitung ulang eksplisit', () => {
  const existingSet = set({ effort: 'thorough', trials: 9 })
  const freshSet = set({ effort: 'reviewed', trials: 14, rounds: 2 })

  /** Backend palsu: mencatat urutan panggilan dan memutar peristiwa yang diberikan. */
  function fake(opts: { plan?: Partial<Plan>; events?: ComputeEvent[]; ensureSaved?: () => Promise<string> } = {}) {
    const calls: string[] = []
    const computeArgs: { id: string; opts: ComputeOptions }[] = []
    const deps: SearchDeps = {
      ensureSaved: opts.ensureSaved ?? (async () => (calls.push('ensureSaved'), 'pln-1')),
      getPlan: async (id) => (calls.push(`getPlan:${id}`), { id, ...opts.plan } as Plan),
      compute: (id, o) =>
        (async function* () {
          calls.push('compute')
          computeArgs.push({ id, opts: o })
          for (const e of opts.events ?? [{ type: 'done', optionSet: freshSet }]) yield e
        })(),
    }
    return { deps, calls, computeArgs }
  }
  const hooks = (current = () => true) => {
    const seen: ProgressState[] = []
    return { seen, current, onProgress: (p: ProgressState) => seen.push(p) }
  }

  it('otomatis (tanpa force): opsi yang sudah tersimpan dipakai apa adanya dan tidak ada perhitungan', async () => {
    const f = fake({ plan: { optionSet: existingSet } })
    const out = await searchOptions(f.deps, 'fast', false, hooks())
    expect(out).toEqual({ kind: 'reused', set: existingSet })
    expect(f.calls).toEqual(['ensureSaved', 'getPlan:pln-1'])
  })

  it('otomatis tanpa opsi tersimpan: menghitung dengan tingkat yang diminta (bawaan fast saat masuk langkah)', async () => {
    const f = fake({ plan: {} })
    const out = await searchOptions(f.deps, 'fast', false, hooks())
    expect(out).toEqual({ kind: 'computed', set: freshSet })
    expect(f.calls).toEqual(['ensureSaved', 'getPlan:pln-1', 'compute'])
    expect(f.computeArgs).toEqual([{ id: 'pln-1', opts: { effort: 'fast' } }])
  })

  it('force ("Cari lagi"): tidak melihat opsi yang sudah ada, langsung menghitung dengan tingkat yang dipilih', async () => {
    for (const effort of EFFORTS) {
      const f = fake({ plan: { optionSet: existingSet } })
      const out = await searchOptions(f.deps, effort, true, hooks())
      expect(out).toEqual({ kind: 'computed', set: freshSet })
      // plan sudah punya opsi, tetapi tidak dibaca ulang dan tidak dipakai
      expect(f.calls).toEqual(['ensureSaved', 'compute'])
      expect(f.computeArgs).toEqual([{ id: 'pln-1', opts: { effort } }])
    }
  })

  it('draft disimpan dulu, sebelum apa pun lainnya', async () => {
    const f = fake({ plan: {} })
    await searchOptions(f.deps, 'thorough', true, hooks())
    expect(f.calls[0]).toBe('ensureSaved')
  })

  it('progres dikumpulkan bertahap dan dilaporkan setiap peristiwa; berhenti di done', async () => {
    const f = fake({
      events: [
        progress({ step: 1, label: 'Membaca' }),
        progress({ step: 3, label: 'Menyusun' }),
        progress({ step: 3, msg: msg('srv.engine.search.polish') }),
        { type: 'done', optionSet: freshSet },
        progress({ step: 4, label: 'tidak pernah dibaca' }),
      ],
    })
    const h = hooks()
    const out = await searchOptions(f.deps, 'thorough', true, h)
    expect(out.kind).toBe('computed')
    expect(h.seen).toHaveLength(3)
    expect(h.seen.at(-1)!.stages[2]).toEqual(['Menyusun', 'Polishing the best routes'])
    expect(h.seen.at(-1)!.step).toBe(3)
  })

  it('dibatalkan sebelum perhitungan mulai (otomatis) atau di tengah aliran: hasil dibuang dan aliran ditutup', async () => {
    const f1 = fake({ plan: {} })
    let alive = true
    const out1 = await searchOptions({ ...f1.deps, getPlan: async (id) => ((alive = false), { id } as Plan) }, 'fast', false, hooks(() => alive))
    expect(out1).toEqual({ kind: 'cancelled' })
    expect(f1.calls).not.toContain('compute')

    const closed = vi.fn()
    const deps: SearchDeps = {
      ensureSaved: async () => 'pln-1',
      getPlan: async (id) => ({ id }) as Plan,
      compute: () =>
        (async function* () {
          try {
            yield progress({ step: 1, label: 'a' })
            yield progress({ step: 2, label: 'b' })
            yield { type: 'done', optionSet: freshSet } as ComputeEvent
          } finally {
            closed()
          }
        })(),
    }
    let n = 0
    const out2 = await searchOptions(deps, 'fast', true, hooks(() => n++ < 1))
    expect(out2).toEqual({ kind: 'cancelled' })
    expect(closed).toHaveBeenCalled()
  })

  it('aliran yang berakhir tanpa done adalah galat, bukan menunggu selamanya', async () => {
    const f = fake({ events: [progress({ step: 1, label: 'a' })] })
    await expect(searchOptions(f.deps, 'fast', true, hooks())).rejects.toMatchObject({ code: 'layanan', msg: { key: 'srv.api.serviceError' } })
  })

  it('galat dari menyimpan atau menghitung diteruskan apa adanya', async () => {
    const boom = new ApiError('tidak_valid', msg('srv.api.badEffort'), { field: 'effort' })
    await expect(searchOptions(fake({ ensureSaved: () => Promise.reject(boom) }).deps, 'fast', true, hooks())).rejects.toBe(boom)
    const deps: SearchDeps = { ...fake().deps, compute: () => ({ [Symbol.asyncIterator]: () => ({ next: () => Promise.reject(boom) }) }) }
    await expect(searchOptions(deps, 'thorough', true, hooks())).rejects.toBe(boom)
  })
})

/* ================================================================== cara opsi ditemukan */

describe('kalimat cara opsi ditemukan', () => {
  const full: Method = { effort: 'reviewed', trials: 12, rounds: 2, polished: true, reviewer: 'ai', proven: true, roadSource: 'mapbox' }

  it('contoh dari brief, lengkap: percobaan, ronde, ditinjau AI, terbukti optimal', () => {
    setLocale('en')
    expect(methodParts(full)).toEqual(['12 attempts', '2 review rounds', 'best routes polished', 'reviewed by AI', 'proven optimal', 'distances along real roads'])
    expect(methodLine(full)).toBe('How these were found: 12 attempts, 2 review rounds, best routes polished, reviewed by AI, proven optimal and distances along real roads.')
    expect(methodLine({ effort: 'reviewed', trials: 12, rounds: 2, reviewer: 'ai', proven: true })).toBe('How these were found: 12 attempts, 2 review rounds, reviewed by AI and proven optimal.')
  })

  it('bagian yang tidak ada dihilangkan, jamak mengikuti angka', () => {
    setLocale('en')
    expect(methodLine({ effort: 'fast', trials: 1 })).toBe('How these were found: 1 attempt.')
    expect(methodLine({ effort: 'reviewed', trials: 5, rounds: 1, reviewer: 'rules' })).toBe('How these were found: 5 attempts, 1 review round and reviewed by the built-in rules.')
    expect(methodLine({ effort: 'thorough', trials: 7, polished: true })).toBe('How these were found: 7 attempts and best routes polished.')
    expect(methodParts({ effort: 'fast', trials: 3, roadSource: 'estimate' })).toEqual(['3 attempts', 'estimated distances'])
  })

  it('tidak ada yang bisa dikatakan dengan jujur: tidak ada kalimat', () => {
    expect(methodLine(undefined)).toBeNull()
    expect(methodLine({ effort: 'fast', trials: 0 })).toBeNull()
    expect(methodParts({ effort: 'thorough', trials: 0, rounds: 0 })).toEqual([])
    // sumber jalan yang tidak dikenal diabaikan
    expect(methodParts({ effort: 'fast', trials: 2, roadSource: 'satelit' })).toEqual(['2 attempts'])
  })

  it('tidak pernah mengaku "optimal" kecuali method.proven (semua kombinasi bagian)', () => {
    for (const effort of EFFORTS) {
      for (const proven of [undefined, true]) {
        for (const polished of [undefined, true]) {
          for (const reviewer of [undefined, 'ai', 'rules'] as const) {
            for (const roadSource of [undefined, 'mapbox', 'estimate']) {
              const line = methodLine({ effort, trials: 4, rounds: reviewer ? 2 : undefined, polished, reviewer, proven, roadSource })!
              expect(/optimal/i.test(line), `${effort} proven=${proven}: ${line}`).toBe(proven === true)
              expect(/optimal|terbukti/i.test(line)).toBe(proven === true)
            }
          }
        }
      }
    }
    setLocale('id')
    expect(methodLine({ effort: 'fast', trials: 4 })).not.toMatch(/optimal/i)
  })

  it('Indonesia: kalimat dan daftar "dan"', () => {
    setLocale('id')
    expect(methodLine({ effort: 'reviewed', trials: 12, rounds: 2, reviewer: 'ai', proven: true })).toBe('Cara menemukannya: 12 percobaan, 2 ronde tinjauan, ditinjau AI, dan terbukti optimal.')
  })
})

/* ================================================================== ringkasan AI */

const report = (text = 'Ringkasan.'): Report => ({ text, locale: 'en', at: '2026-10-21T03:00:00Z' })

describe('keadaan ringkasan AI', () => {
  it('ringkasan yang sudah tersimpan di OptionSet langsung tampil; tanpa itu menunggu diminta', () => {
    expect(initialReport(undefined)).toEqual({ kind: 'idle' })
    expect(initialReport(report())).toEqual({ kind: 'ready', report: report() })
  })

  it('alur: idle -> loading -> ready; membuat ulang menjaga yang lama sampai yang baru datang', () => {
    let s: ReportState = initialReport(undefined)
    s = reportReducer(s, { type: 'start' })
    expect(s).toEqual({ kind: 'loading', previous: undefined })
    s = reportReducer(s, { type: 'done', report: report('satu') })
    expect(s).toEqual({ kind: 'ready', report: report('satu') })
    s = reportReducer(s, { type: 'start' })
    expect(s).toEqual({ kind: 'loading', previous: report('satu') })
    // "start" berulang saat memuat tidak mengubah apa pun
    expect(reportReducer(s, { type: 'start' })).toBe(s)
    s = reportReducer(s, { type: 'done', report: report('dua') })
    expect(s).toEqual({ kind: 'ready', report: report('dua') })
  })

  it('gagal: galat dengan penjelasan; yang lama tetap ada bila membuat ulang gagal, dan mencoba lagi bisa berhasil', () => {
    let s: ReportState = reportReducer(initialReport(undefined), { type: 'start' })
    s = reportReducer(s, { type: 'fail', error: new ApiError('layanan', msg('srv.api.serviceError')) })
    expect(s).toMatchObject({ kind: 'error', problem: { kind: 'failed' } })
    expect((s as { previous?: Report }).previous).toBeUndefined()

    s = reportReducer(initialReport(report('lama')), { type: 'start' })
    s = reportReducer(s, { type: 'fail', error: new ApiError('layanan', msg('srv.ai.report.unverified')) })
    expect(s).toMatchObject({ kind: 'error', problem: { kind: 'unverified' }, previous: report('lama') })
    // coba lagi dari galat: kembali memuat dengan teks lama
    s = reportReducer(s, { type: 'start' })
    expect(s).toEqual({ kind: 'loading', previous: report('lama') })
  })

  it('galat dibedakan: belum ada opsi, tidak lolos pemeriksaan angka, dan galat AI umum', () => {
    setLocale('en')
    const none = reportProblem(new ApiError('tidak_valid', msg('srv.ai.report.noOptions')))
    expect(none).toEqual({ kind: 'noOptions', message: 'There are no options to explain yet.' })
    const unverified = reportProblem(new ApiError('layanan', msg('srv.ai.report.unverified')))
    expect(unverified).toEqual({ kind: 'unverified', message: 'The AI summary could not be checked against the numbers, so it is not shown.' })
    const off = reportProblem(new ApiError('ditolak', msg('srv.api.aiDisabled', { feature: msg('srv.api.feature.report') })))
    expect(off.kind).toBe('failed')
    expect(off.message).toBe('Option summary has been turned off by the admin. Fill it in manually for now.')
    expect(reportProblem(new ApiError('jaringan', 'plain text')).message).toBe('plain text')
    expect(reportProblem(new Error('boom'))).toEqual({ kind: 'failed', message: 'boom' })
    expect(reportProblem('?').kind).toBe('failed')
    // bahasa layar
    setLocale('id')
    expect(reportProblem(new ApiError('tidak_valid', msg('srv.ai.report.noOptions'))).message).toBe('Belum ada opsi untuk dijelaskan.')
  })
})

async function* stream(...events: CopilotEvent[]): AsyncGenerator<CopilotEvent> {
  for (const e of events) yield e
}
const done: CopilotEvent = { type: 'done', actions: [], requestId: 'r' }
const NOW = new Date('2026-10-21T03:04:05.000Z')

describe('readReport', () => {
  it('menggabungkan token, mengabaikan jejak alat dan done, mencatat bahasa dan waktu', async () => {
    const got = await readReport(stream({ type: 'tool', trace: { id: 't', title: 'x', status: 'done' } }, { type: 'token', text: 'Halo ' }, { type: 'token', text: 'dunia.' }, done), 'id', { now: () => NOW })
    expect(got).toEqual({ text: 'Halo dunia.', locale: 'id', at: '2026-10-21T03:04:05.000Z' })
  })

  it('bentuk backend asli: satu token berisi seluruh teks lalu done', async () => {
    const text = 'Opsi terbaik ...\n\nParagraf dua.'
    expect(await readReport(stream({ type: 'token', text }, done), 'en', { now: () => NOW })).toMatchObject({ text, locale: 'en' })
  })

  it('tanpa teks (kosong atau hanya spasi) adalah galat layanan, bukan ringkasan kosong', async () => {
    await expect(readReport(stream(done), 'en')).rejects.toMatchObject({ code: 'layanan', msg: { key: 'srv.api.serviceError' } })
    await expect(readReport(stream({ type: 'token', text: '  \n' }, done), 'en')).rejects.toBeInstanceOf(ApiError)
  })

  it('galat dari aliran diteruskan apa adanya', async () => {
    async function* failing(): AsyncGenerator<CopilotEvent> {
      yield { type: 'token', text: 'sebagian' }
      throw new ApiError('layanan', msg('srv.ai.report.unverified'))
    }
    await expect(readReport(failing(), 'en')).rejects.toMatchObject({ msg: { key: 'srv.ai.report.unverified' } })
  })

  it('dibatalkan di tengah jalan (current() false): null dan aliran ditutup', async () => {
    const closed = vi.fn()
    async function* source(): AsyncGenerator<CopilotEvent> {
      try {
        yield { type: 'token', text: 'a' }
        yield { type: 'token', text: 'b' }
        yield done
      } finally {
        closed()
      }
    }
    let alive = true
    const result = await readReport(source(), 'en', {
      current: () => {
        const was = alive
        alive = false // sah untuk peristiwa pertama saja
        return was
      },
    })
    expect(result).toBeNull()
    expect(closed).toHaveBeenCalled()
  })

  it('dibatalkan setelah aliran habis: null (hasilnya dibuang)', async () => {
    expect(await readReport(stream({ type: 'token', text: 'a' }, done), 'en', { current: () => false })).toBeNull()
  })
})

describe('teks ringkasan', () => {
  it('paragraf dipisah baris kosong dan dirapikan; HTML tidak diurai (tetap string biasa)', () => {
    expect(reportParagraphs('Satu.\n\nDua baris\nsatu paragraf.\n \n\n  Tiga.  ')).toEqual(['Satu.', 'Dua baris\nsatu paragraf.', 'Tiga.'])
    expect(reportParagraphs('<img src=x onerror=alert(1)>\n\n**tebal**')).toEqual(['<img src=x onerror=alert(1)>', '**tebal**'])
    expect(reportParagraphs('   ')).toEqual([])
  })

  it('nama bahasa ringkasan', () => {
    expect(reportLanguage('en')).toBe('English')
    expect(reportLanguage('id')).toBe('Bahasa Indonesia')
    expect(reportLanguage('fr')).toBe('FR')
    expect(reportLanguage('')).toBe('')
  })
})
