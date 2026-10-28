import { afterEach, describe, expect, it, vi } from 'vitest'
import type { AppEvent } from '@loong/domain'
import { ApiError } from '@loong/domain'
import { getLocale, setLocale } from '@loong/i18n'
import { chunked, fakeBackend, json, wireError } from './fake'
import { createTransport, frames, lines } from './transport'

// null = tanpa token (undefined akan memicu nilai bawaan)
function setup(token: string | null = 'tok') {
  const backend = fakeBackend()
  const onUnauthorized = vi.fn()
  const transport = createTransport({
    baseUrl: 'http://api.test',
    fetch: backend.fetch,
    getToken: () => token ?? undefined,
    onUnauthorized,
    timeoutMs: 1000,
  })
  return { backend, transport, onUnauthorized }
}

const bodyOf = (chunks: string[]) =>
  chunked(chunks, 'text/plain').body as ReadableStream<Uint8Array>

async function collect<T>(it: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = []
  for await (const v of it) out.push(v)
  return out
}

const startLocale = getLocale()
afterEach(() => {
  vi.useRealTimers()
  setLocale(startLocale)
})

describe('Accept-Language', () => {
  it('panggilan membawa bahasa layar, dan ganti bahasa langsung berlaku di panggilan berikutnya', async () => {
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/wallet.get', () => json({}))
    setLocale('en')
    await transport.call('wallet.get', {})
    setLocale('id')
    await transport.call('wallet.get', {})
    expect(backend.calls.map((c) => c.headers['accept-language'])).toEqual(['en', 'id'])
  })

  it('juga di panggilan anonim (login), aliran NDJSON, dan sambungan SSE', async () => {
    vi.useFakeTimers()
    setLocale('id')
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/auth.loginCustomer', () => json({}))
    backend.handle('/v1/rpc/plans.computeOptions', () => chunked(['{"type":"done"}\n'], 'application/x-ndjson'))
    backend.handle('/v1/events', () => chunked([': ping\n\n'], 'text/event-stream', new Promise(() => undefined)))

    await transport.call('auth.loginCustomer', {}, { anonymous: true })
    for await (const e of transport.stream('plans.computeOptions', { id: 'p' })) void e
    transport.subscribe(() => undefined)
    await vi.advanceTimersByTimeAsync(0)
    await vi.advanceTimersByTimeAsync(0)

    expect(backend.calls.map((c) => [c.path, c.headers['accept-language']])).toEqual([
      ['/v1/rpc/auth.loginCustomer', 'id'],
      ['/v1/rpc/plans.computeOptions', 'id'],
      ['/v1/events', 'id'],
    ])
    transport.close()
  })
})

