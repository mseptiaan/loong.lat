import { LOW_CONFIDENCE, catalogName, itemFromCatalog, itemLabel, type Item, type Vehicle } from '@loong/domain'
import { INTL_TAG, getLocale } from '@loong/i18n'
import { Checkbox, Chip, Field, IconButton, Input, Notice, NumberStepper, cn } from '@loong/ui'
import { Motorbike, PackagePlus, Trash2, TriangleAlert, Van } from 'lucide-react'
import { useState } from 'react'
import { t } from '../i18n'
import { blankItem, fitInfo, uid, type FieldErrors } from '../pages/wizard/model'

const QUICK = ['dokumen', 'paket-kecil', 'kardus-sedang', 'makanan', 'laptop', 'kue', 'galon', 'dispenser'] as const

/** Angka untuk isian: pemisah desimal mengikuti bahasa ("1,5" id / "1.5" en), tanpa pemisah ribuan. */
const fieldNumber = (n: number) => (Number.isFinite(n) ? new Intl.NumberFormat(INTL_TAG[getLocale()], { useGrouping: false, maximumFractionDigits: 6 }).format(n) : '')

/** Kolom angka yang menerima koma atau titik desimal ("1,5" / "1.5"), tidak melompat saat dikosongkan. */
function NumField({ value, onChange, label, suffix, invalid, className }: { value: number; onChange: (n: number) => void; label: string; suffix?: string; invalid?: boolean; className?: string }) {
  const [text, setText] = useState(fieldNumber(value))
  return (
    <div className={className}>
      <label className="mb-1 block text-caption font-medium text-muted">{label}</label>
      <div className="relative">
        <Input
          inputMode="decimal"
          aria-label={label}
          aria-invalid={invalid || undefined}
          value={text}
          className={cn(suffix && 'pr-9', invalid && 'border-danger')}
          onChange={(e) => {
            setText(e.target.value)
            const n = Number(e.target.value.replace(',', '.'))
            onChange(Number.isFinite(n) && e.target.value.trim() !== '' ? n : 0)
          }}
          onBlur={() => setText(fieldNumber(value))}
        />
        {suffix && <span className="pointer-events-none absolute inset-y-0 right-3 flex items-center text-small text-subtle">{suffix}</span>}
      </div>
    </div>
  )
}

/** "Dari foto · yakin 85%" (foto atau suara; "periksa" bila keyakinan rendah). */
function confidenceText(source: Item['source'], confidence: number): string {
  const pct = Math.round(confidence * 100)
  const low = confidence < LOW_CONFIDENCE
  if (source === 'foto') return low ? t('dash.fields.fromPhotoCheck', { pct }) : t('dash.fields.fromPhoto', { pct })
  return low ? t('dash.fields.fromVoiceCheck', { pct }) : t('dash.fields.fromVoice', { pct })
}

