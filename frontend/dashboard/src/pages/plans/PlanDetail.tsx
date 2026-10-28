import { REAL_BACKEND, errorMessage, qk, useApi, useDrivers, useMutation, usePlan, useQueryClient, useSettings, useVehicles, useVirtualNow } from '@loong/data'
import {
  ApiError,
  VEHICLE_TYPE_LABEL,
  aggregateItems,
  formatDate,
  formatDecimal,
  formatKg,
  formatPhone,
  formatRelativeDay,
  formatTime,
  itemLabel,
  liveEtas,
  planStatusText,
  routePositionAt,
  travelledKm,
  type PaymentMethod,
  type Plan,
  type Schedule,
} from '@loong/domain'
import { renderMsg } from '@loong/i18n'
import {
  Avatar,
  BRAND,
  Button,
  Card,
  CardHeader,
  Chip,
  ConfirmDialog,
  Dialog,
  EmptyState,
  ErrorState,
  FareBreakdown,
  Field,
  Notice,
  PageHeader,
  PlanStatusLabel,
  RouteMap,
  Select,
  Skeleton,
  Spinner,
  StopList,
  toast,
} from '@loong/ui'
import { Copilot } from '@loong/shell'
import { Check, Phone, Printer, Star } from 'lucide-react'
import { useMemo, useState, type ReactNode } from 'react'
import { Navigate, useNavigate, useParams } from 'react-router'
import { StopProof } from '../../components/ProofOfDelivery'
import { ScheduleFields } from '../../components/ScheduleFields'
import { t, tr } from '../../i18n'
import { scheduleError } from '../../lib/schedule'

// Alasan pembatalan disimpan sebagai kunci; teksnya (dalam bahasa saat ini) dikirim ke server saat membatalkan.
const REASONS = ['changedMind', 'wrongLocation', 'otherService', 'other'] as const
type Reason = (typeof REASONS)[number]
const reasonLabel = (r: Reason) =>
  r === 'changedMind'
    ? t('dash.plans.reasonChangedMind')
    : r === 'wrongLocation'
      ? t('dash.plans.reasonWrongLocation')
      : r === 'otherService'
        ? t('dash.plans.reasonOtherService')
        : t('dash.plans.reasonOther')

const paidVia = (m: PaymentMethod) => (m === 'saldo' ? t('dash.plans.paidViaBalance') : m === 'transfer' ? t('dash.plans.paidViaTransfer') : t('dash.plans.paidViaCash'))

const bold = (c: ReactNode) => <strong>{c}</strong>
const stepOf = (p: Plan) => ({ menunggu_driver: 0, ditugaskan: 1, berjalan: 2, selesai: 3 })[p.status as 'menunggu_driver'] ?? -1

function Timeline({ plan }: { plan: Plan }) {
  const done = stepOf(plan)
  const steps = [t('dash.plans.stepConfirmed'), t('dash.plans.stepAssigned'), t('dash.plans.stepOnTheWay'), t('dash.plans.stepDone')]
  return (
    <ol className="grid grid-cols-4 gap-1" aria-label={t('dash.plans.timelineTitle')}>
      {steps.map((label, i) => {
        const reached = i <= done
        return (
          <li key={label} className="relative flex flex-col items-center gap-1.5 text-center" aria-current={i === done ? 'step' : undefined}>
            {i > 0 && <span aria-hidden className={`absolute right-1/2 top-[11px] h-0.5 w-full ${i <= done ? 'bg-accent' : 'bg-line-strong'}`} />}
            <span
              aria-hidden
              className={`relative z-10 grid size-6 place-items-center rounded-full border-2 transition-colors ${reached ? 'border-accent bg-accent text-on-accent' : 'border-line-strong bg-surface'}`}
            >
              {reached && <Check className="size-3.5" />}
            </span>
            <span className={`text-caption leading-tight ${reached ? 'font-semibold text-fg' : 'text-subtle'}`}>{label}</span>
          </li>
        )
      })}
    </ol>
  )
}

