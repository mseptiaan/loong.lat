import { useCallback, useSyncExternalStore } from 'react'

const KEY = 'loong:driver:prefs'

export type DriverPrefs = {
  /** Cegah layar mati selama ada rute berjalan (Screen Wake Lock). */
  keepAwake: boolean
  /** Tampilan rute aktif: daftar atau peta. */
  routeView: 'daftar' | 'peta'
}

const DEFAULTS: DriverPrefs = { keepAwake: true, routeView: 'daftar' }

// snapshot di-cache agar useSyncExternalStore mendapat referensi stabil selama isinya tidak berubah
let cache: { raw: string | null; value: DriverPrefs } | null = null
const listeners = new Set<() => void>()

function read(): DriverPrefs {
  const raw = localStorage.getItem(KEY)
  if (cache && cache.raw === raw) return cache.value
  let value = DEFAULTS
  try {
    value = { ...DEFAULTS, ...(JSON.parse(raw ?? '{}') as Partial<DriverPrefs>) }
  } catch {
    /* pakai bawaan */
  }
  cache = { raw, value }
  return value
}

function write(patch: Partial<DriverPrefs>) {
  localStorage.setItem(KEY, JSON.stringify({ ...read(), ...patch }))
  listeners.forEach((l) => l())
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  window.addEventListener('storage', listener)
  return () => {
    listeners.delete(listener)
    window.removeEventListener('storage', listener)
  }
}

export function useDriverPrefs(): [DriverPrefs, (patch: Partial<DriverPrefs>) => void] {
  const prefs = useSyncExternalStore(subscribe, read, () => DEFAULTS)
  const update = useCallback((patch: Partial<DriverPrefs>) => write(patch), [])
  return [prefs, update]
}
