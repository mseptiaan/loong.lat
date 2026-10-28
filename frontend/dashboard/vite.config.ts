import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

// Aplikasi ini = console.<domain> (Dashboard pengirim). Dev diakses lewat gateway (scripts/dev-gateway.mjs)
// yang merutekan berdasarkan hostname; HMR memakai host:port halaman sehingga ikut lewat gateway.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  // Dependensi yang hanya diimpor dari packages/ui ditemukan terlambat oleh Vite, memicu optimasi ulang
  // di tengah sesi dan hash modul basi. Deklarasikan di depan agar cold start stabil.
  optimizeDeps: { include: ['maplibre-gl', 'react-map-gl/maplibre'] },
  build: {
    rolldownOptions: {
      output: {
        // MapLibre (~800 KB) dan React jarang berubah dibanding kode aplikasi: pisahkan agar cache peramban dan SW bisa
        // memakainya ulang saat aplikasi diperbarui, dan halaman masuk tidak ikut memuat peta di satu berkas raksasa
        codeSplitting: {
          groups: [
            { name: 'peta', test: /node_modules[\\/](maplibre-gl|react-map-gl|@mapbox|geojson-vt|supercluster|kdbush)/, priority: 30 },
            { name: 'react', test: /node_modules[\\/](react|react-dom|react-router|scheduler|@tanstack)[\\/]/, priority: 20 },
            { name: 'vendor', test: /node_modules/, priority: 10 },
          ],
        },
      },
    },
  },
  server: {
    host: '127.0.0.1',
    port: 4171,
    strictPort: true,
  },
})
