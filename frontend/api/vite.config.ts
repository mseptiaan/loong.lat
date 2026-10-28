import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'

// Aplikasi ini = api.<domain> (backend tiruan). Tanpa React: hanya halaman kecil + skrip hub.
export default defineConfig({
  // .env* dibaca dari frontend/ (bukan api/) dan HANYA oleh aplikasi ini: token Mapbox tidak ikut ke bundel
  // console, backoffice, atau driver karena merekalah yang tampil di layar pengguna.
  envDir: fileURLToPath(new URL('..', import.meta.url)),
  server: {
    host: '127.0.0.1',
    port: 4174,
    strictPort: true,
  },
})
