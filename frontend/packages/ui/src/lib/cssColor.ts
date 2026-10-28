const cache = new Map<string, string>()

/**
 * Mengubah token warna CSS (mis. `--accent`, bernilai oklch) menjadi hex sRGB. MapLibre tidak membaca variabel CSS
 * maupun oklch, jadi warna garis peta diambil dari token lewat satu piksel canvas, sehingga tetap satu sumber.
 */
export function resolveCssColor(token: string, fallback: string): string {
  if (typeof document === 'undefined') return fallback
  const hit = cache.get(token)
  if (hit) return hit
  const value = getComputedStyle(document.documentElement).getPropertyValue(token).trim()
  if (!value) return fallback
  const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true })
  if (!ctx) return fallback
  ctx.canvas.width = ctx.canvas.height = 1
  ctx.clearRect(0, 0, 1, 1)
  ctx.fillStyle = value
  ctx.fillRect(0, 0, 1, 1)
  const [r, g, b] = ctx.getImageData(0, 0, 1, 1).data
  const hex = `#${[r, g, b].map((n) => (n ?? 0).toString(16).padStart(2, '0')).join('')}`
  cache.set(token, hex)
  return hex
}
