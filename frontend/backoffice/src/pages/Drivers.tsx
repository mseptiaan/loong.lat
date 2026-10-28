import { errorField, errorMessage, qk, useApi, useDrivers, useMutation, useQueryClient, useVehicles, useVirtualNow } from '@loong/data'
import {
  DRIVER_STATUS_LABEL,
  VEHICLE_TYPE_LABEL,
  formatAgo,
  formatDecimal,
  formatPhone,
  type Driver,
  type DriverStatus,
  type Vehicle,
} from '@loong/domain'
import {
  Avatar,
  Button,
  Card,
  EmptyState,
  ErrorState,
  Field,
  Input,
  PageHeader,
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
  driverTone,
  toast,
} from '@loong/ui'
import { IdCard, Pencil, Plus, Search, Star } from 'lucide-react'
import { useRef, useState } from 'react'
import { useSearchParams } from 'react-router'
import { t } from '../i18n'
import { focusFirstInvalid, useFieldVisibility } from '../lib/form'

const NO_VEHICLE = '__none__'

// PIN driver: tepat 6 digit. Wajib saat membuat driver; saat mengubah boleh kosong (PIN lama dipertahankan).
const PIN_PATTERN = /^\d{6}$/

function validate(name: string, phone: string, pin: string, creating: boolean): Record<string, string> {
  const e: Record<string, string> = {}
  if (name.trim().length < 3) e.name = t('bo.fleet.driverErrName')
  if (phone.replace(/\D/g, '').length < 9) e.phone = t('bo.fleet.driverErrPhone')
  if ((creating || pin) && !PIN_PATTERN.test(pin)) e.pin = t('bo.fleet.driverErrPin')
  return e
}

