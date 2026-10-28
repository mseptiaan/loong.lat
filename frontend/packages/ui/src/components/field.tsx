import * as RadixCheckbox from '@radix-ui/react-checkbox'
import * as RadixRadio from '@radix-ui/react-radio-group'
import * as RadixSelect from '@radix-ui/react-select'
import * as RadixSwitch from '@radix-ui/react-switch'
import { t } from '@loong/i18n'
import { AlertCircle, Check, ChevronDown, Eye, EyeOff, Minus, Plus } from 'lucide-react'
import { createContext, useContext, useId, useState, type ComponentProps, type ReactNode } from 'react'
import { cn } from '../lib/cn'

/* ---------------- Field: label di atas, petunjuk di atas input, galat di bawah ---------------- */

interface FieldContextValue {
  id: string
  describedBy?: string
  invalid: boolean
}
const FieldContext = createContext<FieldContextValue | null>(null)
const useField = () => useContext(FieldContext)

export interface FieldProps {
  label: ReactNode
  /** Petunjuk, ditampilkan DI ATAS input (tidak tertutup keyboard/autofill). */
  hint?: ReactNode
  /** Galat, ditampilkan di bawah input dengan ikon + teks (bukan hanya warna). */
  error?: ReactNode
  /** Tandai kolom opsional; kolom wajib tidak diberi bintang. */
  optional?: boolean
  /** Elemen di kanan label (mis. tautan "Lupa sandi?"); di luar <label> agar tidak menyarangkan elemen interaktif. */
  aside?: ReactNode
  className?: string
  children: ReactNode
}

export function Field({ label, hint, error, optional, aside, className, children }: FieldProps) {
  const id = useId()
  const hintId = hint ? `${id}-hint` : undefined
  const errorId = error ? `${id}-error` : undefined
  const describedBy = [hintId, errorId].filter(Boolean).join(' ') || undefined
  return (
    <FieldContext.Provider value={{ id, describedBy, invalid: !!error }}>
      <div className={cn('flex flex-col gap-1.5', className)}>
        <div className="flex items-baseline justify-between gap-3">
          <label htmlFor={id} className="text-small font-semibold text-fg">
            {label}
            {optional && <span className="ml-1.5 font-normal text-subtle">{t('ui.field.optional')}</span>}
          </label>
          {aside && <span className="text-small">{aside}</span>}
        </div>
        {hint && (
          <p id={hintId} className="text-small text-muted">
            {hint}
          </p>
        )}
        {children}
        {error && (
          <p id={errorId} role="alert" className="flex items-start gap-1.5 text-small text-danger-fg">
            <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden />
            <span>{error}</span>
          </p>
        )}
      </div>
    </FieldContext.Provider>
  )
}

const controlBase =
  'w-full rounded-input border bg-surface text-fg transition-[border-color,box-shadow] duration-[140ms] ' +
  'placeholder:text-subtle hover:border-subtle focus-visible:border-accent focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-accent ' +
  'aria-invalid:border-danger aria-invalid:hover:border-danger'

/* ---------------- Input ---------------- */

export interface InputProps extends Omit<ComponentProps<'input'>, 'size'> {
  leading?: ReactNode
  trailing?: ReactNode
}

export function Input({ className, leading, trailing, id, ...props }: InputProps) {
  const field = useField()
  const input = (
    <input
      id={id ?? field?.id}
      aria-invalid={field?.invalid || undefined}
      aria-describedby={field?.describedBy}
      className={cn(
        controlBase,
        'h-control px-3 text-body max-sm:text-[16px]',
        leading && 'pl-10',
        trailing && 'pr-10',
        'border-line-input',
        className,
      )}
      {...props}
    />
  )
  if (!leading && !trailing) return input
  return (
    <div className="relative">
      {leading && <span className="pointer-events-none absolute inset-y-0 left-3 flex items-center text-subtle">{leading}</span>}
      {input}
      {trailing && <span className="absolute inset-y-0 right-1.5 flex items-center text-subtle">{trailing}</span>}
    </div>
  )
}

/** Input kata sandi dengan tombol tampilkan/sembunyikan (ada aria-label); tidak memblokir paste. */
export function PasswordInput({ noun = t('ui.password.noun'), ...props }: Omit<InputProps, 'type' | 'trailing' | 'leading'> & { noun?: string }) {
  const [shown, setShown] = useState(false)
  return (
    <Input
      {...props}
      type={shown ? 'text' : 'password'}
      trailing={
        <button
          type="button"
          onClick={() => setShown((s) => !s)}
          aria-label={shown ? t('ui.password.hide', { noun }) : t('ui.password.show', { noun })}
          className="pointer-events-auto grid size-8 place-items-center [[data-density=touch]_&]:size-11 rounded-chip text-muted transition-colors hover:bg-sunken hover:text-fg"
        >
          {shown ? <EyeOff className="size-4" aria-hidden /> : <Eye className="size-4" aria-hidden />}
        </button>
      }
    />
  )
}

