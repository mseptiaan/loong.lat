import { ApiError, type ApiErrorCode, type AppEvent } from '@loong/domain'
import { getLocale, msg, type Msg } from '@loong/i18n'

/** Galat dari backend: `{name, code, status, message, msg?, field?}` (backend/pkg/platform/apierror.go). */
interface WireError {
  code?: ApiErrorCode
  status?: number
  message?: string
  msg?: Msg
  field?: string
}

const CODES: readonly ApiErrorCode[] = ['tidak_valid', 'tidak_ditemukan', 'ditolak', 'bentrok', 'layanan', 'jaringan']

export function fromWire(e: WireError, status: number): ApiError {
  const code = e.code && CODES.includes(e.code) ? e.code : 'layanan'
  return new ApiError(code, e.msg ?? e.message ?? msg('srv.api.serviceError'), { status: e.status ?? status, field: e.field })
}

export interface TransportOptions {
  /** Alamat API milik aplikasi ini, tanpa garis miring di ujung. */
  baseUrl: string
  fetch: typeof fetch
  getToken: () => string | undefined
  /** Dipanggil saat backend menjawab 401 padahal ada token: sesi sudah tidak berlaku. */
  onUnauthorized: () => void
  /** Batas tunggu satu panggilan biasa (ms); aliran tidak dibatasi. */
  timeoutMs: number
}

export interface CallOptions {
  idempotencyKey?: string
  /** Tanpa token meskipun ada (login). */
  anonymous?: boolean
}

export interface Transport {
  call(method: string, body: Record<string, unknown>, options?: CallOptions): Promise<unknown>
  stream(method: string, body: Record<string, unknown>): AsyncIterable<unknown>
  /** Mendengarkan `/v1/events`; mengembalikan fungsi berhenti. Koneksi mengikuti sesi (lihat `restartEvents`). */
  subscribe(listener: (event: AppEvent) => void): () => void
  /** Membuka ulang aliran event, mis. setelah masuk atau keluar. */
  restartEvents(): void
  /** Apakah API menjawab. */
  healthy(): Promise<boolean>
  close(): void
}

