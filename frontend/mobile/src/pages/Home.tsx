import { NotificationBell } from '@loong/shell'
import {
  errorMessage,
  qk,
  useApi,
  useDriverEarnings,
  useDriverMe,
  useDriverRoutes,
  useMutation,
  useQueryClient,
  useRoutePosition,
  useSession,
  useVirtualNow,
} from '@loong/data'
import {
  VEHICLE_TYPE_LABEL,
  driverEarningRp,
  formatRelativeDay,
  formatRupiah,
  type DriverRoute,
} from '@loong/domain'
import { Avatar, Button, Card, EmptyState, ErrorState, Skeleton, StatusDot, Switch, toast } from '@loong/ui'
import { ArrowRight, MapPinned, Moon, Package, Radio } from 'lucide-react'
import { Link, useNavigate } from 'react-router'
import { t, tr } from '../i18n'
import { routePath } from '../lib/paths'

const doneCount = (r: DriverRoute) => r.route.stops.filter((s) => s.status === 'selesai' || s.status === 'bermasalah').length

/** Kartu rute yang sedang berjalan: satu-satunya hal yang harus dilihat driver saat membuka aplikasi. */
function ActiveRouteCard({ item }: { item: DriverRoute }) {
  const { route } = item
  const pos = useRoutePosition(route, 1000)
  const next = route.stops[pos?.stopIndex ?? 0] ?? route.stops.find((s) => s.status !== 'selesai' && s.status !== 'bermasalah')
  const done = doneCount(item)
  const arrived = next?.status === 'tiba'
  return (
    <Card className="overflow-hidden border-accent/30 p-0 shadow-card">
      <div className="space-y-3 p-card">
        <p className="flex items-center gap-2 text-small font-semibold text-accent">
          <StatusDot tone="info" pulse /> {t('drv.home.activeRoute', { code: item.planCode })}
        </p>
        {next && (
          <div>
            <p className="text-body text-muted">{next.kind === 'ambil' ? t('drv.app.nextPickup') : t('drv.app.nextDropoff')}</p>
            <p className="text-title">{next.waypoint.name}</p>
            <p className="text-body text-muted">{next.waypoint.address}</p>
          </div>
        )}
        <div className="flex items-center justify-between text-body text-muted">
          <span className="tabular-nums">{t('drv.app.stopsDone', { done, count: route.stops.length })}</span>
          <span className="font-semibold tabular-nums text-fg">{arrived ? t('drv.app.arrived') : pos ? t('drv.app.minutesLeft', { minutes: Math.max(1, Math.ceil(pos.remainingLegMin)) }) : ''}</span>
        </div>
        <div className="h-1.5 overflow-hidden rounded-full bg-sunken" role="progressbar" aria-valuemin={0} aria-valuemax={route.stops.length} aria-valuenow={done} aria-label={t('drv.home.progressLabel')}>
          <div className="h-full rounded-full bg-accent transition-[width] duration-[360ms] ease-out" style={{ width: `${(done / route.stops.length) * 100}%` }} />
        </div>
      </div>
      <Link
        to={routePath(route.id)}
        className="flex h-14 items-center justify-between border-t border-accent/20 bg-accent px-card text-body font-semibold text-on-accent transition-colors hover:bg-accent-hover"
      >
        {t('drv.home.continueRoute')}
        <ArrowRight className="size-5" aria-hidden />
      </Link>
    </Card>
  )
}

/** Tugas baru yang sudah ditugaskan tetapi belum dimulai. */
function AssignedCard({ item, now }: { item: DriverRoute; now: number }) {
  const { route } = item
  const first = route.stops[0]
  const startsLater = new Date(route.startAt).getTime() > now + 20 * 60_000
  return (
    <Card className="space-y-3 p-card">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="text-small font-semibold text-muted">
            {t('drv.home.newJob', { code: item.planCode, vehicle: VEHICLE_TYPE_LABEL[route.vehicleType] })}
          </p>
          <p className="mt-0.5 text-title">{t('drv.home.jobSummary', { count: route.stops.length, km: route.distanceKm })}</p>
        </div>
        <p className="shrink-0 text-right">
          <span className="block text-title tabular-nums">{formatRupiah(driverEarningRp(route.costRp))}</span>
          <span className="block text-caption text-muted">{t('drv.home.estimate')}</span>
        </p>
      </div>
      {first && (
        <p className="flex items-start gap-2 text-body">
          <Package className="mt-0.5 size-4 shrink-0 text-subtle" aria-hidden />
          <span className="min-w-0">
            {tr('drv.home.pickupAt', { name: first.waypoint.name, strong: (c) => <span className="font-semibold">{c}</span> })}
            <span className="block text-small text-muted">
              {startsLater ? t('drv.home.scheduled', { when: formatRelativeDay(route.startAt, new Date(now)) }) : t('drv.home.leavingNow', { time: first.arriveAt })}
            </span>
          </span>
        </p>
      )}
      <Button variant="secondary" size="lg" className="w-full" asChild>
        <Link to={routePath(route.id)}>{t('drv.home.viewJob')}</Link>
      </Button>
    </Card>
  )
}

