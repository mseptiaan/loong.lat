import { t } from '@loong/i18n'
import { AlertTriangle, CheckCircle2, Info, Loader2, RotateCcw, WifiOff, XCircle } from 'lucide-react'
import type { ComponentProps, ReactNode } from 'react'
import { cn } from '../lib/cn'
import { Button } from './button'

/** Placeholder bergeometri sama dengan konten akhir; tak terlihat 200 ms pertama (lihat base.css). */
export function Skeleton({ className, ...props }: ComponentProps<'div'>) {
  return <div aria-hidden className={cn('skeleton rounded-input', className)} {...props} />
}

export function Spinner({ className, label }: { className?: string; label?: string }) {
  return (
    <span role="status" className="inline-flex items-center gap-2 text-muted">
      <Loader2 className={cn('size-4 animate-spin', className)} aria-hidden />
      {label ? <span className="text-small">{label}</span> : <span className="sr-only">{t('ui.spinner.loading')}</span>}
    </span>
  )
}

/** Keadaan kosong: jelaskan MENGAPA kosong dan tawarkan langkah berikutnya. */
export function EmptyState({
  icon,
  title,
  description,
  action,
  className,
  compact,
}: {
  icon: ReactNode
  title: ReactNode
  description?: ReactNode
  action?: ReactNode
  className?: string
  compact?: boolean
}) {
  return (
    <div className={cn('flex flex-col items-center text-center', compact ? 'gap-2 py-8' : 'gap-3 py-14', className)}>
      <div className="grid size-12 place-items-center rounded-card bg-sunken text-muted [&_svg]:size-6" aria-hidden>
        {icon}
      </div>
      <div>
        <p className="text-title">{title}</p>
        {description && <p className="mx-auto mt-1 max-w-sm text-body text-muted">{description}</p>}
      </div>
      {action && <div className="mt-1">{action}</div>}
    </div>
  )
}

/** Galat yang spesifik dan bisa dipulihkan: penyebab + coba lagi; tidak pernah menyalahkan pengguna. */
export function ErrorState({
  title = t('ui.error.title'),
  message,
  offline,
  onRetry,
  retrying,
  className,
  compact,
}: {
  title?: ReactNode
  message?: ReactNode
  offline?: boolean
  onRetry?: () => void
  retrying?: boolean
  className?: string
  compact?: boolean
}) {
  return (
    <div role="alert" className={cn('flex flex-col items-center text-center', compact ? 'gap-2 py-8' : 'gap-3 py-14', className)}>
      <div className="grid size-12 place-items-center rounded-card bg-danger-tint text-danger-fg [&_svg]:size-6" aria-hidden>
        {offline ? <WifiOff /> : <AlertTriangle />}
      </div>
      <div>
        <p className="text-title">{offline ? t('ui.error.offlineTitle') : title}</p>
        <p className="mx-auto mt-1 max-w-sm text-body text-muted">
          {message ?? (offline ? t('ui.error.offlineMessage') : t('ui.error.message'))}
        </p>
      </div>
      {onRetry && (
        <Button variant="secondary" onClick={onRetry} loading={retrying} iconLeft={<RotateCcw className="size-4" aria-hidden />}>
          {t('ui.error.retry')}
        </Button>
      )}
    </div>
  )
}

const noticeTone = {
  info: { box: 'bg-accent-tint text-fg', icon: 'text-accent', Icon: Info },
  success: { box: 'bg-success-tint text-fg', icon: 'text-success-fg', Icon: CheckCircle2 },
  warning: { box: 'bg-warning-tint text-fg', icon: 'text-warning-fg', Icon: AlertTriangle },
  danger: { box: 'bg-danger-tint text-fg', icon: 'text-danger-fg', Icon: XCircle },
} as const

/** Pemberitahuan inline (bukan toast): ikon + teks, aman untuk buta warna. */
export function Notice({
  tone = 'info',
  title,
  children,
  action,
  className,
}: {
  tone?: keyof typeof noticeTone
  title?: ReactNode
  children?: ReactNode
  action?: ReactNode
  className?: string
}) {
  const look = noticeTone[tone]
  return (
    <div role={tone === 'danger' ? 'alert' : 'status'} className={cn('flex items-start gap-3 rounded-card p-3.5', look.box, className)}>
      <look.Icon className={cn('mt-0.5 size-[18px] shrink-0', look.icon)} aria-hidden />
      <div className="min-w-0 flex-1 text-body">
        {title && <p className="font-semibold">{title}</p>}
        {children && <div className={cn(title && 'text-muted')}>{children}</div>}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  )
}

/** Tag netral kecil (bukan lencana warna): mis. tingkat keyakinan AI, kapasitas. */
export function Chip({ children, className, icon }: { children: ReactNode; className?: string; icon?: ReactNode }) {
  return (
    <span className={cn('inline-flex items-center gap-1 rounded-chip bg-sunken px-2 py-0.5 text-caption font-medium text-muted', className)}>
      {icon}
      {children}
    </span>
  )
}
