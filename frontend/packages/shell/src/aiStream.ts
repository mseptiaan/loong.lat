import { errorMessage } from '@loong/data'
import type { CopilotAction, CopilotEvent, CopilotToolTrace } from '@loong/domain'
import { useCallback, useRef } from 'react'

/** Keadaan satu jawaban AI: berpikir (alat berjalan), mengalir (token datang), selesai, gagal, atau dihentikan pengguna. */
export interface StreamState {
  phase: 'thinking' | 'streaming' | 'done' | 'error' | 'stopped'
  traces: CopilotToolTrace[]
  text: string
  actions: CopilotAction[]
  requestId?: string
  error?: string
}

export const initialStream: StreamState = { phase: 'thinking', traces: [], text: '', actions: [] }

/** Peristiwa dengan id jejak yang sama menggantikan yang lama (berjalan -> selesai); lainnya ditambahkan berurutan. */
export function applyEvent(s: StreamState, e: CopilotEvent): StreamState {
  switch (e.type) {
    case 'tool': {
      const i = s.traces.findIndex((t) => t.id === e.trace.id)
      return { ...s, traces: i >= 0 ? s.traces.map((t, j) => (j === i ? e.trace : t)) : [...s.traces, e.trace] }
    }
    case 'token':
      return { ...s, phase: 'streaming', text: s.text + e.text }
    case 'done':
      return { ...s, phase: 'done', actions: e.actions, requestId: e.requestId }
  }
}

type Update = (fn: (s: StreamState) => StreamState) => void

/**
 * Menjalankan satu aliran AI yang bisa dihentikan. Menghentikan membuang peristiwa berikutnya dan meminta host
 * membatalkan aliran (`return()` -> `stream.cancel`), sehingga tombol "Berhenti" benar-benar berhenti.
 */
export function useStreamRunner() {
  const runId = useRef(0)
  const iterator = useRef<AsyncIterator<CopilotEvent> | null>(null)

  const run = useCallback(async (open: () => AsyncIterable<CopilotEvent>, update: Update) => {
    const mine = ++runId.current
    update(() => initialStream)
    try {
      const it = open()[Symbol.asyncIterator]()
      iterator.current = it
      for (;;) {
        const r = await it.next()
        if (runId.current !== mine) return
        if (r.done) break
        update((s) => applyEvent(s, r.value))
      }
      update((s) => (s.phase === 'done' ? s : { ...s, phase: 'done' }))
    } catch (err) {
      if (runId.current !== mine) return
      update((s) => ({ ...s, phase: 'error', error: errorMessage(err) }))
    }
  }, [])

  const stop = useCallback(() => {
    runId.current++
    void iterator.current?.return?.()
    iterator.current = null
  }, [])

  return { run, stop }
}
