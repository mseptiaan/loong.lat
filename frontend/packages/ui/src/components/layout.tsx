import { ArrowLeft } from 'lucide-react'
import type { ComponentProps, ReactNode } from 'react'
import { BRAND, LOCKUP, MARK_RADIUS, MARK_SIZE, MARK_STROKES, WORDMARK, WORDMARK_STROKE } from '../brand'
import { cn } from '../lib/cn'

/** Kartu: permukaan putih hangat, hairline + bayangan lembut, radius 14px. */
export function Card({ className, ...props }: ComponentProps<'div'>) {
  return <div className={cn('rounded-card border border-line bg-surface shadow-card', className)} {...props} />
}

export function CardHeader({
  title,
  description,
  action,
  className,
}: {
  title: ReactNode
  description?: ReactNode
  action?: ReactNode
  className?: string
}) {
  return (
    <div className={cn('flex items-start justify-between gap-4', className)}>
      <div className="min-w-0">
        <h2 className="text-title">{title}</h2>
        {description && <p className="mt-0.5 text-small text-muted">{description}</p>}
      </div>
      {action && <div className="shrink-0">{action}</div>}
    </div>
  )
}

/** Judul halaman + konteks + aksi utama sejajar. Aksi utama hidup di sini, bukan di toolbar filter. */
export function PageHeader({
  title,
  description,
  actions,
  back,
  className,
}: {
  title: ReactNode
  description?: ReactNode
  actions?: ReactNode
  back?: { label: string; onClick?: () => void; href?: string }
  className?: string
}) {
  return (
    <header className={cn('flex flex-wrap items-end justify-between gap-x-6 gap-y-3', className)}>
      <div className="min-w-0">
        {back &&
          (back.href ? (
            <a href={back.href} onClick={back.onClick} className="mb-1 inline-flex items-center gap-1.5 rounded-chip text-small text-muted hover:text-fg">
              <ArrowLeft className="size-3.5" aria-hidden />
              {back.label}
            </a>
          ) : (
            <button type="button" onClick={back.onClick} className="mb-1 inline-flex items-center gap-1.5 rounded-chip text-small text-muted hover:text-fg">
              <ArrowLeft className="size-3.5" aria-hidden />
              {back.label}
            </button>
          ))}
        <h1 className="text-heading">{title}</h1>
        {description && <p className="mt-1 max-w-2xl text-body text-muted">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap items-center gap-2">{actions}</div>}
    </header>
  )
}

/** Sparkline inline (SVG): garis aksen + isi memudar 15% -> 0. Tanpa library. */
export function Sparkline({ values, className, height = 32 }: { values: number[]; className?: string; height?: number }) {
  if (values.length < 2) return <div className={cn('h-8', className)} aria-hidden />
  const max = Math.max(...values)
  const min = Math.min(...values)
  const span = max - min || 1
  const w = 100
  const pts = values.map((v, i) => [(i / (values.length - 1)) * w, height - 3 - ((v - min) / span) * (height - 6)] as const)
  const line = pts.map(([x, y]) => `${x.toFixed(2)},${y.toFixed(2)}`).join(' ')
  const area = `0,${height} ${line} ${w},${height}`
  const gid = `spark-${values.join('').length}-${Math.round(values[0]! * 10)}`
  return (
    <svg viewBox={`0 0 ${w} ${height}`} preserveAspectRatio="none" className={cn('w-full', className)} style={{ height }} aria-hidden>
      <defs>
        <linearGradient id={gid} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0" stopColor="var(--accent)" stopOpacity="0.15" />
          <stop offset="1" stopColor="var(--accent)" stopOpacity="0" />
        </linearGradient>
      </defs>
      <polygon points={area} fill={`url(#${gid})`} />
      <polyline points={line} fill="none" stroke="var(--accent)" strokeWidth="1.5" vectorEffect="non-scaling-stroke" strokeLinejoin="round" strokeLinecap="round" />
    </svg>
  )
}

/**
 * Metrik. `primary` = satu-satunya yang ber-tint aksen dan lebih besar (hero); `secondary` netral.
 * Perbandingan ditulis sebagai teks sekunder biasa, tanpa panah/warna (lihat dashboard.md).
 */
export function Stat({
  label,
  value,
  hint,
  emphasis = 'secondary',
  spark,
  className,
}: {
  label: ReactNode
  value: ReactNode
  hint?: ReactNode
  emphasis?: 'primary' | 'secondary'
  spark?: number[]
  className?: string
}) {
  const primary = emphasis === 'primary'
  return (
    <div className={cn('rounded-card border p-card', primary ? 'border-accent/25 bg-accent-tint' : 'border-line bg-surface', className)}>
      <p className="text-small font-medium text-muted">{label}</p>
      <p className={cn('mt-1 tabular-nums', primary ? 'text-metric text-accent' : 'text-metric-sm text-fg')}>{value}</p>
      {hint && <p className="mt-1 text-small text-muted">{hint}</p>}
      {spark && <Sparkline values={spark} className="mt-3" />}
    </div>
  )
}

/** Avatar inisial netral (bukan warna-warni, agar tidak bersaing dengan aksen). */
export function Avatar({ name, size = 'md', className }: { name: string; size?: 'sm' | 'md' | 'lg'; className?: string }) {
  const initials = name
    .split(/\s+/)
    .filter(Boolean)
    .slice(0, 2)
    .map((p) => p[0]!.toUpperCase())
    .join('')
  return (
    <span
      aria-hidden
      className={cn(
        'inline-grid shrink-0 place-items-center rounded-full border border-line bg-sunken font-semibold text-muted',
        size === 'sm' && 'size-6 text-[0.625rem]',
        size === 'md' && 'size-8 text-caption',
        size === 'lg' && 'size-12 text-body',
        className,
      )}
    >
      {initials}
    </span>
  )
}

/** Tanda keyboard, mis. ⌘K, di menu/palet perintah (mono, redup). */
export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <kbd className={cn('rounded-[5px] border border-line bg-sunken px-1.5 py-px font-mono text-[0.6875rem] font-medium text-muted', className)}>
      {children}
    </kbd>
  )
}

