import { errorMessage, useDrivers, useIdleDrivers, useLiveRoutes, useVehicles, useVirtualNow } from '@loong/data'
import { DRIVER_STATUS_LABEL, VEHICLE_TYPE_LABEL, formatPhone, liveEtas, routePositionAt, type ActiveRouteLive } from '@loong/domain'
import { Avatar, Button, Card, EmptyState, ErrorState, RouteStatusLabel, Skeleton, cn } from '@loong/ui'
import { Map as MapIcon, Motorbike, Phone, Van, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router'
import { LiveMap } from '../components/LiveMap'
import { t, tr } from '../i18n'

const isAnomaly = (r: ActiveRouteLive) => (r.route.delayMin ?? 0) >= 10 || r.route.stops.some((s) => s.status === 'bermasalah')

/** Perlu dilihat dulu: anomali, lalu yang berjalan, lalu yang baru ditugaskan. */
const rank = (r: ActiveRouteLive) => (isAnomaly(r) ? 0 : r.route.status === 'berjalan' ? 1 : 2)

function RouteItem({ item, now, selected, onSelect }: { item: ActiveRouteLive; now: number; selected: boolean; onSelect: () => void }) {
  const { route } = item
  const Icon = route.vehicleType === 'motor' ? Motorbike : Van
  const running = route.status === 'berjalan'
  const pos = running ? routePositionAt(route, now) : undefined
  const next = route.stops[pos?.stopIndex ?? 0]
  const done = route.stops.filter((s) => s.status === 'selesai' || s.status === 'bermasalah').length
  const delayed = (route.delayMin ?? 0) >= 10
  return (
    <li>
      <button
        type="button"
        onClick={onSelect}
        aria-pressed={selected}
        className={cn(
          'w-full rounded-card border p-3 text-left transition-colors',
          selected ? 'border-accent/40 bg-accent-tint' : 'border-transparent hover:bg-sunken',
        )}
      >
        <span className="flex items-center justify-between gap-2">
          <span className="flex items-center gap-2 text-body font-semibold tabular-nums">
            <Icon className="size-4 text-subtle" aria-hidden />
            {item.planCode}
          </span>
          <RouteStatusLabel status={route.status} className="text-small" />
        </span>
        <span className="mt-0.5 block truncate text-small text-muted">
          {item.driverName ?? t('bo.live.noDriver')} · {item.customerName}
        </span>
        {running && next && (
          <span className="mt-1 block truncate text-small tabular-nums">
            {pos && !pos.atStop
              ? t('bo.live.headingTo', { place: next.waypoint.name, min: Math.max(1, Math.ceil(pos.remainingLegMin)) })
              : t('bo.live.headingToArrived', { place: next.waypoint.name })}
            <span className="text-muted"> · {t('bo.live.progress', { done, total: route.stops.length })}</span>
          </span>
        )}
        {delayed && <span className="mt-0.5 block text-small font-medium text-warning-fg">{t('bo.live.delayed', { min: route.delayMin ?? 0 })}</span>}
        {!delayed && route.stops.some((s) => s.status === 'bermasalah') && <span className="mt-0.5 block text-small font-medium text-warning-fg">{t('bo.live.problemStop')}</span>}
      </button>
    </li>
  )
}

/** Peta layar penuh: daftar rute di kiri (anomali dulu), semua kendaraan bergerak di peta; klik memilih dan menyorot rute. */
export function LivePage() {
  const navigate = useNavigate()
  const live = useLiveRoutes()
  const idle = useIdleDrivers()
  const drivers = useDrivers()
  const vehicles = useVehicles()
  const now = useVirtualNow(1000)
  const [selectedId, setSelectedId] = useState<string>()

  const items = useMemo(() => [...(live.data ?? [])].sort((a, b) => rank(a) - rank(b)), [live.data])
  const selected = items.find((r) => r.route.id === selectedId)
  const selectedDriver = selected ? drivers.data?.find((d) => d.id === selected.route.driverId) : undefined
  const selectedVehicle = selected ? vehicles.data?.find((v) => v.id === selected.route.vehicleId) : undefined
  const eta = selected && selected.route.status === 'berjalan' ? liveEtas(selected.route, now)[0] : undefined
  const selectedNext = selected ? selected.route.stops.find((s) => s.status !== 'selesai' && s.status !== 'bermasalah') : undefined

  return (
    <div className="grid h-[calc(100vh-6.5rem)] min-h-[32rem] gap-4 lg:grid-cols-[22rem_minmax(0,1fr)]">
      <Card className="flex min-h-0 flex-col overflow-hidden">
        <div className="border-b border-line px-4 py-3">
          <h1 className="text-title">{t('bo.live.title')}</h1>
          <p className="text-small text-muted">
            {live.data ? `${t('bo.live.routeCount', { count: items.length })} · ${t('bo.live.readyCount', { count: idle.data?.length ?? 0 })}` : t('bo.live.loading')}
          </p>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-2">
          {live.isError ? (
            <ErrorState compact message={errorMessage(live.error)} onRetry={() => void live.refetch()} />
          ) : live.isLoading ? (
            <div className="space-y-2 p-1">
              {Array.from({ length: 5 }, (_, i) => (
                <Skeleton key={i} className="h-16" />
              ))}
            </div>
          ) : items.length === 0 ? (
            <EmptyState compact icon={<MapIcon />} title={t('bo.live.emptyTitle')} description={t('bo.live.emptyHint')} />
          ) : (
            <ul className="space-y-1">
              {items.map((item) => (
                <RouteItem key={item.route.id} item={item} now={now} selected={item.route.id === selectedId} onSelect={() => setSelectedId(item.route.id === selectedId ? undefined : item.route.id)} />
              ))}
            </ul>
          )}

          {(idle.data?.length ?? 0) > 0 && (
            <div className="mt-4 border-t border-line px-2 pt-3">
              <h2 className="text-small font-semibold text-muted">{t('bo.live.readyTitle')}</h2>
              <ul className="mt-2 space-y-2">
                {idle.data!.map((d) => (
                  <li key={d.id} className="flex items-center gap-2.5 text-small">
                    <Avatar name={d.name} size="sm" />
                    <span className="min-w-0 flex-1 truncate font-medium">{d.name}</span>
                    <span className="size-1.5 rounded-full bg-success" aria-hidden />
                    <span className="text-muted">{DRIVER_STATUS_LABEL.online}</span>
                  </li>
                ))}
              </ul>
            </div>
          )}
        </div>
      </Card>

      <Card className="relative min-h-0 overflow-hidden">
        <LiveMap selectedRouteId={selectedId} onSelectRoute={(routeId) => setSelectedId(routeId)} />

        {selected && (
          <div className="absolute bottom-3 left-3 right-3 max-w-md rounded-card border border-line bg-surface p-4 shadow-overlay sm:right-auto">
            <div className="flex items-start justify-between gap-3">
              <div className="min-w-0">
                <p className="text-title tabular-nums">{selected.planCode}</p>
                <p className="text-small text-muted">
                  {VEHICLE_TYPE_LABEL[selected.route.vehicleType]}
                  {selectedVehicle ? ` · ${selectedVehicle.plate}` : ''} · {selected.customerName}
                </p>
              </div>
              <Button size="sm" variant="ghost" aria-label={t('bo.live.closeCard')} onClick={() => setSelectedId(undefined)}>
                <X className="size-4" aria-hidden />
              </Button>
            </div>
            {selectedDriver && (
              <p className="mt-2 flex items-center gap-2 text-body">
                <Avatar name={selectedDriver.name} size="sm" />
                <span className="font-semibold">{selectedDriver.name}</span>
                <span className="tabular-nums text-muted">{formatPhone(selectedDriver.phone)}</span>
              </p>
            )}
            {selectedNext && (
              <p className="mt-2 text-small text-muted">
                {tr(eta ? 'bo.live.nextEta' : 'bo.live.nextScheduled', {
                  kind: selectedNext.kind === 'ambil' ? t('bo.live.kindPickup') : t('bo.live.kindDropoff'),
                  place: selectedNext.waypoint.name,
                  time: eta ? eta.etaAt : selectedNext.arriveAt,
                  stop: (c) => <span className="font-semibold text-fg">{c}</span>,
                })}
              </p>
            )}
            <div className="mt-3 flex gap-2">
              <Button size="sm" variant="primary" onClick={() => navigate(`/plan/${selected.planId}`)}>
                {t('bo.live.openPlan')}
              </Button>
              {selectedDriver && (
                <Button size="sm" variant="secondary" iconLeft={<Phone className="size-3.5" aria-hidden />} asChild>
                  <a href={`tel:${selectedDriver.phone.replace(/[^\d+]/g, '')}`}>{t('bo.live.callDriver')}</a>
                </Button>
              )}
            </div>
          </div>
        )}
      </Card>
    </div>
  )
}
