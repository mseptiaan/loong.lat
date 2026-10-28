import { ApiError, STREAM_METHODS, rootHost, type AppEvent, type ApiClient } from '@loong/domain'
import { isLocale, msg, DEFAULT_LOCALE } from '@loong/i18n'
import { createApi, tickAutopilot } from '../server/api'
import type { RoadProvider } from '../roads/provider'
import { tickDispatch } from '../server/dispatch'
import { createRoadHydrator } from '../server/roads'
import { MockServer, localRawStorage, sleep } from '../server/server'
import { isMessage, PROTOCOL, type ClientMessage, type HostMessage, type WireError } from './protocol'

const EVENT_CHANNEL = 'loong:events'
const AUTOPILOT_LOCK = 'loong:autopilot'
const CLIENT_SUBDOMAINS = new Set(['console', 'backoffice', 'driver'])

/** Hanya aplikasi saudara (console/backoffice/driver di domain induk yang sama) yang boleh memanggil host. */
export function isAllowedOrigin(origin: string, self: Pick<Location, 'protocol' | 'hostname'> = location): boolean {
  try {
    const url = new URL(origin)
    const first = url.hostname.split('.')[0] ?? ''
    return url.protocol === self.protocol && CLIENT_SUBDOMAINS.has(first) && rootHost(url.hostname) === rootHost(self.hostname)
  } catch {
    return false
  }
}

function toWireError(err: unknown): WireError {
  if (err instanceof ApiError) {
    return { name: 'ApiError', code: err.code, status: err.status, message: err.message, msg: err.msg, field: err.field }
  }
  const detail = err instanceof Error ? err.message : String(err)
  console.error('[api-host]', err)
  return { name: 'Error', code: 'layanan', status: 500, message: `Mock backend error: ${detail}`, msg: msg('srv.rpc.internal', { detail }) }
}

/** Menelusuri `api.plans.list` dari ['plans','list'] dengan aman (hanya properti milik sendiri). */
function resolvePath(api: ApiClient, path: string[]): (...args: unknown[]) => unknown {
  let node: unknown = api
  for (const key of path) {
    if (typeof node !== 'object' || node === null || !Object.prototype.hasOwnProperty.call(node, key)) {
      throw new ApiError('tidak_ditemukan', msg('srv.rpc.unknownMethod', { method: path.join('.') }))
    }
    node = (node as Record<string, unknown>)[key]
  }
  if (typeof node !== 'function') throw new ApiError('tidak_ditemukan', msg('srv.rpc.notMethod', { method: path.join('.') }))
  return node as (...args: unknown[]) => unknown
}

export interface ApiHost {
  server: MockServer
  stop(): void
}

/**
 * Menjalankan backend tiruan di halaman api.<domain>: melayani RPC dari aplikasi lewat postMessage,
 * meneruskan peristiwa antar tab (BroadcastChannel), dan menjalankan autopilot (satu pemimpin lewat Web Locks).
 */
export function startApiHost(opts: { server?: MockServer; roads?: RoadProvider } = {}): ApiHost {
  const server = opts.server ?? new MockServer({ storage: localRawStorage(), roads: opts.roads })
  const clients = new Map<Window, string>()
  const streams = new Map<string, { cancel: () => void }>()

  /* --- peristiwa antar tab --- */
  const channel = typeof BroadcastChannel !== 'undefined' ? new BroadcastChannel(EVENT_CHANNEL) : undefined
  server.relay = (event) => channel?.postMessage(event)
  if (channel) channel.onmessage = (m: MessageEvent<AppEvent>) => server.deliver(m.data)

  const unsubscribeServer = server.subscribe((event) => {
    for (const [win, origin] of clients) {
      try {
        win.postMessage({ loong: PROTOCOL, kind: 'event', event } satisfies HostMessage, origin)
      } catch {
        clients.delete(win)
      }
    }
  })

  const post = (win: Window, origin: string, message: HostMessage) => win.postMessage(message, origin)

  async function handleCall(win: Window, origin: string, call: Extract<ClientMessage, { kind: 'call' }>) {
    const { id, path, args, ctx } = call
    const locale = isLocale(call.locale) ? call.locale : DEFAULT_LOCALE
    try {
      const fn = resolvePath(createApi(server, ctx, locale), path)
      const result = fn(...args)
      if (STREAM_METHODS.has(path.join('.'))) {
        const iterator = (result as AsyncIterable<unknown>)[Symbol.asyncIterator]()
        let cancelled = false
        streams.set(id, {
          cancel: () => {
            cancelled = true
            void iterator.return?.()
          },
        })
        try {
          while (!cancelled) {
            const next = await iterator.next()
            if (next.done) break
            post(win, origin, { loong: PROTOCOL, kind: 'stream.chunk', id, value: next.value })
          }
          if (!cancelled) post(win, origin, { loong: PROTOCOL, kind: 'stream.end', id })
        } catch (err) {
          post(win, origin, { loong: PROTOCOL, kind: 'stream.error', id, error: toWireError(err) })
        } finally {
          streams.delete(id)
        }
        return
      }
      post(win, origin, { loong: PROTOCOL, kind: 'result', id, ok: true, value: await result })
    } catch (err) {
      post(win, origin, { loong: PROTOCOL, kind: 'result', id, ok: false, error: toWireError(err) })
    }
  }

  function onMessage(e: MessageEvent) {
    if (!isMessage<ClientMessage>(e.data)) return
    if (!isAllowedOrigin(e.origin)) {
      console.warn('[api-host] pesan dari origin tidak diizinkan diabaikan:', e.origin) // i18n-ignore: log pengembang
      return
    }
    const win = e.source as Window | null
    if (!win) return
    const incoming = e.data
    switch (incoming.kind) {
      case 'hello':
        post(win, e.origin, { loong: PROTOCOL, kind: 'ready' })
        break
      case 'subscribe':
        clients.set(win, e.origin)
        break
      case 'stream.cancel':
        streams.get(incoming.id)?.cancel()
        break
      case 'call':
        void handleCall(win, e.origin, incoming)
        break
    }
  }
  window.addEventListener('message', onMessage)

  /* --- autopilot, penugasan driver otomatis, dan pengisian jalur jalan: satu instance (pemegang lock) menjalankan tick tiap detik --- */
  let stopped = false
  const abort = new AbortController()
  // jalur jalan data yang sudah ada (seed, rute yang titik awalnya berpindah) diisi di latar belakang oleh pemimpin
  const roads = server.options.roads
  const hydrator = roads ? createRoadHydrator(server, roads) : undefined
  if ('locks' in navigator) {
    void navigator.locks
      .request(AUTOPILOT_LOCK, { signal: abort.signal }, async () => {
        while (!stopped) {
          try {
            tickAutopilot(server)
            tickDispatch(server)
            void hydrator?.run().catch((err: unknown) => console.error('[api-host] jalur jalan', err))
          } catch (err) {
            console.error('[api-host] autopilot', err)
          }
          await sleep(1000)
        }
      })
      .catch(() => undefined)
  }

  return {
    server,
    stop() {
      stopped = true
      abort.abort()
      window.removeEventListener('message', onMessage)
      unsubscribeServer()
      channel?.close()
    },
  }
}
