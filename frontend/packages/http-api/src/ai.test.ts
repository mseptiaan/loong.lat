import { afterEach, describe, expect, it, vi } from 'vitest'
import { getLocale, setLocale } from '@loong/i18n'
import { createHttpApi, type HttpApiOptions } from './client'
import { chunked, fakeBackend, json, wireError } from './fake'
import { isOutage } from './ai'
import { ApiError } from '@loong/domain'
import type { ImageScaler } from './media'

const startLocale = getLocale()
afterEach(() => setLocale(startLocale))

function setup(o: Partial<HttpApiOptions> & { fetchFor?: (path: string) => Promise<Response> | undefined } = {}) {
  const backend = fakeBackend()
  backend.handle('/healthz', () => json({ ok: true }))
  const m = new Map<string, string>()
  const storage = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v), removeItem: (k: string) => void m.delete(k) }
  const { fetchFor, ...rest } = o
  // `fetchFor` memutus sambungan untuk jalur tertentu (galat jaringan sungguhan, bukan jawaban HTTP)
  const fetchFn = ((input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url).pathname
    return fetchFor?.(path) ?? backend.fetch(input, init)
  }) as typeof fetch
  const api = createHttpApi({ app: 'console', baseUrl: 'http://api.test', fetch: fetchFn, storage, retryMs: 50, ...rest })
  return { api, backend }
}

async function collect<T>(it: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = []
  for await (const e of it) out.push(e)
  return out
}

const down = () => Promise.reject(new TypeError('Failed to fetch'))
const SPEECH = 'dua kardus sedang dan satu laptop'

describe('isOutage', () => {
  it('hanya jaringan dan layanan; penolakan, validasi, konflik, dan galat biasa bukan', () => {
    expect(isOutage(new ApiError('jaringan', 'x'))).toBe(true)
    expect(isOutage(new ApiError('layanan', 'x'))).toBe(true)
    for (const code of ['ditolak', 'tidak_valid', 'tidak_ditemukan', 'bentrok'] as const) expect(isOutage(new ApiError(code, 'x'))).toBe(false)
    expect(isOutage(new Error('x'))).toBe(false)
  })
})

describe('ai.parseSpeech dan ai.parseIntent: backend dulu, mesin lokal hanya saat layanan mati', () => {
  it('jawaban backend dipakai apa adanya (badan memakai nama parameter, ada Accept-Language)', async () => {
    setLocale('id')
    const { api, backend } = setup()
    const parse = { items: [], matches: [], leftover: 'dari backend' }
    backend.handle('/v1/rpc/ai.parseSpeech', () => json(parse))
    backend.handle('/v1/rpc/ai.parseIntent', () => json({ chips: [], understood: [], leftover: 'dari backend' }))

    expect(await api.ai.parseSpeech(SPEECH)).toEqual(parse)
    expect((await api.ai.parseIntent('paling hemat')).leftover).toBe('dari backend')
    expect(backend.rpc('ai.parseSpeech')[0]).toMatchObject({ body: { transcript: SPEECH }, headers: { 'accept-language': 'id' } })
    expect(backend.rpc('ai.parseIntent')[0]?.body).toEqual({ text: 'paling hemat' })
    api.destroy()
  })

  it('layanan mati (galat layanan, 5xx, atau jaringan putus): mesin lokal menjawab, wizard tetap jalan', async () => {
    const down503 = setup()
    down503.backend.handle('/v1/rpc/ai.parseSpeech', () => wireError(503, 'layanan', 'srv.api.aiError'))
    const speech = await down503.api.ai.parseSpeech(SPEECH)
    expect(speech.items.map((i) => i.key)).toEqual(['kardus-sedang', 'laptop'])
    expect(down503.backend.rpc('ai.parseSpeech')).toHaveLength(1) // backend dicoba dulu
    down503.api.destroy()

    const html502 = setup()
    html502.backend.handle('/v1/rpc/ai.parseIntent', () => new Response('<html>bad gateway</html>', { status: 502 }))
    expect((await html502.api.ai.parseIntent('paling hemat')).chips[0]?.kind).toBe('prioritas')
    html502.api.destroy()

    const offline = setup({ fetchFor: (p) => (p === '/v1/rpc/ai.parseSpeech' ? down() : undefined) })
    expect((await offline.api.ai.parseSpeech(SPEECH)).items).toHaveLength(2)
    offline.api.destroy()
  })

  it('penolakan dan galat validasi diteruskan; TIDAK diulang di mesin lokal', async () => {
    const { api, backend } = setup()
    backend.handle('/v1/rpc/ai.parseSpeech', () => wireError(403, 'ditolak', 'srv.api.aiDisabled'))
    backend.handle('/v1/rpc/ai.parseIntent', () => wireError(422, 'tidak_valid', 'srv.api.intentEmpty', { field: 'text' }))

    // mesin lokal akan berhasil memahami kalimat ini; kalau ia dipakai, tes ini gagal
    await expect(api.ai.parseSpeech(SPEECH)).rejects.toMatchObject({ code: 'ditolak', msg: { key: 'srv.api.aiDisabled' } })
    await expect(api.ai.parseIntent('paling hemat')).rejects.toMatchObject({ code: 'tidak_valid', field: 'text' })
    expect(backend.rpc('ai.parseSpeech')).toHaveLength(1)
    expect(backend.rpc('ai.parseIntent')).toHaveLength(1)
    api.destroy()
  })
})

