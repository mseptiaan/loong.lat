import { REAL_BACKEND, useSettings, useVehicles } from '@loong/data'
import { aggregateShipments, type Item, type Shipment } from '@loong/domain'
import { Button, Card, CardHeader, Notice, toast } from '@loong/ui'
import { Camera, Mic } from 'lucide-react'
import { useState } from 'react'
import { PhotoSheet } from '../../components/ai/PhotoSheet'
import { VoiceSheet } from '../../components/ai/VoiceSheet'
import { ItemEditor } from '../../components/ItemEditor'
import { t } from '../../i18n'
import type { FieldErrors } from './model'

/** Langkah 2: barang tiap pengiriman, dengan umpan balik langsung "muat di motor / perlu blindvan". */
export function StepItems({ shipments, onShipments, errors }: { shipments: Shipment[]; onShipments: (s: Shipment[]) => void; errors: FieldErrors }) {
  // backend asli tidak memberi pelanggan data armada: tanpa petunjuk muat di sini, backend yang menolak muatan terlalu besar
  const fleet = useVehicles({ enabled: !REAL_BACKEND })
  const vehicles = REAL_BACKEND ? undefined : (fleet.data ?? [])
  const { data: settings } = useSettings()
  const ai = settings?.ai
  const [sheet, setSheet] = useState<{ kind: 'foto' | 'suara'; shipment: number }>()
  const total = aggregateShipments(shipments)
  const from = (s: Shipment) => s.pickup.name || t('dash.wizard.pickupFallback')
  const to = (s: Shipment) => s.dropoff.name || t('dash.wizard.dropoffFallback')
  const labels = shipments.map((s, i) => t('dash.wizard.shipmentLabel', { n: i + 1, from: from(s), to: to(s) }))

  /** Barang hasil AI hanya masuk setelah diperiksa pengguna; baris yang ditambahkan tetap bisa disunting di editor. */
  function addItems(items: Item[], index: number) {
    onShipments(shipments.map((x, k) => (k === index ? { ...x, items: [...x.items, ...items] } : x)))
    toast.success(t('dash.wizard.itemsAdded', { count: items.length }))
  }

  return (
    <div className="mx-auto max-w-3xl space-y-5">
      {shipments.map((s, i) => (
        <Card key={s.id} className="p-card">
          <CardHeader
            title={t('dash.wizard.shipmentN', { n: i + 1 })}
            description={t('dash.wizard.fromTo', { from: from(s), to: to(s) })}
            action={
              (ai?.vision || ai?.voice) && (
                <div className="flex shrink-0 gap-2">
                  {ai.vision && (
                    <Button size="sm" variant="secondary" iconLeft={<Camera className="size-4" aria-hidden />} onClick={() => setSheet({ kind: 'foto', shipment: i })}>
                      {t('dash.ai.photoTitle')}
                    </Button>
                  )}
                  {ai.voice && (
                    <Button size="sm" variant="secondary" iconLeft={<Mic className="size-4" aria-hidden />} onClick={() => setSheet({ kind: 'suara', shipment: i })}>
                      {t('dash.ai.speakButton')}
                    </Button>
                  )}
                </div>
              )
            }
          />
          <div className="mt-4">
            <ItemEditor
              prefix={`s${i}`}
              items={s.items}
              vehicles={vehicles}
              errors={errors}
              onChange={(items) => onShipments(shipments.map((x, k) => (k === i ? { ...x, items } : x)))}
            />
          </div>
        </Card>
      ))}
      <PhotoSheet
        key={`foto-${sheet?.shipment ?? 0}`}
        open={sheet?.kind === 'foto'}
        onOpenChange={(open) => !open && setSheet(undefined)}
        shipmentLabels={labels}
        defaultShipment={sheet?.shipment ?? 0}
        onAdd={addItems}
      />
      <VoiceSheet
        key={`suara-${sheet?.shipment ?? 0}`}
        open={sheet?.kind === 'suara'}
        onOpenChange={(open) => !open && setSheet(undefined)}
        shipmentLabels={labels}
        defaultShipment={sheet?.shipment ?? 0}
        onAdd={addItems}
      />
      {shipments.length > 1 && total.itemCount > 0 && (
        <Notice tone="info" title={t('dash.wizard.totalItems', { count: total.itemCount, kg: total.weightKg })}>
          {t('dash.wizard.totalItemsNote')}
        </Notice>
      )}
    </div>
  )
}
