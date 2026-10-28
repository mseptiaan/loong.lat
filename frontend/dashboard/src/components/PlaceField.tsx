import { useApi, useQuery } from '@loong/data'
import type { Place, PlaceRef, SavedAddress } from '@loong/domain'
import { Button, Field, Input, Popover, PopoverAnchor, PopoverContent, Spinner, cn } from '@loong/ui'
import { House, MapPin, Search, Store } from 'lucide-react'
import { useId, useState } from 'react'
import { t } from '../i18n'
import { useDebounced } from '../lib/forms'
import { PlaceMapPicker } from './PlaceMapPicker'
import { addressLabel } from '../lib/address'

const toRef = (p: Place): PlaceRef => ({ placeId: p.id, name: p.name, address: p.address, position: p.position })
const savedIcon = (label: string) => (/toko|usaha|kios|shop|store|business/i.test(label) ? Store : /rumah|home|house/i.test(label) ? House : MapPin)

/**
 * Kolom lokasi: cari (mal, stasiun, kampus, alamat), pilih dari alamat tersimpan, atau tunjuk di peta.
 * Pola combobox ARIA: panah atas/bawah memindah sorotan, Enter memilih, Escape menutup.
 */
export function PlaceField({
  label,
  value,
  onChange,
  error,
  saved = [],
  mapTitle,
  placeholder,
}: {
  label: string
  value?: PlaceRef
  onChange: (place: PlaceRef | undefined) => void
  error?: string
  saved?: SavedAddress[]
  mapTitle?: string
  placeholder?: string
}) {
  const api = useApi()
  const listId = useId()
  const [text, setText] = useState('')
  const [editing, setEditing] = useState(false)
  const [open, setOpen] = useState(false)
  const [active, setActive] = useState(0)
  const [mapOpen, setMapOpen] = useState(false)
  const query = useDebounced(text.trim(), 200)

  const results = useQuery({
    queryKey: ['places', query],
    queryFn: () => api.places.search(query, 6),
    enabled: editing && query.length >= 2,
    staleTime: 60_000,
  })

  const shown = editing ? text : (value?.name ?? '')
  const places = query.length >= 2 ? (results.data ?? []) : []
  const showSaved = !text.trim() && saved.length > 0
  const options: { key: string; ref: PlaceRef; sub: string; icon: React.ReactNode }[] = showSaved
    ? saved.map((s) => {
        const Icon = savedIcon(s.label)
        return { key: s.id, ref: s.place, sub: `${addressLabel(s.label)} · ${s.place.address}`, icon: <Icon className="size-4" aria-hidden /> }
      })
    : places.map((p) => ({ key: p.id, ref: toRef(p), sub: p.address, icon: <MapPin className="size-4" aria-hidden /> }))

  const select = (ref: PlaceRef) => {
    onChange(ref)
    setText('')
    setEditing(false)
    setOpen(false)
  }

  return (
    <div className="space-y-2">
      <Field label={label} error={error}>
        <Popover open={open && (options.length > 0 || results.isFetching || (query.length >= 2 && !results.isFetching))} onOpenChange={setOpen}>
          <PopoverAnchor asChild>
            <div>
              <Input
                role="combobox"
                aria-expanded={open}
                aria-controls={listId}
                aria-autocomplete="list"
                aria-activedescendant={open && options[active] ? `${listId}-${options[active]!.key}` : undefined}
                autoComplete="off"
                leading={<Search className="size-4" aria-hidden />}
                placeholder={editing && value ? value.name : (placeholder ?? t('dash.fields.placeholder'))}
                value={shown}
                onFocus={(e) => {
                  setEditing(true)
                  setText('')
                  setOpen(true)
                  e.currentTarget.select()
                }}
                onBlur={() => {
                  // beri waktu klik pada opsi sebelum menutup
                  setTimeout(() => {
                    setEditing(false)
                    setOpen(false)
                  }, 120)
                }}
                onChange={(e) => {
                  setText(e.target.value)
                  setEditing(true)
                  setOpen(true)
                  setActive(0)
                  if (value) onChange(undefined)
                }}
                onKeyDown={(e) => {
                  if (e.key === 'ArrowDown') {
                    e.preventDefault()
                    setOpen(true)
                    setActive((i) => Math.min(options.length - 1, i + 1))
                  } else if (e.key === 'ArrowUp') {
                    e.preventDefault()
                    setActive((i) => Math.max(0, i - 1))
                  } else if (e.key === 'Enter' && open && options[active]) {
                    e.preventDefault()
                    select(options[active]!.ref)
                  } else if (e.key === 'Escape') {
                    setOpen(false)
                  }
                }}
              />
            </div>
          </PopoverAnchor>
          <PopoverContent
            align="start"
            sideOffset={4}
            onOpenAutoFocus={(e) => e.preventDefault()}
            className="w-[var(--radix-popover-trigger-width)] p-1.5"
          >
            <ul id={listId} role="listbox" aria-label={t('dash.fields.placeResults', { label })} className="max-h-72 overflow-y-auto">
              {showSaved && <li role="presentation" className="px-3 pb-1 pt-1.5 text-caption font-semibold text-subtle">{t('dash.fields.savedAddresses')}</li>}
              {options.map((o, i) => (
                <li
                  key={o.key}
                  id={`${listId}-${o.key}`}
                  role="option"
                  aria-selected={i === active}
                  onMouseDown={(e) => {
                    e.preventDefault()
                    select(o.ref)
                  }}
                  onMouseEnter={() => setActive(i)}
                  className={cn('flex min-h-hit cursor-pointer items-start gap-3 rounded-input px-3 py-2', i === active && 'bg-sunken')}
                >
                  <span className="mt-0.5 text-muted">{o.icon}</span>
                  <span className="min-w-0">
                    <span className="block text-body font-semibold">{o.ref.name}</span>
                    <span className="block truncate text-small text-muted">{o.sub}</span>
                  </span>
                </li>
              ))}
              {results.isFetching && options.length === 0 && (
                <li className="px-3 py-3">
                  <Spinner label={t('dash.fields.searching')} />
                </li>
              )}
              {!results.isFetching && query.length >= 2 && options.length === 0 && (
                <li className="px-3 py-3 text-small text-muted">
                  {t('dash.fields.noResults', { query })}
                </li>
              )}
            </ul>
          </PopoverContent>
        </Popover>
      </Field>

      {value && !editing && <p className="flex items-start gap-1.5 text-small text-muted"><MapPin className="mt-0.5 size-3.5 shrink-0" aria-hidden />{value.address}</p>}

      <div className="flex flex-wrap items-center gap-2">
        {saved.slice(0, 3).map((s) => {
          const Icon = savedIcon(s.label)
          return (
            <button
              key={s.id}
              type="button"
              onClick={() => select(s.place)}
              className="inline-flex h-8 items-center gap-1.5 rounded-chip border border-line bg-surface px-2.5 text-small font-medium text-muted transition-colors hover:border-line-strong hover:text-fg"
            >
              <Icon className="size-3.5" aria-hidden />
              {addressLabel(s.label)}
            </button>
          )
        })}
        <Button variant="link" className="text-small" onClick={() => setMapOpen(true)} iconLeft={<MapPin className="size-3.5" aria-hidden />}>
          {t('dash.fields.pickOnMap')}
        </Button>
      </div>

      <PlaceMapPicker
        open={mapOpen}
        onOpenChange={setMapOpen}
        initial={value?.position}
        title={mapTitle ?? t('dash.fields.mapTitle', { label: label.toLowerCase() })}
        onPick={(p) => onChange(p)}
      />
    </div>
  )
}
