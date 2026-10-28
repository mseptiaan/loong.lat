import { errorField, errorMessage, qk, useApi, useDrivers, useMutation, useQueryClient, useVehicles } from '@loong/data'
import {
  DEFAULT_CAPACITY,
  DEFAULT_FARE,
  VEHICLE_TYPE_LABEL,
  formatFloat,
  formatKg,
  formatRupiah,
  type FareConfig,
  type Vehicle,
  type VehicleInput,
  type VehicleStatus,
  type VehicleType,
} from '@loong/domain'
import {
  Button,
  Card,
  ConfirmDialog,
  EmptyState,
  ErrorState,
  Field,
  Input,
  PageHeader,
  Segmented,
  Select,
  Sheet,
  Skeleton,
  StatusLabel,
  TBody,
  TD,
  TH,
  THead,
  TR,
  TableWrap,
  toast,
  type Tone,
} from '@loong/ui'
import { Motorbike, Pencil, Plus, Search, Trash2, Truck, Van } from 'lucide-react'
import { useRef, useState } from 'react'
import { useSearchParams } from 'react-router'
import { t } from '../i18n'
import type { enBoFleet } from '../i18n/en/fleet'
import { focusFirstInvalid, useFieldVisibility } from '../lib/form'
import { formatInputNumber, parseNum } from '../lib/number'

/** Kunci tanpa parameter yang dipakai tabel konstanta (status dan label tarif). */
type FleetKey = Extract<keyof typeof enBoFleet, `bo.fleet.status${string}` | `bo.fleet.fare${string}`>

// teks disimpan sebagai kunci; t() dipanggil saat render agar mengikuti bahasa
const STATUS_KEY = { aktif: 'bo.fleet.statusActive', servis: 'bo.fleet.statusService', nonaktif: 'bo.fleet.statusInactive' } as const satisfies Record<VehicleStatus, FleetKey>
const STATUS_TONE: Record<VehicleStatus, Tone> = { aktif: 'success', servis: 'warning', nonaktif: 'neutral' }

type FareKey = keyof FareConfig
const FARE_FIELDS: { key: FareKey; label: FleetKey; hint?: FleetKey; money: boolean; unit?: string }[] = [
  { key: 'base', label: 'bo.fleet.fareBase', hint: 'bo.fleet.fareBaseHint', money: true },
  { key: 'perKm', label: 'bo.fleet.farePerKm', money: true },
  { key: 'perMin', label: 'bo.fleet.farePerMin', money: true },
  { key: 'perExtraStop', label: 'bo.fleet.farePerExtraStop', hint: 'bo.fleet.farePerExtraStopHint', money: true },
  { key: 'surchargePerKg', label: 'bo.fleet.fareSurchargePerKg', money: true },
  { key: 'surchargeAboveKg', label: 'bo.fleet.fareSurchargeAboveKg', hint: 'bo.fleet.fareSurchargeAboveKgHint', money: false, unit: 'kg' },
]

interface Values {
  type: VehicleType
  model: string
  plate: string
  maxWeightKg: string
  maxVolumeM3: string
  maxDimensionCm: string
  fare: Record<FareKey, string>
  status: VehicleStatus
}

function toValues(v?: Vehicle, type: VehicleType = 'motor'): Values {
  const kind = v?.type ?? type
  const cap = v ?? { ...DEFAULT_CAPACITY[kind] }
  const fare = v?.fare ?? DEFAULT_FARE[kind]
  return {
    type: kind,
    model: v?.model ?? '',
    plate: v?.plate ?? '',
    maxWeightKg: formatInputNumber(cap.maxWeightKg),
    maxVolumeM3: formatInputNumber(cap.maxVolumeM3),
    maxDimensionCm: formatInputNumber(cap.maxDimensionCm),
    fare: Object.fromEntries(FARE_FIELDS.map((f) => [f.key, formatInputNumber(fare[f.key])])) as Record<FareKey, string>,
    status: v?.status ?? 'aktif',
  }
}

const normPlate = (p: string) => p.trim().toUpperCase().replace(/\s+/g, ' ')
const PLATE_RE = /^[A-Z]{1,2} ?\d{1,4} ?[A-Z]{1,3}$/