export function createTransport(o: TransportOptions): Transport {
  const unreachable = () => new ApiError('jaringan', msg('srv.rpc.serverUnreachable', { origin: o.baseUrl }))

  // Accept-Language ikut bahasa layar (`en`/`id`) di SETIAP permintaan (panggilan, aliran, SSE): backend memakainya untuk teks
  // yang disusun server (jawaban AI, alasan batal bawaan). Dibaca per permintaan, jadi ganti bahasa langsung berlaku.
  const headers = (extra: Record<string, string> = {}, anonymous = false): Record<string, string> => {
    const token = anonymous ? undefined : o.getToken()
    return {
      'Content-Type': 'application/json',
      'Accept-Language': getLocale(),
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...extra,
    }
  }

  async function failure(res: Response): Promise<ApiError> {
    let wire: WireError = {}
    try {
      wire = (await res.json()) as WireError
    } catch {
      // badan bukan JSON (mis. galat dari proksi): jatuh ke pesan umum layanan
    }
    if (res.status === 401 && o.getToken()) o.onUnauthorized()
    return fromWire(wire, res.status)
  }

  async function call(method: string, body: Record<string, unknown>, options: CallOptions = {}): Promise<unknown> {
    const extra: Record<string, string> = options.idempotencyKey ? { 'Idempotency-Key': options.idempotencyKey } : {}
    let res: Response
    try {
      res = await o.fetch(`${o.baseUrl}/v1/rpc/${method}`, {
        method: 'POST',
        headers: headers(extra, options.anonymous),
        body: JSON.stringify(body),
        signal: AbortSignal.timeout(o.timeoutMs),
      })
    } catch (err) {
      if (err instanceof DOMException && err.name === 'TimeoutError') throw new ApiError('jaringan', msg('srv.rpc.timeout'))
      throw unreachable()
    }
    if (!res.ok) throw await failure(res)
    const text = await res.text()
    return text ? (JSON.parse(text) as unknown) : undefined
  }

  /** Aliran NDJSON: satu objek JSON per baris; baris `{"type":"error"}` mengakhiri aliran dengan galat. */
  function stream(method: string, body: Record<string, unknown>): AsyncIterable<unknown> {
    return {
      async *[Symbol.asyncIterator]() {
        const abort = new AbortController()
        let res: Response
        try {
          res = await o.fetch(`${o.baseUrl}/v1/rpc/${method}`, {
            method: 'POST',
            headers: { ...headers(), Accept: 'application/x-ndjson' },
            body: JSON.stringify(body),
            signal: abort.signal,
          })
        } catch {
          throw unreachable()
        }
        if (!res.ok) throw await failure(res)
        if (!res.body) return
        try {
          for await (const line of lines(res.body)) {
            const event = JSON.parse(line) as { type?: string; error?: WireError }
            if (event.type === 'error') throw fromWire(event.error ?? {}, 500)
            yield event
          }
        } finally {
          abort.abort() // pemanggil berhenti lebih awal: lepaskan koneksi
        }
      },
    }
  }

  /* ---------- peristiwa (SSE lewat fetch: EventSource tidak bisa mengirim header Authorization) ---------- */
  const listeners = new Set<(e: AppEvent) => void>()
  let loop: AbortController | undefined
  let connectedBefore = false

  function emit(event: AppEvent) {
    for (const l of listeners) l(event)
  }

  async function runEvents(signal: AbortSignal) {
    let wait = 1000
    while (!signal.aborted) {
      try {
        const res = await o.fetch(`${o.baseUrl}/v1/events`, { headers: headers({ Accept: 'text/event-stream' }), signal })
        if (res.status === 401) {
          o.onUnauthorized()
          return
        }
        if (res.ok && res.body) {
          // setelah sambungan ulang, event yang terlewat tak bisa dipulihkan: minta layar mengambil ulang semuanya
          if (connectedBefore) emit({ type: 'data.reset' })
          connectedBefore = true
          wait = 1000
          for await (const data of frames(res.body)) emit(JSON.parse(data) as AppEvent)
        }
      } catch {
        if (signal.aborted) return
      }
      await new Promise((resolve) => setTimeout(resolve, wait))
      wait = Math.min(wait * 2, 10_000)
    }
  }

  function startEvents() {
    loop?.abort()
    loop = undefined
    if (listeners.size === 0 || !o.getToken()) return
    loop = new AbortController()
    void runEvents(loop.signal)
  }

  return {
    call,
    stream,
    subscribe(listener) {
      listeners.add(listener)
      if (!loop) startEvents()
      return () => {
        listeners.delete(listener)
        if (listeners.size === 0) {
          loop?.abort()
          loop = undefined
        }
      }
    },
    restartEvents() {
      connectedBefore = false
      startEvents()
    },
    async healthy() {
      try {
        // longgar: lewat Cloudflare satu permintaan bisa 1-3 dtk (edge jauh, jabat tangan TLS baru); 5 dtk sempat terlewati
        return (await o.fetch(`${o.baseUrl}/healthz`, { signal: AbortSignal.timeout(15_000) })).ok
      } catch {
        return false
      }
    },
    close() {
      loop?.abort()
      loop = undefined
      listeners.clear()
    },
  }
}

/** Baris demi baris dari sebuah aliran byte. */
export async function* lines(body: ReadableStream<Uint8Array>): AsyncGenerator<string> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let rest = ''
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      rest += decoder.decode(value, { stream: true })
      let nl: number
      while ((nl = rest.indexOf('\n')) >= 0) {
        const line = rest.slice(0, nl).trim()
        rest = rest.slice(nl + 1)
        if (line) yield line
      }
    }
    if (rest.trim()) yield rest.trim()
  } finally {
    reader.releaseLock()
  }
}

/** Isi `data:` tiap bingkai Server-Sent Events; komentar (`: ping`) dan `retry:` diabaikan. */
export async function* frames(body: ReadableStream<Uint8Array>): AsyncGenerator<string> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let rest = ''
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      rest += decoder.decode(value, { stream: true }).replaceAll('\r\n', '\n')
      let end: number
      while ((end = rest.indexOf('\n\n')) >= 0) {
        const frame = rest.slice(0, end)
        rest = rest.slice(end + 2)
        const data = frame
          .split('\n')
          .filter((l) => l.startsWith('data:'))
          .map((l) => l.slice(5).trimStart())
          .join('\n')
        if (data) yield data
      }
    }
  } finally {
    reader.releaseLock()
  }
}
