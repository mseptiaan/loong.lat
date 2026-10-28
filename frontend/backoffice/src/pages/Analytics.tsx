import { errorMessage, usePlans, useSettings, useUsers, useVirtualNow } from '@loong/data'
import { PLAN_STATUS_LABEL, VEHICLE_TYPE_LABEL, formatDate, formatPercent, formatRupiah, type PlanStatus, type PlanSummary } from '@loong/domain'
import { INTL_TAG, getLocale } from '@loong/i18n'
import { Card, CardHeader, EmptyState, ErrorState, PageHeader, Segmented, Skeleton, Stat, cn } from '@loong/ui'
import { Insights } from '@loong/shell'
import { ChartColumn } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { t } from '../i18n'
import { actionKind, isActive, isOperational } from '../lib/plans'

type Range = '7' | '14' | '30'
const DAY = 86_400_000
const TZ = 'Asia/Jakarta'
const dayKey = (ms: number) => new Intl.DateTimeFormat('en-CA', { year: 'numeric', month: '2-digit', day: '2-digit', timeZone: TZ }).format(new Date(ms))
/** Nama hari singkat mengikuti bahasa aktif (belum ada pemformat bersama untuk hari dalam seminggu). */
const weekday = (ms: number) => new Intl.DateTimeFormat(INTL_TAG[getLocale()], { weekday: 'short', timeZone: TZ }).format(new Date(ms))

function Bars({ days, ariaLabel }: { days: { ms: number; count: number; revenue: number }[]; ariaLabel: string }) {
  const max = Math.max(1, ...days.map((d) => d.count))
  const dense = days.length > 14
  return (
    <ol className="flex items-end gap-1.5" aria-label={ariaLabel} style={{ height: 176 }}>
      {days.map((d, i) => {
        const today = i === days.length - 1
        const summary = t('bo.analytics.dayTitle', { date: formatDate(d.ms), count: d.count, revenue: d.revenue })
        return (
          <li key={d.ms} className="flex h-full min-w-0 flex-1 flex-col items-center justify-end gap-1" title={summary}>
            {!dense && <span className="text-caption tabular-nums text-muted">{d.count || '–'}</span>}
            <span className={cn('w-full rounded-t-[5px]', today ? 'bg-accent' : 'bg-line-strong')} style={{ height: `${Math.max(3, (d.count / max) * 112)}px` }} aria-hidden />
            <span className={cn('truncate text-caption', today ? 'font-semibold text-fg' : 'text-muted')}>{dense ? (i % 3 === 0 ? formatDate(d.ms) : '') : weekday(d.ms)}</span>
            <span className="sr-only">{summary}</span>
          </li>
        )
      })}
    </ol>
  )
}

function Meter({ label, value, total, hint }: { label: string; value: number; total: number; hint?: string }) {
  const pct = total > 0 ? (value / total) * 100 : 0
  return (
    <li>
      <div className="flex items-baseline justify-between gap-3 text-body">
        <span className="min-w-0 truncate">{label}</span>
        <span className="shrink-0 tabular-nums text-muted">
          {hint ?? value}
        </span>
      </div>
      <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-sunken" aria-hidden>
        <div className="h-full rounded-full bg-accent/70" style={{ width: `${pct}%` }} />
      </div>
    </li>
  )
}

