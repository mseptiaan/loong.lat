import { afterEach, describe, expect, it, vi } from 'vitest'
import type { AppEvent, Session } from '@loong/domain'
import { DEFAULT_SETTINGS } from '@loong/domain'
import { getLocale, setLocale } from '@loong/i18n'
import { createHttpApi, type HttpApiOptions } from './client'
import { chunked, customer, fakeBackend, json, wireError, type FakeBackend } from './fake'

const KEY = (app: string) => `loong:http:${app}`

function memoryStorage(initial: Record<string, string> = {}) {
  const m = new Map(Object.entries(initial))
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
    raw: m,
  }
}

type Opts = Partial<HttpApiOptions> & { healthy?: boolean }

function setup(app: HttpApiOptions['app'], o: Opts = {}) {
  const backend = fakeBackend()
  if (o.healthy !== false) backend.handle('/healthz', () => json({ ok: true }))
  const storage = o.storage ?? memoryStorage()
  const api = createHttpApi({ app, baseUrl: 'http://api.test/', fetch: backend.fetch, storage, retryMs: 50, ...o })
  return { api, backend, storage: storage as ReturnType<typeof memoryStorage> }
}

const flush = () => vi.advanceTimersByTimeAsync(0)

const startLocale = getLocale()
afterEach(() => {
  vi.useRealTimers()
  setLocale(startLocale)
})

describe('status', () => {
  it('menjadi siap setelah /healthz menjawab', async () => {
    const { api } = setup('console')
    expect(api.status()).toBe('menghubungkan')
    await api.ready()
    expect(api.status()).toBe('siap')
    api.destroy()
  })

  it('gagal dengan alasan, lalu mencoba lagi sampai API menjawab', async () => {
    vi.useFakeTimers()
    const { api, backend } = setup('console', { healthy: false })
    const seen: string[] = []
    api.onStatus((s) => seen.push(s))
    await flush()
    expect(api.status()).toBe('gagal')

    backend.handle('/healthz', () => json({ ok: true }))
    await vi.advanceTimersByTimeAsync(50)
    expect(api.status()).toBe('siap')
    expect(seen).toEqual(['menghubungkan', 'gagal', 'siap'])
    api.destroy()
  })

  it('destroy menghentikan upaya menyambung ulang', async () => {
    vi.useFakeTimers()
    const { api, backend } = setup('console', { healthy: false })
    await flush()
    api.destroy()
    const before = backend.calls.length
    await vi.advanceTimersByTimeAsync(1000)
    expect(backend.calls.length).toBe(before)
  })
})

