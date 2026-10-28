// Menulis berkas logo dari satu sumber geometri (packages/ui/src/brand.ts):
//   brand/loong-lat.svg          logo utama (tanda + wordmark), latar terang
//   brand/loong-lat-mark.svg     tanda saja (avatar, ikon aplikasi)
//   <app>/public/favicon.svg     favicon tiap aplikasi
//   mobile/public/icon.svg       ikon PWA (kotak penuh, di-mask oleh OS)
// Jalankan `npm run build:brand` setelah mengubah brand.ts. brand.test.ts memastikan berkas selalu sama dengan sumbernya.
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { lockupSvg, markSvg } from '../packages/ui/src/brand.ts'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')

export const BRAND_FILES = [
  ['brand/loong-lat.svg', lockupSvg()],
  ['brand/loong-lat-mark.svg', markSvg()],
  ['dashboard/public/favicon.svg', markSvg()],
  ['backoffice/public/favicon.svg', markSvg()],
  ['mobile/public/favicon.svg', markSvg()],
  ['mobile/public/icon.svg', markSvg({ bleed: true })],
]

for (const [file, svg] of BRAND_FILES) {
  const target = path.join(root, file)
  await mkdir(path.dirname(target), { recursive: true })
  await writeFile(target, svg)
  console.log(`[build-brand] ${file}`)
}
