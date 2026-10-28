import { errorMessage, usePlans, useUser, useVirtualNow } from '@loong/data'
import { VEHICLE_TYPE_LABEL, type PlanStatus, type PlanSummary, type VehicleType } from '@loong/domain'
import { Button, Card, EmptyState, ErrorState, Input, PageHeader, Select, Skeleton, Tabs, TabsList, TabsTrigger } from '@loong/ui'
import { ListChecks, Search, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useSearchParams } from 'react-router'
import { AssignSheet } from '../components/AssignPanel'
import { PlanTable } from '../components/PlanTable'
import { t } from '../i18n'
import { isOperational, routeText } from '../lib/plans'

type StatusKey = 'semua' | 'menunggu' | 'aktif' | 'selesai' | 'batal' | 'draft'
type RangeKey = 'semua' | 'hari-ini' | '7' | '30'
type SortKey = 'terbaru' | 'biaya' | 'hemat'

type TabLabelKey = 'bo.plans.tabAll' | 'bo.plans.tabFinding' | 'bo.plans.tabActive' | 'bo.plans.tabDone' | 'bo.plans.tabCancelled' | 'bo.plans.tabDraft'

// simpan kunci terjemahan (bukan teks) agar label mengikuti bahasa saat dirender
const STATUS_TABS: { key: StatusKey; labelKey: TabLabelKey; match: (s: PlanStatus) => boolean }[] = [
  { key: 'semua', labelKey: 'bo.plans.tabAll', match: (s) => s !== 'draft' && s !== 'opsi_siap' },
  { key: 'menunggu', labelKey: 'bo.plans.tabFinding', match: (s) => s === 'menunggu_driver' },
  { key: 'aktif', labelKey: 'bo.plans.tabActive', match: (s) => s === 'ditugaskan' || s === 'berjalan' },
  { key: 'selesai', labelKey: 'bo.plans.tabDone', match: (s) => s === 'selesai' },
  { key: 'batal', labelKey: 'bo.plans.tabCancelled', match: (s) => s === 'dibatalkan' },
  { key: 'draft', labelKey: 'bo.plans.tabDraft', match: (s) => s === 'draft' || s === 'opsi_siap' },
]

const DAY = 86_400_000
const RANGE_MS: Record<RangeKey, number | undefined> = { semua: undefined, 'hari-ini': DAY, '7': 7 * DAY, '30': 30 * DAY }

const isStatusKey = (v: string | null): v is StatusKey => STATUS_TABS.some((tab) => tab.key === v)

function matches(p: PlanSummary, q: string) {
  const needle = q.trim().toLowerCase()
  if (!needle) return true
  return [p.code, p.customerName, routeText(p), ...p.driverNames].some((s) => s.toLowerCase().includes(needle))
}

