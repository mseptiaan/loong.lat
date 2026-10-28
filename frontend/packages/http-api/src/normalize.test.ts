import { describe, expect, it } from 'vitest'
import { methodOf, normalizeResult, normalizeStream, reportOf } from './normalize'

async function drain(it: AsyncIterable<unknown>): Promise<unknown[]> {
  const out: unknown[] = []
  for await (const e of it) out.push(e)
  return out
}
async function* from(...events: unknown[]) {
  for (const e of events) yield e
}

describe('methodOf: cara pencarian dari kawat', () => {
  it('menerima bentuk lengkap apa adanya', () => {
    const m = { effort: 'reviewed', trials: 14, rounds: 2, polished: true, reviewer: 'ai', proven: true, roadSource: 'mapbox-cache' }
    expect(methodOf(m)).toEqual(m)
  })

  it('bagian yang hilang tetap hilang (omitempty di Go): tidak diisi nol atau false', () => {
    const m = methodOf({ effort: 'fast', trials: 6 })
    expect(m).toEqual({ effort: 'fast', trials: 6 })
    expect(Object.keys(m!)).toEqual(['effort', 'trials'])
    // nol dan false yang dikirim eksplisit juga dibuang, supaya ringkasan tidak menyebut "0 ronde"
    expect(methodOf({ effort: 'thorough', trials: 9, rounds: 0, polished: false, proven: false, reviewer: '', roadSource: '' })).toEqual({ effort: 'thorough', trials: 9 })
  })

  it('tingkat yang tidak dikenal atau bentuk salah membuang seluruh method', () => {
    expect(methodOf({ effort: 'turbo', trials: 3 })).toBeUndefined()
    expect(methodOf({ trials: 3 })).toBeUndefined()
    expect(methodOf(null)).toBeUndefined()
    expect(methodOf('fast')).toBeUndefined()
    expect(methodOf([])).toBeUndefined()
  })

  it('angka rusak dirapikan: trials tak valid menjadi 0, pecahan dibulatkan ke bawah, negatif dibuang', () => {
    expect(methodOf({ effort: 'fast', trials: 'banyak' })).toEqual({ effort: 'fast', trials: 0 })
    expect(methodOf({ effort: 'fast', trials: -4 })).toEqual({ effort: 'fast', trials: 0 })
    expect(methodOf({ effort: 'reviewed', trials: 7.9, rounds: 2.6 })).toEqual({ effort: 'reviewed', trials: 7, rounds: 2 })
    expect(methodOf({ effort: 'reviewed', trials: 7, rounds: -1 })).toEqual({ effort: 'reviewed', trials: 7 })
  })

  it('peninjau yang tidak dikenal dibuang; proven hanya bila persis true', () => {
    expect(methodOf({ effort: 'reviewed', trials: 1, reviewer: 'robot' })).toEqual({ effort: 'reviewed', trials: 1 })
    expect(methodOf({ effort: 'fast', trials: 1, proven: 'true' })).toEqual({ effort: 'fast', trials: 1 })
    expect(methodOf({ effort: 'fast', trials: 1, proven: 1 })).toEqual({ effort: 'fast', trials: 1 })
  })
})

describe('reportOf: laporan AI dari kawat', () => {
  it('menerima laporan lengkap', () => {
    const r = { text: 'Opsi terbaik ...', locale: 'id', at: '2026-10-21T03:00:00Z' }
    expect(reportOf(r)).toEqual(r)
  })

  it('tanpa teks (kosong, spasi, bukan string) bukan laporan', () => {
    expect(reportOf({ text: '', locale: 'en', at: '2026-10-21T03:00:00Z' })).toBeUndefined()
    expect(reportOf({ text: '  \n', locale: 'en' })).toBeUndefined()
    expect(reportOf({ text: 42 })).toBeUndefined()
    expect(reportOf(null)).toBeUndefined()
  })

  it('teks tidak diubah (tidak di-trim, tidak di-HTML): layar menampilkannya sebagai teks biasa', () => {
    expect(reportOf({ text: ' <b>x</b>\n\nlagi ', locale: 'en', at: '2026-10-21T03:00:00Z' })?.text).toBe(' <b>x</b>\n\nlagi ')
  })

  it('waktu nol Go atau hilang menjadi string kosong; bahasa hilang menjadi kosong', () => {
    expect(reportOf({ text: 'a', locale: 'en', at: '0001-01-01T00:00:00Z' })).toEqual({ text: 'a', locale: 'en', at: '' })
    expect(reportOf({ text: 'a' })).toEqual({ text: 'a', locale: '', at: '' })
  })
})

