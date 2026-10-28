import { errorMessage, useDriverEarnings } from '@loong/data'
import { formatRupiah, type DriverEarnings } from '@loong/domain'
import { Card, ErrorState, Skeleton, cn } from '@loong/ui'
import { t } from '../i18n'
import { weekdayShort } from '../lib/dates'

/** Batang 7 hari dalam SVG/CSS biasa: tinggi = nominal, hari ini diberi aksen, angka bisa dibaca di bawahnya. */
function WeekBars({ days }: { days: DriverEarnings['byDay'] }) {
  const max = Math.max(1, ...days.map((d) => d.amountRp))
  return (
    <ol className="grid grid-cols-7 gap-2" aria-label={t('drv.home.earningsLast7')}>
      {days.map((d, i) => {
        const today = i === days.length - 1
        const height = Math.max(4, Math.round((d.amountRp / max) * 96))
        return (
          <li key={d.date} className="flex flex-col items-center gap-1.5">
            <span className="text-caption tabular-nums text-muted">{d.amountRp > 0 ? Math.round(d.amountRp / 1000) : '–'}</span>
            <span className="flex h-24 w-full items-end">
              <span className={cn('w-full rounded-t-[6px]', today ? 'bg-accent' : 'bg-line-strong')} style={{ height }} aria-hidden />
            </span>
            <span className={cn('text-caption', today ? 'font-semibold text-fg' : 'text-muted')}>{weekdayShort(d.date)}</span>
            <span className="sr-only">{t('drv.home.dayEarnings', { amount: d.amountRp, count: d.trips })}</span>
          </li>
        )
      })}
    </ol>
  )
}

export function Earnings() {
  const earnings = useDriverEarnings()
  const e = earnings.data

  return (
    <main className="space-y-5 px-4 pb-6 pt-5">
      <h1 className="text-heading">{t('drv.app.navEarnings')}</h1>

      {earnings.isError ? (
        <Card>
          <ErrorState message={errorMessage(earnings.error)} onRetry={() => void earnings.refetch()} />
        </Card>
      ) : !e ? (
        <>
          <Skeleton className="h-36 rounded-card" />
          <Skeleton className="h-52 rounded-card" />
        </>
      ) : (
        <>
          <Card className="p-card">
            <p className="text-small text-muted">{t('drv.home.today')}</p>
            <p className="text-metric tabular-nums text-accent">{formatRupiah(e.todayRp)}</p>
            <p className="mt-1 text-small text-muted">{t('drv.home.routesDone', { count: e.tripsToday })}</p>
            <div className="mt-4 flex items-baseline justify-between border-t border-line pt-3">
              <span className="text-small text-muted">{t('drv.home.last7')}</span>
              <span className="text-body font-semibold tabular-nums">
                {formatRupiah(e.weekRp)} <span className="font-normal text-muted">· {t('drv.home.routeCount', { count: e.tripsWeek })}</span>
              </span>
            </div>
          </Card>

          <Card className="p-card">
            <h2 className="mb-4 text-title">{t('drv.home.perDay')}</h2>
            <WeekBars days={e.byDay} />
            <p className="mt-3 text-caption text-muted">{t('drv.home.barsNote')}</p>
          </Card>

          <p className="px-1 text-small text-muted">{t('drv.home.earningsNote')}</p>
        </>
      )}
    </main>
  )
}
