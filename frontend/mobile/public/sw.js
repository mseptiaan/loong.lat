/*
 * Service worker aplikasi Driver (hanya didaftarkan di build produksi).
 * Tujuannya satu: aplikasi tetap terbuka saat sinyal putus. Tidak menyimpan data pengguna.
 *  - navigasi: jaringan dulu, lalu cangkang aplikasi tersimpan bila gagal
 *  - berkas ber-hash (/assets), font, dan worker peta: cache-first (isinya tidak pernah berubah untuk nama yang sama)
 *  - semua yang lintas-origin (tile peta, host api.) tidak disentuh
 */
const VERSION = 'v1'
const SHELL = `loong-driver-shell-${VERSION}`
const ASSETS = `loong-driver-assets-${VERSION}`

self.addEventListener('install', (event) => {
  event.waitUntil(
    caches
      .open(SHELL)
      .then((cache) => cache.addAll(['/', '/manifest.webmanifest', '/icon.svg']))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== SHELL && k !== ASSETS).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (event) => {
  const { request } = event
  if (request.method !== 'GET') return
  const url = new URL(request.url)
  if (url.origin !== self.location.origin) return

  if (request.mode === 'navigate') {
    event.respondWith(
      fetch(request)
        .then((response) => {
          const copy = response.clone()
          void caches.open(SHELL).then((cache) => cache.put('/', copy))
          return response
        })
        .catch(() => caches.match('/').then((hit) => hit ?? Response.error())),
    )
    return
  }

  const immutable = url.pathname.startsWith('/assets/') || url.pathname.endsWith('.woff2') || url.pathname.startsWith('/maplibre-gl')
  if (immutable) {
    event.respondWith(
      caches.open(ASSETS).then(async (cache) => {
        const hit = await cache.match(request)
        if (hit) return hit
        const response = await fetch(request)
        if (response.ok) void cache.put(request, response.clone())
        return response
      }),
    )
  }
})