export function Textarea({ className, id, ...props }: ComponentProps<'textarea'>) {
  const field = useField()
  return (
    <textarea
      id={id ?? field?.id}
      aria-invalid={field?.invalid || undefined}
      aria-describedby={field?.describedBy}
      className={cn(controlBase, 'min-h-24 border-line-input px-3 py-2.5 text-body max-sm:text-[16px]', className)}
      {...props}
    />
  )
}

/* ---------------- Select (untuk > 5 pilihan; <= 5 pakai Segmented/Radio) ---------------- */

export interface SelectOption {
  value: string
  label: ReactNode
  description?: ReactNode
}

export function Select({
  value,
  onValueChange,
  options,
  placeholder = t('ui.select.placeholder'),
  className,
  'aria-label': ariaLabel,
}: {
  value?: string
  onValueChange: (v: string) => void
  options: SelectOption[]
  placeholder?: string
  className?: string
  'aria-label'?: string
}) {
  const field = useField()
  return (
    <RadixSelect.Root value={value} onValueChange={onValueChange}>
      <RadixSelect.Trigger
        id={field?.id}
        aria-label={ariaLabel}
        aria-invalid={field?.invalid || undefined}
        aria-describedby={field?.describedBy}
        className={cn(
          controlBase,
          'inline-flex h-control items-center justify-between gap-2 border-line-input px-3 text-left text-body data-[placeholder]:text-disabled max-sm:text-[16px]',
          className,
        )}
      >
        <RadixSelect.Value placeholder={placeholder} />
        <RadixSelect.Icon>
          <ChevronDown className="size-4 text-subtle" aria-hidden />
        </RadixSelect.Icon>
      </RadixSelect.Trigger>
      <RadixSelect.Portal>
        <RadixSelect.Content
          position="popper"
          sideOffset={6}
          className="z-[90] max-h-72 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-card border border-line bg-raised shadow-overlay"
        >
          <RadixSelect.Viewport className="p-1.5">
            {options.map((o) => (
              <RadixSelect.Item
                key={o.value}
                value={o.value}
                className="relative flex min-h-hit cursor-pointer select-none items-center rounded-input py-2 pl-8 pr-3 text-body outline-none data-[highlighted]:bg-sunken data-[highlighted]:shadow-[inset_0_0_0_2px_var(--accent)] data-[state=checked]:font-semibold"
              >
                <RadixSelect.ItemIndicator className="absolute left-2.5">
                  <Check className="size-4 text-accent" aria-hidden />
                </RadixSelect.ItemIndicator>
                <div>
                  <RadixSelect.ItemText>{o.label}</RadixSelect.ItemText>
                  {o.description && <p className="text-small text-muted">{o.description}</p>}
                </div>
              </RadixSelect.Item>
            ))}
          </RadixSelect.Viewport>
        </RadixSelect.Content>
      </RadixSelect.Portal>
    </RadixSelect.Root>
  )
}

/* ---------------- Segmented (2-5 pilihan, terlihat semua) ---------------- */

export interface SegmentedOption<T extends string> {
  value: T
  label: ReactNode
  icon?: ReactNode
}

export function Segmented<T extends string>({
  value,
  onValueChange,
  options,
  className,
  'aria-label': ariaLabel,
  fill = false,
}: {
  value: T
  onValueChange: (v: T) => void
  options: SegmentedOption<T>[]
  className?: string
  'aria-label': string
  fill?: boolean
}) {
  return (
    <RadixRadio.Root
      value={value}
      onValueChange={(v) => onValueChange(v as T)}
      aria-label={ariaLabel}
      orientation="horizontal"
      className={cn('inline-flex rounded-input border border-line bg-sunken p-0.5', fill && 'flex w-full', className)}
    >
      {options.map((o) => (
        <RadixRadio.Item
          key={o.value}
          value={o.value}
          className={cn(
            'inline-flex h-[calc(var(--control-h)-0.25rem-2px)] items-center justify-center gap-1.5 rounded-[calc(var(--r-input)-2px)] px-3 text-small font-semibold text-muted',
            'transition-[background-color,color,box-shadow] duration-[140ms] hover:text-fg',
            'data-[state=checked]:border data-[state=checked]:border-line-strong data-[state=checked]:bg-surface data-[state=checked]:font-bold data-[state=checked]:text-fg data-[state=checked]:shadow-press',
            fill && 'flex-1',
          )}
        >
          {o.icon}
          {o.label}
        </RadixRadio.Item>
      ))}
    </RadixRadio.Root>
  )
}