describe('call', () => {
  it('mengirim POST JSON dengan token dan mengembalikan hasilnya', async () => {
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/wallet.get', () => json({ balanceRp: 5000 }))
    expect(await transport.call('wallet.get', {})).toEqual({ balanceRp: 5000 })
    const [c] = backend.calls
    expect(c).toMatchObject({ method: 'POST', path: '/v1/rpc/wallet.get', body: {} })
    expect(c?.headers.authorization).toBe('Bearer tok')
    expect(c?.headers['content-type']).toBe('application/json')
  })

  it('menambahkan Idempotency-Key bila diminta, dan melewatkan token untuk panggilan anonim', async () => {
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/plans.confirm', () => json({}))
    await transport.call('plans.confirm', { id: 'p' }, { idempotencyKey: 'k-1' })
    await transport.call('plans.confirm', { id: 'p' }, { anonymous: true })
    expect(backend.calls[0]?.headers['idempotency-key']).toBe('k-1')
    expect(backend.calls[1]?.headers.authorization).toBeUndefined()
    expect(backend.calls[1]?.headers['idempotency-key']).toBeUndefined()
  })

  it('jawaban kosong menjadi undefined', async () => {
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/auth.logout', () => new Response(null, { status: 204 }))
    expect(await transport.call('auth.logout', {})).toBeUndefined()
  })

  it('memetakan galat backend ke ApiError lengkap dengan kode, status, kolom, dan pesan netral-bahasa', async () => {
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/plans.saveDraft', () => wireError(422, 'tidak_valid', 'srv.plan.invalid', { field: 'items' }))
    const err = await transport.call('plans.saveDraft', {}).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ code: 'tidak_valid', status: 422, field: 'items', msg: { key: 'srv.plan.invalid' } })
  })

  it('kode yang tidak dikenal atau badan bukan JSON menjadi galat layanan', async () => {
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/a', () => json({ code: 'aneh', message: 'x' }, 500))
    backend.handle('/v1/rpc/b', () => new Response('<html>bad gateway</html>', { status: 502 }))
    expect(await transport.call('a', {}).catch((e: unknown) => e)).toMatchObject({ code: 'layanan', status: 500 })
    expect(await transport.call('b', {}).catch((e: unknown) => e)).toMatchObject({ code: 'layanan', status: 502 })
  })

  it('401 dengan token memberi tahu pemilik sesi; tanpa token tidak', async () => {
    const withToken = setup('tok')
    withToken.backend.handle('/v1/rpc/x', () => wireError(401, 'ditolak', 'srv.auth.signInRequired'))
    await withToken.transport.call('x', {}).catch(() => undefined)
    expect(withToken.onUnauthorized).toHaveBeenCalledOnce()

    const anonymous = setup(null)
    anonymous.backend.handle('/v1/rpc/x', () => wireError(401, 'ditolak', 'srv.auth.badCredentials'))
    await anonymous.transport.call('x', {}).catch(() => undefined)
    expect(anonymous.onUnauthorized).not.toHaveBeenCalled()
  })

  it('API yang tidak terjangkau menjadi galat jaringan', async () => {
    const { transport } = setup()
    const down = createTransport({
      baseUrl: 'http://api.test',
      fetch: (() => Promise.reject(new TypeError('Failed to fetch'))) as typeof fetch,
      getToken: () => undefined,
      onUnauthorized: () => undefined,
      timeoutMs: 1000,
    })
    expect(await down.call('x', {}).catch((e: unknown) => e)).toMatchObject({ code: 'jaringan', msg: { key: 'srv.rpc.serverUnreachable' } })
    transport.close()
  })

  it('healthy mengikuti /healthz', async () => {
    const { backend, transport } = setup()
    expect(await transport.healthy()).toBe(false) // 404: belum ada penangan
    backend.handle('/healthz', () => json({ ok: true }))
    expect(await transport.healthy()).toBe(true)
  })
})

describe('stream (NDJSON)', () => {
  it('membaca satu objek per baris, juga ketika baris terpotong di batas potongan', async () => {
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/plans.computeOptions', () =>
      chunked(['{"type":"progress","step":1}\n{"type":"prog', 'ress","step":2}\n', '{"type":"done","optionSet":{}}'], 'application/x-ndjson'),
    )
    const got = await collect(transport.stream('plans.computeOptions', { id: 'p' }))
    expect(got).toEqual([{ type: 'progress', step: 1 }, { type: 'progress', step: 2 }, { type: 'done', optionSet: {} }])
    expect(backend.calls[0]?.headers.accept).toBe('application/x-ndjson')
  })

  it('baris error mengakhiri aliran dengan ApiError', async () => {
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/plans.computeOptions', () =>
      chunked(['{"type":"progress","step":1}\n', '{"type":"error","error":{"code":"layanan","msg":{"key":"srv.engine.unavailable"}}}\n'], 'application/x-ndjson'),
    )
    const seen: unknown[] = []
    const err = await (async () => {
      for await (const e of transport.stream('plans.computeOptions', { id: 'p' })) seen.push(e)
    })().catch((e: unknown) => e)
    expect(seen).toEqual([{ type: 'progress', step: 1 }])
    expect(err).toMatchObject({ code: 'layanan', msg: { key: 'srv.engine.unavailable' } })
  })

  it('status galat sebelum aliran dimulai menjadi ApiError', async () => {
    const { backend, transport } = setup()
    backend.handle('/v1/rpc/plans.computeOptions', () => wireError(404, 'tidak_ditemukan', 'srv.plan.notFound'))
    const err = await collect(transport.stream('plans.computeOptions', { id: 'p' })).catch((e: unknown) => e)
    expect(err).toMatchObject({ code: 'tidak_ditemukan', status: 404 })
  })
})

