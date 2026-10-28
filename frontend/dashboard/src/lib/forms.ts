import { scrollBehavior } from '@loong/ui'
import { useEffect, useState } from 'react'
import { t } from '../i18n'

/** Fokus ke kolom pertama yang bermasalah setelah galat muncul (dan gulirkan ke tampilan). */
export function focusFirstInvalid(root: HTMLElement | null) {
  requestAnimationFrame(() => {
    const el = root?.querySelector<HTMLElement>('[aria-invalid="true"]')
    el?.scrollIntoView({ block: 'center', behavior: scrollBehavior() })
    el?.focus({ preventScroll: true })
  })
}

export const isEmail = (v: string) => /^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(v.trim())

/** Nilai yang baru berubah setelah pengguna berhenti mengetik (validasi inline 300 ms, bukan tiap ketukan). */
export function useDebounced<T>(value: T, ms = 300): T {
  const [debounced, setDebounced] = useState(value)
  useEffect(() => {
    const id = setTimeout(() => setDebounced(value), ms)
    return () => clearTimeout(id)
  }, [value, ms])
  return debounced
}

/** Hitung mundur detik (mis. "kirim ulang dalam 30 dtk"). `start()` memulai ulang. */
export function useCountdown(seconds: number) {
  const [left, setLeft] = useState(0)
  useEffect(() => {
    if (left <= 0) return
    const id = setTimeout(() => setLeft((n) => n - 1), 1000)
    return () => clearTimeout(id)
  }, [left])
  return { left, start: () => setLeft(seconds) }
}

/** Kekuatan kata sandi sederhana (panjang + ragam), dipakai sebagai petunjuk, bukan aturan wajib berlapis. */
export function passwordStrength(pw: string): { label: string; level: 0 | 1 | 2 | 3 } {
  if (!pw) return { label: '', level: 0 }
  let score = 0
  if (pw.length >= 8) score++
  if (pw.length >= 12) score++
  if (/[a-z]/.test(pw) && /[A-Z]/.test(pw)) score++
  if (/\d/.test(pw) || /[^A-Za-z0-9]/.test(pw)) score++
  if (pw.length < 8) return { label: t('dash.auth.strengthShort'), level: 0 }
  if (score <= 2) return { label: t('dash.auth.strengthFair'), level: 1 }
  if (score === 3) return { label: t('dash.auth.strengthGood'), level: 2 }
  return { label: t('dash.auth.strengthStrong'), level: 3 }
}
