import { useApi } from '@loong/data'
import type { CopilotAction } from '@loong/domain'
import { t } from '@loong/i18n'
import { Button, Card, cn } from '@loong/ui'
import { RefreshCw, Sparkles } from 'lucide-react'
import { useCallback, useEffect, useState } from 'react'
import { AiAnswer } from './AiAnswer'
import { initialStream, useStreamRunner, type StreamState } from './aiStream'

/**
 * Ringkasan naratif analitik untuk admin. Mulai otomatis sekali saat dibuka; "Perbarui" menjalankan ulang.
 * Hanya membaca data; tindakan yang diusulkan (mis. buka plan bermasalah) dijalankan oleh pengguna.
 */
export function Insights({ onAction, className }: { onAction?: (action: CopilotAction) => Promise<void>; className?: string }) {
  const api = useApi()
  const { run, stop } = useStreamRunner()
  const [state, setState] = useState<StreamState>(initialStream)

  const start = useCallback(() => {
    void run(
      () => api.ai.insights(),
      (fn) => setState(fn),
    )
  }, [api, run])

  // dijadwalkan (bukan dipanggil sinkron di efek) agar tidak memicu setState saat render; dihentikan saat ditutup
  useEffect(() => {
    const timer = setTimeout(start, 0)
    return () => {
      clearTimeout(timer)
      stop()
    }
  }, [start, stop])

  const busy = state.phase === 'thinking' || state.phase === 'streaming'

  return (
    <Card className={cn('p-card', className)}>
      <div className="flex items-start justify-between gap-3">
        <div className="flex items-start gap-2.5">
          <span className="mt-0.5 grid size-7 place-items-center rounded-input bg-accent-tint text-accent" aria-hidden>
            <Sparkles className="size-4" />
          </span>
          <div>
            <h2 className="text-title leading-tight">{t('shell.insights.title')}</h2>
            <p className="text-small text-muted">{t('shell.insights.description')}</p>
          </div>
        </div>
        <Button size="sm" variant="secondary" iconLeft={<RefreshCw className={cn('size-3.5', busy && 'animate-spin')} aria-hidden />} onClick={start} aria-disabled={busy || undefined}>
          {t('shell.insights.refresh')}
        </Button>
      </div>
      <div className="mt-4">
        <AiAnswer state={state} onAction={onAction} onRetry={start} />
      </div>
    </Card>
  )
}