describe('ai.readImage', () => {
  const b64 = (n: number, fill = 65) => btoa(String.fromCharCode(fill).repeat(n))
  const img = (over: Partial<{ name: string; size: number; data: ArrayBuffer | undefined; type: string }> = {}) => ({
    name: 'foto-kue.jpg',
    type: 'image/jpeg',
    size: 90_000,
    data: new Uint8Array(90_000).fill(65).buffer as ArrayBuffer | undefined,
    ...over,
  })
  const ndjson = (...lines: object[]) => chunked(lines.map((l) => `${JSON.stringify(l)}\n`), 'application/x-ndjson')

  it('mengirim {"input":{name,type,size,data(base64)}} dan meneruskan aliran backend', async () => {
    setLocale('id')
    const { api, backend } = setup()
    backend.handle('/v1/rpc/ai.readImage', () =>
      ndjson({ type: 'progress', step: 1, total: 2, label: 'Membaca' }, { type: 'done', items: [{ key: 'kue' }], summary: 'ok' }),
    )
    const events = await collect(api.ai.readImage(img()))
    expect(events).toEqual([{ type: 'progress', step: 1, total: 2, label: 'Membaca' }, { type: 'done', items: [{ key: 'kue' }], summary: 'ok' }])
    const [call] = backend.rpc('ai.readImage')
    expect(call?.body).toEqual({ input: { name: 'foto-kue.jpg', type: 'image/jpeg', size: 90_000, data: b64(90_000) } })
    expect(call?.headers).toMatchObject({ 'accept-language': 'id', accept: 'application/x-ndjson' })
    api.destroy()
  })

  it('foto besar diperkecil dulu: yang dikirim adalah hasil pengecilan (JPEG), bukan berkas asli', async () => {
    const scale = vi.fn<ImageScaler>(async () => ({ type: 'image/jpeg', data: new Uint8Array(1200).fill(66).buffer }))
    const { api, backend } = setup({ imageScaler: scale })
    backend.handle('/v1/rpc/ai.readImage', () => ndjson({ type: 'done', items: [], summary: '' }))
    await collect(api.ai.readImage(img({ name: 'besar.png', type: 'image/png', size: 2_000_000, data: new Uint8Array(2_000_000).buffer })))
    expect(scale).toHaveBeenCalledOnce()
    expect(backend.rpc('ai.readImage')[0]?.body).toEqual({ input: { name: 'besar.png', type: 'image/jpeg', size: 1200, data: b64(1200, 66) } })
    api.destroy()
  })

  it('layanan mati sebelum aliran dimulai: mesin lokal menjalankan tahapannya sampai selesai', async () => {
    const { api, backend } = setup()
    backend.handle('/v1/rpc/ai.readImage', () => wireError(503, 'layanan', 'srv.api.aiError'))
    const events = await collect(api.ai.readImage(img()))
    expect(events.filter((e) => (e as { type: string }).type === 'progress')).toHaveLength(4)
    const done = events.at(-1) as { type: string; items: { key: string }[] }
    expect(done.type).toBe('done')
    expect(done.items[0]?.key).toBe('kue')
    expect(backend.rpc('ai.readImage')).toHaveLength(1)
    api.destroy()
  })

  it('layanan mati di tengah aliran (galat di baris NDJSON): dilanjutkan oleh mesin lokal', async () => {
    const { api, backend } = setup()
    backend.handle('/v1/rpc/ai.readImage', () =>
      ndjson({ type: 'progress', step: 1, total: 4, label: 'dari backend' }, { type: 'error', error: { code: 'layanan', msg: { key: 'srv.api.aiError' } } }),
    )
    const events = (await collect(api.ai.readImage(img()))) as { type: string; label?: string }[]
    expect(events[0]).toMatchObject({ type: 'progress', label: 'dari backend' })
    expect(events.at(-1)?.type).toBe('done')
    api.destroy()
  })

  it('jaringan putus: mesin lokal', async () => {
    const { api } = setup({ fetchFor: (p) => (p === '/v1/rpc/ai.readImage' ? down() : undefined) })
    const events = await collect(api.ai.readImage(img()))
    expect((events.at(-1) as { type: string }).type).toBe('done')
    api.destroy()
  })

  it('ditolak (dimatikan admin) dan galat validasi diteruskan, tanpa satu pun hasil dari mesin lokal', async () => {
    const refused = setup()
    refused.backend.handle('/v1/rpc/ai.readImage', () => wireError(403, 'ditolak', 'srv.api.aiDisabled'))
    const seen: unknown[] = []
    const run = (async () => {
      for await (const e of refused.api.ai.readImage(img())) seen.push(e)
    })()
    await expect(run).rejects.toMatchObject({ code: 'ditolak', msg: { key: 'srv.api.aiDisabled' } })
    expect(seen).toEqual([])
    refused.api.destroy()

    const invalid = setup()
    invalid.backend.handle('/v1/rpc/ai.readImage', () => wireError(422, 'tidak_valid', 'srv.api.fileNotImage', { field: 'file' }))
    await expect(collect(invalid.api.ai.readImage(img()))).rejects.toMatchObject({ code: 'tidak_valid', field: 'file' })
    invalid.api.destroy()
  })

  it('foto yang tidak bisa dikirim (tanpa data, atau terlalu besar dan tak bisa diperkecil): mesin lokal, backend tidak disentuh', async () => {
    const { api, backend } = setup() // node: tanpa canvas, jadi tidak ada pengecil
    const noData = await collect(api.ai.readImage(img({ data: undefined })))
    expect((noData.at(-1) as { type: string }).type).toBe('done')
    const huge = await collect(api.ai.readImage(img({ size: 5_000_000, data: new Uint8Array(5_000_000).buffer })))
    expect((huge.at(-1) as { type: string }).type).toBe('done')
    expect(backend.rpc('ai.readImage')).toHaveLength(0)
    api.destroy()
  })
})

