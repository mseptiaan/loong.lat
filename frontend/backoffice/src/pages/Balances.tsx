import { errorMessage, useFinanceOverview, useTopUps, useUsers } from '@loong/data'
import { TOPUP_METHODS, TOPUP_METHOD_LABEL, formatRupiah, type TopUpMethod, type TopUpStatus } from '@loong/domain'
import { Button, Card, EmptyState, ErrorState, Input, PageHeader, Select, Skeleton, Stat, Tabs, TabsList, TabsTrigger } from '@loong/ui'
import { Search, Wallet } from 'lucide-react'
import { useSearchParams } from 'react-router'
import { TopUpTable } from '../components/TopUpTable'
import { t } from '../i18n'

// label disimpan sebagai kunci; t() dipanggil saat render agar mengikuti bahasa
const TABS = [
  { value: 'semua', labelKey: 'bo.balances.tabAll', match: () => true },
  { value: 'menunggu', labelKey: 'bo.balances.tabPending', match: (s: TopUpStatus) => s === 'menunggu' },
  { value: 'berhasil', labelKey: 'bo.balances.tabSuccessful', match: (s: TopUpStatus) => s === 'berhasil' },
  { value: 'gagal', labelKey: 'bo.balances.tabNotCompleted', match: (s: TopUpStatus) => s === 'kedaluwarsa' || s === 'dibatalkan' },
] as const

/** Saldo dan top up: dana pengguna yang dipegang platform, dan semua permintaan top up dari semua pengguna. */
export function Balances() {
  const [params, setParams] = useSearchParams()
  const tab = params.get('status') ?? 'semua'
  const q = params.get('q') ?? ''
  const method = (params.get('method') as TopUpMethod | null) ?? undefined
  const overview = useFinanceOverview()
  const topUps = useTopUps()
  const users = useUsers()

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

  const all = topUps.data ?? []
  const active = TABS.find((x) => x.value === tab) ?? TABS[0]
  const needle = q.trim().toLowerCase()
  const rows = all
    .filter((x) => active.match(x.status))
    .filter((x) => !method || x.method === method)
    .filter((x) => !needle || [x.userName, x.userEmail, x.reference, x.id].some((v) => v.toLowerCase().includes(needle)))
  const filtered = tab !== 'semua' || !!method || needle !== ''
  const o = overview.data

  return (
    <div className="space-y-4">
      <PageHeader title={t('bo.balances.title')} description={t('bo.balances.description')} />

      {overview.isError ? (
        <Card>
          <ErrorState compact message={errorMessage(overview.error)} onRetry={() => void overview.refetch()} />
        </Card>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          {overview.isLoading || !o ? (
            Array.from({ length: 4 }, (_, i) => <Skeleton key={i} className="h-28 rounded-card" />)
          ) : (
            <>
              <Stat
                emphasis="primary"
                label={t('bo.balances.statTotal')}
                value={formatRupiah(o.totalBalanceRp)}
                hint={t('bo.balances.statTotalHint', { count: o.usersWithBalance, total: users.data?.length ?? '…' })}
              />
              <Stat
                label={t('bo.balances.statPending')}
                value={o.pendingCount}
                hint={o.pendingCount > 0 ? t('bo.balances.statPendingHint', { amount: o.pendingAmountRp }) : t('bo.balances.statPendingNone')}
              />
              <Stat label={t('bo.balances.statPaidToday')} value={formatRupiah(o.paidTodayRp)} hint={t('bo.balances.transactions', { count: o.paidTodayCount })} />
              <Stat label={t('bo.balances.statPaid30d')} value={formatRupiah(o.paid30dRp)} hint={t('bo.balances.transactions', { count: o.paid30dCount })} />
            </>
          )}
        </div>
      )}

      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2.5">
          <Tabs value={active.value} onValueChange={(v) => set('status', v, 'semua')}>
            <TabsList aria-label={t('bo.balances.tabsLabel')}>
              {TABS.map((tb) => (
                <TabsTrigger key={tb.value} value={tb.value}>
                  {t(tb.labelKey)}
                  {!topUps.isLoading && <span className="text-small tabular-nums text-subtle">{all.filter((x) => tb.match(x.status)).length}</span>}
                </TabsTrigger>
              ))}
            </TabsList>
          </Tabs>
          <Input leading={<Search className="size-4" aria-hidden />} value={q} onChange={(e) => set('q', e.target.value)} placeholder={t('bo.balances.search')} aria-label={t('bo.balances.searchLabel')} className="h-9 w-64" />
          <Select
            aria-label={t('bo.balances.methodFilter')}
            value={method ?? 'semua'}
            onValueChange={(v) => set('method', v, 'semua')}
            className="h-9 w-56"
            options={[{ value: 'semua', label: t('bo.balances.methodAll') }, ...TOPUP_METHODS.map((m) => ({ value: m, label: TOPUP_METHOD_LABEL[m] }))]}
          />
          <span className="ml-auto text-small tabular-nums text-muted">{t('bo.balances.summary', { count: rows.length })}</span>
        </div>

        {topUps.isError ? (
          <ErrorState message={errorMessage(topUps.error)} onRetry={() => void topUps.refetch()} />
        ) : topUps.isLoading ? (
          <div className="space-y-px p-3">
            {Array.from({ length: 7 }, (_, i) => (
              <Skeleton key={i} className="h-12" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <EmptyState
            icon={<Wallet />}
            title={filtered ? t('bo.balances.emptyFilteredTitle') : t('bo.balances.emptyTitle')}
            description={filtered ? t('bo.balances.emptyFilteredHint') : t('bo.balances.emptyHint')}
            action={
              filtered ? (
                <Button
                  onClick={() =>
                    setParams(
                      (prev) => {
                        const next = new URLSearchParams(prev)
                        for (const k of ['status', 'q', 'method']) next.delete(k)
                        return next
                      },
                      { replace: true },
                    )
                  }
                >
                  {t('bo.balances.resetFilters')}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <TopUpTable rows={rows} />
        )}
      </Card>
    </div>
  )
}
