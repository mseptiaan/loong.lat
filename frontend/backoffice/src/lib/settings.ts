import { useCallback, useSyncExternalStore } from 'react'

const KEY = 'loong:backoffice:settings'

export type Density = 'compact' | 'comfortable'

export interface BackofficeSettings {
  /** Kepadatan tabel dan kontrol (data-density di <html>). */
  density: Density
  notifyUnassigned: boolean
  notifyAnomaly: boolean
  notifyDaily: boolean
}

const DEFAULTS: BackofficeSettings = { density: 'compact', notifyUnassigned: true, notifyAnomaly: true, notifyDaily: false }

// snapshot di-cache agar useSyncExternalStore mendapat referensi stabil selama isinya tidak berubah
let cache: { raw: string | null; value: BackofficeSettings } | null = null
const listeners = new Set<() => void>()

function read(): BackofficeSettings {
  const raw = localStorage.getItem(KEY)
  if (cache && cache.raw === raw) return cache.value
  let value = DEFAULTS
  try {
    value = { ...DEFAULTS, ...(JSON.parse(raw ?? '{}') as Partial<BackofficeSettings>) }
  } catch {
    /* pakai bawaan */
  }
  cache = { raw, value }
  return value
}

export const applyDensity = (density: Density) => {
  document.documentElement.dataset.density = density
}

function write(patch: Partial<BackofficeSettings>) {
  const next = { ...read(), ...patch }
  localStorage.setItem(KEY, JSON.stringify(next))
  if (patch.density) applyDensity(patch.density)
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

/** Terapkan kepadatan tersimpan sebelum render pertama (hindari kedip). */
export const initSettings = () => applyDensity(read().density)

export function useSettings(): [BackofficeSettings, (patch: Partial<BackofficeSettings>) => void] {
  const settings = useSyncExternalStore(subscribe, read, () => DEFAULTS)
  const update = useCallback((patch: Partial<BackofficeSettings>) => write(patch), [])
  return [settings, update]
}