function validate(v: Values, all: Vehicle[], editingId?: string): Record<string, string> {
  const e: Record<string, string> = {}
  if (v.model.trim().length < 3) e.model = t('bo.fleet.errModel')
  const plate = normPlate(v.plate)
  if (!plate) e.plate = t('bo.fleet.errPlateRequired')
  else if (!PLATE_RE.test(plate)) e.plate = t('bo.fleet.errPlateFormat')
  else if (all.some((x) => x.id !== editingId && normPlate(x.plate) === plate)) e.plate = t('bo.fleet.errPlateTaken')

  // satu kalimat satu kunci: kata benda dan satuan tidak disusun dari potongan
  const positive = (key: 'maxWeightKg' | 'maxVolumeM3' | 'maxDimensionCm', max: number, msgs: { min: string; over: string }) => {
    const n = parseNum(v[key])
    if (!(n > 0)) e[key] = msgs.min
    else if (n > max) e[key] = msgs.over
  }
  positive('maxWeightKg', 5000, { min: t('bo.fleet.errWeightMin'), over: t('bo.fleet.errWeightMax', { max: 5000 }) })
  positive('maxVolumeM3', 30, { min: t('bo.fleet.errVolumeMin'), over: t('bo.fleet.errVolumeMax', { max: 30 }) })
  positive('maxDimensionCm', 600, { min: t('bo.fleet.errDimensionMin'), over: t('bo.fleet.errDimensionMax', { max: 600 }) })

  for (const f of FARE_FIELDS) {
    const n = parseNum(v.fare[f.key])
    if (!(n >= 0)) e[`fare.${f.key}`] = t('bo.fleet.errFare')
  }
  return e
}

const toInput = (v: Values): VehicleInput => ({
  type: v.type,
  model: v.model.trim(),
  plate: normPlate(v.plate),
  maxWeightKg: parseNum(v.maxWeightKg),
  maxVolumeM3: parseNum(v.maxVolumeM3),
  maxDimensionCm: parseNum(v.maxDimensionCm),
  fare: Object.fromEntries(FARE_FIELDS.map((f) => [f.key, parseNum(v.fare[f.key])])) as unknown as FareConfig,
  status: v.status,
})

