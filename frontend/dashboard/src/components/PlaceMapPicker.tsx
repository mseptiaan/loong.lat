import { useApi } from '@loong/data'
import type { LatLng, PlaceRef } from '@loong/domain'
import { Button, Dialog, MapView, PickerPin, Skeleton, JAKARTA_CENTER } from '@loong/ui'
import { Crosshair, MapPin } from 'lucide-react'
import { useEffect, useState } from 'react'
import { Marker, type MapLayerMouseEvent } from 'react-map-gl/maplibre'
import { t } from '../i18n'

/**
 * Memilih lokasi dengan menaruh/menggeser pin di peta. Nama & alamat diambil dari titik gazetteer terdekat
 * (reverse-geocode tiruan); nanti diganti layanan geocoding sungguhan di balik `places.describe`.
 */
export function PlaceMapPicker({
  open,
  onOpenChange,
  initial,
  title,
  onPick,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  initial?: LatLng
  title: string
  onPick: (place: PlaceRef) => void
}) {
  const api = useApi()
  const [pin, setPin] = useState<LatLng | undefined>(initial)
  const [info, setInfo] = useState<{ name: string; address: string; placeId?: string }>()
  const [loading, setLoading] = useState(false)

  // alamat untuk titik pin; hasil lama diabaikan bila pin sudah pindah lagi
  useEffect(() => {
    if (!open || !pin) return
    let stale = false
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true)
    api.places
      .describe(pin)
      .then((d) => !stale && setInfo(d))
      .catch(() => !stale && setInfo({ name: t('dash.fields.mapPoint'), address: `${pin.lat.toFixed(5)}, ${pin.lng.toFixed(5)}` }))
      .finally(() => !stale && setLoading(false))
    return () => {
      stale = true
    }
  }, [api, pin, open])

  const choose = (e: MapLayerMouseEvent) => setPin({ lat: e.lngLat.lat, lng: e.lngLat.lng })

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={title}
      description={t('dash.fields.mapHint')}
      className="max-w-2xl"
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            {t('dash.fields.cancel')}
          </Button>
          <Button
            variant="primary"
            disabled={!pin}
            loading={loading}
            onClick={() => {
              if (!pin || !info) return
              onPick({ name: info.name, address: info.address, placeId: info.placeId, position: pin })
              onOpenChange(false)
            }}
          >
            {t('dash.fields.useLocation')}
          </Button>
        </>
      }
    >
      <div className="h-80 overflow-hidden rounded-card border border-line sm:h-96">
        <MapView
          className="h-full rounded-none"
          initialViewState={initial ? { longitude: initial.lng, latitude: initial.lat, zoom: 14 } : { ...JAKARTA_CENTER, zoom: 11.5 }}
          onClick={choose}
          cursor="crosshair"
        >
          {pin && (
            <Marker longitude={pin.lng} latitude={pin.lat} anchor="bottom" draggable onDragEnd={(e) => setPin({ lat: e.lngLat.lat, lng: e.lngLat.lng })}>
              <PickerPin />
            </Marker>
          )}
        </MapView>
      </div>
      <div className="mt-3 min-h-14 rounded-card bg-sunken p-3">
        {!pin ? (
          <p className="flex items-center gap-2 text-body text-muted">
            <Crosshair className="size-4" aria-hidden />
            {t('dash.fields.noLocation')}
          </p>
        ) : loading && !info ? (
          <div className="space-y-1.5">
            <Skeleton className="h-4 w-40" />
            <Skeleton className="h-3.5 w-64" />
          </div>
        ) : (
          <div className="flex items-start gap-2">
            <MapPin className="mt-0.5 size-4 shrink-0 text-muted" aria-hidden />
            <div className="min-w-0">
              <p className="text-body font-semibold">{info?.name}</p>
              <p className="text-small text-muted">{info?.address}</p>
            </div>
          </div>
        )}
      </div>
    </Dialog>
  )
}
