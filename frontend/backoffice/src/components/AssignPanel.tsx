import { errorMessage, qk, useApi, useMutation, usePlan, useQueryClient, useSettings, useSuggestions } from '@loong/data'
import { RECOMMEND_MIN_SCORE, VEHICLE_TYPE_LABEL, type DriverSuggestion, type Plan, type Route } from '@loong/domain'
import { renderMsg } from '@loong/i18n'
import { Avatar, Button, ErrorState, Notice, Sheet, Skeleton, toast } from '@loong/ui'
import { Sparkles } from 'lucide-react'
import { t } from '../i18n'

/** Satu saran driver beserta alasannya. Menekan "Tugaskan" adalah konfirmasi manusia; AI hanya mengusulkan. */
function SuggestionRow({ s, best, pending, onAssign }: { s: DriverSuggestion; best: boolean; pending: boolean; onAssign: () => void }) {
  return (
    <li className="flex items-start gap-3 py-3 first:pt-0 last:pb-0">
      <Avatar name={s.driverName} />
      <div className="min-w-0 flex-1">
        <p className="flex flex-wrap items-baseline gap-x-2 text-body">
          <span className="font-semibold">{s.driverName}</span>
          {best && s.fits && s.score >= RECOMMEND_MIN_SCORE && (
            <span className="inline-flex items-center gap-1 text-small font-medium text-accent">
              <Sparkles className="size-3.5" aria-hidden /> {t('bo.plans.recommended')}
            </span>
          )}
        </p>
        <p className="text-small text-muted">
          {VEHICLE_TYPE_LABEL[s.vehicleType]} · {s.plate}
        </p>
        {!s.fits && <p className="mt-0.5 text-small font-medium text-warning-fg">{t('bo.plans.doesNotFit')}</p>}
        {s.reasons.length > 0 ? (
          <ul className="mt-1.5 space-y-0.5 text-small text-muted">
            {s.reasons.map((r, i) => (
              <li key={i}>· {renderMsg(r)}</li>
            ))}
          </ul>
        ) : (
          <p className="mt-1 text-small tabular-nums text-muted">
            {t('bo.plans.suggestionDistance', { km: s.distanceToPickupKm, min: s.etaToPickupMin })}
          </p>
        )}
      </div>
      <div className="flex shrink-0 flex-col items-end gap-1.5">
        <span className="text-small tabular-nums text-muted" title={t('bo.plans.scoreHint')}>
          {t('bo.plans.score', { score: Math.round(s.score) })}
        </span>
        <Button
          size="sm"
          variant={best && s.fits && s.score >= RECOMMEND_MIN_SCORE ? 'primary' : 'secondary'}
          loading={pending}
          aria-disabled={!s.fits || undefined}
          onClick={() => s.fits && onAssign()}
          aria-label={s.fits ? t('bo.plans.assignDriver', { name: s.driverName }) : t('bo.plans.driverDoesNotFit', { name: s.driverName })}
        >
          {s.fits ? t('bo.plans.assign') : t('bo.plans.doesNotFitShort')}
        </Button>
      </div>
    </li>
  )
}

