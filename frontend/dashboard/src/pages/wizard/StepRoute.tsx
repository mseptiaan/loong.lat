import { useSettings } from '@loong/data'
import { type Priority, type Schedule, type SavedAddress, type Shipment } from '@loong/domain'
import { Button, Card, Field, IconButton, Input, RouteMap, StopPin, Textarea } from '@loong/ui'
import { Plus, Trash2 } from 'lucide-react'
import { useMemo } from 'react'
import { Marker } from 'react-map-gl/maplibre'
import { IntentCard } from '../../components/ai/IntentCard'
import { t, tr } from '../../i18n'
import { PlaceField } from '../../components/PlaceField'
import { DeadlineField } from '../../components/DeadlineField'
import { ScheduleFields } from '../../components/ScheduleFields'
import { MAX_SHIPMENTS, asPlaceRef, departureMs, emptyShipment, isSet, pickedPoints, withPlace, type FieldErrors } from './model'

/** Langkah 1: lokasi ambil/antar (satu atau banyak pengiriman) dan jadwal. Peta di kanan menampilkan titik yang dipilih. */
export function StepRoute({
  shipments,
  onShipments,
  schedule,
  onSchedule,
  priority,
  onPriority,
  errors,
  saved,
  nowMs,
  contact,
}: {
  shipments: Shipment[]
  onShipments: (s: Shipment[]) => void
  schedule: Schedule
  onSchedule: (s: Schedule) => void
  priority: Priority
  onPriority: (p: Priority) => void
  errors: FieldErrors
  saved: SavedAddress[]
  nowMs: number
  contact: { name: string; phone: string }
}) {
  const update = (i: number, fn: (s: Shipment) => Shipment) => onShipments(shipments.map((s, k) => (k === i ? fn(s) : s)))
  const points = useMemo(() => pickedPoints(shipments), [shipments])
  const fitKey = points.map((p) => p.key).join('|') || 'kosong'
  const departure = departureMs(schedule, nowMs)
  const { data: settings } = useSettings()

  return (
    <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,26rem)]">
      <div className="min-w-0 space-y-5">
        {shipments.map((s, i) => (
          <Card key={s.id} className="space-y-5 p-card">
            <div className="flex items-center justify-between">
              <h2 className="text-title">{t('dash.wizard.shipmentN', { n: i + 1 })}</h2>
              {shipments.length > 1 && (
                <IconButton label={t('dash.wizard.removeShipment', { n: i + 1 })} icon={<Trash2 className="size-4" />} onClick={() => onShipments(shipments.filter((_, k) => k !== i))} />
              )}
            </div>

            <div className="space-y-3">
              <PlaceField
                label={t('dash.wizard.pickupAt')}
                value={asPlaceRef(s.pickup)}
                saved={saved}
                error={errors[`s${i}.pickup`]}
                onChange={(p) =>
                  update(i, (x) => {
                    const next = withPlace(x.pickup, p)
                    // alamat tersimpan membawa kontaknya
                    const hit = saved.find((a) => a.place.name === p?.name && a.place.address === p?.address)
                    return { ...x, pickup: hit ? { ...next, contactName: hit.contactName, contactPhone: hit.contactPhone } : next }
                  })
                }
              />
              {isSet(s.pickup) && (
                <div className="grid gap-3 sm:grid-cols-2">
                  <Field label={t('dash.wizard.pickupContact')} error={errors[`s${i}.pickupName`]}>
                    <Input value={s.pickup.contactName} onChange={(e) => update(i, (x) => ({ ...x, pickup: { ...x.pickup, contactName: e.target.value } }))} autoComplete="off" />
                  </Field>
                  <Field label={t('dash.wizard.pickupPhone')} error={errors[`s${i}.pickupPhone`]}>
                    <Input type="tel" inputMode="tel" value={s.pickup.contactPhone} onChange={(e) => update(i, (x) => ({ ...x, pickup: { ...x.pickup, contactPhone: e.target.value } }))} />
                  </Field>
                </div>
              )}
            </div>

            <div className="space-y-3 border-t border-line pt-5">
              <PlaceField
                label={t('dash.wizard.dropoffAt')}
                value={asPlaceRef(s.dropoff)}
                saved={saved}
                error={errors[`s${i}.dropoff`]}
                onChange={(p) =>
                  update(i, (x) => {
                    const next = withPlace(x.dropoff, p)
                    const hit = saved.find((a) => a.place.name === p?.name && a.place.address === p?.address)
                    return { ...x, dropoff: hit ? { ...next, contactName: hit.contactName, contactPhone: hit.contactPhone } : next }
                  })
                }
              />
              {isSet(s.dropoff) && (
                <>
                  <div className="grid gap-3 sm:grid-cols-2">
                    <Field label={t('dash.wizard.recipientName')} error={errors[`s${i}.dropoffName`]}>
                      <Input value={s.dropoff.contactName} onChange={(e) => update(i, (x) => ({ ...x, dropoff: { ...x.dropoff, contactName: e.target.value } }))} autoComplete="off" />
                    </Field>
                    <Field label={t('dash.wizard.recipientPhone')} error={errors[`s${i}.dropoffPhone`]}>
                      <Input type="tel" inputMode="tel" value={s.dropoff.contactPhone} onChange={(e) => update(i, (x) => ({ ...x, dropoff: { ...x.dropoff, contactPhone: e.target.value } }))} />
                    </Field>
                  </div>
                  <Field label={t('dash.wizard.driverNote')} optional>
                    <Textarea
                      rows={2}
                      value={s.dropoff.note ?? ''}
                      onChange={(e) => update(i, (x) => ({ ...x, dropoff: { ...x.dropoff, note: e.target.value } }))}
                      placeholder={t('dash.wizard.driverNoteExample')}
                    />
                  </Field>
                  <DeadlineField
                    value={s.deadline}
                    onChange={(deadline) =>
                      update(i, (x) => {
                        const { deadline: _old, ...rest } = x
                        void _old
                        return deadline ? { ...rest, deadline } : rest
                      })
                    }
                    departureMs={departure}
                    nowMs={nowMs}
                    error={errors[`s${i}.deadline`]}
                  />
                </>
              )}
            </div>
          </Card>
        ))}

        <div className="rounded-card border border-dashed border-line-strong p-4">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <p className="max-w-md text-small text-muted">
              {tr('dash.wizard.moreParcels', { b: (c) => <span className="font-semibold text-fg">{c}</span> })}
            </p>
            <Button
              variant="secondary"
              iconLeft={<Plus className="size-4" aria-hidden />}
              onClick={() => onShipments([...shipments, emptyShipment(contact)])}
              aria-disabled={shipments.length >= MAX_SHIPMENTS || undefined}
            >
              {t('dash.wizard.addShipment')}
            </Button>
          </div>
          {shipments.length >= MAX_SHIPMENTS && <p className="mt-2 text-small text-muted">{t('dash.wizard.maxShipments', { max: MAX_SHIPMENTS })}</p>}
        </div>

        <Card className="p-card">
          <ScheduleFields value={schedule} onChange={onSchedule} nowMs={nowMs} error={errors.schedule} />
        </Card>

        {settings?.ai.intent && <IntentCard priority={priority} onPriority={onPriority} shipments={shipments} onShipments={onShipments} departureMs={departure} />}
      </div>

      <div className="lg:sticky lg:top-6">
        <div className="h-72 overflow-hidden rounded-card border border-line sm:h-96 lg:h-[30rem]">
          <RouteMap routes={[]} extraPoints={points.map((p) => p.position)} fitKey={fitKey} padding={72} ariaLabel={t('dash.wizard.pickedMap')}>
            {points.map((p) => (
              <Marker key={p.key} longitude={p.position.lng} latitude={p.position.lat} anchor="center">
                <StopPin index={p.n} kind={p.kind} />
              </Marker>
            ))}
          </RouteMap>
        </div>
        <p className="mt-2 text-small text-subtle">{t('dash.wizard.mapLegend')}</p>
      </div>
    </div>
  )
}
