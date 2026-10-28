import { errorMessage, useDrivers, useLiveRoutes, usePlans, useVirtualNow } from '@loong/data'
import { VEHICLE_TYPE_LABEL, formatPercent, type PlanSummary, type VehicleType } from '@loong/domain'
import { Button, Card, EmptyState, ErrorState, Input, Select, Skeleton, Stat, Tabs, TabsContent, TabsList, TabsTrigger } from '@loong/ui'
import { CircleCheckBig, Maximize2, Search, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router'
import { AssignSheet } from '../components/AssignPanel'
import { LiveMap } from '../components/LiveMap'
import { PlanTable } from '../components/PlanTable'
import { t } from '../i18n'
import { actionKind, isActive, isOperational, needsAction, routeText, sortQueue } from '../lib/plans'

type Tab = 'tindakan' | 'aktif' | 'semua'
const isTab = (v: string | null): v is Tab => v === 'tindakan' || v === 'aktif' || v === 'semua'

const matches = (p: PlanSummary, q: string) => {
  const needle = q.trim().toLowerCase()
  if (!needle) return true
  return [p.code, p.customerName, routeText(p), ...p.driverNames].some((s) => s.toLowerCase().includes(needle))
}

/**
 * Pusat komando: antrean plan adalah produknya (tabel dominan, ~2/3 lebar), peta live di rail kanan.
 * Di atas: SATU angka utama ("Perlu tindakan") dan tiga pendukung yang lebih kecil.
 */
export function Command() {
  const navigate = useNavigate()
  const plans = usePlans()
  const drivers = useDrivers()
  const live = useLiveRoutes()
  const nowMs = useVirtualNow(30_000)
  const [params, setParams] = useSearchParams()
  const tab: Tab = isTab(params.get('tab')) ? (params.get('tab') as Tab) : 'tindakan'
  const [q, setQ] = useState('')
  const [vehicle, setVehicle] = useState<'semua' | VehicleType>('semua')
  const [assignFor, setAssignFor] = useState<string>()
  const [selected, setSelected] = useState<{ routeId: string; planId: string }>()

  const operational = useMemo(() => (plans.data ?? []).filter(isOperational), [plans.data])
  const stats = useMemo(() => {
    const action = operational.filter(needsAction)
    const active = operational.filter(isActive)
    const weekAgo = nowMs - 7 * 86_400_000
    const done = operational.filter((p) => p.status === 'selesai' && new Date(p.updatedAt).getTime() >= weekAgo)
    return {
      action,
      noDriver: action.filter((p) => actionKind(p) === 'tanpa_driver').length,
      anomaly: action.filter((p) => actionKind(p) === 'anomali').length,
      active,
      running: active.filter((p) => p.status === 'berjalan').length,
      assigned: active.filter((p) => p.status === 'ditugaskan').length,
      done,
      cleanPct: done.length ? (done.filter((p) => !p.hasAnomaly).length / done.length) * 100 : undefined,
    }
  }, [operational, nowMs])

  const online = drivers.data?.filter((d) => d.status !== 'offline').length ?? 0
  const idle = drivers.data?.filter((d) => d.status === 'online').length ?? 0

  const source: PlanSummary[] = tab === 'tindakan' ? stats.action : tab === 'aktif' ? stats.active : operational
  const rows = useMemo(
    () => sortQueue(source.filter((p) => matches(p, q) && (vehicle === 'semua' || p.vehicleTypes.includes(vehicle))), nowMs),
    [source, q, vehicle, nowMs],
  )
  const filtered = q.trim() !== '' || vehicle !== 'semua'

  const selectedPlan = selected ? plans.data?.find((p) => p.id === selected.planId) : undefined

  return (
    <div className="space-y-4">
      <h1 className="sr-only">{t('bo.command.title')}</h1>
      <div className="grid gap-3 md:grid-cols-3 lg:grid-cols-[minmax(0,1.5fr)_repeat(3,minmax(0,1fr))]">
        {plans.isLoading ? (
          <>
            <Skeleton className="h-28 rounded-card md:col-span-3 lg:col-span-1" />
            <Skeleton className="h-28 rounded-card" />
            <Skeleton className="h-28 rounded-card" />
            <Skeleton className="h-28 rounded-card" />
          </>
        ) : plans.isError ? (
          // data tidak diketahui BUKAN nol: nol akan terbaca "semua beres" padahal antrean tidak terlihat
          <>
            <Stat emphasis="primary" label={t('bo.command.statAction')} value="—" hint={t('bo.command.statErrorHint')} className="md:col-span-3 lg:col-span-1" />
            <Stat label={t('bo.command.statActive')} value="—" hint={t('bo.command.statUnavailable')} />
            <Stat label={t('bo.command.statDrivers')} value="—" hint={t('bo.command.statUnavailable')} />
            <Stat label={t('bo.command.statClean')} value="—" hint={t('bo.command.statUnavailable')} />
          </>
        ) : (
          <>
            <Stat
              emphasis="primary"
              className="md:col-span-3 lg:col-span-1"
              label={t('bo.command.statAction')}
              value={stats.action.length}
              hint={stats.action.length === 0 ? t('bo.command.statActionNone') : t('bo.command.statActionHint', { noDriver: stats.noDriver, anomaly: stats.anomaly })}
            />
            <Stat label={t('bo.command.statActive')} value={stats.active.length} hint={t('bo.command.statActiveHint', { running: stats.running, assigned: stats.assigned })} />
            <Stat
              label={t('bo.command.statDrivers')}
              value={drivers.data ? `${online}/${drivers.data.length}` : '—'}
              hint={drivers.data ? t('bo.command.statDriversHint', { idle }) : t('bo.command.statUnavailable')}
            />
            <Stat
              label={t('bo.command.statClean')}
              value={stats.cleanPct === undefined ? '—' : formatPercent(stats.cleanPct)}
              hint={t('bo.command.statCleanHint', { count: stats.done.length })}
            />
          </>
        )}
      </div>

      <div className="grid items-start gap-4 2xl:grid-cols-[minmax(0,1fr)_24rem]">
        <Card className="flex min-h-[30rem] flex-col overflow-hidden">
          <Tabs value={tab} onValueChange={(v) => setParams(v === 'tindakan' ? {} : { tab: v }, { replace: true })} className="flex min-h-0 flex-1 flex-col">
            <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-b border-line pr-3">
              <TabsList className="border-b-0 pl-2">
                <TabsTrigger value="tindakan">
                  {t('bo.command.tabAction')} <span className="tabular-nums text-muted">{plans.isError ? '–' : stats.action.length}</span>
                </TabsTrigger>
                <TabsTrigger value="aktif">
                  {t('bo.command.tabActive')} <span className="tabular-nums text-muted">{plans.isError ? '–' : stats.active.length}</span>
                </TabsTrigger>
                <TabsTrigger value="semua">
                  {t('bo.command.tabAll')} <span className="tabular-nums text-muted">{plans.isError ? '–' : operational.length}</span>
                </TabsTrigger>
              </TabsList>
              <div className="flex items-center gap-2 py-2">
                <Input
                  leading={<Search className="size-4" aria-hidden />}
                  value={q}
                  onChange={(e) => setQ(e.target.value)}
                  placeholder={t('bo.command.search')}
                  aria-label={t('bo.command.searchLabel')}
                  className="h-9 w-56"
                />
                <Select
                  aria-label={t('bo.command.vehicleFilter')}
                  value={vehicle}
                  onValueChange={(v) => setVehicle(v as 'semua' | VehicleType)}
                  className="h-9 w-44"
                  options={[
                    { value: 'semua', label: t('bo.command.vehicleAll') },
                    { value: 'motor', label: VEHICLE_TYPE_LABEL.motor },
                    { value: 'blindvan', label: VEHICLE_TYPE_LABEL.blindvan },
                  ]}
                />
                {filtered && (
                  <Button
                    variant="ghost"
                    size="sm"
                    iconLeft={<X className="size-3.5" aria-hidden />}
                    onClick={() => {
                      setQ('')
                      setVehicle('semua')
                    }}
                  >
                    {t('bo.command.reset')}
                  </Button>
                )}
              </div>
            </div>

            <TabsContent value={tab} className="min-h-0 flex-1 overflow-auto focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-accent">
              {plans.isError ? (
                <ErrorState message={errorMessage(plans.error)} onRetry={() => void plans.refetch()} />
              ) : plans.isLoading ? (
                <div className="space-y-px p-3">
                  {Array.from({ length: 7 }, (_, i) => (
                    <Skeleton key={i} className="h-12" />
                  ))}
                </div>
              ) : rows.length === 0 ? (
                filtered ? (
                  <EmptyState
                    icon={<Search />}
                    title={t('bo.command.emptyFilteredTitle')}
                    description={t('bo.command.emptyFilteredHint')}
                    action={
                      <Button
                        onClick={() => {
                          setQ('')
                          setVehicle('semua')
                        }}
                      >
                        {t('bo.command.resetFilters')}
                      </Button>
                    }
                  />
                ) : tab === 'tindakan' ? (
                  <EmptyState
                    icon={<CircleCheckBig />}
                    title={t('bo.command.emptyActionTitle')}
                    description={t('bo.command.emptyActionHint')}
                    action={<Button onClick={() => setParams({ tab: 'aktif' }, { replace: true })}>{t('bo.command.viewActive')}</Button>}
                  />
                ) : (
                  <EmptyState icon={<CircleCheckBig />} title={t('bo.command.emptyTitle')} description={t('bo.command.emptyHint')} />
                )
              ) : (
                <PlanTable plans={rows} nowMs={nowMs} onAssign={setAssignFor} />
              )}
            </TabsContent>
          </Tabs>
          {rows.length > 0 && (
            <p className="hidden border-t border-line px-4 py-2 text-caption text-muted lg:block">{t('bo.command.shortcuts')}</p>
          )}
        </Card>

        <Card className="flex h-[28rem] flex-col overflow-hidden 2xl:sticky 2xl:top-[4.5rem] 2xl:h-[calc(100vh-6rem)] 2xl:min-h-[26rem]">
          <div className="flex items-center justify-between gap-3 border-b border-line px-4 py-2.5">
            <div>
              <h2 className="text-title">{t('bo.command.mapTitle')}</h2>
              <p className="text-small text-muted">
                {live.data ? t('bo.command.mapRoutes', { count: live.data.length }) : live.isError ? t('bo.command.mapError') : t('bo.command.mapLoading')}
              </p>
            </div>
            <Button variant="ghost" size="sm" iconLeft={<Maximize2 className="size-3.5" aria-hidden />} onClick={() => navigate('/map')}>
              {t('bo.command.fullscreen')}
            </Button>
          </div>
          <div className="relative min-h-0 flex-1">
            <LiveMap selectedRouteId={selected?.routeId} onSelectRoute={(routeId, planId) => setSelected({ routeId, planId })} />
            {selected && selectedPlan && (
              <div className="absolute inset-x-3 bottom-3 flex items-center justify-between gap-3 rounded-card border border-line bg-surface p-3 shadow-overlay">
                <div className="min-w-0">
                  <p className="text-body font-semibold tabular-nums">{selectedPlan.code}</p>
                  <p className="truncate text-small text-muted">{routeText(selectedPlan)}</p>
                </div>
                <div className="flex shrink-0 gap-1.5">
                  <Button size="sm" variant="primary" onClick={() => navigate(`/plan/${selectedPlan.id}`)}>
                    {t('bo.command.openPlan')}
                  </Button>
                  <Button size="sm" variant="ghost" aria-label={t('bo.command.closePlanCard')} onClick={() => setSelected(undefined)}>
                    <X className="size-4" aria-hidden />
                  </Button>
                </div>
              </div>
            )}
          </div>
        </Card>
      </div>

      <AssignSheet planId={assignFor} open={!!assignFor} onOpenChange={(open) => !open && setAssignFor(undefined)} />
    </div>
  )
}