function VehicleForm({ vehicle, all, onDone }: { vehicle?: Vehicle; all: Vehicle[]; onDone: () => void }) {
  const api = useApi()
  const qc = useQueryClient()
  const form = useRef<HTMLFormElement>(null)
  const [values, setValues] = useState<Values>(() => toValues(vehicle))
  const [serverError, setServerError] = useState<{ field?: string; message: string }>()
  const { touch, visible, submit, submitted } = useFieldVisibility()
  const errors = validate(values, all, vehicle?.id)
  const err = (key: string) => (visible(key) ? errors[key] : undefined) ?? (serverError?.field === key.replace('fare.', '') || serverError?.field === key ? serverError.message : undefined)

  const set = <K extends keyof Values>(key: K, value: Values[K]) => {
    setValues((v) => ({ ...v, [key]: value }))
    setServerError(undefined)
  }
  const setFare = (key: FareKey, value: string) => setValues((v) => ({ ...v, fare: { ...v.fare, [key]: value } }))

  const save = useMutation({
    mutationFn: () => (vehicle ? api.fleet.vehicles.update(vehicle.id, toInput(values)) : api.fleet.vehicles.create(toInput(values))),
    onSuccess: (v) => {
      toast.success(vehicle ? t('bo.fleet.vehicleToastUpdated', { plate: v.plate }) : t('bo.fleet.vehicleToastAdded', { plate: v.plate }))
      void qc.invalidateQueries({ queryKey: qk.vehicles })
      onDone()
    },
    onError: (e) => setServerError({ field: errorField(e), message: errorMessage(e) }),
  })

  function onSubmit(e: React.FormEvent) {
    e.preventDefault()
    submit()
    if (Object.keys(errors).length > 0) return focusFirstInvalid(form.current)
    save.mutate()
  }

  const create = !vehicle

  return (
    <form ref={form} onSubmit={onSubmit} noValidate id="form-kendaraan" className="space-y-7">
      <section className="space-y-4" aria-labelledby="h-identitas">
        <h3 id="h-identitas" className="text-title">
          {t('bo.fleet.formIdentity')}
        </h3>
        {create ? (
          <Field label={t('bo.fleet.formTypeLabel')} hint={t('bo.fleet.formTypeHint')}>
            <Segmented
              aria-label={t('bo.fleet.formTypeLabel')}
              fill
              value={values.type}
              onValueChange={(kind) => setValues((v) => ({ ...toValues(undefined, kind), model: v.model, plate: v.plate }))}
              options={[
                { value: 'motor', label: VEHICLE_TYPE_LABEL.motor },
                { value: 'blindvan', label: VEHICLE_TYPE_LABEL.blindvan },
              ]}
            />
          </Field>
        ) : (
          <p className="text-small text-muted">{t('bo.fleet.formTypeFixed', { type: VEHICLE_TYPE_LABEL[values.type] })}</p>
        )}
        <Field label={t('bo.fleet.formModel')} error={err('model')}>
          <Input value={values.model} onChange={(e) => set('model', e.target.value)} onBlur={() => touch('model')} placeholder={t('bo.fleet.formModelPlaceholder')} autoFocus={create} />
        </Field>
        <Field label={t('bo.fleet.formPlate')} error={err('plate')}>
          <Input value={values.plate} onChange={(e) => set('plate', e.target.value.toUpperCase())} onBlur={() => touch('plate')} placeholder={t('bo.fleet.formPlatePlaceholder')} autoCapitalize="characters" />
        </Field>
        <Field label={t('bo.fleet.formStatus')}>
          <Select
            aria-label={t('bo.fleet.formStatusAria')}
            value={values.status}
            onValueChange={(s) => set('status', s as VehicleStatus)}
            options={(Object.keys(STATUS_KEY) as VehicleStatus[]).map((s) => ({ value: s, label: t(STATUS_KEY[s]) }))}
          />
        </Field>
        {serverError && !serverError.field && <p role="alert" className="text-small text-danger-fg">{serverError.message}</p>}
        {serverError?.field === 'status' && <p role="alert" className="text-small text-danger-fg">{serverError.message}</p>}
      </section>

      <section className="space-y-4" aria-labelledby="h-kapasitas">
        <div>
          <h3 id="h-kapasitas" className="text-title">
            {t('bo.fleet.formCapacity')}
          </h3>
          <p className="text-small text-muted">{t('bo.fleet.formCapacityHint')}</p>
        </div>
        <div className="grid grid-cols-3 gap-3">
          <Field label={t('bo.fleet.formWeight')} error={err('maxWeightKg')}>
            <Input inputMode="decimal" value={values.maxWeightKg} onChange={(e) => set('maxWeightKg', e.target.value)} onBlur={() => touch('maxWeightKg')} />
          </Field>
          <Field label={t('bo.fleet.formVolume')} error={err('maxVolumeM3')}>
            <Input inputMode="decimal" value={values.maxVolumeM3} onChange={(e) => set('maxVolumeM3', e.target.value)} onBlur={() => touch('maxVolumeM3')} />
          </Field>
          <Field label={t('bo.fleet.formDimension')} error={err('maxDimensionCm')}>
            <Input inputMode="decimal" value={values.maxDimensionCm} onChange={(e) => set('maxDimensionCm', e.target.value)} onBlur={() => touch('maxDimensionCm')} />
          </Field>
        </div>
      </section>

      <section className="space-y-4" aria-labelledby="h-tarif">
        <div>
          <h3 id="h-tarif" className="text-title">
            {t('bo.fleet.formFare')}
          </h3>
          <p className="text-small text-muted">{t('bo.fleet.formFareHint')}</p>
        </div>
        <div className="grid grid-cols-2 gap-x-3 gap-y-4">
          {FARE_FIELDS.map((f) => (
            <Field key={f.key} label={t(f.label)} hint={f.hint ? t(f.hint) : undefined} error={err(`fare.${f.key}`)}>
              <Input
                inputMode="decimal"
                leading={f.money ? <span className="text-small">Rp</span> : undefined}
                trailing={f.unit ? <span className="pr-2 text-small">{f.unit}</span> : undefined}
                value={values.fare[f.key]}
                onChange={(e) => setFare(f.key, e.target.value)}
                onBlur={() => touch(`fare.${f.key}`)}
              />
            </Field>
          ))}
        </div>
      </section>
      {submitted && Object.keys(errors).length > 0 && <p className="text-small text-danger-fg">{t('bo.fleet.errCheckFields')}</p>}
    </form>
  )
}

