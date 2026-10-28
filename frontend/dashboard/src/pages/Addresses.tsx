import { errorField, errorMessage, useApi, useMutation, useProfile, useQueryClient, qk } from '@loong/data'
import { formatPhone, type PlaceRef, type SavedAddress } from '@loong/domain'
import { Button, Card, EmptyState, ErrorState, Field, Input, IconButton, Notice, PageHeader, Segmented, Sheet, Skeleton, toast } from '@loong/ui'
import { House, MapPin, Pencil, Plus, Store, Trash2 } from 'lucide-react'
import { useState } from 'react'
import { PlaceField } from '../components/PlaceField'
import { t } from '../i18n'
import { ADDRESS_PRESETS, addressKind, addressLabel } from '../lib/address'

// nilai tersimpan tetap 'Rumah' dst (data); teksnya dibaca saat render agar mengikuti bahasa
const labelOptions = () => [
  { value: 'Rumah', label: t('dash.account.labelHome'), icon: <House className="size-3.5" aria-hidden /> },
  { value: 'Toko', label: t('dash.account.labelShop'), icon: <Store className="size-3.5" aria-hidden /> },
  { value: 'Kantor', label: t('dash.account.labelOffice') },
  { value: 'Lainnya', label: t('dash.account.labelOther') },
]

type Draft = { label: string; place?: PlaceRef; contactName: string; contactPhone: string }
type Errors = Partial<Record<'label' | 'place' | 'contactName' | 'contactPhone' | 'form', string>>

function AddressSheet({ open, onOpenChange, editing, defaults }: { open: boolean; onOpenChange: (o: boolean) => void; editing?: SavedAddress; defaults: { name: string; phone: string } }) {
  const api = useApi()
  const qc = useQueryClient()
  const [draft, setDraft] = useState<Draft>(() => ({
    label: editing?.label ?? 'Rumah',
    place: editing?.place,
    contactName: editing?.contactName ?? defaults.name,
    contactPhone: editing?.contactPhone ?? defaults.phone,
  }))
  const [errors, setErrors] = useState<Errors>({})

  const save = useMutation({
    mutationFn: async () => {
      const input = { label: draft.label, place: draft.place!, contactName: draft.contactName.trim(), contactPhone: draft.contactPhone.trim() }
      return editing ? api.account.addresses.update(editing.id, input) : api.account.addresses.add(input)
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.profile })
      toast.success(editing ? t('dash.account.adrUpdated') : t('dash.account.adrSaved'))
      onOpenChange(false)
    },
    onError: (err) => {
      const f = errorField(err)
      setErrors(f ? { [f]: errorMessage(err) } : { form: errorMessage(err) })
    },
  })

  function submit() {
    const next: Errors = {}
    if (!draft.place) next.place = t('dash.account.adrErrPlace')
    if (!draft.contactName.trim()) next.contactName = t('dash.account.adrErrContact')
    if (draft.contactPhone.replace(/\D/g, '').length < 9) next.contactPhone = t('dash.app.errPhone')
    setErrors(next)
    if (Object.keys(next).length === 0) save.mutate()
  }

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      title={editing ? t('dash.account.adrSheetEdit') : t('dash.account.adrSheetAdd')}
      description={t('dash.account.adrSheetHint')}
      footer={
        <>
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            {t('dash.account.adrCancel')}
          </Button>
          <Button variant="primary" loading={save.isPending} onClick={submit}>
            {editing ? t('dash.account.adrSaveChanges') : t('dash.account.adrSave')}
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        {errors.form && <Notice tone="danger">{errors.form}</Notice>}
        <Field label={t('dash.account.adrNameLabel')} error={errors.label}>
          <Segmented
            aria-label={t('dash.account.adrNameLabel')}
            value={ADDRESS_PRESETS.some((v) => v === draft.label) ? draft.label : 'Lainnya'}
            onValueChange={(label) => setDraft((d) => ({ ...d, label }))}
            options={labelOptions()}
            fill
          />
        </Field>
        <PlaceField label={t('dash.account.adrPlace')} value={draft.place} onChange={(place) => setDraft((d) => ({ ...d, place }))} error={errors.place} />
        <Field label={t('dash.account.adrContactName')} error={errors.contactName}>
          <Input value={draft.contactName} onChange={(e) => setDraft((d) => ({ ...d, contactName: e.target.value }))} autoComplete="off" />
        </Field>
        <Field label={t('dash.account.adrContactPhone')} error={errors.contactPhone}>
          <Input type="tel" inputMode="tel" value={draft.contactPhone} onChange={(e) => setDraft((d) => ({ ...d, contactPhone: e.target.value }))} />
        </Field>
      </div>
    </Sheet>
  )
}

