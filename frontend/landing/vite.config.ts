import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Aplikasi ini = domain induk (loong.lat, dev: loong.localhost): halaman depan produk, dua bahasa.
// Dev diakses lewat gateway (scripts/dev-gateway.mjs) yang merutekan hostname tanpa subdomain aplikasi ke sini.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    host: '127.0.0.1',
    port: 4170,
    strictPort: true,
  },
})
