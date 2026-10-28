import {
  errorMessage,
  qk,
  useApi,
  useDriverHistory,
  useDriverRoutes,
  useMutation,
  useQueryClient,
  useVirtualNow,
} from '@loong/data'
import {
  VEHICLE_TYPE_LABEL,
  driverEarningRp,
  formatDuration,
  formatKg,
  formatKm,
  formatRupiah,
  routePositionAt,
  travelledKm,
  type DriverRoute,
} from '@loong/domain'
import { Button, Card, EmptyState, ErrorState, IconButton, RouteMap, Segmented, Skeleton, StopList, toast } from '@loong/ui'
import { ArrowLeft, Compass, Play } from 'lucide-react'
import { Navigate, useNavigate, useParams } from 'react-router'
import { NextStopCard } from '../components/NextStopCard'
import { SwipeToConfirm } from '../components/SwipeToConfirm'
import { t } from '../i18n'
import { useWakeLock } from '../lib/device'
import { routeDonePath, stopPath } from '../lib/paths'
import { useDriverPrefs } from '../lib/prefs'

/** Peta rute dengan kendaraan bergerak. Terpisah agar ticker waktu tidak merender ulang seluruh halaman. */
function RouteMapPanel({ item }: { item: DriverRoute }) {
  const { route } = item
  const now = useVirtualNow(1000)
  const pos = routePositionAt(route, now)
  const running = route.status === 'berjalan'
  return (
    <div className="h-[56dvh] min-h-72 overflow-hidden rounded-card border border-line">
      <RouteMap
        cooperativeGestures={false}
        ariaLabel={t('drv.route.mapLabel')}
        routes={[
          {
            id: route.id,
            route,
            emphasis: 'primary',
            travelledKm: running ? travelledKm(route, now) : undefined,
            activeStopIndex: pos.stopIndex,
            vehicle: running ? { position: pos.position, bearing: pos.bearing, type: route.vehicleType } : undefined,
          },
        ]}
        fitKey={route.id}
      />
    </div>
  )
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div>
      <p className="text-small text-muted">{label}</p>
      <p className="text-body font-semibold tabular-nums">{value}</p>
    </div>
  )
}

