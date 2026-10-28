import { errorMessage, usePlans, useVirtualNow } from '@loong/data'
import { ApiError, type PlanSummary } from '@loong/domain'
import { Button, Card, EmptyState, ErrorState, Input, PageHeader, Skeleton, Tabs, TabsList, TabsTrigger } from '@loong/ui'
import { Package, Plus, Search, SearchX } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'
import { PlanRow } from '../../components/plans'
import { t } from '../../i18n'

// label dipanggil saat render (bukan di tingkat modul) agar mengikuti bahasa
const TABS = [
  { value: 'semua', label: () => t('dash.plans.tabAll'), match: () => true },
  { value: 'aktif', label: () => t('dash.plans.tabActive'), match: (p: PlanSummary) => ['menunggu_driver', 'ditugaskan', 'berjalan'].includes(p.status) },
  { value: 'selesai', label: () => t('dash.plans.tabDone'), match: (p: PlanSummary) => p.status === 'selesai' },
  { value: 'batal', label: () => t('dash.plans.tabCancelled'), match: (p: PlanSummary) => p.status === 'dibatalkan' },
] as const

export function PlanList() {
  const [params, setParams] = useSearchParams()
  const tab = params.get('tab') ?? 'semua'
  const q = params.get('q') ?? ''
  const now = useVirtualNow(1000)
  const { data: all, isLoading, isError, error, refetch, isRefetching } = usePlans()

  const set = (key: string, value: string, fallback = '') =>
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        if (value && value !== fallback) next.set(key, value)
        else next.delete(key)
        return next
      },
      { replace: true },
    )

  const drafts = (all ?? []).filter((p) => p.status === 'draft' || p.status === 'opsi_siap')
  const orders = (all ?? []).filter((p) => p.status !== 'draft' && p.status !== 'opsi_siap')
  const active = TABS.find((x) => x.value === tab) ?? TABS[0]
  const needle = q.trim().toLowerCase()
  const rows = orders
    .filter((p) => active.match(p))
    .filter((p) => !needle || [p.code, p.fromLabel, p.toLabel, ...p.driverNames].some((x) => x.toLowerCase().includes(needle)))
  const filtered = tab !== 'semua' || !!needle

  return (
    <div className="space-y-6">
      <PageHeader title={t('dash.plans.listTitle')} description={t('dash.plans.listLead')} />

      <div className="space-y-4">
        <Tabs value={active.value} onValueChange={(v) => set('tab', v, 'semua')}>
          <TabsList aria-label={t('dash.plans.filterLabel')}>
            {TABS.map((item) => {
              const n = orders.filter((p) => item.match(p)).length
              return (
                <TabsTrigger key={item.value} value={item.value}>
                  {item.label()}
                  {!isLoading && <span className="text-small tabular-nums text-subtle">{n}</span>}
                </TabsTrigger>
              )
            })}
          </TabsList>
        </Tabs>
        <div className="max-w-md">
          <Input
            aria-label={t('dash.plans.searchLabel')}
            leading={<Search className="size-4" aria-hidden />}
            placeholder={t('dash.plans.searchPlaceholder')}
            value={q}
            onChange={(e) => set('q', e.target.value)}
            type="search"
          />
        </div>
      </div>

      {isError ? (
        <Card>
          <ErrorState message={errorMessage(error)} onRetry={() => void refetch()} retrying={isRefetching} offline={error instanceof ApiError && error.code === 'jaringan'} />
        </Card>
      ) : isLoading ? (
        <div className="space-y-3">
          {[0, 1, 2, 3].map((i) => (
            <Skeleton key={i} className="h-[4.75rem] w-full rounded-card" />
          ))}
        </div>
      ) : (
        <>
          {drafts.length > 0 && tab === 'semua' && !needle && (
            <section aria-labelledby="draft" className="space-y-3">
              <h2 id="draft" className="text-title">
                {t('dash.plans.draftsHeading')}
              </h2>
              {drafts.map((p) => (
                <PlanRow key={p.id} plan={p} now={now} />
              ))}
            </section>
          )}

          {rows.length === 0 ? (
            <Card>
              {filtered ? (
                <EmptyState
                  icon={<SearchX />}
                  title={t('dash.plans.noMatchTitle')}
                  description={t('dash.plans.noMatchHint')}
                  action={
                    <Button
                      variant="secondary"
                      onClick={() => {
                        set('tab', '')
                        set('q', '')
                      }}
                    >
                      {t('dash.plans.clearFilters')}
                    </Button>
                  }
                />
              ) : (
                <EmptyState
                  icon={<Package />}
                  title={t('dash.plans.emptyTitle')}
                  description={t('dash.plans.emptyHint')}
                  action={
                    <Button variant="tint" asChild>
                      <Link to="/plan/new">
                        <Plus className="size-4" aria-hidden />
                        {t('dash.app.newDelivery')}
                      </Link>
                    </Button>
                  }
                />
              )}
            </Card>
          ) : (
            <section aria-label={t('dash.plans.listLabel')} className="space-y-3">
              {drafts.length > 0 && tab === 'semua' && !needle && (
                <h2 className="text-title">{t('dash.plans.ordersHeading')}</h2>
              )}
              {rows.map((p) => (
                <PlanRow key={p.id} plan={p} now={now} />
              ))}
            </section>
          )}
        </>
      )}
    </div>
  )
}