describe('plan dan optionSet dari backend', () => {
  it('method dan report dirapikan di semua jawaban yang membawa plan', () => {
    for (const name of ['plans.get', 'plans.saveDraft', 'plans.chooseOption', 'plans.confirm', 'plans.cancel', 'plans.reschedule', 'plans.createDraft']) {
      const out = normalizeResult(name, {
        id: 'p1',
        status: 'opsi_siap',
        optionSet: { options: [], method: { effort: 'thorough', trials: 5, rounds: 0 }, report: { text: 'x', locale: 'en', at: '0001-01-01T00:00:00Z' } },
      }) as { optionSet: { method: unknown; report: unknown } }
      expect(out.optionSet.method, name).toEqual({ effort: 'thorough', trials: 5 })
      expect(out.optionSet.report, name).toEqual({ text: 'x', locale: 'en', at: '' })
    }
  })

  it('plan tanpa optionSet tidak disentuh; optionSet null berarti belum ada', () => {
    const bare = { id: 'p1', status: 'draft' }
    expect(normalizeResult('plans.get', bare)).toEqual(bare)
    expect(normalizeResult('plans.get', { id: 'p1', optionSet: null })).toEqual({ id: 'p1' })
  })

  it('method yang rusak dibuang dari optionSet; kolom lain tidak berubah', () => {
    const out = normalizeResult('plans.get', { id: 'p1', optionSet: { inputHash: 'abc', options: [1], method: { effort: 'warp', trials: 1 } } }) as { optionSet: Record<string, unknown> }
    expect(out.optionSet).toEqual({ inputHash: 'abc', options: [1] })
  })
})

describe('normalizeStream', () => {
  it('progres computeOptions: msg sah dipertahankan, rusak dibuang, label selalu string', async () => {
    const got = await drain(
      normalizeStream(
        'plans.computeOptions',
        from(
          { type: 'progress', step: 3, total: 4, msg: { key: 'srv.engine.search.round', params: { round: 2 } } },
          { type: 'progress', step: 3, total: 4, label: 'ok', msg: { params: {} } },
          { type: 'progress', step: '3', total: null },
        ),
      ),
    )
    expect(got).toEqual([
      { type: 'progress', step: 3, total: 4, label: '', msg: { key: 'srv.engine.search.round', params: { round: 2 } } },
      { type: 'progress', step: 3, total: 4, label: 'ok' },
      { type: 'progress', step: 0, total: 0, label: '' },
    ])
  })

  it('done computeOptions: optionSet dirapikan', async () => {
    const [done] = await drain(normalizeStream('plans.computeOptions', from({ type: 'done', optionSet: { options: [], method: { effort: 'fast', trials: 2 } } })))
    expect(done).toEqual({ type: 'done', optionSet: { options: [], method: { effort: 'fast', trials: 2 } } })
  })

  it('done milik aliran AI: actions null menjadi daftar kosong; token dan alat lewat apa adanya', async () => {
    for (const method of ['ai.copilot', 'ai.insights', 'ai.explainOptions']) {
      const events = [{ type: 'tool', trace: { id: 't' } }, { type: 'token', text: 'halo' }, { type: 'done', actions: null, requestId: 'r' }]
      expect(await drain(normalizeStream(method, from(...events)))).toEqual([events[0], events[1], { type: 'done', actions: [], requestId: 'r' }])
    }
  })

  it('metode lain dan peristiwa aneh lewat apa adanya', async () => {
    const events = [{ type: 'progress', step: 1 }, 'teks', null, 7]
    expect(await drain(normalizeStream('ai.readImage', from(...events)))).toEqual(events)
  })

  it('berhenti lebih awal menutup aliran asal (pembatalan sampai ke jaringan)', async () => {
    let closed = false
    async function* source() {
      try {
        yield { type: 'token', text: 'a' }
        yield { type: 'token', text: 'b' }
      } finally {
        closed = true
      }
    }
    const it = normalizeStream('ai.explainOptions', source())[Symbol.asyncIterator]()
    await it.next()
    await it.return(undefined)
    expect(closed).toBe(true)
  })
})
