const PREFS_KEY = 'loong:prefs'

export type Prefs = { notifDriver: boolean; notifPromo: boolean; payment: 'saldo' | 'transfer' | 'tunai' }
export const DEFAULT_PREFS: Prefs = { notifDriver: true, notifPromo: false, payment: 'saldo' }

export function readPrefs(): Prefs {
  try {
    return { ...DEFAULT_PREFS, ...(JSON.parse(localStorage.getItem(PREFS_KEY) ?? '{}') as Partial<Prefs>) }
  } catch {
    return DEFAULT_PREFS
  }
}

export function writePrefs(prefs: Prefs) {
  localStorage.setItem(PREFS_KEY, JSON.stringify(prefs))
}