/* ---------------- Checkbox & Switch ---------------- */

export function Checkbox({
  checked,
  onCheckedChange,
  label,
  description,
  invalid,
  className,
}: {
  checked: boolean | 'indeterminate'
  onCheckedChange: (v: boolean) => void
  label: ReactNode
  description?: ReactNode
  /** Menandai kotak bermasalah: batas merah dan `aria-invalid` (agar `focusFirstInvalid` menemukannya). */
  invalid?: boolean
  className?: string
}) {
  const id = useId()
  return (
    <div className={cn('flex items-start gap-3', className)}>
      <RadixCheckbox.Root
        id={id}
        checked={checked}
        onCheckedChange={(v) => onCheckedChange(v === true)}
        aria-invalid={invalid || undefined}
        className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-[6px] border border-line-input bg-surface transition-colors hover:border-subtle aria-invalid:border-danger aria-invalid:hover:border-danger data-[state=checked]:border-accent data-[state=checked]:bg-accent data-[state=indeterminate]:border-accent data-[state=indeterminate]:bg-accent"
      >
        <RadixCheckbox.Indicator className="text-on-accent">
          {checked === 'indeterminate' ? <Minus className="size-3.5" aria-hidden /> : <Check className="size-3.5" aria-hidden />}
        </RadixCheckbox.Indicator>
      </RadixCheckbox.Root>
      <label htmlFor={id} className="cursor-pointer text-body">
        {label}
        {description && <span className="block text-small text-muted">{description}</span>}
      </label>
    </div>
  )
}

export function Switch({
  checked,
  onCheckedChange,
  label,
  description,
  className,
  size = 'md',
}: {
  checked: boolean
  onCheckedChange: (v: boolean) => void
  label: ReactNode
  description?: ReactNode
  className?: string
  size?: 'md' | 'lg'
}) {
  const id = useId()
  const lg = size === 'lg'
  return (
    <div className={cn('flex items-center justify-between gap-4', className)}>
      <label htmlFor={id} className="cursor-pointer text-body">
        <span className={cn(lg && 'text-title')}>{label}</span>
        {description && <span className="block text-small text-muted">{description}</span>}
      </label>
      <RadixSwitch.Root
        id={id}
        checked={checked}
        onCheckedChange={onCheckedChange}
        className={cn(
          'relative shrink-0 rounded-full border border-transparent bg-line-strong transition-colors duration-[220ms] data-[state=checked]:bg-accent',
          lg ? 'h-9 w-16' : 'h-6 w-11',
        )}
      >
        <RadixSwitch.Thumb
          className={cn(
            'block rounded-full bg-white shadow-card transition-transform duration-[220ms] ease-out',
            lg ? 'size-7 translate-x-0.5 data-[state=checked]:translate-x-[30px]' : 'size-[18px] translate-x-0.5 data-[state=checked]:translate-x-[22px]',
          )}
        />
      </RadixSwitch.Root>
    </div>
  )
}

/* ---------------- Stepper angka: tombol - / + selebar 48px, bukan dropdown ---------------- */

export function NumberStepper({
  value,
  onChange,
  min = 1,
  max = 99,
  label,
  className,
}: {
  value: number
  onChange: (v: number) => void
  min?: number
  max?: number
  label: string
  className?: string
}) {
  const clamp = (n: number) => Math.min(max, Math.max(min, n))
  return (
    <div role="group" aria-label={label} className={cn('inline-flex items-center rounded-input border border-line-input bg-surface', className)}>
      <button
        type="button"
        aria-label={t('ui.stepper.decrease', { label })}
        onClick={() => onChange(clamp(value - 1))}
        className="grid size-control place-items-center rounded-l-input text-fg transition-colors hover:bg-sunken active:bg-line aria-disabled:opacity-40"
        aria-disabled={value <= min}
      >
        <Minus className="size-4" aria-hidden />
      </button>
      <input
        inputMode="numeric"
        aria-label={label}
        value={value}
        onChange={(e) => {
          const n = Number(e.target.value.replace(/\D/g, ''))
          if (Number.isFinite(n)) onChange(clamp(n))
        }}
        className="h-control w-12 bg-transparent text-center text-body font-semibold tabular-nums max-sm:text-[16px]"
      />
      <button
        type="button"
        aria-label={t('ui.stepper.increase', { label })}
        onClick={() => onChange(clamp(value + 1))}
        className="grid size-control place-items-center rounded-r-input text-fg transition-colors hover:bg-sunken active:bg-line aria-disabled:opacity-40"
        aria-disabled={value >= max}
      >
        <Plus className="size-4" aria-hidden />
      </button>
    </div>
  )
}