describe('ai.transcribe', () => {
  const audio = (n: number, type = 'audio/wav') => ({ type, size: n, data: new TextEncoder().encode('x'.repeat(n)).buffer })

  it('mengirim {"input":{type,size,data(base64)}} dan mengembalikan transkrip', async () => {
    setLocale('id')
    const { api, backend } = setup()
    backend.handle('/v1/rpc/ai.transcribe', () => json({ transcript: 'dua kardus sedang' }))
    expect(await api.ai.transcribe(audio(6))).toEqual({ transcript: 'dua kardus sedang' })
    const [call] = backend.rpc('ai.transcribe')
    expect(call?.body).toEqual({ input: { type: 'audio/wav', size: 6, data: btoa('xxxxxx') } })
    expect(call?.headers['accept-language']).toBe('id')
    api.destroy()
  })

  it('tanpa cadangan lokal: layanan mati menolak, supaya layar jatuh ke Web Speech API atau ketik', async () => {
    const { api, backend } = setup()
    backend.handle('/v1/rpc/ai.transcribe', () => wireError(503, 'layanan', 'srv.api.aiError'))
    await expect(api.ai.transcribe(audio(6))).rejects.toMatchObject({ code: 'layanan' })
    expect(backend.rpc('ai.transcribe')).toHaveLength(1)

    const offline = setup({ fetchFor: (p) => (p === '/v1/rpc/ai.transcribe' ? down() : undefined) })
    await expect(offline.api.ai.transcribe(audio(6))).rejects.toMatchObject({ code: 'jaringan' })
    api.destroy()
    offline.api.destroy()
  })

  it('penolakan backend diteruskan', async () => {
    const { api, backend } = setup()
    backend.handle('/v1/rpc/ai.transcribe', () => wireError(403, 'ditolak', 'srv.api.aiDisabled'))
    await expect(api.ai.transcribe(audio(6))).rejects.toMatchObject({ code: 'ditolak' })
    api.destroy()
  })

  it('rekaman kosong atau terlalu besar ditolak di klien tanpa menyentuh backend', async () => {
    const { api, backend } = setup({ maxBodyBytes: 1000 })
    await expect(api.ai.transcribe({ type: 'audio/wav', size: 0 })).rejects.toMatchObject({ code: 'tidak_valid', msg: { key: 'srv.api.noSpeech' } })
    await expect(api.ai.transcribe(audio(2000))).rejects.toMatchObject({ code: 'tidak_valid', msg: { key: 'srv.api.audioTooLarge' } })
    expect(backend.rpc('ai.transcribe')).toHaveLength(0)
    api.destroy()
  })
})
