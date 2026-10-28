import { useIdleDrivers, useLiveRoutes, useVirtualNow } from '@loong/data'
import { routePositionAt, travelledKm } from '@loong/domain'
import { IdleDriverDot, RouteMap, type MapRouteInput } from '@loong/ui'
import { useMemo } from 'react'
import { Marker } from 'react-map-gl/maplibre'
import { t } from '../i18n'

/**
 * Peta semua rute aktif: kendaraan bergerak mengikuti jam virtual (satu fungsi murni, tanpa simulator),
 * driver idle sebagai titik hijau. Rute terpilih digambar tebal beserta pin stop; sisanya abu-abu tipis.
 */
export function LiveMap({
  selectedRouteId,
  onSelectRoute,
  cooperativeGestures = false,
  showIdle = true,
  className,
}: {
  selectedRouteId?: string
  onSelectRoute?: (routeId: string, planId: string) => void
  cooperativeGestures?: boolean
  showIdle?: boolean
  className?: string
}) {
  const live = useLiveRoutes()
  const idle = useIdleDrivers()
  const now = useVirtualNow(1000)

  const routes = useMemo<MapRouteInput[]>(
    () =>
      (live.data ?? []).map((item) => {
        const { route } = item
        const selected = route.id === selectedRouteId
        const running = route.status === 'berjalan'
        const pos = running ? routePositionAt(route, now) : undefined
        return {
          id: route.id,
          route,
          emphasis: selected ? 'primary' : 'muted',
          showStops: selected,
          travelledKm: running ? travelledKm(route, now) : undefined,
          activeStopIndex: pos?.stopIndex,
          vehicleMuted: false,
          vehicle: pos ? { position: pos.position, bearing: pos.bearing, type: route.vehicleType, label: item.driverName?.split(' ')[0] } : undefined,
        }
      }),
    [live.data, selectedRouteId, now],
  )

  const planOf = useMemo(() => new Map((live.data ?? []).map((r) => [r.route.id, r.planId])), [live.data])

  return (
    <RouteMap
      className={className}
      cooperativeGestures={cooperativeGestures}
      routes={routes}
      fitKey={`${selectedRouteId ?? 'semua'}:${live.data?.length ?? 0}`} // kunci internal, bukan teks // i18n-ignore
      padding={48}
      ariaLabel={t('bo.live.mapLabel')}
      onSelectRoute={onSelectRoute ? (routeId) => onSelectRoute(routeId, planOf.get(routeId) ?? '') : undefined}
    >
      {showIdle &&
        idle.data?.map((d) =>
          d.position ? (
            <Marker key={d.id} longitude={d.position.lng} latitude={d.position.lat} anchor="center">
              <IdleDriverDot label={d.name} />
            </Marker>
          ) : null,
        )}
    </RouteMap>
  )
}
