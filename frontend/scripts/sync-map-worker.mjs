// Menyalin worker MapLibre ke public/ tiap app agar disajikan apa adanya (tanpa transformasi Vite).
//
// Mengapa: di mode dev, pipeline `?worker&url` Vite membungkus worker 4 MB dengan klien HMR dan peta
// berhenti memuat tile tanpa pesan error (event `load` tidak pernah terpicu). Berkas statis bekerja sama
// di dev dan produksi, dan mudah di-cache untuk PWA. Versi worker selalu sama dengan maplibre-gl terpasang
// karena salinan dibuat dari node_modules sebelum dev/build (lihat script predev/prebuild/postinstall).
import { copyFile, mkdir, stat } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, '..')
const source = path.join(root, 'node_modules', 'maplibre-gl', 'dist')
const FILES = ['maplibre-gl-worker.mjs', 'maplibre-gl-shared.mjs']
const APPS = ['dashboard', 'backoffice', 'mobile']

try {
  await stat(source)
} catch {
  console.error(`[sync-map-worker] ${source} tidak ada. Jalankan \`npm install\` dulu.`)
  process.exit(1)
}

for (const app of APPS) {
  const publicDir = path.join(root, app, 'public')
  await mkdir(publicDir, { recursive: true })
  for (const file of FILES) await copyFile(path.join(source, file), path.join(publicDir, file))
}
console.log(`[sync-map-worker] ${FILES.length} berkas disalin ke public/ di ${APPS.length} app`)