describe('lines dan frames', () => {
  it('lines melewatkan baris kosong dan menutup dengan sisa tanpa baris baru', async () => {
    expect(await collect(lines(bodyOf(['a\n\n  b  \n', 'c'])))).toEqual(['a', 'b', 'c'])
  })

  it('frames mengambil data: saja, menggabungkan baris data, dan mengabaikan komentar, retry, serta CRLF', async () => {
    const input = ['retry: 3000\n\n: ping\n\n', 'data: {"a":1}\r\n\r\n', 'data: {"b":\n', 'data: 2}\n\n', 'event: x\n\n']
    expect(await collect(frames(bodyOf(input)))).toEqual(['{"a":1}', '{"b":\n2}'])
  })

  it('frames tahan terhadap bingkai yang terbelah di tengah batas "\\n\\n"', async () => {
    expect(await collect(frames(bodyOf(['data: 1\n', '\ndata: 2\n\n'])))).toEqual(['1', '2'])
  })
})

describe('events (SSE)', () => {
  const tick = () => vi.advanceTimersByTimeAsync(0)

  it('mengirim event dari bingkai data, dengan token di header', async () => {
    vi.useFakeTimers()
    const { backend, transport } = setup()
    let release!: () => void
    const hold = new Promise<void>((r) => (release = r))
    backend.handle('/v1/events', () =>
      chunked(['retry: 3000\n\n', 'data: {"type":"plans.changed","planId":"p1"}\n\n', ': ping\n\n'], 'text/event-stream', hold),
    )
    const got: AppEvent[] = []
    transport.subscribe((e) => got.push(e))
    await tick()
    await tick()
    expect(got).toEqual([{ type: 'plans.changed', planId: 'p1' }])
    expect(backend.calls[0]?.headers.authorization).toBe('Bearer tok')
    release()
    transport.close()
  })

  it('tanpa token tidak menyambung', async () => {
    vi.useFakeTimers()
    const { backend, transport } = setup(null)
    transport.subscribe(() => undefined)
    await tick()
    expect(backend.calls).toHaveLength(0)
    transport.close()
  })

  it('menyambung ulang dengan jeda dan memberi data.reset setelah sambungan kedua, bukan yang pertama', async () => {
    vi.useFakeTimers()
    const { backend, transport } = setup()
    let n = 0
    backend.handle('/v1/events', () => {
      n++
      return chunked([`data: {"type":"plans.changed","planId":"p${n}"}\n\n`], 'text/event-stream') // aliran berakhir sendiri
    })
    const got: AppEvent[] = []
    transport.subscribe((e) => got.push(e))
    await tick()
    await tick()
    expect(got.map((e) => e.type)).toEqual(['plans.changed'])

    await vi.advanceTimersByTimeAsync(999)
    expect(n).toBe(1) // masih menunggu jeda 1 dtk
    await vi.advanceTimersByTimeAsync(1)
    await tick()
    expect(got.map((e) => e.type)).toEqual(['plans.changed', 'data.reset', 'plans.changed'])
    transport.close()
  })

  it('401 pada aliran event menghentikan sambungan dan memberi tahu pemilik sesi', async () => {
    vi.useFakeTimers()
    const { backend, transport, onUnauthorized } = setup()
    backend.handle('/v1/events', () => wireError(401, 'ditolak', 'srv.auth.signInRequired'))
    transport.subscribe(() => undefined)
    await tick()
    await vi.advanceTimersByTimeAsync(30_000)
    expect(onUnauthorized).toHaveBeenCalledOnce()
    expect(backend.calls).toHaveLength(1)
    transport.close()
  })

  it('berhenti berlangganan menutup koneksi bila tak ada pendengar tersisa', async () => {
    vi.useFakeTimers()
    const { backend, transport } = setup()
    backend.handle('/v1/events', () => chunked([], 'text/event-stream'))
    const off = transport.subscribe(() => undefined)
    await tick()
    off()
    await vi.advanceTimersByTimeAsync(30_000)
    expect(backend.calls).toHaveLength(1) // tidak menyambung ulang
    transport.close()
  })
})
