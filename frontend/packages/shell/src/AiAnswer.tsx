import { errorMessage } from '@loong/data'
import type { CopilotAction } from '@loong/domain'
import { t } from '@loong/i18n'
import { Button, Markdown, ToolTrace, toast } from '@loong/ui'
import { AlertCircle, Check, RotateCcw } from 'lucide-react'
import { useEffect, useState } from 'react'
import type { StreamState } from './aiStream'

/** "Masih berpikir…" bila belum ada token setelah 2 dtk (kontrak AI: jangan biarkan pengguna menebak apakah macet). */
function ThinkingNote() {
  const [slow, setSlow] = useState(false)
  useEffect(() => {
    const timer = setTimeout(() => setSlow(true), 2000)
    return () => clearTimeout(timer)
  }, [])
  return (
    <p className="flex items-center gap-2 text-small text-muted">
      <span className="flex gap-1" aria-hidden>
        {[0, 1, 2].map((i) => (
          <span key={i} className="size-1.5 rounded-full bg-subtle [animation:pulse-dot_1.2s_ease-in-out_infinite]" style={{ animationDelay: `${i * 160}ms` }} />
        ))}
      </span>
      {slow ? t('shell.ai.stillThinking') : t('shell.ai.thinking')}
    </p>
  )
}

/**
 * Satu jawaban AI: jejak alat berurutan (terbuka bila diklik), teks yang mengalir dengan caret, dan tindakan
 * yang diusulkan. Tindakan baru dijalankan saat pengguna menekannya; AI tidak pernah mengubah data sendiri.
 */
export function AiAnswer({
  state,
  onAction,
  applied,
  onRetry,
}: {
  state: StreamState
  onAction?: (action: CopilotAction) => Promise<void>
  applied?: string[]
  onRetry?: () => void
}) {
  const [busy, setBusy] = useState<string>()
  const streaming = state.phase === 'streaming'
  const noOutputYet = state.phase === 'thinking' && state.traces.length === 0 && !state.text
  // Pembaca layar: bukan setiap token (berisik, dan aria-busy menahannya), melainkan satu pengumuman per fase;
  // saat selesai, seluruh jawaban dibacakan sekali.
  const announcement =
    state.phase === 'thinking'
      ? t('shell.ai.preparing')
      : state.phase === 'streaming'
        ? t('shell.ai.answering')
        : state.phase === 'stopped'
          ? t('shell.ai.stopped')
          : state.phase === 'done'
            ? t('shell.ai.complete', { text: state.text.replace(/[*`|]/g, ' ').replace(/\s+/g, ' ').trim() })
            : ''

  return (
    <div className="space-y-2">
      <p role="status" className="sr-only">
        {announcement}
      </p>
      {state.traces.length > 0 && (
        <div role="group" className="space-y-px rounded-input border border-line bg-surface px-3 py-1.5" aria-label={t('shell.ai.steps')}>
          {state.traces.map((trace) => (
            <ToolTrace key={trace.id} trace={trace} />
          ))}
        </div>
      )}

      {(state.phase === 'thinking' && (noOutputYet || state.traces.every((trace) => trace.status === 'done'))) && <ThinkingNote />}

      {(state.text || streaming) && <Markdown text={state.text} streaming={streaming} />}

      {state.phase === 'stopped' && <p className="text-small text-muted">{t('shell.ai.stoppedNote')}</p>}

      {state.phase === 'error' && (
        <div role="alert" className="flex items-start gap-2.5 rounded-card bg-danger-tint p-3 text-small text-danger-fg">
          <AlertCircle className="mt-0.5 size-4 shrink-0" aria-hidden />
          <div className="min-w-0 flex-1">
            <p className="font-semibold">{t('shell.ai.errorTitle')}</p>
            <p className="mt-0.5">{state.error}</p>
            {onRetry && (
              <Button variant="link" className="mt-1 text-small" iconLeft={<RotateCcw className="size-3.5" aria-hidden />} onClick={onRetry}>
                {t('ui.error.retry')}
              </Button>
            )}
          </div>
        </div>
      )}

      {state.phase === 'done' && state.actions.length > 0 && onAction && (
        <div className="flex flex-wrap gap-2 pt-1">
          {state.actions.map((a) => {
            const done = applied?.includes(a.id)
            return (
              <Button
                key={a.id}
                size="sm"
                variant={a.primary && !done ? 'primary' : 'secondary'}
                loading={busy === a.id}
                aria-disabled={done || undefined}
                iconLeft={done ? <Check className="size-3.5" aria-hidden /> : undefined}
                onClick={async () => {
                  if (done) return
                  setBusy(a.id)
                  try {
                    await onAction(a)
                  } catch (err) {
                    // tindakan yang gagal tidak boleh senyap: tombol kembali aktif dan penyebabnya disebut
                    toast.error(errorMessage(err))
                  } finally {
                    setBusy(undefined)
                  }
                }}
              >
                {done ? t('shell.ai.applied') : a.label}
              </Button>
            )
          })}
        </div>
      )}
    </div>
  )
}
