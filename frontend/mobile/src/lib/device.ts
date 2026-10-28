import { useEffect, useSyncExternalStore } from 'react'

const subscribeOnline = (listener: () => void) => {
  window.addEventListener('online', listener)
  window.addEventListener('offline', listener)
  return () => {
    window.removeEventListener('online', listener)
    window.removeEventListener('offline', listener)
  }
}

/** Status jaringan perangkat (banner offline). */
export function useOnlineStatus(): boolean {
  return useSyncExternalStore(
    subscribeOnline,
    () => navigator.onLine,
    () => true,
  )
}

type WakeLockSentinelLike = { release(): Promise<void> }

/** Menjaga layar tetap menyala selama `active` (Screen Wake Lock). Diam-diam tak berbuat apa pun bila peramban tak mendukung. */
export function useWakeLock(active: boolean) {
  useEffect(() => {
    const wakeLock = (navigator as unknown as { wakeLock?: { request(type: 'screen'): Promise<WakeLockSentinelLike> } }).wakeLock
    if (!active || !wakeLock) return
    let sentinel: WakeLockSentinelLike | null = null
    let cancelled = false

    const acquire = () => {
      wakeLock
        .request('screen')
        .then((s) => {
          if (cancelled) void s.release()
          else sentinel = s
        })
        .catch(() => undefined) // ditolak (mis. baterai hemat): tidak fatal
    }
    // kunci dilepas peramban saat tab tersembunyi; ambil lagi ketika kembali terlihat
    const onVisible = () => {
      if (document.visibilityState === 'visible') acquire()
    }
    acquire()
    document.addEventListener('visibilitychange', onVisible)
    return () => {
      cancelled = true
      document.removeEventListener('visibilitychange', onVisible)
      void sentinel?.release()
    }
  }, [active])
}

/** Getar singkat sebagai umpan balik fisik bila perangkat mendukung. */
export const haptic = (ms = 15) => {
  try {
    navigator.vibrate?.(ms)
  } catch {
    /* tidak didukung */
  }
}
