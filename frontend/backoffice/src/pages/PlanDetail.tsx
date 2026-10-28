import { errorMessage, qk, useApi, useDrivers, useMutation, usePlan, useQueryClient, useSettings, useUser, useVehicles, useVirtualNow } from '@loong/data'
import {
  PRESET_LABEL,
  VEHICLE_TYPE_LABEL,
  formatDateTime,
  formatPhone,
  formatRelativeDay,
  formatTime,
  itemLabel,
  liveEtas,
  routePositionAt,
  travelledKm,
  type Driver,
  type PaymentMethod,
  type Plan,
  type Route,
  type Vehicle,
} from '@loong/domain'
import {
  Avatar,
  Button,
  Card,
  CardHeader,
  ConfirmDialog,
  ErrorState,
  FareBreakdown,
  Notice,
  PageHeader,
  PlanStatusLabel,
  RouteMap,
  RouteStatusLabel,
  Segmented,
  Skeleton,
  StopList,
  Textarea,
  toast,
} from '@loong/ui'
import { Copilot } from '@loong/shell'
import { Ban, Phone, UserMinus } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { renderMsg } from '@loong/i18n'
import { AssignPanel, AssignSheet } from '../components/AssignPanel'
import { t, tr } from '../i18n'

// simpan kunci terjemahan (bukan teks) agar label mengikuti bahasa saat dirender
const PAYMENT_LABEL_KEY: Record<PaymentMethod, 'bo.plans.paymentBalance' | 'bo.plans.paymentTransfer' | 'bo.plans.paymentCash'> = {
  saldo: 'bo.plans.paymentBalance',
  transfer: 'bo.plans.paymentTransfer',
  tunai: 'bo.plans.paymentCash',
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2 first:pt-0 last:pb-0">
      <dt className="shrink-0 text-small text-muted">{label}</dt>
      <dd className="text-right text-body">{children}</dd>
    </div>
  )
}

/** Peta rute plan dengan kendaraan bergerak (jam virtual). Terpisah agar ticker tidak merender ulang halaman. */
function PlanMap({ plan, selectedId }: { plan: Plan; selectedId: string }) {
  const now = useVirtualNow(1000)
  const routes = plan.routes.map((route) => {
    const running = route.status === 'berjalan'
    const pos = running ? routePositionAt(route, now) : undefined
    return {
      id: route.id,
      route,
      emphasis: route.id === selectedId ? ('primary' as const) : ('muted' as const),
      showStops: route.id === selectedId,
      travelledKm: running ? travelledKm(route, now) : undefined,
      activeStopIndex: pos?.stopIndex,
      vehicle: pos ? { position: pos.position, bearing: pos.bearing, type: route.vehicleType } : undefined,
    }
  })
  return <RouteMap routes={routes} fitKey={`${plan.id}:${selectedId}`} ariaLabel={t('bo.plans.mapLabel')} />
}

