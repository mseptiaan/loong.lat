import {
  ApiError,
  STREAM_METHODS,
  appOrigin,
  type ApiClient,
  type AppEvent,
  type Session,
  type Unsubscribe,
} from '@loong/domain'
import { getLocale, msg, type Msg } from '@loong/i18n'
import { isMessage, PROTOCOL, type ClientMessage, type HostMessage, type WireError } from './protocol'

export type ApiStatus = 'menghubungkan' | 'siap' | 'gagal'

/** Jenis backend di balik klien: tiruan di browser (iframe `api.<domain>`) atau backend asli lewat HTTP. */
export type ApiKind = 'mock' | 'http'

export interface RemoteApi extends ApiClient {
  /** Layar yang hanya masuk akal untuk tiruan (Panel Demo: jam virtual, kegagalan tiruan) memeriksanya. */
  readonly kind: ApiKind
  /** Selesai saat host api.<domain> siap; menolak bila host tidak bisa dimuat. */
  ready(): Promise<void>
  status(): ApiStatus
  onStatus(cb: (status: ApiStatus, detail?: Msg) => void): Unsubscribe
  /** Sesi login (disimpan di origin aplikasi ini, seperti cookie). */
  getSession(): Session | null
  onSession(cb: (session: Session | null) => void): Unsubscribe
  destroy(): void
}

export interface RemoteApiOptions {
  /** Default: api.<domain induk> dari alamat halaman ini. */
  apiOrigin?: string
  sessionKey?: string
  /** Batas tunggu satu panggilan (ms). */
  timeoutMs?: number
  /** Batas tunggu host siap (ms). */
  connectTimeoutMs?: number
}

const DEFAULT_SESSION_KEY = 'loong:session'

const fromWire = (e: WireError) => new ApiError(e.code, e.msg ?? e.message, { status: e.status, field: e.field })

/**
 * ApiClient yang meneruskan semua panggilan ke backend tiruan di iframe tersembunyi `api.<domain>`.
 * Dari sisi layar ia tampak seperti ApiClient biasa; nanti diganti klien HTTP ke backend asli.
 */