/** Analitik dihitung langsung dari data plan di cache; setiap angka bisa ditelusuri ke daftar plan. */
export function Analytics() {
  const plans = usePlans()
  const users = useUsers()
  const nowMs = useVirtualNow(60_000)
  const { data: settings } = useSettings()
  const navigate = useNavigate()
  const [range, setRange] = useState<Range>('7')

  const data = useMemo(() => {
    const span = Number(range) * DAY
    const all = (plans.data ?? []).filter(isOperational)
    const inRange = all.filter((p) => new Date(p.createdAt).getTime() >= nowMs - span)
    const billable = inRange.filter((p) => p.status !== 'dibatalkan')
    const revenue = billable.reduce((s, p) => s + (p.totalCostRp ?? 0), 0)
    const saved = billable.reduce((s, p) => s + Math.max(0, p.savingsRp ?? 0), 0)
    const done = inRange.filter((p) => p.status === 'selesai')

    const days = Array.from({ length: Number(range) }, (_, i) => {
      const ms = nowMs - (Number(range) - 1 - i) * DAY
      const key = dayKey(ms)
      const ofDay = billable.filter((p) => dayKey(new Date(p.createdAt).getTime()) === key)
      return { ms, count: ofDay.length, revenue: ofDay.reduce((s, p) => s + (p.totalCostRp ?? 0), 0) }
    })

    const routesBy = (t: 'motor' | 'blindvan') => billable.reduce((s, p) => s + p.vehicleTypes.filter((x) => x === t).length, 0)
    const statusCount = (Object.keys(PLAN_STATUS_LABEL) as PlanStatus[])
      .filter((s) => s !== 'draft' && s !== 'opsi_siap')
      .map((s) => ({ status: s, count: inRange.filter((p) => p.status === s).length }))

    const pickups = new Map<string, number>()
    for (const p of billable) if (p.fromLabel) pickups.set(p.fromLabel, (pickups.get(p.fromLabel) ?? 0) + 1)
    const topPickups = [...pickups.entries()].sort((a, b) => b[1] - a[1]).slice(0, 6)

    return {
      count: billable.length,
      cancelled: inRange.length - billable.length,
      revenue,
      saved,
      savedPct: revenue + saved > 0 ? (saved / (revenue + saved)) * 100 : undefined,
      cleanPct: done.length ? (done.filter((p) => !p.hasAnomaly).length / done.length) * 100 : undefined,
      doneCount: done.length,
      days,
      motor: routesBy('motor'),
      van: routesBy('blindvan'),
      statusCount,
      topPickups,
      anomalies: all.filter((p): p is PlanSummary => isActive(p) && actionKind(p) === 'anomali'),
    }
  }, [plans.data, nowMs, range])

  const loading = plans.isLoading
  const routesTotal = data.motor + data.van

  return (
    <div className="space-y-4">
      <PageHeader
        title={t('bo.analytics.title')}
        description={t('bo.analytics.description')}
        actions={
          <Segmented
            aria-label={t('bo.analytics.rangeLabel')}
            value={range}
            onValueChange={setRange}
            options={[
              { value: '7', label: t('bo.analytics.rangeDays', { count: 7 }) },
              { value: '14', label: t('bo.analytics.rangeDays', { count: 14 }) },
              { value: '30', label: t('bo.analytics.rangeDays', { count: 30 }) },
            ]}
          />
        }
      />

      {plans.isError ? (
        <Card>
          <ErrorState message={errorMessage(plans.error)} onRetry={() => void plans.refetch()} />
        </Card>
      ) : loading ? (
        <div className="grid gap-3 lg:grid-cols-4">
          {Array.from({ length: 4 }, (_, i) => (
            <Skeleton key={i} className="h-28 rounded-card" />
          ))}
        </div>
      ) : data.count === 0 ? (
        <Card>
          <EmptyState icon={<ChartColumn />} title={t('bo.analytics.emptyTitle')} description={t('bo.analytics.emptyHint')} />
        </Card>
      ) : (
        <>
          <div className="grid gap-3 lg:grid-cols-[minmax(0,1.5fr)_repeat(3,minmax(0,1fr))]">
            <Stat
              emphasis="primary"
              label={t('bo.analytics.statOrderValue')}
              value={formatRupiah(data.revenue)}
              hint={data.cancelled ? t('bo.analytics.plansCountCancelled', { count: data.count, cancelled: data.cancelled }) : t('bo.analytics.plansCount', { count: data.count })}
            />
            <Stat
              label={t('bo.analytics.statSavings')}
              value={formatRupiah(data.saved)}
              hint={data.savedPct !== undefined ? t('bo.analytics.statSavingsHint', { value: data.savedPct }) : undefined}
            />
            <Stat label={t('bo.analytics.statClean')} value={data.cleanPct === undefined ? '—' : formatPercent(data.cleanPct)} hint={t('bo.analytics.statCleanHint', { count: data.doneCount })} />
            <Stat label={t('bo.analytics.statRoutes')} value={routesTotal} hint={t('bo.analytics.statRoutesHint', { motor: data.motor, van: data.van })} />
          </div>

          {/* ringkasan AI setelah angkanya: data dulu, narasi kemudian, dan angka di atas tidak bergeser saat teks mengalir */}
          {settings?.ai.copilot && (
            <Insights
              onAction={async (a) => {
                if (a.kind === 'buka_plan' && a.planId) navigate(`/plan/${a.planId}`)
              }}
            />
          )}

          <div className="grid items-start gap-4 lg:grid-cols-[minmax(0,1.5fr)_minmax(0,1fr)]">
            <Card className="p-card">
              <CardHeader title={t('bo.analytics.chartTitle')} description={t('bo.analytics.chartDescription')} />
              <div className="mt-5">
                <Bars days={data.days} ariaLabel={t('bo.analytics.chartAria', { count: Number(range) })} />
              </div>
            </Card>

            <Card className="p-card">
              <CardHeader title={t('bo.analytics.anomaliesTitle')} description={t('bo.analytics.anomaliesDescription')} />
              {data.anomalies.length === 0 ? (
                <p className="mt-4 text-body text-muted">{t('bo.analytics.anomaliesNone')}</p>
              ) : (
                <ul className="mt-4 divide-y divide-line">
                  {data.anomalies.map((p) => (
                    <li key={p.id} className="py-2 first:pt-0 last:pb-0">
                      <Link to={`/plan/${p.id}`} className="block rounded-input hover:text-accent">
                        <span className="text-body font-semibold tabular-nums">{p.code}</span>
                        <span className="block truncate text-small text-muted">
                          {p.customerName} · {p.fromLabel} → {p.toLabel}
                        </span>
                      </Link>
                    </li>
                  ))}
                </ul>
              )}
            </Card>
          </div>

          <div className="grid items-start gap-4 lg:grid-cols-3">
            <Card className="p-card">
              <CardHeader title={t('bo.analytics.statusTitle')} />
              <ul className="mt-4 space-y-3">
                {data.statusCount.map((s) => (
                  <Meter key={s.status} label={PLAN_STATUS_LABEL[s.status]} value={s.count} total={Math.max(1, data.count + data.cancelled)} />
                ))}
              </ul>
              <div className="mt-5 border-t border-line pt-4">
                <p className="text-small font-semibold text-muted">{t('bo.analytics.vehicleMix')}</p>
                <div className="mt-2 flex h-2 overflow-hidden rounded-full bg-sunken" role="img" aria-label={t('bo.analytics.vehicleMixAria', { motor: data.motor, van: data.van })}>
                  <div className="bg-accent" style={{ width: `${routesTotal ? (data.motor / routesTotal) * 100 : 0}%` }} />
                  <div className="bg-line-strong" style={{ width: `${routesTotal ? (data.van / routesTotal) * 100 : 0}%` }} />
                </div>
                <p className="mt-2 flex justify-between text-small tabular-nums text-muted">
                  <span>
                    {VEHICLE_TYPE_LABEL.motor} {formatPercent(routesTotal ? (data.motor / routesTotal) * 100 : 0)}
                  </span>
                  <span>
                    {VEHICLE_TYPE_LABEL.blindvan} {formatPercent(routesTotal ? (data.van / routesTotal) * 100 : 0)}
                  </span>
                </p>
              </div>
            </Card>

            <Card className="p-card">
              <CardHeader title={t('bo.analytics.pickupsTitle')} description={t('bo.analytics.pickupsDescription')} />
              <ul className="mt-4 space-y-3">
                {data.topPickups.map(([label, n]) => (
                  <Meter key={label} label={label} value={n} total={data.topPickups[0]![1]} hint={t('bo.analytics.plansCount', { count: n })} />
                ))}
              </ul>
            </Card>

            <Card className="p-card">
              <CardHeader title={t('bo.analytics.customersTitle')} description={t('bo.analytics.customersDescription')} />
              <ol className="mt-4 divide-y divide-line">
                {(users.data ?? []).slice(0, 5).map((u, i) => (
                  <li key={u.id} className="flex items-baseline justify-between gap-3 py-2 first:pt-0 last:pb-0">
                    <Link to={`/user/${u.id}`} className="min-w-0 truncate text-body hover:text-accent">
                      <span className="mr-2 tabular-nums text-muted">{i + 1}</span>
                      {u.name}
                    </Link>
                    <span className="shrink-0 text-body tabular-nums">{formatRupiah(u.totalSpendRp)}</span>
                  </li>
                ))}
              </ol>
            </Card>
          </div>
        </>
      )}
    </div>
  )
}