export function PlanDetail() {
  const { id } = useParams()
  const api = useApi()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const now = useVirtualNow(500)
  const { data: plan, isLoading, isError, error, refetch, isRefetching } = usePlan(id)
  const { data: drivers } = useDrivers({ enabled: !REAL_BACKEND })
  // armada dan pengemudinya bukan untuk pelanggan di backend asli (plat dan pengemudi pesanannya sendiri ada di riwayat pesanan)
  const { data: vehicles } = useVehicles({ enabled: !REAL_BACKEND })
  const { data: settings } = useSettings()
  const [cancelOpen, setCancelOpen] = useState(false)
  const [reason, setReason] = useState<Reason>(REASONS[0])
  const [rescheduleOpen, setRescheduleOpen] = useState(false)
  const [schedule, setSchedule] = useState<Schedule>({ mode: 'sekarang' })

  // Hasil mutasi langsung masuk ke cache (peristiwa `plans.changed` menyusul): pembatalan backend asli dua tahap, jadi plan
  // yang dikembalikan masih berstatus lama tetapi `pendingAction: 'cancel'`, dan layar langsung menampilkan "Membatalkan…".
  const settle = (p: Plan) => {
    qc.setQueryData(qk.plan(p.id), p)
    void qc.invalidateQueries({ queryKey: ['plans'] })
  }
  const cancel = useMutation({
    mutationFn: () => api.plans.cancel(id!, reasonLabel(reason)),
    onSuccess: (p) => {
      setCancelOpen(false)
      settle(p)
      toast.success(p.pendingAction ? t('dash.plans.toastCancelling') : t('dash.plans.toastCancelled'))
    },
    onError: (err) => toast.error(errorMessage(err)),
  })
  const reschedule = useMutation({
    mutationFn: () => api.plans.reschedule(id!, schedule),
    onSuccess: (p) => {
      setRescheduleOpen(false)
      settle(p)
      toast.success(t('dash.plans.toastRescheduled'))
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  const live = useMemo(() => {
    if (!plan) return []
    return plan.routes.map((r) => {
      const pos = routePositionAt(r, now)
      return {
        route: r,
        pos,
        travelled: r.status === 'berjalan' ? travelledKm(r, now) : r.status === 'selesai' ? Infinity : 0,
        etas: r.status === 'berjalan' ? liveEtas(r, now) : [],
      }
    })
  }, [plan, now])

  if (isLoading) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-10 w-64" />
        <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_22rem]">
          <Skeleton className="h-[26rem] rounded-card" />
          <Skeleton className="h-[26rem] rounded-card" />
        </div>
      </div>
    )
  }
  if (isError || !plan) {
    const missing = error instanceof ApiError && (error.code === 'tidak_ditemukan' || error.code === 'ditolak')
    return (
      <Card>
        {missing ? (
          <EmptyState icon={<Check />} title={t('dash.plans.notFoundTitle')} description={t('dash.plans.notFoundHint')} action={<Button onClick={() => navigate('/plan')}>{t('dash.plans.viewAll')}</Button>} />
        ) : (
          <ErrorState message={errorMessage(error)} onRetry={() => void refetch()} retrying={isRefetching} offline={error instanceof ApiError && error.code === 'jaringan'} />
        )}
      </Card>
    )
  }
  if (plan.status === 'draft' || plan.status === 'opsi_siap') {
    return <Navigate to={`/plan/new?draft=${plan.id}&step=${Math.max(1, Math.min(plan.draftStep, 4))}`} replace />
  }

  // selama pembatalan menunggu jawaban driver, tidak ada tindakan lain yang boleh ditawarkan
  const cancelling = plan.pendingAction === 'cancel'
  const cancellable = (plan.status === 'menunggu_driver' || plan.status === 'ditugaskan') && !cancelling
  const primaryRoute = live.find((l) => l.route.status === 'berjalan') ?? live.find((l) => l.route.status === 'ditugaskan') ?? live[0]
  const nextStop = primaryRoute?.route.stops[primaryRoute.pos.stopIndex]
  const lastEta = live.flatMap((l) => (l.etas.length ? [l.etas.at(-1)!.etaAt] : l.route.stops.at(-1) ? [l.route.stops.at(-1)!.arriveAt] : [])).sort().at(-1)
  const assigned = plan.routes.flatMap((r) => {
    const d = drivers?.find((x) => x.id === r.driverId)
    const v = vehicles?.find((x) => x.id === r.vehicleId)
    return d ? [{ driver: d, vehicle: v, route: r }] : []
  })
  const savings = plan.optionSet?.options.find((o) => o.id === plan.selectedOptionId)?.savings
  const events = [...plan.events].reverse()

  const banner = cancelling ? (
    <span className="inline-flex items-center gap-2">
      <Spinner /> {t('dash.plans.bannerCancelling')}
    </span>
  ) : plan.status === 'menunggu_driver' ? (
      <span className="inline-flex items-center gap-2">
        <Spinner /> {plan.schedule.mode === 'terjadwal' ? t('dash.plans.bannerWaitingSchedule') : t('dash.plans.bannerFinding')}
      </span>
    ) : plan.status === 'ditugaskan' ? (
      <>
        {assigned[0]
          ? t('dash.plans.bannerAssigned', { driver: assigned[0].driver.name, time: primaryRoute ? formatTime(primaryRoute.route.startAt) : '—' })
          : t('dash.plans.bannerAssignedAnon', { time: primaryRoute ? formatTime(primaryRoute.route.startAt) : '—' })}
      </>
    ) : plan.status === 'berjalan' && nextStop ? (
      <>
        {tr(
          nextStop.kind === 'ambil'
            ? lastEta
              ? 'dash.plans.bannerPickupEta'
              : 'dash.plans.bannerPickup'
            : lastEta
              ? 'dash.plans.bannerDropoffEta'
              : 'dash.plans.bannerDropoff',
          { name: nextStop.waypoint.name, time: lastEta ? formatTime(lastEta) : '', b: bold },
        )}
      </>
    ) : plan.status === 'selesai' ? (
      <>{plan.finishedAt ? t('dash.plans.bannerDoneAt', { when: formatRelativeDay(plan.finishedAt, new Date(now)) }) : t('dash.plans.bannerDone')}</>
    ) : (
      <>{plan.cancelReason ? t('dash.plans.bannerCancelledReason', { reason: plan.cancelReason }) : t('dash.plans.bannerCancelled')}</>
    )

  return (
    <div className="space-y-6">
      <PageHeader
        back={{ label: t('dash.plans.back'), onClick: () => navigate('/plan') }}
        title={<span className="tabular-nums">{plan.code}</span>}
        description={
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <PlanStatusLabel status={plan.status} pendingAction={plan.pendingAction} />
            <span className="text-subtle">{t('dash.plans.created', { when: plan.createdAt })}</span>
          </span>
        }
        actions={
          <>
            {plan.status === 'selesai' && (
              <Button variant="secondary" iconLeft={<Printer className="size-4" aria-hidden />} onClick={() => window.print()}>
                {t('dash.plans.receipt')}
              </Button>
            )}
            {cancellable && (
              <>
                <Button
                  variant="secondary"
                  onClick={() => {
                    setSchedule(plan.schedule)
                    setRescheduleOpen(true)
                  }}
                >
                  {t('dash.plans.reschedule')}
                </Button>
                <Button variant="ghost" onClick={() => setCancelOpen(true)}>
                  {t('dash.plans.cancel')}
                </Button>
              </>
            )}
          </>
        }
      />

      {cancelling && (
        <Notice tone="warning" title={t('dash.plans.cancellingTitle')}>
          {t('dash.plans.cancellingBody')}
        </Notice>
      )}

      {plan.status === 'dibatalkan' && (
        <Notice tone="warning" title={t('dash.plans.cancelledTitle')}>
          {plan.cancelReason ?? t('dash.plans.cancelledDefault')}
        </Notice>
      )}

      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_22rem]">
        <div className="min-w-0 space-y-6">
          <Card className="overflow-hidden">
            <div className="h-80 border-b border-line bg-sunken sm:h-96">
              <RouteMap
                className="rounded-none"
                padding={56}
                fitKey={`${plan.id}:${plan.routes.length}`}
                ariaLabel={t('dash.plans.mapLabel', { code: plan.code })}
                routes={live.map((l) => ({
                  id: l.route.id,
                  route: l.route,
                  emphasis: plan.status === 'dibatalkan' ? 'muted' : 'primary',
                  travelledKm: l.travelled,
                  activeStopIndex: l.route.status === 'berjalan' ? l.pos.stopIndex : undefined,
                  vehicle:
                    l.route.status === 'berjalan'
                      ? { position: l.pos.position, bearing: l.pos.bearing, type: l.route.vehicleType, label: drivers?.find((d) => d.id === l.route.driverId)?.name.split(' ')[0] }
                      : undefined,
                }))}
              />
            </div>
            <div className="flex flex-wrap items-center justify-between gap-3 p-card">
              <p className="text-body">{banner}</p>
              {plan.status === 'berjalan' && primaryRoute && (
                <Chip>
                  {t('dash.plans.stopsDoneOf', {
                    done: primaryRoute.route.stops.filter((s) => s.status === 'selesai' || s.status === 'bermasalah').length,
                    count: primaryRoute.route.stops.length,
                  })}
                </Chip>
              )}
            </div>
          </Card>

          <Card className="p-card">
            <CardHeader title={t('dash.plans.stopsTitle')} description={t('dash.plans.stopsHint')} />
            <div className="mt-5 space-y-6">
              {live.map((l, ri) => (
                <div key={l.route.id}>
                  {live.length > 1 && (
                    <p className="mb-3 text-small font-semibold text-muted">
                      {t('dash.plans.routeHeading', { n: ri + 1, vehicle: VEHICLE_TYPE_LABEL[l.route.vehicleType], km: l.route.distanceKm, min: l.route.durationMin })}
                    </p>
                  )}
                  <StopList
                    stops={l.route.stops.map((s) => ({ ...s, arriveAt: l.etas.find((e) => e.stopId === s.id)?.etaAt ?? s.arriveAt }))}
                    activeIndex={l.route.status === 'berjalan' ? l.pos.stopIndex : undefined}
                    showStatus
                    group
                    extra={(s) => (
                      <>
                        {s.kind === 'antar' && s.deliveryCode && s.status !== 'selesai' && plan.status !== 'dibatalkan' && (
                          <p className="mt-1.5 flex w-fit items-center gap-2 rounded-chip bg-sunken px-2 py-1 text-small">
                            <span className="text-muted">{t('dash.plans.handoverCode')}</span>
                            <span className="font-mono font-semibold tracking-widest tabular-nums">{s.deliveryCode}</span>
                          </p>
                        )}
                        <StopProof plan={plan} stop={s} />
                      </>
                    )}
                  />
                </div>
              ))}
            </div>
            {plan.routes.some((r) => r.stops.some((s) => s.kind === 'antar' && s.deliveryCode && s.status !== 'selesai')) && plan.status !== 'dibatalkan' && (
              <p className="mt-5 text-small text-muted">{t('dash.plans.handoverHint')}</p>
            )}
          </Card>

          <Card className="p-card">
            <CardHeader title={t('dash.plans.itemsTitle')} />
            <ul className="mt-4 divide-y divide-line">
              {plan.shipments.map((s, i) => {
                const agg = aggregateItems(s.items)
                return (
                  <li key={s.id} className="py-3 first:pt-0 last:pb-0">
                    <p className="text-body font-semibold">
                      {t('dash.plans.shipmentHeading', { n: i + 1, from: s.pickup.name, to: s.dropoff.name })}
                    </p>
                    <p className="text-small text-muted">
                      {t('dash.plans.recipient', { name: s.dropoff.contactName, phone: formatPhone(s.dropoff.contactPhone) })}
                    </p>
                    <ul className="mt-1.5 space-y-0.5 text-small">
                      {s.items.map((it) => (
                        <li key={it.id} className="flex justify-between gap-3">
                          <span>
                            {it.qty} × {itemLabel(it)}
                            {it.fragile && <span className="text-subtle"> · {t('dash.plans.fragile')}</span>}
                          </span>
                          <span className="tabular-nums text-muted">{formatKg(it.qty * it.weightKg)}</span>
                        </li>
                      ))}
                    </ul>
                    <p className="mt-1 text-small text-subtle">
                      {t('dash.plans.itemsTotal', { count: agg.itemCount, weight: agg.weightKg })}
                    </p>
                  </li>
                )
              })}
            </ul>
          </Card>
        </div>

        <aside className="min-w-0 space-y-6">
          {plan.status !== 'dibatalkan' && (
            <Card className="p-card">
              <CardHeader title={t('dash.plans.timelineTitle')} />
              <div className="mt-4">
                <Timeline plan={plan} />
              </div>
              <p className="mt-3 text-center text-small text-muted">{planStatusText(plan)}</p>
            </Card>
          )}

          {assigned.length > 0 && (
            <Card className="p-card">
              <CardHeader title={assigned.length > 1 ? t('dash.plans.driversTitle') : t('dash.plans.driverTitle')} />
              <ul className="mt-4 space-y-4">
                {assigned.map(({ driver, vehicle, route }) => (
                  <li key={route.id} className="flex items-center gap-3">
                    <Avatar name={driver.name} size="lg" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-body font-semibold">{driver.name}</p>
                      <p className="flex items-center gap-1 text-small text-muted">
                        <Star className="size-3.5 fill-current" aria-hidden />
                        {formatDecimal(driver.rating, 2)} · {vehicle ? `${vehicle.model} · ${vehicle.plate}` : VEHICLE_TYPE_LABEL[route.vehicleType]}
                      </p>
                    </div>
                    <Button variant="secondary" size="sm" asChild>
                      <a href={`tel:${driver.phone}`} aria-label={t('dash.plans.callLabel', { name: driver.name })}>
                        <Phone className="size-4" aria-hidden />
                        {t('dash.plans.call')}
                      </a>
                    </Button>
                  </li>
                ))}
              </ul>
            </Card>
          )}

          <Card className="p-card" id="struk">
            <CardHeader title={t('dash.plans.costTitle')} description={plan.paymentMethod ? paidVia(plan.paymentMethod) : undefined} />
            <FareBreakdown routes={plan.routes} className="mt-4" />
            {savings && savings.costRp > 0 && (
              <p className="mt-3 text-small text-muted">
                {t('dash.plans.savings', { amount: savings.costRp, pct: savings.pct })}
              </p>
            )}
            {plan.status === 'selesai' && <p className="mt-3 hidden text-small text-muted print:block">{BRAND.name} · {plan.code} · {plan.finishedAt ? formatDate(plan.finishedAt) : ''}</p>}
          </Card>

          {settings?.ai.copilot && plan.routes.length > 0 && (
            <Copilot scope={{ kind: 'plan', planId: plan.id }} starters={[t('dash.plans.starterWhen'), t('dash.plans.starterCost'), t('dash.plans.starterLate')]} listClassName="max-h-[24rem]" />
          )}

          <Card className="p-card">
            <CardHeader title={t('dash.plans.historyTitle')} />
            <ol className="mt-4 space-y-3">
              {events.map((e) => (
                <li key={e.id} className="flex gap-3 text-small">
                  <span className="w-12 shrink-0 tabular-nums text-subtle">{formatTime(e.at)}</span>
                  <span>{renderMsg(e.text)}</span>
                </li>
              ))}
            </ol>
          </Card>
        </aside>
      </div>

      <ConfirmDialog
        open={cancelOpen}
        onOpenChange={setCancelOpen}
        title={t('dash.plans.cancelDialogTitle', { code: plan.code })}
        description={t('dash.plans.cancelDialogHint')}
        confirmLabel={t('dash.plans.cancel')}
        cancelLabel={t('dash.plans.cancelBack')}
        tone="danger"
        loading={cancel.isPending}
        onConfirm={() => cancel.mutate()}
      >
        <Field label={t('dash.plans.cancelReasonLabel')}>
          <Select value={reason} onValueChange={(v) => setReason(v as Reason)} options={REASONS.map((r) => ({ value: r, label: reasonLabel(r) }))} />
        </Field>
      </ConfirmDialog>

      <Dialog
        open={rescheduleOpen}
        onOpenChange={setRescheduleOpen}
        title={t('dash.plans.rescheduleTitle')}
        description={t('dash.plans.rescheduleHint')}
        footer={
          <>
            <Button variant="secondary" onClick={() => setRescheduleOpen(false)}>
              {t('dash.plans.dismiss')}
            </Button>
            <Button
              variant="primary"
              loading={reschedule.isPending}
              onClick={() => {
                const err = scheduleError(schedule, now)
                if (err) return toast.error(err)
                reschedule.mutate()
              }}
            >
              {t('dash.plans.rescheduleSave')}
            </Button>
          </>
        }
      >
        <ScheduleFields value={schedule} onChange={setSchedule} nowMs={now} error={scheduleError(schedule, now)} />
      </Dialog>
    </div>
  )
}