export function createRemoteApi(options: RemoteApiOptions = {}): RemoteApi {
  const apiOrigin = options.apiOrigin ?? appOrigin('api')
  const sessionKey = options.sessionKey ?? DEFAULT_SESSION_KEY
  const timeoutMs = options.timeoutMs ?? 20_000
  const connectTimeoutMs = options.connectTimeoutMs ?? 12_000

  /* ---------- iframe ---------- */
  const iframe = document.createElement('iframe')
  iframe.src = `${apiOrigin}/`
  iframe.title = 'Mock backend'
  iframe.setAttribute('aria-hidden', 'true')
  iframe.tabIndex = -1
  // 1x1 px di dalam viewport (bukan display:none): iframe lintas-origin yang tersembunyi memicu throttling timer
  iframe.style.cssText =
    'position:fixed;right:0;bottom:0;width:1px;height:1px;border:0;opacity:0;pointer-events:none;'
  document.body.append(iframe)

  /* ---------- status ---------- */
  let status: ApiStatus = 'menghubungkan'
  let statusDetail: Msg | undefined
  const statusListeners = new Set<(s: ApiStatus, d?: Msg) => void>()
  const setStatus = (s: ApiStatus, d?: Msg) => {
    status = s
    statusDetail = d
    for (const l of statusListeners) l(s, d)
  }

  let resolveReady!: () => void
  let rejectReady!: (e: Error) => void
  const readyPromise = new Promise<void>((resolve, reject) => {
    resolveReady = resolve
    rejectReady = reject
  })
  // cegah "unhandled rejection" bila tak ada yang menunggu ready()
  readyPromise.catch(() => undefined)

  const send = (message: ClientMessage) => iframe.contentWindow?.postMessage(message, apiOrigin)

  let helloTimer: ReturnType<typeof setInterval> | undefined
  function stopHello() {
    clearInterval(helloTimer)
    clearTimeout(connectTimer)
  }

  iframe.addEventListener('load', () => {
    helloTimer = setInterval(() => send({ loong: PROTOCOL, kind: 'hello' }), 150)
    send({ loong: PROTOCOL, kind: 'hello' })
  })
  const connectTimer = setTimeout(() => {
    if (status === 'siap') return
    stopHello()
    const detail = msg('srv.rpc.unreachable', { origin: apiOrigin })
    setStatus('gagal', detail)
    rejectReady(new ApiError('jaringan', detail))
  }, connectTimeoutMs)

  /* ---------- sesi ---------- */
  const readSession = (): Session | null => {
    try {
      const raw = localStorage.getItem(sessionKey)
      return raw ? (JSON.parse(raw) as Session) : null
    } catch {
      return null
    }
  }
  let session = readSession()
  const sessionListeners = new Set<(s: Session | null) => void>()
  const setSession = (next: Session | null) => {
    session = next
    if (next) localStorage.setItem(sessionKey, JSON.stringify(next))
    else localStorage.removeItem(sessionKey)
    for (const l of sessionListeners) l(next)
  }
  // masuk/keluar di tab lain pada aplikasi yang sama
  const onStorage = (e: StorageEvent) => {
    if (e.key !== sessionKey) return
    session = readSession()
    for (const l of sessionListeners) l(session)
  }
  window.addEventListener('storage', onStorage)

  /* ---------- panggilan ---------- */
  let seq = 0
  const pending = new Map<string, { resolve: (v: unknown) => void; reject: (e: Error) => void; timer: ReturnType<typeof setTimeout> }>()
  const streams = new Map<string, { push: (m: HostMessage) => void }>()
  const eventListeners = new Set<(e: AppEvent) => void>()
  let subscribed = false

  const ensureSubscribed = () => {
    if (subscribed || status !== 'siap' || eventListeners.size === 0) return
    subscribed = true
    send({ loong: PROTOCOL, kind: 'subscribe' })
  }

  function onMessage(e: MessageEvent) {
    if (e.source !== iframe.contentWindow || e.origin !== apiOrigin) return
    if (!isMessage<HostMessage>(e.data)) return
    const msg = e.data
    switch (msg.kind) {
      case 'ready':
        if (status !== 'siap') {
          stopHello()
          setStatus('siap')
          resolveReady()
          ensureSubscribed()
        }
        break
      case 'result': {
        const p = pending.get(msg.id)
        if (!p) return
        pending.delete(msg.id)
        clearTimeout(p.timer)
        if (msg.ok) p.resolve(msg.value)
        else p.reject(fromWire(msg.error))
        break
      }
      case 'stream.chunk':
      case 'stream.end':
      case 'stream.error':
        streams.get(msg.id)?.push(msg)
        break
      case 'event':
        for (const l of eventListeners) l(msg.event)
        break
    }
  }
  window.addEventListener('message', onMessage)

  async function callRemote(path: string[], args: unknown[]): Promise<unknown> {
    await readyPromise
    const id = `c${++seq}`
    return new Promise<unknown>((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id)
        reject(new ApiError('jaringan', msg('srv.rpc.timeout')))
      }, timeoutMs)
      pending.set(id, { resolve, reject, timer })
      send({ loong: PROTOCOL, kind: 'call', id, path, args, ctx: session, locale: getLocale() })
    })
  }

  function streamRemote(path: string[], args: unknown[]): AsyncIterable<unknown> {
    return {
      [Symbol.asyncIterator]() {
        const id = `s${++seq}`
        const queue: HostMessage[] = []
        let wake: (() => void) | undefined
        let started = false
        let finished = false
        streams.set(id, {
          push: (m) => {
            queue.push(m)
            wake?.()
          },
        })
        const cleanup = () => {
          finished = true
          streams.delete(id)
        }
        return {
          async next(): Promise<IteratorResult<unknown>> {
            if (!started) {
              started = true
              await readyPromise
              send({ loong: PROTOCOL, kind: 'call', id, path, args, ctx: session, locale: getLocale() })
            }
            while (queue.length === 0) {
              if (finished) return { done: true, value: undefined }
              await new Promise<void>((resolve) => (wake = resolve))
            }
            const m = queue.shift()!
            if (m.kind === 'stream.chunk') return { done: false, value: m.value }
            cleanup()
            if (m.kind === 'stream.error') throw fromWire(m.error)
            return { done: true, value: undefined }
          },
          async return(): Promise<IteratorResult<unknown>> {
            if (!finished) {
              if (started) send({ loong: PROTOCOL, kind: 'stream.cancel', id })
              cleanup()
            }
            return { done: true, value: undefined }
          },
        }
      },
    }
  }

  /** Titik pencegatan sisi klien; selebihnya diteruskan ke host. */
  function invoke(path: string[], args: unknown[]): unknown {
    const name = path.join('.')
    if (name === 'auth.session') return Promise.resolve(session)
    if (name === 'events.subscribe') {
      const listener = args[0] as (e: AppEvent) => void
      eventListeners.add(listener)
      ensureSubscribed()
      return () => void eventListeners.delete(listener)
    }
    if (STREAM_METHODS.has(name)) return streamRemote(path, args)
    if (name === 'auth.loginCustomer' || name === 'auth.loginAdmin' || name === 'auth.loginDriver' || name === 'auth.register') {
      return callRemote(path, args).then((s) => {
        setSession(s as Session)
        return s
      })
    }
    if (name === 'auth.logout') {
      return callRemote(path, args).finally(() => setSession(null))
    }
    return callRemote(path, args)
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
    kind: 'mock' as const,
    ready: () => readyPromise,
    status: () => status,
    onStatus(cb: (s: ApiStatus, d?: Msg) => void) {
      statusListeners.add(cb)
      cb(status, statusDetail)
      return () => void statusListeners.delete(cb)
    },
    getSession: () => session,
    onSession(cb: (s: Session | null) => void) {
      sessionListeners.add(cb)
      return () => void sessionListeners.delete(cb)
    },
    destroy() {
      stopHello()
      window.removeEventListener('message', onMessage)
      window.removeEventListener('storage', onStorage)
      iframe.remove()
      for (const p of pending.values()) {
        clearTimeout(p.timer)
        p.reject(new ApiError('jaringan', msg('srv.rpc.closed')))
      }
      pending.clear()
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
