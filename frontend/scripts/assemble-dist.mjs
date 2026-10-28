// Merakit hasil build tiap app ke satu folder statis, satu subfolder per hostname:
//   dist/console    -> console.<domain>     (dashboard/)
//   dist/backoffice -> backoffice.<domain>  (backoffice/)
//   dist/driver     -> driver.<domain>      (mobile/)
//   dist/api        -> api.<domain>         (api/)
// plus halaman depan (landing/) di akar dist/ untuk domain induk. Di hosting produksi, arahkan domain induk
// (loong.lat) ke dist/ dan tiap subdomain (console.loong.lat, dst.) ke subfolder yang sesuai.
import { cp, mkdir, rm } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const root = path.resolve(here, '..')
const dist = path.join(root, 'dist')

const OUTPUTS = [
  ['dashboard', 'console'],
  ['backoffice', 'backoffice'],
  ['mobile', 'driver'],
  ['api', 'api'],
]

await rm(dist, { recursive: true, force: true })
await mkdir(dist, { recursive: true })

for (const [dir, out] of OUTPUTS) {
  await cp(path.join(root, dir, 'dist'), path.join(dist, out), { recursive: true })
  console.log(`dist/${out}  <-  ${dir}/dist`)
}
await cp(path.join(root, 'landing', 'dist'), dist, { recursive: true })
console.log('dist/  <-  landing/dist (halaman depan)')