export function Home() {
  const api = useApi()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const now = useVirtualNow(30_000)
  const session = useSession()
  const me = useDriverMe()
  const routes = useDriverRoutes()
  const earnings = useDriverEarnings()

  const setOnline = useMutation({
    mutationFn: (online: boolean) => api.driver.setOnline(online),
    onSuccess: (d) => {
      void qc.invalidateQueries({ queryKey: qk.driverMe })
      toast.success(d.status === 'online' ? t('drv.home.toastOnline') : t('drv.home.toastOffline'))
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  const driver = me.data?.driver
  const vehicle = me.data?.vehicle
  const working = driver?.status === 'bertugas'
  const online = driver?.status === 'online' || working
  const active = routes.data?.filter((r) => r.route.status === 'berjalan') ?? []
  const assigned = routes.data?.filter((r) => r.route.status === 'ditugaskan') ?? []

  function toggle(next: boolean) {
    if (working) return void toast.info(t('drv.home.toastFinishFirst'))
    setOnline.mutate(next)
  }

  // nama dari sesi sudah ada di perangkat, jadi sapaan tidak menunggu (atau bergantung pada) jaringan
  const displayName = driver?.name ?? session?.name
  const firstName = displayName?.split(' ')[0]

  const hasTasks = active.length + assigned.length > 0

  const switchCard = (
    <Card className="p-card">
        {me.isLoading ? (
          <Skeleton className="h-12" />
        ) : me.isError ? (
          // status tidak diketahui: jangan tampilkan saklar yang bisa salah ("Offline" padahal sedang bertugas)
          <ErrorState compact title={t('drv.home.statusError')} message={errorMessage(me.error)} onRetry={() => void me.refetch()} />
        ) : (
          <Switch
            size="lg"
            checked={online}
            onCheckedChange={toggle}
            label={working ? t('drv.home.statusWorking') : online ? t('drv.home.statusOnline') : t('drv.home.statusOffline')}
            description={working ? t('drv.home.statusWorkingHint') : online ? t('drv.home.statusOnlineHint') : t('drv.home.statusOfflineHint')}
          />
        )}
    </Card>
  )

  const taskBlock = (
    <>
      {routes.isError ? (
        <Card>
          <ErrorState message={errorMessage(routes.error)} onRetry={() => void routes.refetch()} compact />
        </Card>
      ) : routes.isLoading ? (
        <Skeleton className="h-52 rounded-card" />
      ) : active.length + assigned.length === 0 ? (
        <Card>
          {online ? (
            <EmptyState
              compact
              icon={<Radio />}
              title={t('drv.home.waitingTitle')}
              description={t('drv.home.waitingHint')}
            />
          ) : (
            <EmptyState
              compact
              icon={<Moon />}
              title={t('drv.home.offlineTitle')}
              description={t('drv.home.offlineHint')}
            />
          )}
        </Card>
      ) : (
        <section aria-label={t('drv.home.jobs')} className="space-y-3">
          {active.map((item) => (
            <ActiveRouteCard key={item.route.id} item={item} />
          ))}
          {assigned.map((item) => (
            <AssignedCard key={item.route.id} item={item} now={now} />
          ))}
        </section>
      )}
    </>
  )

  return (
    <main className="space-y-5 px-4 pb-6 pt-5">
      <header className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          {displayName ? <Avatar name={displayName} size="lg" /> : <Skeleton className="size-12 rounded-full" />}
          <div className="min-w-0">
            <h1 className="truncate text-title">{firstName ? t('drv.home.greeting', { name: firstName }) : <Skeleton className="h-6 w-28" />}</h1>
            <p className="truncate text-small text-muted">{vehicle ? `${vehicle.model} · ${vehicle.plate}` : ' '}</p>
          </div>
        </div>
        <NotificationBell
          onOpenPlan={(planId) => {
            const hit = routes.data?.find((r) => r.planId === planId)
            navigate(hit ? routePath(hit.route.id) : '/')
          }}
        />
      </header>

      {/* saat ada tugas, tugas yang utama dan saklar status menyusul; tanpa tugas, saklar adalah aksi utamanya */}
      {hasTasks ? (
        <>
          {taskBlock}
          {switchCard}
        </>
      ) : (
        <>
          {switchCard}
          {taskBlock}
        </>
      )}

      <Link to="/earnings" className="flex items-center justify-between rounded-card border border-line bg-surface px-card py-3.5 transition-colors hover:bg-sunken">
        <span className="flex items-center gap-3">
          <MapPinned className="size-5 text-subtle" aria-hidden />
          <span>
            <span className="block text-small text-muted">{t('drv.home.today')}</span>
            <span className="block text-body font-semibold tabular-nums">
              {earnings.data ? t('drv.home.todaySummary', { amount: earnings.data.todayRp, count: earnings.data.tripsToday }) : earnings.isError ? t('drv.home.todayError') : <Skeleton className="mt-1 h-5 w-40" />}
            </span>
          </span>
        </span>
        <ArrowRight className="size-4 text-subtle" aria-hidden />
      </Link>
    </main>
  )
}
