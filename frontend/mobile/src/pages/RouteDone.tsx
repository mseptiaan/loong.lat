import { errorMessage, useDriverHistory } from '@loong/data'
import { formatDuration, formatKm, formatRupiah } from '@loong/domain'
import { Button, Card, EmptyState, ErrorState, Skeleton } from '@loong/ui'
import { Check, Compass } from 'lucide-react'
import { Link, useParams } from 'react-router'
import { t } from '../i18n'

/** Layar penutup rute: berapa yang didapat, lalu dua langkah lanjut yang jelas. */
export function RouteDone() {
  const { routeId } = useParams()
  const history = useDriverHistory()
  const item = history.data?.find((h) => h.routeId === routeId)

  if (history.isLoading) {
    return (
      <div className="space-y-4 p-6">
        <Skeleton className="mx-auto size-16 rounded-full" />
        <Skeleton className="h-40 rounded-card" />
      </div>
    )
  }
  if (history.isError) {
    return (
      <div className="p-4">
        <Card>
          <ErrorState message={errorMessage(history.error)} onRetry={() => void history.refetch()} />
        </Card>
      </div>
    )
  }
  if (!item) {
    return (
      <main className="p-4">
        <EmptyState
          icon={<Compass />}
          title={t('drv.route.doneMissingTitle')}
          description={t('drv.route.doneMissingHint')}
          action={
            <Button variant="primary" size="lg" asChild>
              <Link to="/">{t('drv.app.backHome')}</Link>
            </Button>
          }
        />
      </main>
    )
  }

  return (
    <main className="flex min-h-dvh flex-col px-6 py-10 [animation:fade-up_360ms_var(--ease-out)]">
      <div className="flex flex-1 flex-col items-center text-center">
        <div className="grid size-16 place-items-center rounded-full bg-accent text-on-accent shadow-card" aria-hidden>
          <Check className="size-8" strokeWidth={2.5} />
        </div>
        <h1 className="mt-5 text-heading">{t('drv.route.doneTitle')}</h1>
        <p className="mt-1 text-body text-muted">{t('drv.route.doneThanks', { code: item.planCode })}</p>

        <Card className="mt-6 w-full p-card text-left">
          <p className="text-small text-muted">{t('drv.route.doneEarnings')}</p>
          <p className="text-metric tabular-nums">{formatRupiah(item.earningsRp)}</p>
          <dl className="mt-4 grid grid-cols-3 gap-3 border-t border-line pt-4">
            <div>
              <dt className="text-small text-muted">{t('drv.route.doneStops')}</dt>
              <dd className="text-body font-semibold tabular-nums">{item.stops}</dd>
            </div>
            <div>
              <dt className="text-small text-muted">{t('drv.route.factDistance')}</dt>
              <dd className="text-body font-semibold tabular-nums">{formatKm(item.distanceKm)}</dd>
            </div>
            <div>
              <dt className="text-small text-muted">{t('drv.route.doneTime')}</dt>
              <dd className="text-body font-semibold tabular-nums">{formatDuration(item.durationMin)}</dd>
            </div>
          </dl>
        </Card>
      </div>

      <div className="mt-8 space-y-2.5 pb-[env(safe-area-inset-bottom)]">
        <Button variant="primary" size="lg" className="w-full" asChild>
          <Link to="/">{t('drv.app.backHome')}</Link>
        </Button>
        <Button variant="secondary" size="lg" className="w-full" asChild>
          <Link to="/history">{t('drv.route.viewHistory')}</Link>
        </Button>
      </div>
    </main>
  )
}
