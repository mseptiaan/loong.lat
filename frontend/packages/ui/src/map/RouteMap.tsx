import {
  boundsOf,
  partialPolyline,
  routeGeometry,
  splitPolyline,
  type LatLng,
  type LngLat,
  type Route,
  type VehicleType,
} from '@loong/domain'
import { t } from '@loong/i18n'
import type { Feature, FeatureCollection, LineString } from 'geojson'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Layer, Marker, Source, type MapLayerMouseEvent, type MapRef } from 'react-map-gl/maplibre'
import { JAKARTA_CENTER, MapView } from '../components/MapView'
import { cn } from '../lib/cn'
import { resolveCssColor } from '../lib/cssColor'
import { groupPinState, groupStops } from '../components/route'
import { StopPin, VehiclePin } from './markers'

export interface MapVehicle {
  position: LatLng
  bearing: number
  type: VehicleType
  label?: string
}

export interface MapRouteInput {
  id: string
  route: Route
  /** primary = aksen tebal; muted = abu-abu tipis (alternatif yang bisa diklik). */
  emphasis: 'primary' | 'muted'
  showStops?: boolean
  /** Km yang sudah ditempuh: bagian itu digambar abu-abu, sisanya aksen. */
  travelledKm?: number
  vehicle?: MapVehicle
  /** Redam penanda kendaraan (abu-abu). Bawaan mengikuti `emphasis`; peta operasional mematikannya agar kendaraan tetap hidup. */
  vehicleMuted?: boolean
  /** Indeks stop aktif (untuk menandai pin). */
  activeStopIndex?: number
}

const prefersReducedMotion = () =>
  typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches

/**
 * Pecahan 0..1 untuk animasi menggambar rute utama (~450 ms), signature Dashboard ("route-trace").
 * State di-reset saat `traceKey` berubah memakai pola "sesuaikan state saat prop berubah" (bukan lewat effect).
 */
function useTrace(traceKey: string | undefined): number {
  const skip = traceKey === undefined || prefersReducedMotion()
  const [anim, setAnim] = useState<{ key: string | undefined; fraction: number }>({ key: traceKey, fraction: skip ? 1 : 0 })
  if (anim.key !== traceKey) setAnim({ key: traceKey, fraction: skip ? 1 : 0 })

  useEffect(() => {
    if (skip) return
    let frame = 0
    const start = performance.now()
    const tick = (now: number) => {
      const p = Math.min(1, (now - start) / 450)
      setAnim((prev) => (prev.key === traceKey ? { key: traceKey, fraction: 1 - (1 - p) ** 3 } : prev))
      if (p < 1) frame = requestAnimationFrame(tick)
    }
    frame = requestAnimationFrame(tick)
    return () => cancelAnimationFrame(frame)
  }, [traceKey, skip])

  return skip ? 1 : anim.key === traceKey ? anim.fraction : 0
}

type LineProps = { id: string; rank: number; width: number; color: string; opacity: number; hit: boolean }

/** Semua rute dalam SATU sumber; urutan gambar lewat `line-sort-key` sehingga mengganti rute utama hanya mengubah data. */
function buildFeatures(
  routes: { input: MapRouteInput; coords: LngLat[] }[],
  trace: number,
  colors: { accent: string; muted: string },
): FeatureCollection<LineString, LineProps> {
  const features: Feature<LineString, LineProps>[] = []
  for (const { input, coords } of routes) {
    const primary = input.emphasis === 'primary'
    const base = { id: input.id, width: primary ? 5 : 3.5, hit: !primary }
    const [done, remaining] = input.travelledKm ? splitPolyline(coords, input.travelledKm) : [[] as LngLat[], coords]
    const shown = primary ? partialPolyline(remaining, trace) : remaining
    if (done.length > 1) {
      features.push({ type: 'Feature', properties: { ...base, rank: primary ? 2 : 0, color: colors.muted, opacity: 0.85, hit: false }, geometry: { type: 'LineString', coordinates: done } })
    }
    if (shown.length > 1) {
      features.push({
        type: 'Feature',
        properties: { ...base, rank: primary ? 3 : 1, color: primary ? colors.accent : colors.muted, opacity: 1 },
        geometry: { type: 'LineString', coordinates: shown },
      })
    }
  }
  return { type: 'FeatureCollection', features }
}

export interface RouteMapProps {
  routes: MapRouteInput[]
  className?: string
  /** Berubah => peta menyesuaikan lagi ke rute (default: id rute + jumlah stop). */
  fitKey?: string
  /** Berubah => rute utama digambar ulang dengan animasi. Kosong = tanpa animasi. */
  traceKey?: string
  padding?: number | { top: number; bottom: number; left: number; right: number }
  onSelectRoute?: (id: string) => void
  /** Penanda tambahan (mis. driver idle, pin picker). */
  children?: ReactNode
  /** Titik tambahan yang ikut dihitung saat menyesuaikan batas peta. */
  extraPoints?: LatLng[]
  interactive?: boolean
  /** Zoom gulir butuh Ctrl/⌘ (default, agar peta di dalam halaman tidak menjebak gulir). Matikan bila peta adalah konten utama layar. */
  cooperativeGestures?: boolean
  ariaLabel?: string
}