export function ItemEditor({
  items,
  onChange,
  vehicles,
  errors,
  prefix,
}: {
  items: Item[]
  onChange: (items: Item[]) => void
  /** undefined: armada tidak diketahui di klien, jadi tidak ada petunjuk "muat di motor". */
  vehicles: Vehicle[] | undefined
  errors: FieldErrors
  prefix: string
}) {
  const fit = fitInfo(items, vehicles)
  const set = (id: string, patch: Partial<Item>) => onChange(items.map((it) => (it.id === id ? { ...it, ...patch } : it)))
  // nama barang katalog yang diubah pengguna menjadi teks ketikan biasa: tidak lagi mengikuti bahasa
  const rename = (id: string, name: string) =>
    onChange(
      items.map((it) => {
        if (it.id !== id) return it
        const { catalogKey: _key, ...rest } = it
        void _key
        return { ...rest, name }
      }),
    )

  return (
    <div className="space-y-4">
      <div>
        <p className="mb-2 text-small font-semibold">{t('dash.fields.quickAdd')}</p>
        <div className="flex flex-wrap gap-2">
          {QUICK.map((key) => (
            <button
              key={key}
              type="button"
              onClick={() => onChange([...items, itemFromCatalog(key, { id: uid('itm') })])}
              className="inline-flex h-9 items-center gap-1.5 rounded-chip border border-line bg-surface px-3 text-small font-medium text-fg transition-colors hover:border-line-strong hover:bg-sunken active:scale-[0.98]"
            >
              <PackagePlus className="size-3.5 text-muted" aria-hidden />
              {catalogName(key).replace(/ \(.*\)/, '')}
            </button>
          ))}
          <button
            type="button"
            onClick={() => onChange([...items, blankItem()])}
            className="inline-flex h-9 items-center gap-1.5 rounded-chip border border-dashed border-line-strong px-3 text-small font-medium text-muted transition-colors hover:text-fg"
          >
            {t('dash.fields.manual')}
          </button>
        </div>
      </div>

      {errors[`${prefix}.items`] && <Notice tone="danger">{errors[`${prefix}.items`]}</Notice>}

      {items.length > 0 && (
        <ul className="space-y-3">
          {items.map((it, j) => (
            <li key={it.id} className="rounded-card border border-line bg-surface p-3.5">
              <div className="flex items-start gap-3">
                <Field label={t('dash.fields.itemName')} error={errors[`${prefix}.i${j}.name`]} className="min-w-0 flex-1">
                  <Input value={itemLabel(it)} onChange={(e) => rename(it.id, e.target.value)} placeholder={t('dash.fields.itemExample')} />
                </Field>
                <div className="space-y-1.5">
                  <span className="block text-small font-semibold">{t('dash.fields.quantity')}</span>
                  <NumberStepper label={t('dash.fields.qtyOf', { name: itemLabel(it) || t('dash.fields.itemFallback') })} value={it.qty} onChange={(qty) => set(it.id, { qty })} max={50} />
                </div>
                <IconButton label={t('dash.fields.removeItem', { name: itemLabel(it) || t('dash.fields.itemFallback') })} icon={<Trash2 className="size-4" />} className="mt-6 shrink-0" onClick={() => onChange(items.filter((x) => x.id !== it.id))} />
              </div>
              <div className="mt-3 grid grid-cols-2 gap-3 sm:grid-cols-[6rem_1fr_1fr_1fr_auto] sm:items-end">
                <NumField label={t('dash.fields.unitWeight')} suffix="kg" value={it.weightKg} onChange={(n) => set(it.id, { weightKg: n })} invalid={!!errors[`${prefix}.i${j}.weight`]} className="col-span-2 sm:col-span-1" />
                <NumField label={t('dash.fields.length')} suffix="cm" value={it.lengthCm} onChange={(n) => set(it.id, { lengthCm: n })} invalid={!!errors[`${prefix}.i${j}.dims`]} />
                <NumField label={t('dash.fields.width')} suffix="cm" value={it.widthCm} onChange={(n) => set(it.id, { widthCm: n })} invalid={!!errors[`${prefix}.i${j}.dims`]} />
                <NumField label={t('dash.fields.height')} suffix="cm" value={it.heightCm} onChange={(n) => set(it.id, { heightCm: n })} invalid={!!errors[`${prefix}.i${j}.dims`]} />
                <div className="col-span-2 flex h-control items-center sm:col-span-1">
                  <Checkbox checked={it.fragile} onCheckedChange={(fragile) => set(it.id, { fragile })} label={t('dash.fields.fragile')} />
                </div>
              </div>
              {(errors[`${prefix}.i${j}.weight`] || errors[`${prefix}.i${j}.dims`]) && (
                <p role="alert" className="mt-2 text-small text-danger-fg">
                  {errors[`${prefix}.i${j}.weight`] ?? errors[`${prefix}.i${j}.dims`]}
                </p>
              )}
              {it.source !== 'manual' && it.confidence !== undefined && (
                <p className="mt-2">
                  <Chip
                    className={it.confidence < LOW_CONFIDENCE ? 'bg-warning-tint text-warning-fg' : undefined}
                    icon={it.confidence < LOW_CONFIDENCE ? <TriangleAlert className="size-3" aria-hidden /> : undefined}
                  >
                    {confidenceText(it.source, it.confidence)}
                  </Chip>
                </p>
              )}
            </li>
          ))}
        </ul>
      )}

      {errors[`${prefix}.fit`] && <Notice tone="danger">{errors[`${prefix}.fit`]}</Notice>}

      {items.length > 0 && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-card bg-sunken px-3.5 py-2.5 text-small" aria-live="polite">
          <span className="font-semibold tabular-nums">
            {t('dash.fields.itemsKg', { count: fit.agg.itemCount, kg: fit.agg.weightKg })}
          </span>
          {!fit.unknown && fit.type === 'motor' && (
            <span className="inline-flex items-center gap-1.5 text-muted">
              <Motorbike className="size-4" aria-hidden />
              {t('dash.fields.fitsMotor')}
            </span>
          )}
          {!fit.unknown && fit.type === 'blindvan' && (
            <span className="inline-flex items-center gap-1.5 text-muted">
              <Van className="size-4" aria-hidden />
              {fit.reason ? t('dash.fields.needsVanWhy', { why: fit.reason }) : t('dash.fields.needsVan')}
            </span>
          )}
          {!fit.unknown && !fit.type && <span className="text-danger-fg">{t('dash.fields.overCapacity')}</span>}
        </div>
      )}
    </div>
  )
}