function RouteAssign({ plan, route, index, total }: { plan: Plan; route: Route; index: number; total: number }) {
  const api = useApi()
  const qc = useQueryClient()
  const suggestions = useSuggestions(plan.id, route.id)
  const assign = useMutation({
    mutationFn: (driverId: string) => api.dispatch.assign(plan.id, route.id, driverId),
    onSuccess: (_p, driverId) => {
      const name = suggestions.data?.find((s) => s.driverId === driverId)?.driverName ?? t('bo.plans.driverFallback')
      toast.success(t('bo.plans.assigned', { name }))
      void qc.invalidateQueries({ queryKey: qk.plan(plan.id) })
      // driver yang baru ditugaskan tidak lagi tersedia untuk rute lain pada plan ini
      void qc.invalidateQueries({ queryKey: ['suggest', plan.id] })
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  const sorted = [...(suggestions.data ?? [])].sort((a, b) => Number(b.fits) - Number(a.fits) || b.score - a.score)
  const shown = sorted.slice(0, 4)
  const first = route.stops[0]

  return (
    <section aria-label={t('bo.plans.routeN', { n: index + 1 })} className="space-y-3">
      <div>
        <p className="text-small font-semibold text-muted">
          {total > 1
            ? t('bo.plans.assignRouteMetaN', { n: index + 1, vehicle: VEHICLE_TYPE_LABEL[route.vehicleType], count: route.stops.length, km: route.distanceKm })
            : t('bo.plans.assignRouteMeta', { vehicle: VEHICLE_TYPE_LABEL[route.vehicleType], count: route.stops.length, km: route.distanceKm })}
        </p>
        {first && <p className="text-small text-muted">{t('bo.plans.assignPickup', { place: first.waypoint.name, time: route.startAt })}</p>}
      </div>

      {suggestions.isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-20" />
          <Skeleton className="h-20" />
        </div>
      ) : suggestions.isError ? (
        <ErrorState compact message={errorMessage(suggestions.error)} onRetry={() => void suggestions.refetch()} />
      ) : shown.length === 0 ? (
        <Notice tone="warning" title={t('bo.plans.noDriversTitle', { vehicle: VEHICLE_TYPE_LABEL[route.vehicleType].toLowerCase() })}>
          {t('bo.plans.noDriversHint')}
        </Notice>
      ) : (
        <ul className="divide-y divide-line">
          {shown.map((s, i) => (
            <SuggestionRow key={s.driverId} s={s} best={i === 0} pending={assign.isPending && assign.variables === s.driverId} onAssign={() => assign.mutate(s.driverId)} />
          ))}
        </ul>
      )}
    </section>
  )
}

/** Rute yang belum punya driver beserta saran penugasan. Dipakai di sheet antrean dan di detail plan. */
export function AssignPanel({ plan }: { plan: Plan }) {
  const api = useApi()
  const qc = useQueryClient()
  const open = plan.routes.filter((r) => !r.driverId && r.status === 'menunggu_driver')
  const { data: settings } = useSettings()
  const autoOn = settings?.dispatch.auto ?? true
  const applyBest = useMutation({
    mutationFn: () => api.dispatch.autoAssign(plan.id),
    onSuccess: () => {
      toast.success(t('bo.plans.bestApplied'))
      void qc.invalidateQueries({ queryKey: qk.plan(plan.id) })
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  // plan yang sedang dibatalkan membeku: backend menolak penugasan (dan tidak ada gunanya mengusulkan driver)
  if (plan.pendingAction === 'cancel') {
    return (
      <Notice tone="warning" title={t('bo.plans.cancellingTitle')}>
        {t('bo.plans.pausedAssign')}
      </Notice>
    )
  }
  if (open.length === 0) {
    return <Notice tone="success">{t('bo.plans.allAssigned')}</Notice>
  }

  return (
    <div className="space-y-5">
      {autoOn ? (
        <Notice tone="info" title={t('bo.plans.autoOnTitle')}>
          {plan.schedule.mode === 'terjadwal' ? t('bo.plans.autoOnScheduled') : t('bo.plans.autoOnNow')}
        </Notice>
      ) : (
        <Notice tone="warning" title={t('bo.plans.autoOffTitle')}>
          {t('bo.plans.autoOffHint')}
        </Notice>
      )}
      {open.length > 1 && (
        <Button variant="secondary" className="w-full" iconLeft={<Sparkles className="size-4" aria-hidden />} loading={applyBest.isPending} onClick={() => applyBest.mutate()}>
          {t('bo.plans.applyBest', { count: open.length })}
        </Button>
      )}
      {open.map((route) => (
        <RouteAssign key={route.id} plan={plan} route={route} index={plan.routes.indexOf(route)} total={plan.routes.length} />
      ))}
    </div>
  )
}

/** Panel samping untuk menugaskan dari antrean tanpa meninggalkan konteks tabel. */
export function AssignSheet({ planId, open, onOpenChange }: { planId: string | undefined; open: boolean; onOpenChange: (open: boolean) => void }) {
  const plan = usePlan(open ? planId : undefined)
  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title={t('bo.plans.sheetTitle')}
      description={plan.data ? t('bo.plans.sheetDescription', { code: plan.data.code }) : t('bo.plans.loading')}
    >
      {plan.isLoading ? (
        <Skeleton className="h-40" />
      ) : plan.isError ? (
        <ErrorState compact message={errorMessage(plan.error)} onRetry={() => void plan.refetch()} />
      ) : plan.data ? (
        <AssignPanel plan={plan.data} />
      ) : null}
    </Sheet>
  )
}

