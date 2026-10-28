import { formatKm, formatRupiah, msg, t } from '@loong/i18n'
import { cn } from '../lib/cn'
import { MapView } from './MapView'

/** Contoh jam untuk uji tipografi (zona WIB): 14.20 dan 07.05. */
const SAMPLE_TIME = '2026-01-01T07:20:00Z'
const SAMPLE_CLOCK = '2026-01-01T00:05:00Z'

type Swatch = { name: string; className: string; note?: string }

const NEUTRALS: Swatch[] = [
  { name: 'bg', className: 'bg-bg' },
  { name: 'surface', className: 'bg-surface' },
  { name: 'line', className: 'bg-line' },
  { name: 'line-strong', className: 'bg-line-strong' },
  { name: 'subtle', className: 'bg-subtle' },
  { name: 'muted', className: 'bg-muted' },
  { name: 'fg', className: 'bg-fg' },
]

const ACCENT: Swatch[] = [
  { name: 'accent', className: 'bg-accent' },
  { name: 'accent-hover', className: 'bg-accent-hover' },
  { name: 'accent-tint', className: 'bg-accent-tint' },
]

const STATUS: Swatch[] = [
  { name: 'success', className: 'bg-success' },
  { name: 'warning', className: 'bg-warning' },
  { name: 'danger', className: 'bg-danger' },
]

function SwatchRow({ title, items }: { title: string; items: Swatch[] }) {
  return (
    <div>
      <h3 className="mb-2 text-small font-semibold text-muted">{title}</h3>
      <ul className="flex flex-wrap gap-3">
        {items.map((s) => (
          <li key={s.name} className="w-24">
            <div className={cn('h-12 rounded-input border border-line', s.className)} />
            <p className="mt-1 text-caption text-muted">{s.name}</p>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Halaman uji token untuk QA desain. Dipakai di rute `/_tokens` setiap app. */
export function TokenSheet({ appName, density }: { appName: string; density: string }) {
  return (
    <main className="mx-auto max-w-4xl space-y-8 p-6">
      <header>
        <p className="text-small text-muted">{t('ui.tokens.eyebrow', { density })}</p>
        <h1 className="text-display">{appName}</h1>
        <p className="mt-1 max-w-prose text-body text-muted">{t('ui.tokens.intro')}</p>
      </header>

      <section className="space-y-4 rounded-card border border-line bg-surface p-card shadow-card">
        <h2 className="text-title">{t('ui.tokens.colorTitle')}</h2>
        <SwatchRow title={t('ui.tokens.neutrals')} items={NEUTRALS} />
        <SwatchRow title={t('ui.tokens.accent')} items={ACCENT} />
        <SwatchRow title={t('ui.tokens.status')} items={STATUS} />
      </section>

      <section className="space-y-3 rounded-card border border-line bg-surface p-card shadow-card">
        <h2 className="text-title">{t('ui.tokens.typographyTitle')}</h2>
        <p className="text-metric tabular-nums">{formatRupiah(46_500)}</p>
        <p className="text-metric-sm tabular-nums">{formatKm(12.4)}</p>
        <p className="text-display">{t('ui.tokens.sampleDisplay')}</p>
        <p className="text-heading">{t('ui.tokens.sampleHeading')}</p>
        <p className="text-title">{t('ui.tokens.sampleTitle')}</p>
        <p className="text-body">{t('ui.tokens.sampleBody', { time: SAMPLE_TIME })}</p>
        <p className="text-small text-muted">{t('ui.tokens.sampleSmall', { weight: 8.5 })}</p>
        <p className="text-caption text-subtle">{t('ui.tokens.sampleCaption', { ago: msg('fmt.ago.seconds', { n: 2 }) })}</p>
        <p className="tabular-nums text-body">{t('ui.tokens.sampleNumbers', { a: 1_234_567, b: 98.5, c: SAMPLE_CLOCK, d: 0, e: 10_000_000 })}</p>
      </section>

      <section className="space-y-3 rounded-card border border-line bg-surface p-card shadow-card">
        <h2 className="text-title">{t('ui.tokens.shapeTitle')}</h2>
        <div className="flex flex-wrap gap-4">
          <div className="h-16 w-28 rounded-input border border-line bg-surface p-2 text-caption text-muted">{t('ui.tokens.shapeInput')}</div>
          <div className="h-16 w-28 rounded-card bg-surface p-2 text-caption text-muted shadow-card">{t('ui.tokens.shapeCard')}</div>
          <div className="h-16 w-28 rounded-modal bg-raised p-2 text-caption text-muted shadow-overlay">{t('ui.tokens.shapeModal')}</div>
        </div>
      </section>

      <section className="space-y-3 rounded-card border border-line bg-surface p-card shadow-card">
        <h2 className="text-title">{t('ui.tokens.mapTitle')}</h2>
        <MapView className="h-72" />
      </section>
    </main>
  )
}
