import { errorMessage, useDriverHistory, useVirtualNow } from '@loong/data'
import { formatRupiah, type DriverHistoryItem } from '@loong/domain'
import { Card, EmptyState, ErrorState, Skeleton } from '@loong/ui'
import { PackageCheck } from 'lucide-react'
import { t } from '../i18n'
import { dayKeyOf, dayLabel } from '../lib/dates'

function groupByDay(items: DriverHistoryItem[]): { key: string; first: DriverHistoryItem; items: DriverHistoryItem[]; total: number }[] {
  const groups = new Map<string, { key: string; first: DriverHistoryItem; items: DriverHistoryItem[]; total: number }>()
  for (const it of items) {
    const key = dayKeyOf(it.finishedAt)
    const g = groups.get(key) ?? { key, first: it, items: [], total: 0 }
    g.items.push(it)
    g.total += it.earningsRp
    groups.set(key, g)
  }
  return [...groups.values()]
}

export function History() {
  const history = useDriverHistory()
  const now = useVirtualNow(60_000)

  return (
    <main className="px-4 pb-6 pt-5">
      <h1 className="text-heading">{t('drv.app.navHistory')}</h1>
      <p className="mt-1 text-body text-muted">{t('drv.home.historyIntro')}</p>

      <div className="mt-5 space-y-6">
        {history.isLoading ? (
          <>
            <Skeleton className="h-24 rounded-card" />
            <Skeleton className="h-24 rounded-card" />
          </>
        ) : history.isError ? (
          <Card>
            <ErrorState message={errorMessage(history.error)} onRetry={() => void history.refetch()} />
          </Card>
        ) : !history.data?.length ? (
          <Card>
            <EmptyState icon={<PackageCheck />} title={t('drv.home.historyEmptyTitle')} description={t('drv.home.historyEmptyHint')} />
          </Card>
        ) : (
          groupByDay(history.data).map((g) => (
            <section key={g.key} aria-label={dayLabel(g.first.finishedAt, now)}>
              <div className="mb-2 flex items-baseline justify-between">
                <h2 className="text-small font-semibold text-muted">{dayLabel(g.first.finishedAt, now)}</h2>
                <p className="text-small tabular-nums text-muted">{formatRupiah(g.total)}</p>
              </div>
              <Card className="divide-y divide-line">
                {g.items.map((it) => (
                  <div key={it.routeId} className="flex items-start justify-between gap-3 p-card">
                    <div className="min-w-0">
                      <p className="text-body font-semibold">{it.planCode}</p>
                      <p className="text-small tabular-nums text-muted">
                        {t('drv.home.historyLine', { time: it.finishedAt, count: it.stops, km: it.distanceKm, duration: it.durationMin })}
                      </p>
                    </div>
                    <p className="shrink-0 text-body font-semibold tabular-nums">{formatRupiah(it.earningsRp)}</p>
                  </div>
                ))}
              </Card>
            </section>
          ))
        )}
      </div>
    </main>
  )
}
