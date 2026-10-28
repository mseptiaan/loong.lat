import { usePlan, useVirtualNow } from '@loong/data'
import {
  formatKm,
  formatRelativeDay,
  formatRupiah,
  formatTime,
  liveEtas,
  planStatusText,
  routePositionAt,
  travelledKm,
  type PlanSummary,
  type Route,
} from '@loong/domain'
import { Button, Card, PlanStatusLabel, RouteMap, Skeleton, Spinner } from '@loong/ui'
import { ChevronRight, Package, UserRound } from 'lucide-react'
import { useMemo } from 'react'
import { Link } from 'react-router'
import { t } from '../i18n'

export const routeLabel = (p: Pick<PlanSummary, 'fromLabel' | 'toLabel'>) => {
  // draft yang baru dibuat belum punya lokasi: jangan tampilkan panah kosong
  if (!p.fromLabel && !p.toLabel) return t('dash.plans.noLocations')
  if (!p.toLabel) return t('dash.plans.toNoDestination', { from: p.fromLabel })
  if (!p.fromLabel) return t('dash.plans.fromNoPickup', { to: p.toLabel })
  return `${p.fromLabel} → ${p.toLabel}`
}

/** Baris plan ringkas: dipakai di Beranda dan daftar. Seluruh baris adalah tautan (target sentuh penuh). */
export function PlanRow({ plan, now }: { plan: PlanSummary; now: number }) {
  const href = plan.status === 'draft' || plan.status === 'opsi_siap' ? `/plan/new?draft=${plan.id}&step=${Math.max(1, Math.min(plan.draftStep, 4))}` : `/plan/${plan.id}`
  return (
    <Link
      to={href}
      className="group flex items-center gap-4 rounded-card border border-line bg-surface p-4 shadow-card transition-[border-color,transform] duration-[140ms] hover:border-line-strong active:scale-[0.995]"
    >
      <div className="grid size-10 shrink-0 place-items-center rounded-input bg-sunken text-muted" aria-hidden>
        <Package className="size-5" />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <p className="text-body font-semibold tabular-nums">{plan.code}</p>
          <p className="text-small text-subtle">{formatRelativeDay(plan.createdAt, new Date(now))}</p>
        </div>
        <p className="truncate text-small text-muted">{routeLabel(plan)}</p>
        <p className="mt-1 text-small">
          <PlanStatusLabel status={plan.status} pendingAction={plan.pendingAction} />
        </p>
      </div>
      <div className="shrink-0 text-right">
        {plan.totalCostRp !== undefined ? <p className="text-body font-semibold tabular-nums">{formatRupiah(plan.totalCostRp)}</p> : <p className="text-small text-subtle">—</p>}
        <p className="text-small text-muted">{t('dash.plans.parcels', { count: plan.shipments })}</p>
      </div>
      <ChevronRight className="size-4 shrink-0 text-subtle transition-transform group-hover:translate-x-0.5" aria-hidden />
    </Link>
  )
}

/** Teks status + ETA dari rute aktif, dihitung dari jam virtual. */
export function useRouteStatus(route: Route | undefined, now: number) {
  return useMemo(() => {
    if (!route) return undefined
    const pos = routePositionAt(route, now)
    const etas = route.status === 'berjalan' ? liveEtas(route, now) : []
    const lastEta = etas.at(-1)?.etaAt ?? route.stops.at(-1)?.arriveAt
    const next = route.stops[pos.stopIndex]
    return { pos, lastEta, next, travelled: route.status === 'berjalan' ? travelledKm(route, now) : 0 }
  }, [route, now])
}

