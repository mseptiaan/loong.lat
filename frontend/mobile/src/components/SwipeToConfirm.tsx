import { cn } from '@loong/ui'
import { ArrowRight, Loader2 } from 'lucide-react'
import { useEffect, useRef, useState, type MouseEvent, type PointerEvent } from 'react'
import { t } from '../i18n'
import { haptic } from '../lib/device'

const THUMB = 56 // px, = size-14
const INSET = 4 // jarak thumb ke tepi trek
const THRESHOLD = 0.82 // pecahan trek yang harus dilewati agar dianggap konfirmasi
const ARM_MS = 3000 // jendela waktu untuk ketukan kedua

/**
 * Konfirmasi untuk aksi penting yang tak boleh terpicu tak sengaja di saku atau saat motor berguncang. Tiga cara,
 * agar tak ada yang bergantung pada gestur menyeret (WCAG 2.5.1/2.5.7):
 *  1. geser thumb ke kanan;
 *  2. ketuk thumb DUA KALI dalam 3 detik (ketukan pertama hanya "menyiapkan" dan berubah label);
 *  3. keyboard / teknologi bantu: Enter atau Spasi pada thumb (klik tanpa pointer, `detail === 0`).
 */
export function SwipeToConfirm({
  label,
  busyLabel = t('drv.stop.swipeBusy'),
  onConfirm,
  className,
}: {
  label: string
  busyLabel?: string
  onConfirm: () => Promise<unknown> | void
  className?: string
}) {
  const track = useRef<HTMLDivElement>(null)
  const gesture = useRef({ startX: 0, moved: 0 })
  const armTimer = useRef<number | undefined>(undefined)
  const [dx, setDx] = useState(0)
  const [max, setMax] = useState(0) // lebar jalur geser, diukur di event handler (ref tidak boleh dibaca saat render)
  const [phase, setPhase] = useState<'idle' | 'drag' | 'sending'>('idle')
  const [armed, setArmed] = useState(false)

  useEffect(() => () => window.clearTimeout(armTimer.current), [])

  const measure = (): number => {
    const m = Math.max(0, (track.current?.clientWidth ?? 0) - THUMB - INSET * 2)
    setMax(m)
    return m
  }

  function disarm() {
    window.clearTimeout(armTimer.current)
    setArmed(false)
  }

  async function confirm() {
    if (phase === 'sending') return
    disarm()
    setPhase('sending')
    setDx(measure())
    haptic()
    try {
      await onConfirm()
    } catch {
      /* induk menampilkan galatnya; di sini cukup mengembalikan thumb */
    } finally {
      setDx(0)
      setPhase('idle')
    }
  }

  function arm() {
    haptic(8)
    setArmed(true)
    window.clearTimeout(armTimer.current)
    armTimer.current = window.setTimeout(() => setArmed(false), ARM_MS)
  }

  const down = (e: PointerEvent<HTMLButtonElement>) => {
    if (phase === 'sending') return
    e.currentTarget.setPointerCapture(e.pointerId)
    measure()
    gesture.current = { startX: e.clientX, moved: 0 }
    setPhase('drag')
  }
  const move = (e: PointerEvent<HTMLButtonElement>) => {
    if (phase !== 'drag') return
    const raw = e.clientX - gesture.current.startX
    gesture.current.moved = Math.max(gesture.current.moved, Math.abs(raw))
    setDx(Math.min(max, Math.max(0, raw)))
  }
  const up = (e: PointerEvent<HTMLButtonElement>) => {
    if (phase !== 'drag') return
    e.currentTarget.releasePointerCapture(e.pointerId)
    if (max > 0 && dx >= max * THRESHOLD) {
      void confirm()
      return
    }
    setDx(0)
    setPhase('idle')
    // ketukan (bukan geseran): ketukan kedua dalam jendela waktu = konfirmasi
    if (gesture.current.moved < 8) {
      if (armed) void confirm()
      else arm()
    } else {
      disarm()
    }
  }
  const click = (e: MouseEvent<HTMLButtonElement>) => {
    // klik dari keyboard / teknologi bantu tidak punya pointer (detail 0): konfirmasi langsung
    if (e.detail === 0) void confirm()
  }

  const progress = max > 0 ? dx / max : 0
  const sending = phase === 'sending'
  const hint = t('drv.stop.swipeConfirmHint')

  return (
    <div
      ref={track}
      role="group"
      aria-label={label}
      className={cn('relative h-16 w-full select-none overflow-hidden rounded-full border border-accent/25 bg-accent-tint', className)}
    >
      <div
        aria-hidden
        className="absolute inset-y-0 left-0 rounded-full bg-accent/15"
        style={{ width: dx + THUMB + INSET * 2, transition: phase === 'drag' ? 'none' : 'width 220ms var(--ease-out)' }}
      />
      <p
        aria-hidden
        className="absolute inset-0 grid place-items-center pl-14 pr-3 text-center text-body font-semibold text-accent"
        style={{ opacity: sending ? 0.9 : Math.max(0.15, 1 - progress * 1.4) }}
      >
        {sending ? busyLabel : armed ? hint : label}
      </p>
      {/* diumumkan ke pembaca layar; teks visual di atas disembunyikan agar tidak terbaca dua kali */}
      <span role="status" className="sr-only">
        {sending ? busyLabel : armed ? hint : ''}
      </span>
      <button
        type="button"
        aria-label={t('drv.stop.swipeAria', { label })}
        aria-busy={sending || undefined}
        onPointerDown={down}
        onPointerMove={move}
        onPointerUp={up}
        onPointerCancel={up}
        onClick={click}
        className={cn(
          'absolute top-1 grid size-14 touch-none place-items-center rounded-full bg-accent text-on-accent shadow-press outline-offset-2 active:cursor-grabbing motion-reduce:transition-none',
          armed && 'ring-4 ring-accent/30',
        )}
        style={{ left: INSET, transform: `translateX(${dx}px)`, transition: phase === 'drag' ? 'none' : 'transform 220ms var(--ease-out)' }}
      >
        {sending ? <Loader2 className="size-6 animate-spin" aria-hidden /> : <ArrowRight className="size-6" aria-hidden />}
      </button>
    </div>
  )
}