/** Peta rute: garis rute (utama/alternatif), pin stop bernomor, kendaraan; menyesuaikan batas otomatis. */
export function RouteMap({
  routes,
  className,
  fitKey,
  traceKey,
  padding = 56,
  onSelectRoute,
  children,
  extraPoints,
  interactive = true,
  cooperativeGestures = true,
  ariaLabel = t('ui.map.routeLabel'),
}: RouteMapProps) {
  const mapRef = useRef<MapRef>(null)
  const trace = useTrace(traceKey)

  const geometries = useMemo(() => routes.map((r) => ({ input: r, coords: routeGeometry(r.route) })), [routes])
  // jalur jalan sungguhan datang dari Mapbox Directions; sumbernya dicantumkan pada atribusi peta
  const roadsFromMapbox = routes.some((r) => r.route.geometry && r.route.geometry.length > 1)

  const points = useMemo<LatLng[]>(() => {
    const out: LatLng[] = [...(extraPoints ?? [])]
    for (const { input } of geometries) {
      out.push(input.route.startPosition, ...input.route.stops.map((s) => s.waypoint.position))
      if (input.vehicle) out.push(input.vehicle.position)
    }
    return out
  }, [geometries, extraPoints])

  const bounds = useMemo(() => boundsOf(points), [points])
  const computedFitKey = fitKey ?? routes.map((r) => `${r.id}:${r.route.stops.length}`).join('|')

  // menyesuaikan batas saat rute berganti (bukan tiap posisi kendaraan bergerak)
  const lastFit = useRef<string>('')
  useEffect(() => {
    if (!bounds || lastFit.current === computedFitKey) return
    const map = mapRef.current
    if (!map) return
    lastFit.current = computedFitKey
    map.fitBounds(bounds, { padding, duration: prefersReducedMotion() ? 0 : 600, maxZoom: 15.5 })
  }, [bounds, computedFitKey, padding])

  const colors = {
    accent: resolveCssColor('--accent', '#00618a'),
    muted: resolveCssColor('--text-subtle', '#8a837c'),
  }
  const casing = resolveCssColor('--surface', '#ffffff')
  const data = useMemo(() => buildFeatures(geometries, trace, colors), [geometries, trace, colors.accent, colors.muted]) // eslint-disable-line react-hooks/exhaustive-deps

  const onClick = (e: MapLayerMouseEvent) => {
    const id = e.features?.[0]?.properties?.id as string | undefined
    if (id && onSelectRoute) onSelectRoute(id)
  }

  // pin: rute utama di atas alternatif
  const ordered = [...geometries].sort((a, b) => Number(a.input.emphasis === 'primary') - Number(b.input.emphasis === 'primary'))

  return (
    <MapView
      className={cn('h-full w-full', className)}
      mapRef={mapRef}
      initialViewState={bounds ? { bounds, fitBoundsOptions: { padding, maxZoom: 15.5 } } : { ...JAKARTA_CENTER, zoom: 11.5 }}
      interactive={interactive}
      cooperativeGestures={cooperativeGestures}
      interactiveLayerIds={onSelectRoute ? ['routes-hit'] : []}
      onClick={onClick}
      cursor={onSelectRoute ? 'pointer' : undefined}
      // atribusi dibuat ulang (MapView memakai teksnya sebagai key) saat rute jalan sungguhan baru tiba, mis. dimuat belakangan
      attribution={roadsFromMapbox ? t('ui.map.routesAttribution') : undefined}
      aria-label={ariaLabel}
    >
      <Source id="routes" type="geojson" data={data}>
        <Layer
          id="routes-casing"
          type="line"
          layout={{ 'line-cap': 'round', 'line-join': 'round', 'line-sort-key': ['get', 'rank'] }}
          paint={{ 'line-color': casing, 'line-width': ['+', ['get', 'width'], 3], 'line-opacity': 0.9 }}
        />
        <Layer
          id="routes-line"
          type="line"
          layout={{ 'line-cap': 'round', 'line-join': 'round', 'line-sort-key': ['get', 'rank'] }}
          paint={{ 'line-color': ['get', 'color'], 'line-width': ['get', 'width'], 'line-opacity': ['get', 'opacity'] }}
        />
        {/* area klik lebar tak terlihat untuk rute alternatif */}
        <Layer id="routes-hit" type="line" filter={['==', ['get', 'hit'], true]} paint={{ 'line-color': '#000000', 'line-opacity': 0, 'line-width': 18 }} />
      </Source>

      {/* stop berurutan di tempat yang sama satu penanda ("1–4"), sama dengan daftar stop; bila tidak, penandanya bertumpuk dan hanya satu nomor yang terbaca */}
      {ordered.map(({ input }) =>
        input.showStops === false
          ? null
          : groupStops(input.route.stops).map((g) => {
              const active = g.stops.some((x) => input.route.stops.indexOf(x) === input.activeStopIndex)
              return (
                <Marker key={g.first.id} longitude={g.first.waypoint.position.lng} latitude={g.first.waypoint.position.lat} anchor="center" style={{ zIndex: input.emphasis === 'primary' ? 2 : 1 }}>
                  <StopPin index={g.first.seq} to={g.stops.at(-1)?.seq} kind={g.first.kind} state={groupPinState(g, active)} className={input.emphasis === 'muted' ? 'opacity-60' : undefined} />
                </Marker>
              )
            }),
      )}

      {ordered.map(({ input }) =>
        input.vehicle ? (
          <Marker key={`veh-${input.id}`} longitude={input.vehicle.position.lng} latitude={input.vehicle.position.lat} anchor="center" style={{ zIndex: 3 }}>
            <VehiclePin type={input.vehicle.type} bearing={input.vehicle.bearing} label={input.vehicle.label} muted={input.vehicleMuted ?? input.emphasis === 'muted'} />
          </Marker>
        ) : null,
      )}

      {children}
    </MapView>
  )
}