export function Vehicles() {
  const api = useApi()
  const qc = useQueryClient()
  const vehicles = useVehicles()
  const drivers = useDrivers()
  const [params, setParams] = useSearchParams()
  const [q, setQ] = useState('')
  const [type, setType] = useState<'semua' | VehicleType>('semua')
  const [editing, setEditing] = useState<Vehicle | 'baru' | undefined>(params.get('new') ? 'baru' : undefined)
  const [deleting, setDeleting] = useState<Vehicle>()
  const [deleteError, setDeleteError] = useState<string>()

  const remove = useMutation({
    mutationFn: (id: string) => api.fleet.vehicles.remove(id),
    onSuccess: () => {
      toast.success(t('bo.fleet.vehicleToastDeleted', { plate: deleting?.plate ?? '' }))
      setDeleting(undefined)
      void qc.invalidateQueries({ queryKey: qk.vehicles })
      void qc.invalidateQueries({ queryKey: qk.drivers })
    },
    // galat tampil di dalam dialog (toast berada di luar modal dan tidak diumumkan pembaca layar)
    onError: (e) => setDeleteError(errorMessage(e)),
  })

  const closeSheet = () => {
    setEditing(undefined)
    if (params.get('new')) setParams({}, { replace: true })
  }

  const needle = q.trim().toLowerCase()
  const rows = (vehicles.data ?? []).filter((v) => (type === 'semua' || v.type === type) && (!needle || `${v.plate} ${v.model}`.toLowerCase().includes(needle)))
  const driverOf = (v: Vehicle) => drivers.data?.find((d) => d.vehicleId === v.id)

  return (
    <div className="space-y-4">
      <PageHeader
        title={t('bo.fleet.vehiclesTitle')}
        description={t('bo.fleet.vehiclesDescription')}
        actions={
          <Button variant="primary" iconLeft={<Plus className="size-4" aria-hidden />} onClick={() => setEditing('baru')}>
            {t('bo.fleet.vehiclesAdd')}
          </Button>
        }
      />

      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2.5">
          <Input leading={<Search className="size-4" aria-hidden />} value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('bo.fleet.vehiclesSearch')} aria-label={t('bo.fleet.vehiclesSearchLabel')} className="h-9 w-64" />
          <Select
            aria-label={t('bo.fleet.vehiclesTypeFilter')}
            value={type}
            onValueChange={(v) => setType(v as 'semua' | VehicleType)}
            className="h-9 w-44"
            options={[
              { value: 'semua', label: t('bo.fleet.vehiclesTypeAll') },
              { value: 'motor', label: VEHICLE_TYPE_LABEL.motor },
              { value: 'blindvan', label: VEHICLE_TYPE_LABEL.blindvan },
            ]}
          />
          <span className="ml-auto text-small tabular-nums text-muted">{t('bo.fleet.vehiclesCount', { count: rows.length })}</span>
        </div>

        {vehicles.isError ? (
          <ErrorState message={errorMessage(vehicles.error)} onRetry={() => void vehicles.refetch()} />
        ) : vehicles.isLoading ? (
          <div className="space-y-px p-3">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-12" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <EmptyState
            icon={<Truck />}
            title={q || type !== 'semua' ? t('bo.fleet.vehiclesEmptyFilteredTitle') : t('bo.fleet.vehiclesEmptyTitle')}
            description={q || type !== 'semua' ? t('bo.fleet.vehiclesEmptyFilteredHint') : t('bo.fleet.vehiclesEmptyHint')}
            action={
              q || type !== 'semua' ? (
                <Button
                  onClick={() => {
                    setQ('')
                    setType('semua')
                  }}
                >
                  {t('bo.fleet.resetFilters')}
                </Button>
              ) : (
                <Button variant="primary" onClick={() => setEditing('baru')}>
                  {t('bo.fleet.vehiclesAdd')}
                </Button>
              )
            }
          />
        ) : (
          <TableWrap>
            <THead>
              <tr>
                <TH>{t('bo.fleet.vehiclesColVehicle')}</TH>
                <TH>{t('bo.fleet.vehiclesColType')}</TH>
                <TH>{t('bo.fleet.vehiclesColCapacity')}</TH>
                <TH numeric>{t('bo.fleet.vehiclesColBaseFare')}</TH>
                <TH numeric>{t('bo.fleet.vehiclesColPerKm')}</TH>
                <TH>{t('bo.fleet.vehiclesColDriver')}</TH>
                <TH>{t('bo.fleet.colStatus')}</TH>
                <TH>
                  <span className="sr-only">{t('bo.fleet.colActions')}</span>
                </TH>
              </tr>
            </THead>
            <TBody>
              {rows.map((v) => {
                const Icon = v.type === 'motor' ? Motorbike : Van
                const d = driverOf(v)
                return (
                  <TR key={v.id}>
                    <TD className="whitespace-nowrap py-2">
                      <span className="block font-semibold">{v.plate}</span>
                      <span className="block text-small text-muted">{v.model}</span>
                    </TD>
                    <TD className="whitespace-nowrap">
                      <span className="inline-flex items-center gap-2">
                        <Icon className="size-4 text-subtle" aria-hidden />
                        {VEHICLE_TYPE_LABEL[v.type]}
                      </span>
                    </TD>
                    <TD className="whitespace-nowrap py-2 text-muted">
                      <span className="block tabular-nums">
                        {formatKg(v.maxWeightKg)} · {formatFloat(v.maxVolumeM3)} m³
                      </span>
                      <span className="block text-small">{t('bo.fleet.vehiclesLongestSide', { cm: v.maxDimensionCm })}</span>
                    </TD>
                    <TD numeric className="whitespace-nowrap">
                      {formatRupiah(v.fare.base)}
                    </TD>
                    <TD numeric className="whitespace-nowrap">
                      {formatRupiah(v.fare.perKm)}
                    </TD>
                    <TD className="whitespace-nowrap">{d ? d.name : <span className="text-subtle">—</span>}</TD>
                    <TD className="whitespace-nowrap">
                      <StatusLabel tone={STATUS_TONE[v.status]}>{t(STATUS_KEY[v.status])}</StatusLabel>
                    </TD>
                    <TD className="whitespace-nowrap text-right">
                      <Button size="sm" variant="ghost" iconLeft={<Pencil className="size-3.5" aria-hidden />} onClick={() => setEditing(v)} aria-label={t('bo.fleet.editAria', { name: v.plate })}>
                        {t('bo.fleet.edit')}
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => setDeleting(v)} aria-label={t('bo.fleet.deleteAria', { name: v.plate })}>
                        <Trash2 className="size-3.5" aria-hidden />
                      </Button>
                    </TD>
                  </TR>
                )
              })}
            </TBody>
          </TableWrap>
        )}
      </Card>

      <Sheet
        open={!!editing}
        onOpenChange={(open) => !open && closeSheet()}
        title={editing === 'baru' ? t('bo.fleet.vehiclesAdd') : t('bo.fleet.vehicleSheetEdit', { plate: (editing as Vehicle | undefined)?.plate ?? '' })}
        description={editing === 'baru' ? t('bo.fleet.vehicleSheetAddHint') : t('bo.fleet.vehicleSheetEditHint')}
        className="sm:max-w-xl"
        footer={
          <>
            <Button variant="secondary" onClick={closeSheet}>
              {t('bo.fleet.cancel')}
            </Button>
            <Button variant="primary" type="submit" form="form-kendaraan">
              {t('bo.fleet.save')}
            </Button>
          </>
        }
      >
        {editing && <VehicleForm key={editing === 'baru' ? 'baru' : editing.id} vehicle={editing === 'baru' ? undefined : editing} all={vehicles.data ?? []} onDone={closeSheet} />}
      </Sheet>

      <ConfirmDialog
        open={!!deleting}
        onOpenChange={(open) => {
          if (!open) {
            setDeleting(undefined)
            setDeleteError(undefined)
          }
        }}
        title={deleting ? t('bo.fleet.deleteTitle', { plate: deleting.plate }) : t('bo.fleet.deleteTitleEmpty')}
        description={t('bo.fleet.deleteDescription')}
        confirmLabel={t('bo.fleet.deleteConfirm')}
        tone="danger"
        typeToConfirm={deleting?.plate}
        loading={remove.isPending}
        onConfirm={() => deleting && remove.mutate(deleting.id)}
      >
        {deleteError && (
          <p role="alert" className="mt-3 rounded-input bg-danger-tint px-3 py-2 text-small text-danger-fg">
            {deleteError}
          </p>
        )}
      </ConfirmDialog>
    </div>
  )
}