function DriverForm({ driver, vehicles, onDone }: { driver?: Driver; vehicles: Vehicle[]; onDone: () => void }) {
  const api = useApi()
  const qc = useQueryClient()
  const form = useRef<HTMLFormElement>(null)
  const [name, setName] = useState(driver?.name ?? '')
  const [phone, setPhone] = useState(driver ? formatPhone(driver.phone) : '')
  const [vehicleId, setVehicleId] = useState(driver?.vehicleId ?? NO_VEHICLE)
  const [status, setStatus] = useState<DriverStatus>(driver?.status ?? 'offline')
  const [pin, setPin] = useState('')
  const [serverError, setServerError] = useState<{ field?: string; message: string }>()
  const { touch, visible, submit } = useFieldVisibility()
  const errors = validate(name, phone, pin, !driver)
  const err = (key: string) => (visible(key) ? errors[key] : undefined) ?? (serverError?.field === key ? serverError.message : undefined)

  // kendaraan yang bisa dipilih: aktif dan belum dipakai driver lain (kendaraan driver ini sendiri tetap muncul)
  const available = vehicles.filter((v) => v.status === 'aktif' && (!v.driverId || v.driverId === driver?.id))
  const working = driver?.status === 'bertugas'

  const save = useMutation({
    mutationFn: () => {
      const vid = vehicleId === NO_VEHICLE ? undefined : vehicleId
      return driver
        ? api.fleet.drivers.update(driver.id, { name: name.trim(), phone, vehicleId: vid ?? '', ...(working ? {} : { status }), ...(pin ? { pin } : {}) })
        : api.fleet.drivers.create({ name: name.trim(), phone, vehicleId: vid, pin })
    },
    onSuccess: (d) => {
      toast.success(driver ? t('bo.fleet.driverToastUpdated', { name: d.name }) : t('bo.fleet.driverToastAdded', { name: d.name }))
      void qc.invalidateQueries({ queryKey: qk.drivers })
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

  return (
    <form ref={form} onSubmit={onSubmit} noValidate id="form-driver" className="space-y-4">
      <Field label={t('bo.fleet.driverFormName')} error={err('name')}>
        <Input value={name} onChange={(e) => {
          setName(e.target.value)
          setServerError(undefined)
        }} onBlur={() => touch('name')} autoFocus={!driver} placeholder={t('bo.fleet.driverFormNamePlaceholder')} />
      </Field>
      <Field label={t('bo.fleet.driverFormPhone')} hint={t('bo.fleet.driverFormPhoneHint')} error={err('phone')}>
        <Input type="tel" inputMode="tel" value={phone} onChange={(e) => {
          setPhone(e.target.value)
          setServerError(undefined)
        }} onBlur={() => touch('phone')} placeholder="0812-3456-7890" />
      </Field>
      <Field label={t('bo.fleet.driverFormVehicle')} hint={working ? t('bo.fleet.driverFormVehicleBusyHint') : t('bo.fleet.driverFormVehicleHint')} error={err('vehicleId')}>
        <Select
          aria-label={t('bo.fleet.driverFormVehicle')}
          value={vehicleId}
          onValueChange={(v) => {
            setVehicleId(v)
            setServerError(undefined)
          }}
          options={[
            { value: NO_VEHICLE, label: t('bo.fleet.driverFormVehicleNone'), description: t('bo.fleet.driverFormVehicleNoneHint') },
            ...available.map((v) => ({ value: v.id, label: `${v.model} · ${v.plate}`, description: VEHICLE_TYPE_LABEL[v.type] })),
          ]}
        />
      </Field>
      {driver && (
        <Field label={t('bo.fleet.driverFormStatus')} hint={working ? t('bo.fleet.driverFormStatusHint') : undefined} error={err('status')}>
          {working ? (
            <p className="text-body">{DRIVER_STATUS_LABEL.bertugas}</p>
          ) : (
            <Select
              aria-label={t('bo.fleet.driverFormStatusAria')}
              value={status}
              onValueChange={(s) => setStatus(s as DriverStatus)}
              options={[
                { value: 'online', label: DRIVER_STATUS_LABEL.online },
                { value: 'offline', label: DRIVER_STATUS_LABEL.offline },
              ]}
            />
          )}
        </Field>
      )}
      <Field label={t('bo.fleet.driverFormPin')} hint={driver ? t('bo.fleet.driverFormPinEditHint') : t('bo.fleet.driverFormPinHint')} error={err('pin')}>
        <Input
          inputMode="numeric"
          autoComplete="off"
          maxLength={6}
          value={pin}
          onChange={(e) => {
            setPin(e.target.value.replace(/\D/g, ''))
            setServerError(undefined)
          }}
          onBlur={() => touch('pin')}
          placeholder="••••••"
        />
      </Field>
      {serverError && !serverError.field && (
        <p role="alert" className="text-small text-danger-fg">
          {serverError.message}
        </p>
      )}
    </form>
  )
}

export function Drivers() {
  const drivers = useDrivers()
  const vehicles = useVehicles()
  const nowMs = useVirtualNow(30_000)
  const [params, setParams] = useSearchParams()
  const [q, setQ] = useState(params.get('search') ?? '')
  const [status, setStatus] = useState<'semua' | DriverStatus>('semua')
  const [editing, setEditing] = useState<Driver | 'baru' | undefined>(params.get('new') ? 'baru' : undefined)

  const closeSheet = () => {
    setEditing(undefined)
    if (params.get('new')) setParams({}, { replace: true })
  }

  const needle = q.trim().toLowerCase()
  const rows = (drivers.data ?? []).filter((d) => (status === 'semua' || d.status === status) && (!needle || `${d.name} ${d.phone}`.toLowerCase().includes(needle)))
  const vehicleOf = (d: Driver) => vehicles.data?.find((v) => v.id === d.vehicleId)

  return (
    <div className="space-y-4">
      <PageHeader
        title={t('bo.fleet.driversTitle')}
        description={t('bo.fleet.driversDescription')}
        actions={
          <Button variant="primary" iconLeft={<Plus className="size-4" aria-hidden />} onClick={() => setEditing('baru')}>
            {t('bo.fleet.driversAdd')}
          </Button>
        }
      />

      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2.5">
          <Input leading={<Search className="size-4" aria-hidden />} value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('bo.fleet.driversSearch')} aria-label={t('bo.fleet.driversSearchLabel')} className="h-9 w-64" />
          <Select
            aria-label={t('bo.fleet.driversStatusFilter')}
            value={status}
            onValueChange={(v) => setStatus(v as 'semua' | DriverStatus)}
            className="h-9 w-44"
            options={[
              { value: 'semua', label: t('bo.fleet.driversStatusAll') },
              { value: 'online', label: DRIVER_STATUS_LABEL.online },
              { value: 'bertugas', label: DRIVER_STATUS_LABEL.bertugas },
              { value: 'offline', label: DRIVER_STATUS_LABEL.offline },
            ]}
          />
          <span className="ml-auto text-small tabular-nums text-muted">{t('bo.fleet.driversCount', { count: rows.length })}</span>
        </div>

        {drivers.isError ? (
          <ErrorState message={errorMessage(drivers.error)} onRetry={() => void drivers.refetch()} />
        ) : drivers.isLoading ? (
          <div className="space-y-px p-3">
            {Array.from({ length: 6 }, (_, i) => (
              <Skeleton key={i} className="h-12" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <EmptyState
            icon={<IdCard />}
            title={q || status !== 'semua' ? t('bo.fleet.driversEmptyFilteredTitle') : t('bo.fleet.driversEmptyTitle')}
            description={q || status !== 'semua' ? t('bo.fleet.driversEmptyFilteredHint') : t('bo.fleet.driversEmptyHint')}
            action={
              q || status !== 'semua' ? (
                <Button
                  onClick={() => {
                    setQ('')
                    setStatus('semua')
                  }}
                >
                  {t('bo.fleet.resetFilters')}
                </Button>
              ) : (
                <Button variant="primary" onClick={() => setEditing('baru')}>
                  {t('bo.fleet.driversAdd')}
                </Button>
              )
            }
          />
        ) : (
          <TableWrap>
            <THead>
              <tr>
                <TH>{t('bo.fleet.driversColDriver')}</TH>
                <TH>{t('bo.fleet.colStatus')}</TH>
                <TH>{t('bo.fleet.driversColVehicle')}</TH>
                <TH numeric>{t('bo.fleet.driversColRating')}</TH>
                <TH numeric>{t('bo.fleet.driversColCompleted')}</TH>
                <TH>{t('bo.fleet.driversColLastSeen')}</TH>
                <TH>
                  <span className="sr-only">{t('bo.fleet.colActions')}</span>
                </TH>
              </tr>
            </THead>
            <TBody>
              {rows.map((d) => {
                const v = vehicleOf(d)
                return (
                  <TR key={d.id}>
                    <TD className="whitespace-nowrap py-2">
                      <span className="flex items-center gap-3">
                        <Avatar name={d.name} />
                        <span>
                          <span className="block font-semibold">{d.name}</span>
                          <span className="block text-small tabular-nums text-muted">{formatPhone(d.phone)}</span>
                        </span>
                      </span>
                    </TD>
                    <TD className="whitespace-nowrap">
                      <StatusLabel tone={driverTone[d.status]} pulse={d.status === 'bertugas'}>
                        {DRIVER_STATUS_LABEL[d.status]}
                      </StatusLabel>
                    </TD>
                    <TD className="whitespace-nowrap py-2">
                      {v ? (
                        <>
                          <span className="block">{v.model}</span>
                          <span className="block text-small text-muted">
                            {v.plate} · {VEHICLE_TYPE_LABEL[v.type]}
                          </span>
                        </>
                      ) : vehicles.data ? (
                        <span className="text-small font-medium text-warning-fg">{t('bo.fleet.driversNoVehicle')}</span>
                      ) : (
                        <Skeleton className="h-4 w-28" />
                      )}
                    </TD>
                    <TD numeric className="whitespace-nowrap">
                      <span className="inline-flex items-center justify-end gap-1">
                        <Star className="size-3 fill-current text-warning" aria-hidden />
                        {formatDecimal(d.rating, 2)}
                      </span>
                    </TD>
                    <TD numeric className="whitespace-nowrap">
                      {d.completedTrips}
                    </TD>
                    <TD className="whitespace-nowrap text-muted">{d.lastSeen ? formatAgo(d.lastSeen, new Date(nowMs)) : '—'}</TD>
                    <TD className="whitespace-nowrap text-right">
                      <Button size="sm" variant="ghost" iconLeft={<Pencil className="size-3.5" aria-hidden />} onClick={() => setEditing(d)} aria-label={t('bo.fleet.editAria', { name: d.name })}>
                        {t('bo.fleet.edit')}
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
        title={editing === 'baru' ? t('bo.fleet.driversAdd') : t('bo.fleet.driverSheetEdit', { name: (editing as Driver | undefined)?.name ?? '' })}
        description={editing === 'baru' ? t('bo.fleet.driverSheetAddHint') : undefined}
        footer={
          <>
            <Button variant="secondary" onClick={closeSheet}>
              {t('bo.fleet.cancel')}
            </Button>
            <Button variant="primary" type="submit" form="form-driver">
              {t('bo.fleet.save')}
            </Button>
          </>
        }
      >
        {editing && <DriverForm key={editing === 'baru' ? 'baru' : editing.id} driver={editing === 'baru' ? undefined : editing} vehicles={vehicles.data ?? []} onDone={closeSheet} />}
      </Sheet>
    </div>
  )
}
