import type { StopKind, VehicleType } from '@loong/domain'
import { t } from '@loong/i18n'
import { Check, MapPin, Motorbike, Van, X } from 'lucide-react'
import { cn } from '../lib/cn'

export type StopPinState = 'default' | 'active' | 'done' | 'problem'

/**
 * Penanda stop. Ambil = lingkaran, antar = kotak membulat (bentuk, bukan hanya warna, yang membedakan).
 * Netral gelap supaya aksen tersisa untuk rute terpilih; stop aktif memakai aksen.
 */
export function StopPin({
  index,
  to,
  kind,
  state = 'default',
  className,
}: {
  index: number
  /** Nomor stop terakhir bila penanda ini mewakili beberapa stop berurutan di satu tempat ("1–4"). */
  to?: number
  kind: StopKind
  state?: StopPinState
  className?: string
}) {
  const range = to !== undefined && to > index
  const shape = kind === 'ambil' ? 'rounded-full' : 'rounded-[8px]'

  const tone =
    state === 'active'
      ? 'border-accent bg-accent text-on-accent'
      : state === 'done'
        ? 'border-line-strong bg-surface text-muted'
        : state === 'problem'
          ? 'border-danger-fg bg-danger-fg text-on-accent'
          : kind === 'ambil'
            ? 'border-fg bg-surface text-fg'
            : 'border-fg bg-fg text-white'
  return (
    <span
      role="img"
      aria-label={
        range
          ? t(kind === 'ambil' ? 'ui.stopPin.pickupRange' : 'ui.stopPin.dropoffRange', { from: index, to })
          : kind === 'ambil'
            ? t('ui.stopPin.pickup', { index })
            : t('ui.stopPin.dropoff', { index })
      }
      className={cn(
        'grid h-[26px] place-items-center border-2 text-[0.6875rem] font-bold leading-none tabular-nums shadow-card',
        range ? 'min-w-[26px] px-1.5' : 'w-[26px]',
        shape,
        tone,
        className,
      )}
    >
      {state === 'done' ? <Check className="size-3.5" aria-hidden /> : state === 'problem' ? <X className="size-3.5" aria-hidden /> : range ? `${index}–${to}` : index}
    </span>
  )
}

/** Penanda kendaraan: ikon motor/van pada lingkaran aksen, panah kecil menunjuk arah gerak. */
export function VehiclePin({
  type,
  bearing = 0,
  label,
  muted,
  className,
}: {
  type: VehicleType
  bearing?: number
  label?: string
  muted?: boolean
  className?: string
}) {
  const Icon = type === 'motor' ? Motorbike : Van
  return (
    <span className={cn('relative flex flex-col items-center', className)}>
      <span className="relative grid size-9 place-items-center">
        <span
          aria-hidden
          className="absolute inset-0"
          style={{ transform: `rotate(${bearing}deg)`, transition: 'transform 600ms var(--ease-out)' }}
        >
          <span className="absolute left-1/2 top-[-3px] size-0 -translate-x-1/2 border-x-[5px] border-b-[7px] border-x-transparent border-b-current text-accent" />
        </span>
        <span
          className={cn(
            'grid size-8 place-items-center rounded-full border-2 border-white shadow-overlay',
            muted ? 'bg-subtle text-white' : 'animate-[pulse-dot_1.8s_ease-in-out_infinite] bg-accent text-on-accent',
          )}
        >
          <Icon className="size-[18px]" aria-hidden />
        </span>
      </span>
      {label && (
        <span className="-mt-0.5 max-w-28 truncate rounded-chip bg-surface px-1.5 py-0.5 text-[0.6875rem] font-semibold text-fg shadow-card">{label}</span>
      )}
    </span>
  )
}

/** Pin untuk memilih lokasi di peta. */
export function PickerPin({ className }: { className?: string }) {
  return (
    <span className={cn('relative -translate-y-1/2 drop-shadow-md', className)}>
      <MapPin className="size-9 fill-accent text-white" strokeWidth={1.75} aria-hidden />
    </span>
  )
}

/** Titik kecil untuk driver yang online tanpa tugas. */
export function IdleDriverDot({ label }: { label: string }) {
  return (
    <span role="img" aria-label={t('ui.driverDot.available', { label })} className="block size-3.5 rounded-full border-2 border-white bg-success shadow-card" />
  )
}
