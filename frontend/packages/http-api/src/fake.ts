import type { Session } from '@loong/domain'

/** Backend palsu untuk tes: `fetch` yang merekam permintaan dan menjawab lewat penangan per jalur. */
export interface Call {
  path: string
  method: string
  headers: Record<string, string>
  body: unknown
}

export type Handler = (call: Call) => Response | Promise<Response>

export interface FakeBackend {
  fetch: typeof fetch
  calls: Call[]
  /** Panggilan RPC ke sebuah metode, urut. */
  rpc(method: string): Call[]
  handle(path: string, handler: Handler): void
}

export const json = (value: unknown, status = 200): Response =>
  new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } })

/** Galat dalam bentuk kawat backend. */
export const wireError = (status: number, code: string, key: string, extra: Record<string, unknown> = {}): Response =>
  json({ name: 'ApiError', code, status, message: key, msg: { key }, ...extra }, status)

/** Aliran teks yang tiap potongannya dikirim terpisah, supaya batas bingkai tidak sejajar dengan batas potongan. */
export function chunked(chunks: string[], contentType: string, hold?: Promise<void>): Response {
  const enc = new TextEncoder()
  return new Response(
    new ReadableStream({
      async start(controller) {
        for (const c of chunks) controller.enqueue(enc.encode(c))
        if (hold) await hold
        controller.close()
      },
    }),
    { status: 200, headers: { 'Content-Type': contentType } },
  )
}

export function fakeBackend(): FakeBackend {
  const calls: Call[] = []
  const handlers = new Map<string, Handler>()
  const fetchFn = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = new URL(typeof input === 'string' ? input : input instanceof URL ? input.href : input.url)
    const headers: Record<string, string> = {}
    new Headers(init?.headers).forEach((v, k) => (headers[k.toLowerCase()] = v))
    const body = init?.body ? JSON.parse(String(init.body)) : undefined
    const call: Call = { path: url.pathname, method: init?.method ?? 'GET', headers, body }
    calls.push(call)
    const handler = handlers.get(url.pathname)
    if (!handler) return new Response('not found', { status: 404 })
    return handler(call)
  }) as typeof fetch
  return {
    fetch: fetchFn,
    calls,
    rpc: (method) => calls.filter((c) => c.path === `/v1/rpc/${method}`),
    handle: (path, handler) => void handlers.set(path, handler),
  }
}

export const customer: Session & { token: string } = { role: 'customer', userId: 'cus-1', name: 'Dina', token: 'tok-customer' }