export function Addresses() {
  const api = useApi()
  const qc = useQueryClient()
  const { data: profile, isLoading, isError, error, refetch, isRefetching } = useProfile()
  const [editing, setEditing] = useState<SavedAddress | undefined>()
  const [open, setOpen] = useState(false)

  const remove = useMutation({
    mutationFn: async (adr: SavedAddress) => {
      await api.account.addresses.remove(adr.id)
      return adr
    },
    onSuccess: (adr) => {
      void qc.invalidateQueries({ queryKey: qk.profile })
      // hapus bisa dibatalkan: dampaknya kecil, jadi langsung dijalankan dengan tombol Urungkan
      toast(t('dash.account.adrDeleted', { label: addressLabel(adr.label) }), {
        action: {
          label: t('dash.account.adrUndo'),
          onClick: () => {
            void api.account.addresses
              .add({ label: adr.label, place: adr.place, contactName: adr.contactName, contactPhone: adr.contactPhone })
              .then(() => qc.invalidateQueries({ queryKey: qk.profile }))
          },
        },
      })
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  const openNew = () => {
    setEditing(undefined)
    setOpen(true)
  }

  return (
    <div className="space-y-6">
      <PageHeader
        title={t('dash.account.adrTitle')}
        description={t('dash.account.adrLead')}
        actions={
          <Button variant="secondary" iconLeft={<Plus className="size-4" aria-hidden />} onClick={openNew}>
            {t('dash.account.adrAdd')}
          </Button>
        }
      />

      {isError ? (
        <Card>
          <ErrorState message={errorMessage(error)} onRetry={() => void refetch()} retrying={isRefetching} />
        </Card>
      ) : isLoading ? (
        <div className="grid gap-4 md:grid-cols-2">
          {[0, 1].map((i) => (
            <Skeleton key={i} className="h-36 rounded-card" />
          ))}
        </div>
      ) : !profile?.savedAddresses.length ? (
        <Card>
          <EmptyState
            icon={<MapPin />}
            title={t('dash.account.adrEmptyTitle')}
            description={t('dash.account.adrEmptyHint')}
            action={
              <Button variant="tint" iconLeft={<Plus className="size-4" aria-hidden />} onClick={openNew}>
                {t('dash.account.adrAdd')}
              </Button>
            }
          />
        </Card>
      ) : (
        <ul className="grid gap-4 md:grid-cols-2">
          {profile.savedAddresses.map((a) => (
            <li key={a.id}>
              <Card className="flex h-full flex-col gap-3 p-card">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <p className="flex items-center gap-2 text-title">
                      {addressKind(a.label) === 'home' ? <House className="size-[18px] text-muted" aria-hidden /> : addressKind(a.label) === 'shop' ? <Store className="size-[18px] text-muted" aria-hidden /> : <MapPin className="size-[18px] text-muted" aria-hidden />}
                      {addressLabel(a.label)}
                    </p>
                    <p className="mt-1 text-body font-semibold">{a.place.name}</p>
                    <p className="text-small text-muted">{a.place.address}</p>
                  </div>
                  <div className="-mr-2 -mt-1 flex shrink-0">
                    <IconButton
                      label={t('dash.account.adrEditLabel', { label: addressLabel(a.label) })}
                      icon={<Pencil className="size-4" />}
                      onClick={() => {
                        setEditing(a)
                        setOpen(true)
                      }}
                    />
                    <IconButton label={t('dash.account.adrDeleteLabel', { label: addressLabel(a.label) })} icon={<Trash2 className="size-4" />} onClick={() => remove.mutate(a)} />
                  </div>
                </div>
                <p className="mt-auto border-t border-line pt-3 text-small text-muted">
                  {t('dash.account.adrContact', { name: a.contactName, phone: formatPhone(a.contactPhone) })}
                </p>
              </Card>
            </li>
          ))}
        </ul>
      )}

      {/* key = ulangi form setiap kali dibuka agar isian lama tidak terbawa */}
      {open && (
        <AddressSheet
          key={editing?.id ?? 'baru'}
          open={open}
          onOpenChange={setOpen}
          editing={editing}
          defaults={{ name: profile?.name ?? '', phone: profile?.phone ?? '' }}
        />
      )}
    </div>
  )
}