describe('masuk dan sesi', () => {
  it('masuk menyimpan token, mengembalikan sesi tanpa token, dan memakai token pada panggilan berikutnya', async () => {
    const { api, backend, storage } = setup('console')
    backend.handle('/v1/rpc/auth.loginCustomer', () => json(customer))
    backend.handle('/v1/rpc/wallet.get', () => json({ balanceRp: 12000 }))

    const sessions: (Session | null)[] = []
    api.onSession((s) => sessions.push(s))
    const session = await api.auth.loginCustomer('dina@contoh.id', 'rahasia')

    expect(session).toEqual({ role: 'customer', userId: 'cus-1', name: 'Dina' })
    expect(api.getSession()).toEqual(session)
    expect(sessions).toEqual([session])
    expect(JSON.parse(storage.raw.get(KEY('console'))!)).toEqual({ token: 'tok-customer', session })

    // badan memakai nama parameter, dan login tidak membawa token lama
    expect(backend.rpc('auth.loginCustomer')[0]).toMatchObject({ body: { email: 'dina@contoh.id', password: 'rahasia' } })
    expect(backend.rpc('auth.loginCustomer')[0]?.headers.authorization).toBeUndefined()

    expect(await api.wallet.get()).toEqual({ balanceRp: 12000 })
    expect(backend.rpc('wallet.get')[0]?.headers.authorization).toBe('Bearer tok-customer')
    api.destroy()
  })

  it('galat login tidak menyimpan apa pun dan diteruskan sebagai ApiError', async () => {
    const { api, backend, storage } = setup('console')
    backend.handle('/v1/rpc/auth.loginCustomer', () => wireError(403, 'ditolak', 'srv.auth.badCredentials'))
    await expect(api.auth.loginCustomer('x@y.id', 'salah')).rejects.toMatchObject({ code: 'ditolak', msg: { key: 'srv.auth.badCredentials' } })
    expect(storage.raw.size).toBe(0)
    expect(api.getSession()).toBeNull()
    api.destroy()
  })

  it('keluar menghapus sesi dari penyimpanan dan memberi tahu pendengar', async () => {
    const { api, backend, storage } = setup('console')
    backend.handle('/v1/rpc/auth.loginCustomer', () => json(customer))
    backend.handle('/v1/rpc/auth.logout', () => new Response(null, { status: 204 }))
    await api.auth.loginCustomer('a@b.id', 'pw')
    const sessions: (Session | null)[] = []
    api.onSession((s) => sessions.push(s))

    await api.auth.logout()
    expect(api.getSession()).toBeNull()
    expect(storage.raw.has(KEY('console'))).toBe(false)
    expect(sessions).toEqual([null])
    api.destroy()
  })

  it('401 dari backend menghapus sesi yang sudah tidak berlaku', async () => {
    const { api, backend, storage } = setup('console')
    backend.handle('/v1/rpc/auth.loginCustomer', () => json(customer))
    backend.handle('/v1/rpc/wallet.get', () => wireError(401, 'ditolak', 'srv.auth.signInRequired'))
    await api.auth.loginCustomer('a@b.id', 'pw')

    await expect(api.wallet.get()).rejects.toMatchObject({ status: 401 })
    expect(api.getSession()).toBeNull()
    expect(storage.raw.has(KEY('console'))).toBe(false)
    api.destroy()
  })

  it('auth.session memulihkan sesi tersimpan, dan null bila tidak ada', async () => {
    const stored = memoryStorage({ [KEY('console')]: JSON.stringify({ token: 't', session: { role: 'customer', userId: 'u', name: 'U' } }) })
    const restored = setup('console', { storage: stored })
    expect(restored.api.getSession()).toEqual({ role: 'customer', userId: 'u', name: 'U' })
    expect(await restored.api.auth.session()).toEqual({ role: 'customer', userId: 'u', name: 'U' })
    restored.api.destroy()

    const empty = setup('console')
    expect(await empty.api.auth.session()).toBeNull()
    empty.api.destroy()
  })

  it('penyimpanan rusak diabaikan', () => {
    const { api } = setup('console', { storage: memoryStorage({ [KEY('console')]: '{bukan json' }) })
    expect(api.getSession()).toBeNull()
    api.destroy()
  })

  it('tiap aplikasi menyimpan sesinya sendiri', async () => {
    const storage = memoryStorage()
    const a = setup('console', { storage })
    a.backend.handle('/v1/rpc/auth.loginCustomer', () => json(customer))
    await a.api.auth.loginCustomer('a@b.id', 'pw')
    const b = setup('backoffice', { storage })
    expect(b.api.getSession()).toBeNull()
    a.api.destroy()
    b.api.destroy()
  })
})