export function RoutePage() {
  const { routeId } = useParams()
  const api = useApi()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const routes = useDriverRoutes()
  const history = useDriverHistory()
  const [prefs, setPrefs] = useDriverPrefs()
  const item = routes.data?.find((r) => r.route.id === routeId)
  const running = item?.route.status === 'berjalan'
  useWakeLock(!!running && prefs.keepAwake)

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: qk.driverRoutes })
    void qc.invalidateQueries({ queryKey: qk.driverMe })
  }

  const start = useMutation({
    mutationFn: () => api.driver.startRoute(item!.planId, item!.route.id),
    onSuccess: () => {
      refresh()
      toast.success(t('drv.route.toastStarted'))
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  const arrive = useMutation({
    mutationFn: (stopId: string) => api.driver.arrive(item!.planId, item!.route.id, stopId),
    onSuccess: (_plan, stopId) => {
      refresh()
      toast.success(t('drv.app.toastArrived'))
      // lanjut ke detail stop: di sana bukti diambil/diantar dan stop diselesaikan
      navigate(stopPath(item!.route.id, stopId))
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  if (routes.isLoading) {
    return (
      <div className="space-y-4 p-4">
        <Skeleton className="h-10 w-48" />
        <Skeleton className="h-40 rounded-card" />
        <Skeleton className="h-64 rounded-card" />
      </div>
    )
  }
  if (routes.isError) {
    return (
      <div className="p-4">
        <Card>
          <ErrorState message={errorMessage(routes.error)} onRetry={() => void routes.refetch()} />
        </Card>
      </div>
    )
  }
  if (!item) {
    // rute yang baru selesai tidak ada lagi di daftar tugas: arahkan ke ringkasannya
    if (history.data?.some((h) => h.routeId === routeId)) return <Navigate to={routeDonePath(routeId!)} replace />
    return (
      <main className="p-4">
        <EmptyState
          icon={<Compass />}
          title={t('drv.route.notFoundTitle')}
          description={t('drv.route.notFoundHint')}
          action={
            <Button variant="primary" size="lg" onClick={() => navigate('/')}>
              {t('drv.app.backHome')}
            </Button>
          }
        />
      </main>
    )
  }

  const { route } = item
  const activeIndex = route.stops.findIndex((s) => s.status !== 'selesai' && s.status !== 'bermasalah')
  const active = route.stops[activeIndex]
  const done = route.stops.filter((s) => s.status === 'selesai' || s.status === 'bermasalah').length

  return (
    <div className="min-h-dvh pb-44">
      <header className="sticky top-0 z-30 border-b border-line bg-bg/95 backdrop-blur-md">
        <div className="flex h-16 items-center gap-2 px-2">
          <IconButton label={t('drv.app.backHome')} icon={<ArrowLeft className="size-5" />} onClick={() => navigate('/')} />
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-title">{item.planCode}</h1>
            <p className="truncate text-small text-muted">
              {running
                ? t('drv.route.headerRunning', { done, count: route.stops.length, vehicle: VEHICLE_TYPE_LABEL[route.vehicleType], km: route.distanceKm })
                : t('drv.route.headerIdle', { count: route.stops.length, vehicle: VEHICLE_TYPE_LABEL[route.vehicleType], km: route.distanceKm })}
            </p>
          </div>
          <Segmented
            aria-label={t('drv.route.view')}
            value={prefs.routeView}
            onValueChange={(v) => setPrefs({ routeView: v })}
            options={[
              { value: 'daftar', label: t('drv.route.viewList') },
              { value: 'peta', label: t('drv.route.viewMap') },
            ]}
          />
        </div>
      </header>

      <main className="space-y-4 px-4 pt-4">
        {running && <NextStopCard route={route} className="sticky top-[4.5rem] z-20" />}

        {!running && (
          <Card className="space-y-4 p-card">
            <div className="flex items-end justify-between gap-3">
              <div>
                <p className="text-small text-muted">{t('drv.route.estimatedEarnings')}</p>
                <p className="text-metric-sm tabular-nums">{formatRupiah(driverEarningRp(route.costRp))}</p>
              </div>
              <p className="text-small text-muted">{t('drv.route.departs', { time: route.startAt })}</p>
            </div>
            <div className="grid grid-cols-3 gap-3 border-t border-line pt-3">
              <Fact label={t('drv.route.factDistance')} value={formatKm(route.distanceKm)} />
              <Fact label={t('drv.route.factDuration')} value={formatDuration(route.durationMin)} />
              <Fact label={t('drv.route.factWeight')} value={formatKg(route.totalWeightKg)} />
            </div>
          </Card>
        )}

        {prefs.routeView === 'peta' ? (
          <RouteMapPanel item={item} />
        ) : (
          <Card className="p-card">
            <h2 className="mb-4 text-title">{t('drv.route.stopOrder')}</h2>
            <StopList
              stops={route.stops}
              activeIndex={running ? activeIndex : undefined}
              showStatus
              onSelect={running ? (s) => navigate(stopPath(route.id, s.id)) : undefined}
            />
          </Card>
        )}
      </main>

      <div className="fixed inset-x-0 bottom-0 z-40 mx-auto max-w-[28rem] border-t border-line bg-surface/95 p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] backdrop-blur-md">
        {!running ? (
          <Button variant="primary" size="lg" className="w-full" loading={start.isPending} iconLeft={<Play className="size-4" aria-hidden />} onClick={() => start.mutate()}>
            {route.stops[0] ? t('drv.route.startTo', { name: route.stops[0].waypoint.name }) : t('drv.route.start')}
          </Button>
        ) : active?.status === 'menuju' ? (
          <SwipeToConfirm key={active.id} label={t('drv.route.swipeArrive')} onConfirm={() => arrive.mutateAsync(active.id)} />
        ) : active ? (
          <Button variant="primary" size="lg" className="w-full" onClick={() => navigate(stopPath(route.id, active.id))}>
            {active.kind === 'ambil' ? t('drv.route.completePickup') : t('drv.route.completeDropoff')}
          </Button>
        ) : null}
      </div>
    </div>
  )
}