/** Kartu pengiriman aktif dengan peta mini yang bergerak (hanya untuk satu-dua kartu agar ringan). */
export function ActiveShipmentCard({ plan, withMap = true }: { plan: PlanSummary; withMap?: boolean }) {
  const { data: full } = usePlan(plan.id)
  const now = useVirtualNow(500)
  const route = full?.routes.find((r) => r.status === 'berjalan') ?? full?.routes.find((r) => r.status === 'ditugaskan') ?? full?.routes[0]
  const live = useRouteStatus(route, now)
  const driver = plan.driverNames[0]

  const headline =
    plan.pendingAction === 'cancel'
      ? t('dash.plans.cancellingShort')
      : plan.status === 'berjalan' && live?.next
      ? t(live.next.kind === 'ambil' ? 'dash.plans.headingPickup' : 'dash.plans.headingDropoff', { name: live.next.waypoint.name })
      : plan.status === 'ditugaskan'
        ? driver
          ? t('dash.plans.driverDepartingNamed', { driver })
          : t('dash.plans.driverDeparting')
        : plan.scheduleMode === 'terjadwal'
          ? t('dash.plans.waitingForSchedule')
          : t('dash.plans.findingNearest')

  return (
    <Card className="overflow-hidden">
      {withMap && (
        <div className="h-44 border-b border-line bg-sunken">
          {route && live ? (
            <RouteMap
              className="rounded-none"
              interactive={false}
              padding={36}
              fitKey={`${plan.id}:${route.id}`}
              routes={[
                {
                  id: route.id,
                  route,
                  emphasis: 'primary',
                  travelledKm: live.travelled,
                  activeStopIndex: live.pos.stopIndex,
                  vehicle: route.status === 'berjalan' ? { position: live.pos.position, bearing: live.pos.bearing, type: route.vehicleType } : undefined,
                }
              ]}
              ariaLabel={t('dash.plans.mapLabel', { code: plan.code })}
            />
          ) : (
            <Skeleton className="h-full w-full rounded-none" />
          )}
        </div>
      )}
      <div className="space-y-3 p-card">
        <div className="flex items-start justify-between gap-3">
          <div className="min-w-0">
            <p className="flex items-baseline gap-2">
              <span className="text-title tabular-nums">{plan.code}</span>
              <PlanStatusLabel status={plan.status} pendingAction={plan.pendingAction} className="text-small text-muted" />
            </p>
            <p className="mt-0.5 truncate text-small text-muted">{routeLabel(plan)}</p>
          </div>
          <p className="shrink-0 text-body font-semibold tabular-nums">{plan.totalCostRp !== undefined ? formatRupiah(plan.totalCostRp) : ''}</p>
        </div>

        <div className="flex items-center gap-2 text-body">
          {(plan.status === 'menunggu_driver' || plan.pendingAction === 'cancel') && <Spinner />}
          <span>{headline}</span>
        </div>

        <dl className="grid grid-cols-3 gap-2 border-t border-line pt-3 text-small">
          <div>
            <dt className="text-muted">{t('dash.plans.arrivesAround')}</dt>
            <dd className="mt-0.5 font-semibold tabular-nums">{live?.lastEta ? formatTime(live.lastEta) : '—'}</dd>
          </div>
          <div>
            <dt className="text-muted">{t('dash.plans.progress')}</dt>
            <dd className="mt-0.5 font-semibold tabular-nums">{t('dash.plans.progressStops', { done: plan.progress.done, count: plan.progress.total })}</dd>
          </div>
          <div>
            <dt className="text-muted">{t('dash.plans.distance')}</dt>
            <dd className="mt-0.5 font-semibold tabular-nums">{plan.distanceKm !== undefined ? formatKm(plan.distanceKm) : '—'}</dd>
          </div>
        </dl>

        <div className="flex items-center justify-between gap-3">
          <p className="flex min-w-0 items-center gap-1.5 text-small text-muted">
            <UserRound className="size-4 shrink-0" aria-hidden />
            <span className="truncate">{driver ?? planStatusText(plan)}</span>
          </p>
          <Button variant="secondary" size="sm" asChild>
            <Link to={`/plan/${plan.id}`}>{t('dash.plans.track')}</Link>
          </Button>
        </div>
      </div>
    </Card>
  )
}