describe('pengiriman metode', () => {
  it('operasi uang membawa Idempotency-Key baru di setiap panggilan', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/plans.confirm', () => json({ id: 'p1' }))
    await api.plans.confirm('p1', 'saldo')
    await api.plans.confirm('p1', 'saldo')
    const keys = backend.rpc('plans.confirm').map((c) => c.headers['idempotency-key'])
    expect(keys[0]).toMatch(/^[0-9a-f-]{36}$/)
    expect(keys[1]).toMatch(/^[0-9a-f-]{36}$/)
    expect(keys[0]).not.toBe(keys[1])
    expect(backend.rpc('plans.confirm')[0]?.body).toEqual({ id: 'p1', paymentMethod: 'saldo' })
    api.destroy()
  })

  it('metode yang bukan milik API aplikasi ini dijawab klien sendiri atau ditolak jelas', async () => {
    const { api, backend } = setup('console')
    // ditolak: data pengemudi bukan milik console-api
    await expect(api.fleet.drivers.list()).rejects.toMatchObject({ code: 'tidak_ditemukan', msg: { key: 'srv.api.notAvailableYet' } })
    // dijawab sendiri: pencarian tempat (gazetteer) dan jam nyata
    expect((await api.places.search('Kemang', 3)).length).toBeGreaterThan(0)
    const demo = await api.demo.state()
    expect(demo).toMatchObject({ scenario: 'live', fault: 'none', autopilot: false, clock: { speed: 1 } })
    expect(Math.abs(demo.now - Date.now())).toBeLessThan(5000)
    expect(backend.calls.filter((c) => c.path.startsWith('/v1/rpc/'))).toHaveLength(0)
    api.destroy()
  })

  it('pengaturan, notifikasi, dan akun kini dijawab backend apa adanya (tidak ada lagi tiruan kosong)', async () => {
    const { api, backend } = setup('console')
    const settings = { ...DEFAULT_SETTINGS, ai: { ...DEFAULT_SETTINGS.ai, copilot: true, voice: false } }
    backend.handle('/v1/rpc/settings.get', () => json(settings))
    backend.handle('/v1/rpc/notifications.list', () => json([{ id: 'n1', read: false }]))
    expect(await api.settings.get()).toEqual(settings) // copilot tetap menyala: bendera datang dari backend
    expect(await api.notifications.list()).toEqual([{ id: 'n1', read: false }])
    expect(backend.rpc('settings.get')).toHaveLength(1)
    expect(backend.rpc('notifications.list')).toHaveLength(1)
    api.destroy()
  })

  it('pengaturan di backoffice juga tidak lagi dimatikan copilotnya', async () => {
    const { api, backend } = setup('backoffice')
    backend.handle('/v1/rpc/settings.get', () => json({ ...DEFAULT_SETTINGS, ai: { ...DEFAULT_SETTINGS.ai, copilot: true } }))
    expect((await api.settings.get()).ai.copilot).toBe(true)
    api.destroy()
  })

  it('kind: klien http menyebut dirinya http (Panel Demo hanya untuk tiruan)', () => {
    const { api } = setup('console')
    expect(api.kind).toBe('http')
    api.destroy()
  })

  it('metode berstream: dilayani backend jika ada, gagal jelas jika tidak ada siapa pun yang melayani', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/plans.computeOptions', () =>
      chunked(['{"type":"progress","step":1,"total":4,"label":"Membaca"}\n', '{"type":"done","optionSet":{"options":[]}}\n'], 'application/x-ndjson'),
    )
    const got: unknown[] = []
    for await (const e of api.plans.computeOptions('p1')) got.push(e)
    expect(got).toEqual([{ type: 'progress', step: 1, total: 4, label: 'Membaca' }, { type: 'done', optionSet: { options: [] } }])
    expect(backend.rpc('plans.computeOptions')[0]?.body).toEqual({ id: 'p1', effort: 'fast' }) // tingkat bawaan dikirim eksplisit

    // driver-api tidak punya Copilot dan tidak ada mesin lokalnya: gagal jelas, tidak menggantung
    const driver = setup('driver')
    const failing = (async () => {
      for await (const e of driver.api.ai.copilot({ kind: 'ops' }, 'apa kabar?')) void e
    })()
    await expect(failing).rejects.toMatchObject({ msg: { key: 'srv.api.notAvailableYet' } })
    expect(driver.backend.rpc('ai.copilot')).toHaveLength(0)
    driver.api.destroy()
    api.destroy()
  })

  it('metode yang tidak ada di ApiClient maupun klien ditolak, bukan menggantung', async () => {
    const { api } = setup('console')
    await expect((api as unknown as { nope: { x(): Promise<void> } }).nope.x()).rejects.toMatchObject({ code: 'tidak_ditemukan' })
    api.destroy()
  })

  it('panggilan ke backend menunggu API siap', async () => {
    vi.useFakeTimers()
    const { api, backend } = setup('console', { healthy: false })
    backend.handle('/v1/rpc/wallet.get', () => json({ balanceRp: 1 }))
    const pending = api.wallet.get()
    await vi.advanceTimersByTimeAsync(10)
    expect(backend.rpc('wallet.get')).toHaveLength(0)

    backend.handle('/healthz', () => json({ ok: true }))
    await vi.advanceTimersByTimeAsync(50)
    expect(await pending).toEqual({ balanceRp: 1 })
    api.destroy()
  })
})

