import { ApiError, STREAM_METHODS, type AppEvent, type AppId, type Session, type Unsubscribe } from '@loong/domain'
import { msg, type Msg } from '@loong/i18n'
import type { ApiStatus, RemoteApi } from '@loong/mock-api'
import { createAiRoutes } from './ai'
import { createLocal, unavailable } from './local'
import { browserImageScaler, type ImageScaler } from './media'
import { normalizeResult, normalizeStream } from './normalize'
import { createTransport } from './transport'
import { BACKEND_METHODS, IDEMPOTENT, SIGN_IN, toBody } from './wire'

type ApiApp = Extract<AppId, 'console' | 'backoffice' | 'driver'>

export interface HttpApiOptions {
  app: ApiApp
  /** Alamat API milik aplikasi ini, mis. "http://localhost:8083". */
  baseUrl: string
  fetch?: typeof fetch
  /** Tempat sesi disimpan (bawaan: localStorage; di tes: penyimpanan di memori). */
  storage?: Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>
  /** Batas tunggu satu panggilan (ms). */
  timeoutMs?: number
  /** Selang antar upaya menyambung ulang saat API belum menjawab (ms). */
  retryMs?: number
  /** Aplikasi driver: tiap berapa ms posisi dikirim selama online. */
  pingEveryMs?: number
  /** Aplikasi driver: posisi tetap bila GPS tidak ada atau ditolak (pengembangan dan demo). */
  fallbackPosition?: { lat: number; lng: number }
  /** Pembaca GPS; bawaan `navigator.geolocation`. */
  geolocation?: Pick<Geolocation, 'getCurrentPosition'>
  /** Penahan layar agar tetap menyala selama driver online; bawaan `navigator.wakeLock`. */
  wakeLock?: Pick<WakeLock, 'request'>
  /** Sumber kejadian tampil/sembunyi halaman; bawaan `document`. */
  visibility?: Pick<Document, 'addEventListener' | 'removeEventListener'> & { readonly visibilityState: DocumentVisibilityState }
  /** Pengecil foto sebelum dikirim ke `ai.readImage` (bawaan: canvas peramban; tanpa canvas foto dikirim apa adanya bila muat). */
  imageScaler?: ImageScaler
  /** Batas badan permintaan yang membawa foto atau suara (byte; bawaan ~3 MB). */
  maxBodyBytes?: number
}

interface Stored {
  token: string
  session: Session
}

const memoryStorage = (): Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> => {
  const m = new Map<string, string>()
  return { getItem: (k) => m.get(k) ?? null, setItem: (k, v) => void m.set(k, v), removeItem: (k) => void m.delete(k) }
}

/**
 * ApiClient yang bicara HTTP ke backend asli. Sisi layar sama persis dengan `createRemoteApi()` (tiruan), jadi
 * `DataProvider` dan semua layar tidak tahu bedanya. Tiap aplikasi menyambung ke SATU API (milik aplikasinya):
 * metode yang dilayani API itu dikirim ke sana; sisanya dijawab klien sendiri (local.ts) atau ditolak jelas.
 * Setiap permintaan membawa `Accept-Language` (bahasa layar). `ai.readImage/parseSpeech/parseIntent` mencoba backend dulu
 * dan jatuh ke mesin lokal hanya bila layanannya mati (ai.ts).
 */