function RouteCard({
  plan,
  route,
  index,
  driver,
  vehicle,
  selected,
  onSelect,
}: {
  plan: Plan
  route: Route
  index: number
  driver?: Driver
  vehicle?: Vehicle
  selected: boolean
  onSelect: () => void
}) {
  const api = useApi()
  const qc = useQueryClient()
  const now = useVirtualNow(5000)
  const [confirmRelease, setConfirmRelease] = useState(false)
  const release = useMutation({
    mutationFn: () => api.dispatch.unassign(plan.id, route.id),
    onSuccess: () => {
      toast.success(t('bo.plans.released'))
      setConfirmRelease(false)
      void qc.invalidateQueries({ queryKey: qk.plan(plan.id) })
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  const running = route.status === 'berjalan'
  const etas = running ? new Map(liveEtas(route, now).map((e) => [e.stopId, e.etaAt])) : undefined
  const pos = running ? routePositionAt(route, now) : undefined
  const delayed = (route.delayMin ?? 0) >= 10 && route.status !== 'selesai'
  const cancelling = plan.pendingAction === 'cancel' // pembatalan menunggu driver: penugasan dan pelepasan dijeda
  const unassigned = !route.driverId && route.status === 'menunggu_driver' && !cancelling

  return (
    <Card className={selected ? 'border-accent/40 p-card' : 'p-card'}>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <button type="button" onClick={onSelect} aria-pressed={selected} className="min-w-0 rounded-input text-left outline-offset-4">
          <h2 className="flex flex-wrap items-center gap-x-3 gap-y-1 text-title">
            {t('bo.plans.routeHeading', { n: index + 1, vehicle: VEHICLE_TYPE_LABEL[route.vehicleType] })}
            <RouteStatusLabel status={route.status} className="text-body font-normal" />
          </h2>
          <p className="mt-0.5 text-small tabular-nums text-muted">
            {t('bo.plans.routeMeta', { count: route.stops.length, km: route.distanceKm, min: route.durationMin, cost: route.costRp })}
          </p>
        </button>

        {driver ? (
          <div className="flex items-center gap-3">
            <Avatar name={driver.name} />
            <div className="text-small">
              <p className="text-body font-semibold">{driver.name}</p>
              <p className="tabular-nums text-muted">
                {vehicle ? `${vehicle.model} · ${vehicle.plate}` : VEHICLE_TYPE_LABEL[route.vehicleType]} · {formatPhone(driver.phone)}
              </p>
            </div>
            <div className="flex gap-1.5">
              <Button size="sm" variant="secondary" iconLeft={<Phone className="size-3.5" aria-hidden />} asChild>
                <a href={`tel:${driver.phone.replace(/[^\d+]/g, '')}`}>{t('bo.plans.call')}</a>
              </Button>
              {route.status === 'ditugaskan' && !cancelling && (
                <Button size="sm" variant="ghost" iconLeft={<UserMinus className="size-3.5" aria-hidden />} onClick={() => setConfirmRelease(true)}>
                  {t('bo.plans.replaceDriver')}
                </Button>
              )}
            </div>
          </div>
        ) : (
          <p className="text-small font-medium text-warning-fg">{t('bo.plans.noDriver')}</p>
        )}
      </div>

      {delayed && (
        <Notice tone="warning" title={t('bo.plans.delayedTitle', { count: route.delayMin ?? 0 })} className="mt-4">
          {t('bo.plans.delayedHint')}
        </Notice>
      )}

      <div className="mt-5">
        <StopList
          stops={route.stops}
          activeIndex={pos?.stopIndex}
          showStatus
          extra={(s) => {
            const eta = etas?.get(s.id)
            return eta && s.status !== 'selesai' ? <span className="mt-0.5 block text-small tabular-nums text-accent">{t('bo.plans.eta', { time: eta })}</span> : null
          }}
        />
      </div>

      {unassigned && (
        <div className="mt-5 border-t border-line pt-5">
          <AssignPanel plan={{ ...plan, routes: [route] }} />
        </div>
      )}

      <ConfirmDialog
        open={confirmRelease}
        onOpenChange={setConfirmRelease}
        title={driver ? t('bo.plans.releaseTitle', { name: driver.name }) : t('bo.plans.releaseTitleAnon')}
        description={t('bo.plans.releaseHint')}
        confirmLabel={t('bo.plans.releaseConfirm')}
        tone="danger"
        loading={release.isPending}
        onConfirm={() => release.mutate()}
      />
    </Card>
  )
}

export function PlanDetail() {
  const { id } = useParams()
  const api = useApi()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const plan = usePlan(id)
  const customer = useUser(plan.data?.userId)
  const drivers = useDrivers()
  const vehicles = useVehicles()
  const { data: settings } = useSettings()
  const autoDispatch = settings?.dispatch.auto ?? true
  const nowMs = useVirtualNow(60_000)
  const [picked, setPicked] = useState<string>()
  const [confirmCancel, setConfirmCancel] = useState(false)
  const [assignOpen, setAssignOpen] = useState(false)
  const [reason, setReason] = useState('')

  const cancel = useMutation({
    mutationFn: () => api.plans.cancel(id!, reason.trim() || undefined),
    onSuccess: (p) => {
      // backend asli: dua tahap, plan kembali dengan `pendingAction` sampai semua driver menjawab
      toast.success(p.pendingAction ? t('bo.plans.toastCancelling') : t('bo.plans.cancelled'))
      setConfirmCancel(false)
      qc.setQueryData(qk.plan(p.id), p)
      void qc.invalidateQueries({ queryKey: ['plans'] })
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  if (plan.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-16 w-80" />
        <Skeleton className="h-80 rounded-card" />
        <Skeleton className="h-64 rounded-card" />
      </div>
    )
  }
  if (plan.isError || !plan.data) {
    return (
      <Card>
        <ErrorState
          title={t('bo.plans.notFoundTitle')}
          message={plan.error ? errorMessage(plan.error) : t('bo.plans.notFoundHint')}
          onRetry={() => void plan.refetch()}
        />
      </Card>
    )
  }

  const p = plan.data
  const selectedId = picked && p.routes.some((r) => r.id === picked) ? picked : (p.routes.find((r) => r.status === 'berjalan') ?? p.routes[0])?.id
  const selectedOption = p.optionSet?.options.find((o) => o.id === p.selectedOptionId)
  const cancelling = p.pendingAction === 'cancel'
  const cancellable = (p.status === 'menunggu_driver' || p.status === 'ditugaskan' || p.status === 'berjalan') && !cancelling
  const needsDriver = !cancelling && p.routes.some((r) => !r.driverId && r.status === 'menunggu_driver')

  return (
    <div className="space-y-4">
      <PageHeader
        back={{ label: t('bo.plans.backAll'), onClick: () => navigate('/plan') }}
        title={
          <span className="flex flex-wrap items-center gap-x-4 gap-y-1">
            <span className="tabular-nums">{p.code}</span>
            <PlanStatusLabel status={p.status} pendingAction={p.pendingAction} className="text-body font-normal" />
          </span>
        }
        description={
          customer.data
            ? t('bo.plans.createdBy', { when: formatRelativeDay(p.createdAt, new Date(nowMs)), name: customer.data.user.name })
            : t('bo.plans.created', { when: formatRelativeDay(p.createdAt, new Date(nowMs)) })
        }
        actions={
          cancellable ? (
            <div className="flex flex-wrap gap-2">
              {needsDriver && (
                <Button variant={autoDispatch ? 'secondary' : 'primary'} onClick={() => setAssignOpen(true)}>
                  {autoDispatch ? t('bo.plans.assignManual') : t('bo.plans.assignDriverAction')}
                </Button>
              )}
              <Button variant="secondary" iconLeft={<Ban className="size-4" aria-hidden />} onClick={() => setConfirmCancel(true)}>
                {t('bo.plans.cancelPlan')}
              </Button>
            </div>
          ) : undefined
        }
      />

      {cancelling && (
        <Notice tone="warning" title={t('bo.plans.cancellingTitle')}>
          {t('bo.plans.cancellingHint')}
        </Notice>
      )}
      {p.status === 'dibatalkan' && (
        <Notice tone="danger" title={t('bo.plans.cancelledTitle')}>
          {p.cancelReason ?? t('bo.plans.noReason')}
        </Notice>
      )}
      {(p.status === 'draft' || p.status === 'opsi_siap') && (
        <Notice tone="info" title={t('bo.plans.draftTitle')}>
          {t('bo.plans.draftHint')}
        </Notice>
      )}

      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0 space-y-4">
          {p.routes.length > 0 && selectedId && (
            <Card className="overflow-hidden">
              <div className="flex flex-wrap items-center justify-between gap-3 border-b border-line px-4 py-2.5">
                <div>
                  <h2 className="text-title">{t('bo.plans.mapTitle')}</h2>
                  <p className="text-small text-muted">{t('bo.plans.mapHint')}</p>
                </div>
                {p.routes.length > 1 && (
                  <Segmented
                    aria-label={t('bo.plans.mapPick')}
                    value={selectedId}
                    onValueChange={setPicked}
                    options={p.routes.map((r, i) => ({ value: r.id, label: t('bo.plans.routeN', { n: i + 1 }) }))}
                  />
                )}
              </div>
              <div className="h-[22rem]">
                <PlanMap plan={p} selectedId={selectedId} />
              </div>
            </Card>
          )}

          {p.routes.map((route, i) => (
            <RouteCard
              key={route.id}
              plan={p}
              route={route}
              index={i}
              driver={drivers.data?.find((d) => d.id === route.driverId)}
              vehicle={vehicles.data?.find((v) => v.id === route.vehicleId)}
              selected={route.id === selectedId}
              onSelect={() => setPicked(route.id)}
            />
          ))}

          {p.routes.length === 0 && (
            <Card className="p-card">
              <CardHeader title={t('bo.plans.shipments')} />
              <ul className="mt-3 divide-y divide-line">
                {p.shipments.map((s, i) => (
                  <li key={s.id} className="py-2.5 first:pt-0 last:pb-0">
                    <p className="text-body font-semibold">
                      {t('bo.plans.shipmentLine', { n: i + 1, from: s.pickup?.name ?? '—', to: s.dropoff?.name ?? '—' })}
                    </p>
                    <p className="text-small text-muted">{s.items.length ? s.items.map((it) => `${it.qty} × ${itemLabel(it)}`).join(', ') : t('bo.plans.noItems')}</p>
                  </li>
                ))}
              </ul>
            </Card>
          )}
        </div>

        <aside className="min-w-0 space-y-4">
          <Card className="p-card">
            <CardHeader title={t('bo.plans.customer')} />
            {customer.data ? (
              <div className="mt-3 flex items-center gap-3">
                <Avatar name={customer.data.user.name} size="lg" />
                <div className="min-w-0 text-small">
                  <Link to={`/user/${customer.data.user.id}`} className="block truncate text-body font-semibold text-accent underline-offset-4 hover:underline">
                    {customer.data.user.name}
                  </Link>
                  <p className="truncate text-muted">{customer.data.user.email}</p>
                  <p className="tabular-nums text-muted">{formatPhone(customer.data.user.phone)}</p>
                </div>
              </div>
            ) : (
              <Skeleton className="mt-3 h-12" />
            )}
            <Button variant="link" className="mt-3 text-small" asChild>
              <Link to={`/plan?user=${p.userId}`}>{t('bo.plans.customerPlans')}</Link>
            </Button>
          </Card>

          {settings?.ai.copilot && p.routes.length > 0 && (
            <Copilot
              scope={{ kind: 'plan', planId: p.id }}
              starters={[t('bo.plans.starterTodo'), t('bo.plans.starterBest'), t('bo.plans.starterLate')]}
              subtitle={t('bo.plans.copilotSubtitle')}
              placeholder={t('bo.plans.copilotPlaceholder')}
              listClassName="max-h-[26rem]"
              onAction={async (a) => {
                if (a.kind === 'tugaskan_terbaik') {
                  if (cancelling) return void toast.error(t('bo.plans.pausedAssign'))
                  await api.dispatch.autoAssign(p.id)
                  toast.success(t('bo.plans.bestAppliedOne'))
                  void qc.invalidateQueries({ queryKey: qk.plan(p.id) })
                } else if (a.kind === 'buka_plan' && a.planId) {
                  navigate(`/plan/${a.planId}`)
                }
              }}
            />
          )}

          <Card className="p-card">
            <CardHeader title={t('bo.plans.order')} />
            <dl className="mt-3 divide-y divide-line">
              <Row label={t('bo.plans.rowSchedule')}>
                {p.schedule.mode === 'sekarang' ? t('bo.plans.asap') : p.schedule.window ? formatRelativeDay(p.schedule.window.from, new Date(nowMs)) : '—'}
              </Row>
              <Row label={t('bo.plans.rowPriority')}>{PRESET_LABEL[p.priority.preset]}</Row>
              {p.shipments.some((s) => s.deadline) && (
                <Row label={t('bo.plans.rowDeadline')}>
                  {p.shipments
                    .filter((s) => s.deadline)
                    .map((s) => (
                      <span key={s.id} className="block">
                        {s.dropoff.name} <span className="text-muted">{t('bo.plans.deadlineBefore', { time: s.deadline! })}</span>
                      </span>
                    ))}
                </Row>
              )}
              {p.priority.text && <Row label={t('bo.plans.rowNotes')}>“{p.priority.text}”</Row>}
              {p.paymentMethod && <Row label={t('bo.plans.rowPayment')}>{t(PAYMENT_LABEL_KEY[p.paymentMethod])}</Row>}
              {p.confirmedAt && <Row label={t('bo.plans.rowConfirmed')}>{formatDateTime(p.confirmedAt)}</Row>}
              {p.finishedAt && <Row label={t('bo.plans.rowFinished')}>{formatDateTime(p.finishedAt)}</Row>}
            </dl>
          </Card>

          {p.routes.length > 0 && (
            <Card className="p-card">
              <CardHeader title={t('bo.plans.cost')} description={t('bo.plans.costHint')} />
              <FareBreakdown routes={p.routes} className="mt-4" />
              {selectedOption && selectedOption.savings.costRp > 0 && (
                <p className="mt-3 text-small text-muted">
                  {t('bo.plans.savings', { amount: selectedOption.savings.costRp, pct: selectedOption.savings.pct })}
                </p>
              )}
            </Card>
          )}

          <Card className="p-card">
            <CardHeader title={t('bo.plans.events')} />
            <ol className="mt-4 space-y-3">
              {[...p.events].reverse().map((e) => (
                <li key={e.id} className="grid grid-cols-[3rem_1fr] gap-3 text-small">
                  <span className="tabular-nums text-muted">{formatTime(e.at)}</span>
                  <span>{renderMsg(e.text)}</span>
                </li>
              ))}
            </ol>
          </Card>
        </aside>
      </div>

      <AssignSheet planId={p.id} open={assignOpen} onOpenChange={setAssignOpen} />

      <ConfirmDialog
        open={confirmCancel}
        onOpenChange={setConfirmCancel}
        title={t('bo.plans.cancelTitle', { code: p.code })}
        description={t('bo.plans.cancelHint')}
        confirmLabel={t('bo.plans.cancelPlan')}
        cancelLabel={t('bo.plans.cancelKeep')}
        tone="danger"
        loading={cancel.isPending}
        onConfirm={() => cancel.mutate()}
      >
        <label className="mt-4 block text-small font-semibold" htmlFor="alasan-batal">
          {tr('bo.plans.cancelReasonLabel', { hint: (c) => <span className="font-normal text-subtle">{c}</span> })}
        </label>
        <Textarea id="alasan-batal" className="mt-1.5" value={reason} onChange={(e) => setReason(e.target.value)} placeholder={t('bo.plans.cancelReasonPlaceholder')} />
      </ConfirmDialog>
    </div>
  )
}