const ndjson = (...events: unknown[]) => chunked(events.map((e) => `${JSON.stringify(e)}\n`), 'application/x-ndjson')

async function drain<T>(it: AsyncIterable<T>): Promise<T[]> {
  const out: T[] = []
  for await (const e of it) out.push(e)
  return out
}

describe('kedalaman pencarian: plans.computeOptions(id, { effort })', () => {
  it('badan RPC adalah {id, effort}: tingkat dari argumen, bawaan fast bila kosong', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/plans.computeOptions', () => ndjson({ type: 'done', optionSet: { options: [] } }))
    await drain(api.plans.computeOptions('p1', { effort: 'thorough' }))
    await drain(api.plans.computeOptions('p1', { effort: 'reviewed' }))
    await drain(api.plans.computeOptions('p1', {}))
    await drain(api.plans.computeOptions('p1', { effort: undefined }))
    await drain(api.plans.computeOptions('p1'))
    expect(backend.rpc('plans.computeOptions').map((c) => c.body)).toEqual([
      { id: 'p1', effort: 'thorough' },
      { id: 'p1', effort: 'reviewed' },
      { id: 'p1', effort: 'fast' },
      { id: 'p1', effort: 'fast' },
      { id: 'p1', effort: 'fast' },
    ])
    api.destroy()
  })

  it('progres: msg diteruskan dan label tetap string; msg yang rusak dibuang', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/plans.computeOptions', () =>
      ndjson(
        { type: 'progress', step: 3, total: 4, label: 'Menyusun kombinasi' },
        { type: 'progress', step: 3, total: 4, msg: { key: 'srv.engine.search.experiments', params: { n: 8 } } },
        { type: 'progress', step: 3, total: 4, label: 'x', msg: { key: '' } },
        { type: 'progress', step: 3, total: 4, label: 'y', msg: 'bukan objek' },
        { type: 'progress', step: 3, total: 4, msg: { key: 'srv.engine.search.polish' } },
      ),
    )
    const got = await drain(api.plans.computeOptions('p1', { effort: 'thorough' }))
    expect(got).toEqual([
      { type: 'progress', step: 3, total: 4, label: 'Menyusun kombinasi' },
      { type: 'progress', step: 3, total: 4, label: '', msg: { key: 'srv.engine.search.experiments', params: { n: 8 } } },
      { type: 'progress', step: 3, total: 4, label: 'x' },
      { type: 'progress', step: 3, total: 4, label: 'y' },
      { type: 'progress', step: 3, total: 4, label: '', msg: { key: 'srv.engine.search.polish' } },
    ])
    api.destroy()
  })

  it('done: method dan report dirapikan; tingkat yang tidak dikenal membuang method', async () => {
    const { api, backend } = setup('console')
    const method = { effort: 'reviewed', trials: 12, rounds: 2, polished: true, reviewer: 'ai', proven: true, roadSource: 'mapbox' }
    backend.handle('/v1/rpc/plans.computeOptions', () => ndjson({ type: 'done', optionSet: { options: [], method, report: { text: 'Halo', locale: 'id', at: '2026-10-21T03:00:00Z' } } }))
    const [done] = await drain(api.plans.computeOptions('p1', { effort: 'reviewed' }))
    expect(done).toMatchObject({ type: 'done', optionSet: { method, report: { text: 'Halo', locale: 'id', at: '2026-10-21T03:00:00Z' } } })

    backend.handle('/v1/rpc/plans.computeOptions', () => ndjson({ type: 'done', optionSet: { options: [], method: { effort: 'turbo', trials: 3 } } }))
    const [odd] = await drain(api.plans.computeOptions('p1'))
    expect((odd as { optionSet: object }).optionSet).not.toHaveProperty('method')
    api.destroy()
  })

  it('galat dari backend sampai sebagai ApiError dengan pesan netral-bahasa (HTTP dan di tengah aliran)', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/plans.computeOptions', () => wireError(422, 'tidak_valid', 'srv.api.badEffort', { field: 'effort' }))
    await expect(drain(api.plans.computeOptions('p1', { effort: 'turbo' as never }))).rejects.toMatchObject({ code: 'tidak_valid', field: 'effort', msg: { key: 'srv.api.badEffort' } })

    backend.handle('/v1/rpc/plans.computeOptions', () =>
      ndjson({ type: 'progress', step: 1, total: 4, label: 'a' }, { type: 'error', error: { code: 'layanan', msg: { key: 'srv.api.routeEngineError' } } }),
    )
    const seen: unknown[] = []
    await expect(
      (async () => {
        for await (const e of api.plans.computeOptions('p1')) seen.push(e)
      })(),
    ).rejects.toMatchObject({ code: 'layanan', msg: { key: 'srv.api.routeEngineError' } })
    expect(seen).toHaveLength(1)
    api.destroy()
  })

  it('plans.get merapikan optionSet.method dan optionSet.report; waktu nol menjadi kosong', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/plans.get', () =>
      json({
        id: 'p1',
        optionSet: { options: [], method: { effort: 'thorough', trials: 9 }, report: { text: '  ', locale: 'en', at: '2026-10-21T03:00:00Z' } },
      }),
    )
    const plan = await api.plans.get('p1')
    expect(plan.optionSet?.method).toEqual({ effort: 'thorough', trials: 9 })
    expect(plan.optionSet).not.toHaveProperty('report') // teks kosong bukan laporan

    backend.handle('/v1/rpc/plans.get', () => json({ id: 'p1', optionSet: { options: [], report: { text: 'Ringkasan', locale: 'en', at: '0001-01-01T00:00:00Z' } } }))
    expect((await api.plans.get('p1')).optionSet?.report).toEqual({ text: 'Ringkasan', locale: 'en', at: '' })

    backend.handle('/v1/rpc/plans.get', () => json({ id: 'p1', optionSet: null }))
    expect(await api.plans.get('p1')).not.toHaveProperty('optionSet')
    api.destroy()
  })
})

