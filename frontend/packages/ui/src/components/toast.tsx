import { Toaster as Sonner, toast as sonnerToast } from 'sonner'

/**
 * Toast mengikuti token. Dipakai untuk konfirmasi singkat (mis. "Tersimpan"). Galat yang menghalangi pengguna harus juga tampil
 * inline di dekat aksinya (toast berada di luar dialog dan tidak diumumkan pembaca layar saat dialog terbuka).
 */
export function Toaster({ position = 'top-center' }: { position?: 'top-center' | 'bottom-center' | 'top-right' }) {
  return (
    <Sonner
      position={position}
      duration={4500}
      closeButton
      style={
        {
          '--normal-bg': 'var(--surface-raised)',
          '--normal-text': 'var(--text)',
          '--normal-border': 'var(--border)',
          '--border-radius': 'var(--r-card)',
          '--success-bg': 'var(--surface-raised)',
          '--success-text': 'var(--text)',
          '--success-border': 'var(--border)',
          '--error-bg': 'var(--surface-raised)',
          '--error-text': 'var(--text)',
          '--error-border': 'var(--border)',
          fontFamily: 'inherit',
        } as React.CSSProperties
      }
    />
  )
}

type Message = Parameters<typeof sonnerToast.error>[0]
type Options = Parameters<typeof sonnerToast.error>[1]

/** Galat bertahan 9 dtk (konfirmasi 4,5 dtk): pengguna perlu waktu membacanya, terutama di ponsel. */
export const toast = Object.assign((message: Parameters<typeof sonnerToast>[0], options?: Parameters<typeof sonnerToast>[1]) => sonnerToast(message, options), {
  success: sonnerToast.success,
  info: sonnerToast.info,
  warning: sonnerToast.warning,
  dismiss: sonnerToast.dismiss,
  error: (message: Message, options?: Options) => sonnerToast.error(message, { duration: 9000, ...options }),
})
