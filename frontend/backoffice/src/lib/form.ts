import { scrollBehavior } from '@loong/ui'
import { useCallback, useState } from 'react'

/**
 * Kapan galat boleh tampil: setelah kolom ditinggalkan (blur) atau setelah pengguna menekan Simpan.
 * Galat dihitung ulang dari nilai pada setiap render, jadi hilang sendiri begitu diperbaiki.
 */
export function useFieldVisibility() {
  const [touched, setTouched] = useState<Record<string, boolean>>({})
  const [submitted, setSubmitted] = useState(false)
  const touch = useCallback((key: string) => setTouched((t) => (t[key] ? t : { ...t, [key]: true })), [])
  const visible = useCallback((key: string) => submitted || !!touched[key], [submitted, touched])
  return { touch, visible, submit: () => setSubmitted(true), submitted }
}

/** Fokus ke kolom pertama yang bermasalah setelah galat muncul. */
export function focusFirstInvalid(root: HTMLElement | null) {
  requestAnimationFrame(() => {
    const el = root?.querySelector<HTMLElement>('[aria-invalid="true"]')
    el?.scrollIntoView({ block: 'center', behavior: scrollBehavior() })
    el?.focus({ preventScroll: true })
  })
}