describe('laporan AI: ai.explainOptions(planId)', () => {
  it('aliran RPC dengan badan {planId}: satu token lalu done; actions null menjadi daftar kosong', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/ai.explainOptions', () => ndjson({ type: 'token', text: 'Opsi terbaik ...' }, { type: 'done', actions: null, requestId: 'req-1' }))
    const got = await drain(api.ai.explainOptions('p1'))
    expect(got).toEqual([{ type: 'token', text: 'Opsi terbaik ...' }, { type: 'done', actions: [], requestId: 'req-1' }])
    expect(backend.rpc('ai.explainOptions')).toHaveLength(1)
    expect(backend.rpc('ai.explainOptions')[0]?.body).toEqual({ planId: 'p1' })
    expect(backend.rpc('ai.explainOptions')[0]?.headers).toMatchObject({ accept: 'application/x-ndjson' })
    api.destroy()
  })

  it('membawa Accept-Language bahasa layar saat itu', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/ai.explainOptions', () => ndjson({ type: 'done', actions: [], requestId: 'r' }))
    setLocale('id')
    await drain(api.ai.explainOptions('p1'))
    setLocale('en')
    await drain(api.ai.explainOptions('p1'))
    expect(backend.rpc('ai.explainOptions').map((c) => c.headers['accept-language'])).toEqual(['id', 'en'])
    api.destroy()
  })

  it('galat laporan (belum ada opsi, tak bisa diperiksa) sampai dengan kunci pesan aslinya', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/ai.explainOptions', () => wireError(422, 'tidak_valid', 'srv.ai.report.noOptions'))
    await expect(drain(api.ai.explainOptions('p1'))).rejects.toMatchObject({ code: 'tidak_valid', msg: { key: 'srv.ai.report.noOptions' } })
    backend.handle('/v1/rpc/ai.explainOptions', () => ndjson({ type: 'error', error: { code: 'layanan', msg: { key: 'srv.ai.report.unverified' } } }))
    await expect(drain(api.ai.explainOptions('p1'))).rejects.toMatchObject({ code: 'layanan', msg: { key: 'srv.ai.report.unverified' } })
    api.destroy()
  })

  it('khusus console: backoffice dan driver menolak jelas tanpa menghubungi backend', async () => {
    for (const app of ['backoffice', 'driver'] as const) {
      const { api, backend } = setup(app)
      await expect(drain(api.ai.explainOptions('p1'))).rejects.toMatchObject({ msg: { key: 'srv.api.notAvailableYet' } })
      expect(backend.rpc('ai.explainOptions')).toHaveLength(0)
      api.destroy()
    }
  })

  it('tidak punya cadangan lokal: backend mati tetap galat, bukan teks karangan klien', async () => {
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/ai.explainOptions', () => wireError(503, 'layanan', 'srv.api.aiError'))
    await expect(drain(api.ai.explainOptions('p1'))).rejects.toMatchObject({ code: 'layanan' })
    api.destroy()
  })
})

