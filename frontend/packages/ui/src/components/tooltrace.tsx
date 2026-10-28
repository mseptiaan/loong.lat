import type { CopilotToolTrace } from '@loong/domain'
import { t } from '@loong/i18n'
import { Check, ChevronRight, Loader2, TriangleAlert } from 'lucide-react'
import { cn } from '../lib/cn'

const seconds = (ms: number) => t('ui.toolTrace.seconds', { sec: ms / 1000 })

/**
 * Satu panggilan alat AI yang terlihat: judul + sasaran, status, durasi setelah selesai, dan detail yang bisa dibuka
 * (dasar `<details>`, jadi bisa dioperasikan dengan keyboard). Tidak pernah disembunyikan di balik sakelar.
 */
export function ToolTrace({ trace, className }: { trace: CopilotToolTrace; className?: string }) {
  const body = trace.detail ? (
    <details className={cn('group rounded-input', className)}>
      <summary className="flex cursor-pointer list-none items-center gap-2 rounded-input py-1 text-small text-muted outline-offset-2 marker:hidden [&::-webkit-details-marker]:hidden">
        <Head trace={trace} />
        <ChevronRight className="ml-auto size-3.5 shrink-0 text-subtle transition-transform group-open:rotate-90" aria-hidden />
      </summary>
      <pre className="mb-1 ml-6 mt-1 overflow-x-auto whitespace-pre-wrap rounded-input bg-sunken px-3 py-2 font-mono text-[0.75rem] leading-relaxed text-muted">{trace.detail}</pre>
    </details>
  ) : (
    <div className={cn('flex items-center gap-2 py-1 text-small text-muted', className)}>
      <Head trace={trace} />
    </div>
  )
  return body
}

function Head({ trace }: { trace: CopilotToolTrace }) {
  return (
    <>
      <span className="grid size-4 shrink-0 place-items-center" aria-hidden>
        {trace.status === 'running' ? (
          <Loader2 className="size-3.5 animate-spin text-accent motion-reduce:animate-none" />
        ) : trace.status === 'failed' ? (
          <TriangleAlert className="size-3.5 text-warning-fg" />
        ) : (
          <Check className="size-3.5 text-success-fg" />
        )}
      </span>
      <span className="min-w-0 truncate">
        <span className="font-medium text-fg">{trace.title}</span>
        {trace.target && <span> · {trace.target}</span>}
        {trace.status === 'done' && trace.ms !== undefined && <span className="tabular-nums"> · {seconds(trace.ms)}</span>}
        {trace.status === 'running' && <span className="sr-only"> {t('ui.toolTrace.running')}</span>}
        {trace.status === 'failed' && <span> · {t('ui.toolTrace.failed')}</span>}
      </span>
    </>
  )
}