/** Semua plan: filter status (dengan hitungan), kendaraan, tanggal, dan pencarian; urut dan batasi baris. */
export function Plans() {
  const plans = usePlans()
  const nowMs = useVirtualNow(60_000)
  const [params, setParams] = useSearchParams()
  const status: StatusKey = isStatusKey(params.get('status')) ? (params.get('status') as StatusKey) : 'semua'
  const userId = params.get('user') ?? undefined
  const user = useUser(userId)
  const [q, setQ] = useState('')
  const [vehicle, setVehicle] = useState<'semua' | VehicleType>('semua')
  const [range, setRange] = useState<RangeKey>('semua')
  const [sort, setSort] = useState<SortKey>('terbaru')
  const [limit, setLimit] = useState(100)
  const [assignFor, setAssignFor] = useState<string>()

  const scoped = useMemo(() => (plans.data ?? []).filter((p) => (!userId || p.userId === userId) && (isOperational(p) || status === 'draft')), [plans.data, userId, status])

  const counts = useMemo(() => Object.fromEntries(STATUS_TABS.map((tab) => [tab.key, scoped.filter((p) => tab.match(p.status)).length])) as Record<StatusKey, number>, [scoped])

  const rows = useMemo(() => {
    const tab = STATUS_TABS.find((x) => x.key === status)!
    const span = RANGE_MS[range]
    const list = scoped.filter(
      (p) =>
        tab.match(p.status) &&
        matches(p, q) &&
        (vehicle === 'semua' || p.vehicleTypes.includes(vehicle)) &&
        (!span || new Date(p.createdAt).getTime() >= nowMs - span),
    )
    return list.sort((a, b) =>
      sort === 'biaya' ? (b.totalCostRp ?? 0) - (a.totalCostRp ?? 0) : sort === 'hemat' ? (b.savingsRp ?? 0) - (a.savingsRp ?? 0) : b.createdAt.localeCompare(a.createdAt),
    )
  }, [scoped, status, q, vehicle, range, sort, nowMs])

  const filtered = q.trim() !== '' || vehicle !== 'semua' || range !== 'semua'
  const totalCost = rows.reduce((s, p) => s + (p.totalCostRp ?? 0), 0)

  const reset = () => {
    setQ('')
    setVehicle('semua')
    setRange('semua')
  }

  return (
    <div className="space-y-4">
      <PageHeader
        title={user.data ? t('bo.plans.titleUser', { name: user.data.user.name }) : t('bo.plans.title')}
        description={user.data ? t('bo.plans.descriptionUser') : t('bo.plans.description')}
        back={userId ? { label: t('bo.plans.backAll'), onClick: () => setParams({}, { replace: true }) } : undefined}
      />

      <Card className="flex flex-col overflow-hidden">
        <Tabs value={status} onValueChange={(v) => setParams(v === 'semua' ? (userId ? { user: userId } : {}) : { status: v, ...(userId ? { user: userId } : {}) }, { replace: true })}>
          <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-line pr-3">
            <TabsList className="border-b-0 pl-2">
              {STATUS_TABS.map((tab) => (
                <TabsTrigger key={tab.key} value={tab.key}>
                  {t(tab.labelKey)} <span className="tabular-nums text-muted">{plans.isError ? '–' : counts[tab.key]}</span>
                </TabsTrigger>
              ))}
            </TabsList>
          </div>
        </Tabs>

        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2.5">
          <Input
            leading={<Search className="size-4" aria-hidden />}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            placeholder={t('bo.plans.search')}
            aria-label={t('bo.plans.searchLabel')}
            className="h-9 w-72"
          />
          <Select
            aria-label={t('bo.plans.vehicleFilter')}
            value={vehicle}
            onValueChange={(v) => setVehicle(v as 'semua' | VehicleType)}
            className="h-9 w-44"
            options={[
              { value: 'semua', label: t('bo.plans.vehicleAll') },
              { value: 'motor', label: VEHICLE_TYPE_LABEL.motor },
              { value: 'blindvan', label: VEHICLE_TYPE_LABEL.blindvan },
            ]}
          />
          <Select
            aria-label={t('bo.plans.rangeFilter')}
            value={range}
            onValueChange={(v) => setRange(v as RangeKey)}
            className="h-9 w-40"
            options={[
              { value: 'semua', label: t('bo.plans.rangeAll') },
              { value: 'hari-ini', label: t('bo.plans.range24h') },
              { value: '7', label: t('bo.plans.range7d') },
              { value: '30', label: t('bo.plans.range30d') },
            ]}
          />
          {filtered && (
            <Button variant="ghost" size="sm" iconLeft={<X className="size-3.5" aria-hidden />} onClick={reset}>
              {t('bo.plans.reset')}
            </Button>
          )}
          <div className="ml-auto flex items-center gap-3">
            <span className="hidden text-small tabular-nums text-muted md:inline">
              {totalCost > 0 ? t('bo.plans.summaryCost', { count: rows.length, total: totalCost }) : t('bo.plans.summary', { count: rows.length })}
            </span>
            <Select
              aria-label={t('bo.plans.sort')}
              value={sort}
              onValueChange={(v) => setSort(v as SortKey)}
              className="h-9 w-44"
              options={[
                { value: 'terbaru', label: t('bo.plans.sortNewest') },
                { value: 'biaya', label: t('bo.plans.sortCost') },
                { value: 'hemat', label: t('bo.plans.sortSavings') },
              ]}
            />
          </div>
        </div>

        {plans.isError ? (
          <ErrorState message={errorMessage(plans.error)} onRetry={() => void plans.refetch()} />
        ) : plans.isLoading ? (
          <div className="space-y-px p-3">
            {Array.from({ length: 8 }, (_, i) => (
              <Skeleton key={i} className="h-12" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <EmptyState
            icon={<ListChecks />}
            title={filtered ? t('bo.plans.emptyFilteredTitle') : t('bo.plans.emptyTitle')}
            description={filtered ? t('bo.plans.emptyFilteredHint') : t('bo.plans.emptyHint')}
            action={filtered ? <Button onClick={reset}>{t('bo.plans.resetFilters')}</Button> : undefined}
          />
        ) : (
          <>
            <PlanTable plans={rows.slice(0, limit)} nowMs={nowMs} variant="list" onAssign={setAssignFor} />
            {rows.length > limit && (
              <div className="border-t border-line p-3 text-center">
                <Button onClick={() => setLimit((l) => l + 100)}>
                  {t('bo.plans.showMore', { next: Math.min(100, rows.length - limit), rest: rows.length - limit })}
                </Button>
              </div>
            )}
          </>
        )}
      </Card>

      <AssignSheet planId={assignFor} open={!!assignFor} onOpenChange={(open) => !open && setAssignFor(undefined)} />
    </div>
  )
}