describe('events', () => {
  it('events.subscribe lewat transport; masuk membuka aliran, keluar menutupnya', async () => {
    vi.useFakeTimers()
    const { api, backend } = setup('console')
    backend.handle('/v1/rpc/auth.loginCustomer', () => json(customer))
    backend.handle('/v1/rpc/auth.logout', () => new Response(null, { status: 204 }))
    backend.handle('/v1/events', () => chunked(['data: {"type":"plans.changed","planId":"p9"}\n\n'], 'text/event-stream', new Promise(() => undefined)))

    const got: AppEvent[] = []
    api.events.subscribe((e: AppEvent) => got.push(e))
    await flush()
    expect(backend.calls.filter((c) => c.path === '/v1/events')).toHaveLength(0) // belum masuk: tidak ada yang didengar

    await api.auth.loginCustomer('a@b.id', 'pw')
    await flush()
    await flush()
    expect(got).toEqual([{ type: 'plans.changed', planId: 'p9' }])
    expect(backend.calls.find((c) => c.path === '/v1/events')?.headers.authorization).toBe('Bearer tok-customer')
    api.destroy()
  })
})

describe('aplikasi driver: posisi', () => {
  const geolocation = (lat: number, lng: number, speed: number | null = null) => ({
    getCurrentPosition: vi.fn((ok: PositionCallback) => ok({ coords: { latitude: lat, longitude: lng, speed } } as GeolocationPosition)),
  })

  function driverSetup(o: Opts = {}) {
    const s = setup('driver', { pingEveryMs: 1000, ...o })
    const me = vi.fn(() => json({ driver: { status: 'offline' } }))
    s.backend.handle('/v1/rpc/auth.loginDriver', () => json({ role: 'driver', userId: 'drv-1', name: 'Budi', token: 'tok-driver' }))
    s.backend.handle('/v1/rpc/driver.me', me)
    s.backend.handle('/v1/rpc/driver.setOnline', () => json({ status: 'online' }))
    s.backend.handle('/v1/rpc/driver.ping', () => json({}))
    return { ...s, me }
  }
  const pings = (b: FakeBackend) => b.rpc('driver.ping').map((c) => c.body)

  it('online mulai mengirim posisi GPS segera dan berkala; offline menghentikannya', async () => {
    vi.useFakeTimers()
    const geo = geolocation(-6.2, 106.8, 10) // 10 m/dtk = 36 km/jam
    const { api, backend } = driverSetup({ geolocation: geo })
    await flush()
    await api.auth.loginDriver('0812', '1234')
    await flush()
    expect(pings(backend)).toHaveLength(0) // masuk saja belum online

    await api.driver.setOnline(true)
    await flush()
    expect(pings(backend)).toEqual([{ position: { lat: -6.2, lng: 106.8 }, speedKmh: 36 }])
    expect(backend.rpc('driver.ping')[0]?.headers.authorization).toBe('Bearer tok-driver')

    await vi.advanceTimersByTimeAsync(2000)
    expect(pings(backend)).toHaveLength(3)

    await api.driver.setOnline(false)
    await vi.advanceTimersByTimeAsync(5000)
    expect(pings(backend)).toHaveLength(3)
    api.destroy()
  })

  it('tanpa GPS memakai posisi cadangan bila ada, dan diam bila tidak ada', async () => {
    vi.useFakeTimers()
    const denied = { getCurrentPosition: vi.fn((_ok: PositionCallback, err?: PositionErrorCallback | null) => err?.({} as GeolocationPositionError)) }
    const withFallback = driverSetup({ geolocation: denied, fallbackPosition: { lat: -6.1, lng: 106.9 } })
    await flush()
    await withFallback.api.auth.loginDriver('0812', '1234')
    await withFallback.api.driver.setOnline(true)
    await flush()
    expect(pings(withFallback.backend)).toEqual([{ position: { lat: -6.1, lng: 106.9 }, speedKmh: 0 }])
    withFallback.api.destroy()

    const without = driverSetup({ geolocation: denied })
    await flush()
    await without.api.auth.loginDriver('0812', '1234')
    await without.api.driver.setOnline(true)
    await vi.advanceTimersByTimeAsync(3000)
    expect(pings(without.backend)).toHaveLength(0)
    without.api.destroy()
  })

  it('keluar menghentikan pengiriman posisi', async () => {
    vi.useFakeTimers()
    const { api, backend } = driverSetup({ geolocation: geolocation(-6.2, 106.8) })
    backend.handle('/v1/rpc/auth.logout', () => new Response(null, { status: 204 }))
    await flush()
    await api.auth.loginDriver('0812', '1234')
    await api.driver.setOnline(true)
    await flush()
    const before = pings(backend).length
    await api.auth.logout()
    await vi.advanceTimersByTimeAsync(5000)
    expect(pings(backend)).toHaveLength(before)
    api.destroy()
  })

  it('driver yang masih online sebelum halaman dimuat ulang melanjutkan pengiriman posisi', async () => {
    vi.useFakeTimers()
    const storage = memoryStorage({ [KEY('driver')]: JSON.stringify({ token: 'tok-driver', session: { role: 'driver', userId: 'drv-1', name: 'Budi' } }) })
    const { api, backend, me } = driverSetup({ storage, geolocation: geolocation(-6.2, 106.8) })
    me.mockReturnValue(json({ driver: { status: 'bertugas' } }))
    await flush()
    await flush()
    expect(pings(backend).length).toBeGreaterThanOrEqual(1)
    api.destroy()
  })

  /* Peramban membekukan halaman yang tersembunyi dan layar yang mati menghentikan JavaScript: layar ditahan menyala dan
   * posisi dikirim lagi seketika saat halaman tampil, supaya backend tidak menganggap driver sudah pergi. */
  function fakeWakeLock(refuse = false) {
    const locks: { released: boolean; fireRelease(): void }[] = []
    const wl = {
      request: vi.fn(async () => {
        if (refuse) throw new DOMException('denied', 'NotAllowedError')
        let onRelease: (() => void) | undefined
        const lock = {
          released: false,
          release: vi.fn(async () => void (lock.released = true)),
          addEventListener: (_type: string, h: () => void) => void (onRelease = h),
        }
        locks.push({ get released() { return lock.released }, fireRelease: () => { lock.released = true; onRelease?.() } })
        return lock as unknown as WakeLockSentinel
      }),
    }
    return { wl: wl as unknown as Pick<WakeLock, 'request'>, request: wl.request, locks }
  }

  function fakeVisibility() {
    const handlers = new Set<() => void>()
    const doc = {
      visibilityState: 'visible' as DocumentVisibilityState,
      addEventListener: (_type: string, h: () => void) => void handlers.add(h),
      removeEventListener: (_type: string, h: () => void) => void handlers.delete(h),
    }
    return {
      doc: doc as never,
      listeners: () => handlers.size,
      show: () => { doc.visibilityState = 'visible'; handlers.forEach((h) => h()) },
      hide: () => { doc.visibilityState = 'hidden'; handlers.forEach((h) => h()) },
    }
  }

  it('online menahan layar menyala dan offline atau keluar melepasnya', async () => {
    vi.useFakeTimers()
    const { wl, request, locks } = fakeWakeLock()
    const vis = fakeVisibility()
    const { api } = driverSetup({ geolocation: geolocation(-6.2, 106.8), wakeLock: wl, visibility: vis.doc })
    await flush()
    await api.auth.loginDriver('0812', '1234')
    await flush()
    expect(request).not.toHaveBeenCalled() // masuk saja belum online

    await api.driver.setOnline(true)
    await flush()
    expect(request).toHaveBeenCalledWith('screen')
    expect(locks).toHaveLength(1)
    expect(locks[0]?.released).toBe(false)
    expect(vis.listeners()).toBe(1)

    await api.driver.setOnline(false)
    await flush()
    expect(locks[0]?.released).toBe(true)
    expect(vis.listeners()).toBe(0)

    await api.driver.setOnline(true) // online lagi: layar ditahan lagi, bukan kunci lama
    await flush()
    expect(locks).toHaveLength(2)
    api.destroy()
    await flush()
    expect(locks[1]?.released).toBe(true)
    expect(vis.listeners()).toBe(0)
  })

  it('halaman yang tampil kembali mengirim posisi seketika dan menahan layar lagi bila peramban melepasnya', async () => {
    vi.useFakeTimers()
    const { wl, locks } = fakeWakeLock()
    const vis = fakeVisibility()
    const { api, backend } = driverSetup({ geolocation: geolocation(-6.2, 106.8), wakeLock: wl, visibility: vis.doc, pingEveryMs: 60_000 })
    await flush()
    await api.auth.loginDriver('0812', '1234')
    await api.driver.setOnline(true)
    await flush()
    expect(pings(backend)).toHaveLength(1)

    // peramban melepas penahan saat halaman disembunyikan; menyembunyikan sendiri tidak mengirim apa pun
    locks[0]?.fireRelease()
    vis.hide()
    await flush()
    expect(pings(backend)).toHaveLength(1)

    vis.show()
    await flush()
    expect(pings(backend)).toHaveLength(2) // seketika, tanpa menunggu selang berikutnya
    expect(locks).toHaveLength(2)
    expect(locks[1]?.released).toBe(false)
    api.destroy()
  })

  it('izin penahan layar ditolak: posisi tetap dikirim', async () => {
    vi.useFakeTimers()
    const { wl, request } = fakeWakeLock(true)
    const { api, backend } = driverSetup({ geolocation: geolocation(-6.2, 106.8), wakeLock: wl, visibility: fakeVisibility().doc })
    await flush()
    await api.auth.loginDriver('0812', '1234')
    await api.driver.setOnline(true)
    await vi.advanceTimersByTimeAsync(2000)
    expect(request).toHaveBeenCalled()
    expect(pings(backend).length).toBeGreaterThanOrEqual(2)
    api.destroy()
  })

  it('layar tidak diminta saat halaman tersembunyi, dan berhenti online selagi menunggu izin melepasnya lagi', async () => {
    vi.useFakeTimers()
    const hidden = fakeVisibility()
    hidden.hide()
    const first = fakeWakeLock()
    const a = driverSetup({ geolocation: geolocation(-6.2, 106.8), wakeLock: first.wl, visibility: hidden.doc })
    await flush()
    await a.api.auth.loginDriver('0812', '1234')
    await a.api.driver.setOnline(true)
    await flush()
    expect(first.request).not.toHaveBeenCalled() // penahan hanya bisa diminta saat halaman tampil
    a.api.destroy()

    // izin yang baru turun setelah driver offline tidak boleh meninggalkan layar tertahan
    let grant!: (l: WakeLockSentinel) => void
    const released = vi.fn(async () => undefined)
    const slow = { request: vi.fn(() => new Promise<WakeLockSentinel>((resolve) => (grant = resolve))) } as unknown as Pick<WakeLock, 'request'>
    const b = driverSetup({ geolocation: geolocation(-6.2, 106.8), wakeLock: slow, visibility: fakeVisibility().doc })
    await flush()
    await b.api.auth.loginDriver('0812', '1234')
    await b.api.driver.setOnline(true)
    await b.api.driver.setOnline(false)
    grant({ release: released, addEventListener: () => undefined } as unknown as WakeLockSentinel)
    await flush()
    expect(released).toHaveBeenCalled()
    b.api.destroy()
  })

  it('aplikasi lain tidak pernah mengirim posisi', async () => {
    vi.useFakeTimers()
    const { api, backend } = setup('console', { geolocation: geolocation(-6.2, 106.8) })
    backend.handle('/v1/rpc/auth.loginCustomer', () => json(customer))
    await flush()
    await api.auth.loginCustomer('a@b.id', 'pw')
    await vi.advanceTimersByTimeAsync(20_000)
    expect(backend.rpc('driver.ping')).toHaveLength(0)
    api.destroy()
  })
})