/**
 * Logo loong.lat: tanda dua cincin saling mengait di atas kotak aksen, plus wordmark. Geometri dari `brand.ts`
 * (sumber yang sama dengan berkas SVG di `brand/`). Warna mengikuti token: kotak dan ".lat" = aksen, "loong" = warna teks.
 * Tinggi bawaan h-8; ubah lewat `className`.
 */
export function Logo({ wordmark = true, className }: { wordmark?: boolean; className?: string }) {
  const mark = (
    <>
      <rect width={MARK_SIZE} height={MARK_SIZE} rx={MARK_RADIUS} fill="var(--accent)" />
      <g fill="none">
        {MARK_STROKES.map((s, i) => (
          <path key={i} d={s.d} stroke={s.tone === 'fg' ? 'var(--on-accent)' : 'var(--accent)'} strokeWidth={s.width} />
        ))}
      </g>
    </>
  )
  if (!wordmark) {
    return (
      <svg viewBox={`0 0 ${MARK_SIZE} ${MARK_SIZE}`} className={cn('h-8 w-auto shrink-0', className)} aria-hidden>
        {mark}
      </svg>
    )
  }
  return (
    <svg viewBox={`0 0 ${LOCKUP.width} ${LOCKUP.height}`} className={cn('h-8 w-auto shrink-0', className)} role="img" aria-label={BRAND.name}>
      {mark}
      <g transform={LOCKUP.wordmarkTransform} fill="none" strokeWidth={WORDMARK_STROKE} strokeLinecap="round" strokeLinejoin="round">
        <path d={WORDMARK.loong} stroke="currentColor" />
        <path d={WORDMARK.lat} stroke="var(--accent)" />
        <rect x={WORDMARK.dot.x} y={WORDMARK.dot.y} width={WORDMARK.dot.size} height={WORDMARK.dot.size} rx={WORDMARK.dot.radius} fill="var(--accent)" />
      </g>
    </svg>
  )
}

/** Pembungkus judul bagian di dalam halaman. */
export function Section({ title, description, action, children, className }: { title: ReactNode; description?: ReactNode; action?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={cn('space-y-3', className)}>
      <CardHeader title={title} description={description} action={action} />
      {children}
    </section>
  )
}
