import { AttributionControl, setWorkerUrl, type Map as MapLibreMap } from 'maplibre-gl'
import { useMemo, type Ref } from 'react'
import { Map, useControl, type MapProps, type MapRef } from 'react-map-gl/maplibre'
import { t } from '@loong/i18n'
import 'maplibre-gl/dist/maplibre-gl.css'
import { cn } from '../lib/cn'

// MapLibre 6 di bundler wajib diberi URL worker. Kita sajikan sebagai berkas statis di public/ tiap app
// (disalin oleh scripts/sync-map-worker.mjs) karena jalur `?worker&url` Vite membuat peta berhenti memuat
// di mode dev. BASE_URL membuat ini benar di semua app.
setWorkerUrl(new URL(`${import.meta.env.BASE_URL}maplibre-gl-worker.mjs`, window.location.origin).href)

/**
 * Basemap tanpa API key. OpenFreeMap tidak punya SLA: kalau tile gagal dimuat, layar yang memakai peta harus
 * tetap menyajikan padanan daftar (lihat rencana: risiko tile mati).
 * Pindah ke Mapbox nanti = ganti URL style ini dan impor `react-map-gl/mapbox`.
 */
export const MAP_STYLE_URL = 'https://tiles.openfreemap.org/styles/positron'

/** Pusat Jakarta (Bundaran HI), titik awal peta bila belum ada rute. */
export const JAKARTA_CENTER = { longitude: 106.8229, latitude: -6.1944 } as const

/** Teks bantuan "gestur kooperatif" (peta di dalam halaman yang bisa digulir), mengikuti bahasa saat peta dipasang. */
const mapLocale = () => ({
  'CooperativeGesturesHandler.WindowsHelpText': t('ui.map.helpWindows'),
  'CooperativeGesturesHandler.MacHelpText': t('ui.map.helpMac'),
  'CooperativeGesturesHandler.MobileHelpText': t('ui.map.helpMobile'),
  'AttributionControl.ToggleAttribution': t('ui.map.attributionToggle'),
})

/**
 * Atribusi ringkas yang tertutup sejak awal. MapLibre selalu membuka atribusi mode `compact` saat dibuat dan baru
 * menutupnya setelah peta digeser; tidak ada opsi untuk memulai tertutup. Keadaan "tertutup" versi MapLibre = tanpa
 * atribut `open` dan tanpa kelas `maplibregl-compact-show` (lihat `_toggleAttribution`), jadi cukup dilepas setelah
 * kontrol dibuat. Tombol (i) tetap ada dan membukanya lagi dengan satu ketukan.
 */
class CollapsedAttribution extends AttributionControl {
  override onAdd(map: MapLibreMap): HTMLElement {
    const el = super.onAdd(map)
    el.removeAttribute('open')
    el.classList.remove('maplibregl-compact-show')
    return el
  }
}

/** Kontrol dibuat sekali per pemasangan; ganti `key` bila teks atribusinya berubah. */
function MapAttribution({ text }: { text?: string }) {
  useControl(() => new CollapsedAttribution({ compact: true, customAttribution: text }), { position: 'bottom-right' })
  return null
}

export type MapViewProps = Omit<MapProps, 'mapStyle' | 'ref' | 'attributionControl'> & {
  className?: string
  mapStyle?: string
  mapRef?: Ref<MapRef>
  /** Atribusi tambahan di luar yang dibawa style peta (mis. "Rute © Mapbox"). Atribusi selalu ada, tertutup di balik tombol (i). */
  attribution?: string
}

function assignRef<T>(ref: Ref<T> | undefined, value: T | null) {
  if (typeof ref === 'function') ref(value)
  else if (ref) (ref as { current: T | null }).current = value
}

export function MapView({ className, mapStyle = MAP_STYLE_URL, initialViewState, style, onError, mapRef, attribution, children, ...rest }: MapViewProps) {
  // tetap satu objek selama peta hidup (react-map-gl membacanya saat dibuat); pergantian bahasa memasang ulang pohon
  const locale = useMemo(() => mapLocale(), [])
  return (
    <div className={cn('relative overflow-hidden rounded-card bg-sunken', className)}>
      <Map
        ref={(r) => {
          // hanya untuk debugging di dev: window.__loongMap
          if (import.meta.env.DEV && r) (window as unknown as { __loongMap?: unknown }).__loongMap = r.getMap()
          assignRef(mapRef, r)
        }}
        initialViewState={initialViewState ?? { ...JAKARTA_CENTER, zoom: 11.5 }}
        mapStyle={mapStyle}
        // peta di dalam halaman tidak boleh menjebak gulir: zoom butuh Ctrl/⌘ (peta layar penuh mematikan ini)
        cooperativeGestures
        locale={locale}
        style={{ width: '100%', height: '100%', ...style }}
        onError={(e) => {
          console.warn('[peta]', e.error?.message ?? e)
          onError?.(e)
        }}
        {...rest}
        attributionControl={false}
      >
        <MapAttribution key={attribution ?? ''} text={attribution} />
        {children}
      </Map>
    </div>
  )
}