export function createHttpApi(options: HttpApiOptions): RemoteApi {
  const { app } = options
  const storage = options.storage ?? (typeof localStorage === 'undefined' ? memoryStorage() : localStorage)
  const storageKey = `loong:http:${app}`
  const retryMs = options.retryMs ?? 3000

  /* ---------- sesi ---------- */
  const read = (): Stored | null => {
    try {
      const raw = storage.getItem(storageKey)
      return raw ? (JSON.parse(raw) as Stored) : null
    } catch {
      return null
    }
  }
  let stored = read()
  const sessionListeners = new Set<(s: Session | null) => void>()
  const setStored = (next: Stored | null) => {
    stored = next
    if (next) storage.setItem(storageKey, JSON.stringify(next))
    else storage.removeItem(storageKey)
    transport.restartEvents()
    syncPings()
    for (const l of sessionListeners) l(next?.session ?? null)
  }

  const transport = createTransport({
    baseUrl: options.baseUrl.replace(/\/+$/, ''),
    fetch: options.fetch ?? ((...args) => globalThis.fetch(...args)),
    getToken: () => stored?.token,
    onUnauthorized: () => setStored(null),
    timeoutMs: options.timeoutMs ?? 20_000,
  })

  /* ---------- status ---------- */
  let status: ApiStatus = 'menghubungkan'
  let statusDetail: Msg | undefined
  const statusListeners = new Set<(s: ApiStatus, d?: Msg) => void>()
  const setStatus = (s: ApiStatus, d?: Msg) => {
    if (s === status && d === statusDetail) return
    status = s
    statusDetail = d
    for (const l of statusListeners) l(s, d)
  }
  let alive = true
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let resolveReady!: () => void
  const readyPromise = new Promise<void>((resolve) => (resolveReady = resolve))

  async function connect() {
    if (await transport.healthy()) {
      setStatus('siap')
      resolveReady()
      return
    }
    setStatus('gagal', msg('srv.rpc.serverUnreachable', { origin: options.baseUrl }))
    if (alive) retryTimer = setTimeout(() => void connect(), retryMs)
  }
  void connect()

  /* ---------- posisi driver ---------- */
  // Backend memilih driver terdekat dari posisi terakhirnya, jadi aplikasi driver harus mengirimnya selama online.
  let pingTimer: ReturnType<typeof setInterval> | undefined
  let online = false

  async function ping() {
    const geo = options.geolocation ?? (typeof navigator === 'undefined' ? undefined : navigator.geolocation)
    const send = (lat: number, lng: number, speedKmh = 0) =>
      transport.call('driver.ping', toBody('driver.ping', [{ lat, lng }, speedKmh])).catch(() => undefined)
    if (!geo) return options.fallbackPosition && void send(options.fallbackPosition.lat, options.fallbackPosition.lng)
    geo.getCurrentPosition(
      (p) => void send(p.coords.latitude, p.coords.longitude, Math.max(0, (p.coords.speed ?? 0) * 3.6)),
      () => options.fallbackPosition && void send(options.fallbackPosition.lat, options.fallbackPosition.lng),
      { enableHighAccuracy: true, maximumAge: 5000, timeout: 8000 },
    )
  }

  // Peramban membekukan halaman yang tersembunyi dan layar ponsel yang mati menghentikan JavaScript, padahal backend
  // menganggap driver yang diam lebih dari 2 menit sudah pergi dan mencabut tugas yang akan berangkat. Karena itu layar
  // ditahan menyala selama online, dan posisi dikirim lagi seketika saat halaman tampil kembali.
  let screenLock: WakeLockSentinel | undefined
  const doc = options.visibility ?? (typeof document === 'undefined' ? undefined : document)
  const visible = () => doc?.visibilityState !== 'hidden'

  async function holdScreen() {
    const wl = options.wakeLock ?? (typeof navigator === 'undefined' ? undefined : navigator.wakeLock)
    if (!wl || screenLock || !visible()) return // penahan hanya bisa diminta saat halaman tampil
    try {
      const lock = await wl.request('screen')
      if (!pingTimer) return void lock.release().catch(() => undefined) // berhenti online selagi menunggu izin
      screenLock = lock
      lock.addEventListener('release', () => {
        if (screenLock === lock) screenLock = undefined // peramban melepasnya saat halaman disembunyikan
      })
    } catch {
      // ditolak (mis. baterai hemat): layar boleh mati, ping tetap jalan selama halaman hidup
    }
  }

  function onVisible() {
    if (!pingTimer || !visible()) return
    void ping()
    void holdScreen()
  }

  function syncPings() {
    const want = app === 'driver' && online && !!stored
    if (want && !pingTimer) {
      void ping()
      pingTimer = setInterval(() => void ping(), options.pingEveryMs ?? 8000)
      doc?.addEventListener('visibilitychange', onVisible)
      void holdScreen()
    } else if (!want && pingTimer) {
      clearInterval(pingTimer)
      pingTimer = undefined
      doc?.removeEventListener('visibilitychange', onVisible)
      void screenLock?.release().catch(() => undefined)
      screenLock = undefined
    }
  }

  /** Sesi yang dipulihkan dari penyimpanan: driver yang masih online sebelum halaman dimuat ulang mengirim posisi lagi. */
  async function resumePings() {
    try {
      const me = (await transport.call('driver.me', {})) as { driver?: { status?: string } }
      online = me.driver?.status === 'online' || me.driver?.status === 'bertugas'
      syncPings()
    } catch {
      // belum bisa; dicoba lagi saat panggilan berikutnya berhasil
    }
  }
  if (app === 'driver' && stored) void readyPromise.then(() => resumePings())

  /* ---------- panggilan ---------- */
  const backend = BACKEND_METHODS[app]
  const local = createLocal()

  async function backendCall(name: string, args: unknown[]): Promise<unknown> {
    const key = IDEMPOTENT.has(name) ? { idempotencyKey: crypto.randomUUID() } : {}
    const result = await transport.call(name, toBody(name, args), { ...key, anonymous: SIGN_IN.has(name) })

    if (SIGN_IN.has(name)) {
      const { token, ...session } = result as Session & { token: string }
      setStored({ token, session })
      if (app === 'driver') void resumePings()
      return session
    }
    switch (name) {
      case 'auth.logout':
        online = false
        setStored(null)
        return undefined
      case 'driver.setOnline': {
        online = (args[0] as boolean) === true
        syncPings()
        return result
      }
    }
    return normalizeResult(name, result)
  }

  const ai = createAiRoutes({
    call: (name, args) => readyPromise.then(() => backendCall(name, args)),
    stream: (name, body) => transport.stream(name, body),
    local,
    scale: options.imageScaler ?? browserImageScaler(),
    maxBodyBytes: options.maxBodyBytes,
  })

  function invoke(path: string[], args: unknown[]): unknown {
    const name = path.join('.')

    if (name === 'auth.session') {
      // sesi dianggap berlaku sampai backend menolaknya (401 menghapusnya); tanpa token tidak perlu bertanya
      if (!stored) return Promise.resolve(null)
      return readyPromise.then(() => stored?.session ?? null)
    }
    if (name === 'events.subscribe') {
      const unsubscribe = transport.subscribe(args[0] as (e: AppEvent) => void)
      return unsubscribe satisfies Unsubscribe
    }

    const aiRoute = backend.has(name) ? ai[name] : undefined
    if (aiRoute) return (aiRoute as (...a: unknown[]) => unknown)(...args)

    if (STREAM_METHODS.has(name)) {
      if (backend.has(name)) return normalizeStream(name, transport.stream(name, toBody(name, args)))
      const fn = local[name]
      if (fn) return (fn as (...a: unknown[]) => unknown)(...args)
      return failingStream()
    }
    if (backend.has(name)) return readyPromise.then(() => backendCall(name, args))

    const fn = local[name]
    if (fn) return Promise.resolve().then(() => (fn as (...a: unknown[]) => unknown)(...args))
    return Promise.reject(unavailable())
  }

  const proxy = (path: string[]): unknown =>
    new Proxy(function () {}, {
      get(_t, prop) {
        // `then` harus undefined agar proxy tidak dianggap thenable saat di-await
        if (typeof prop === 'symbol' || prop === 'then') return undefined
        return proxy([...path, prop])
      },
      apply: (_t, _this, args) => invoke(path, args),
    })

  const base = {
    kind: 'http' as const,
    ready: () => readyPromise,
    status: () => status,
    onStatus(cb: (s: ApiStatus, d?: Msg) => void) {
      statusListeners.add(cb)
      cb(status, statusDetail)
      return () => void statusListeners.delete(cb)
    },
    getSession: () => stored?.session ?? null,
    onSession(cb: (s: Session | null) => void) {
      sessionListeners.add(cb)
      return () => void sessionListeners.delete(cb)
    },
    destroy() {
      alive = false
      clearTimeout(retryTimer)
      clearInterval(pingTimer)
      pingTimer = undefined
      doc?.removeEventListener('visibilitychange', onVisible)
      void screenLock?.release().catch(() => undefined)
      transport.close()
    },
  }

  return new Proxy(base as unknown as RemoteApi, {
    get(target, prop, receiver) {
      if (typeof prop === 'symbol' || prop === 'then') return undefined
      if (prop in target) return Reflect.get(target, prop, receiver)
      return proxy([prop])
    },
  })
}

/** Aliran yang langsung gagal: metode streaming yang belum dilayani siapa pun. */
function failingStream(): AsyncIterable<never> {
  return {
    [Symbol.asyncIterator]() {
      return { next: () => Promise.reject(new ApiError('tidak_ditemukan', msg('srv.api.notAvailableYet'))) }
    },
  }
}
