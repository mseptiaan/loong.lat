// Menghasilkan packages/gazetteer/src/places.ts dari Nominatim (OpenStreetMap), SEKALI di dev.
// Hasilnya di-commit, jadi runtime aplikasi tidak butuh jaringan atau API key.
//
// Mematuhi kebijakan Nominatim: maks 1 permintaan/detik, User-Agent yang jelas, hasil di-cache di file.
// Pakai: node scripts/build-gazetteer.mjs            (menimpa places.ts)
//        node scripts/build-gazetteer.mjs --dry       (hanya cetak ringkasan)
import { mkdir, readFile, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const here = path.dirname(fileURLToPath(import.meta.url))
const outFile = path.resolve(here, '../packages/gazetteer/src/places.ts')
const dry = process.argv.includes('--dry')

// [kueri Nominatim, nama tampilan, jenis, kawasan (opsional, menimpa hasil)]
const QUERIES = [
  // mall
  ['Grand Indonesia, Jakarta Pusat', 'Grand Indonesia', 'mall'],
  ['Plaza Indonesia, Jakarta Pusat', 'Plaza Indonesia', 'mall'],
  ['Plaza Senayan, Jakarta Selatan', 'Plaza Senayan', 'mall'],
  ['Senayan City, Jakarta Selatan', 'Senayan City', 'mall'],
  ['Pacific Place, SCBD, Jakarta Selatan', 'Pacific Place', 'mall'],
  ['Central Park Mall, Tanjung Duren, Jakarta Barat', 'Central Park Mall', 'mall'],
  ['Mal Taman Anggrek, Jakarta Barat', 'Mal Taman Anggrek', 'mall'],
  ['Mal Kelapa Gading, Jakarta Utara', 'Mal Kelapa Gading', 'mall'],
  ['Pondok Indah Mall, Jakarta Selatan', 'Pondok Indah Mall', 'mall'],
  ['Kota Kasablanka, Jakarta Selatan', 'Kota Kasablanka', 'mall'],
  ['Gandaria City, Jakarta Selatan', 'Gandaria City', 'mall'],
  ['Kemang Village, Jakarta Selatan', 'Kemang Village', 'mall'],
  ['Lippo Mall Puri, Jakarta Barat', 'Lippo Mall Puri', 'mall'],
  ['Pluit Village, Jakarta Utara', 'Pluit Village', 'mall'],
  ['Summarecon Mall Serpong, Tangerang', 'Summarecon Mall Serpong', 'mall'],
  ['AEON Mall BSD City, Tangerang Selatan', 'AEON Mall BSD City', 'mall'],
  ['Margo City, Depok', 'Margo City', 'mall'],
  ['Mall Metropolitan Bekasi', 'Metropolitan Mall Bekasi', 'mall'],
  ['Cibubur Junction, Jakarta Timur', 'Cibubur Junction', 'mall'],
  ['Blok M Square, Jakarta Selatan', 'Blok M Square', 'mall'],
  // stasiun
  ['Stasiun Gambir, Jakarta Pusat', 'Stasiun Gambir', 'stasiun'],
  ['Stasiun Sudirman, Jakarta Pusat', 'Stasiun Sudirman', 'stasiun'],
  ['Stasiun Manggarai, Jakarta Selatan', 'Stasiun Manggarai', 'stasiun'],
  ['Stasiun Tebet, Jakarta Selatan', 'Stasiun Tebet', 'stasiun'],
  ['Stasiun Pasar Minggu, Jakarta Selatan', 'Stasiun Pasar Minggu', 'stasiun'],
  ['Stasiun Depok', 'Stasiun Depok', 'stasiun'],
  ['Stasiun Bekasi', 'Stasiun Bekasi', 'stasiun'],
  ['Stasiun Tanah Abang, Jakarta Pusat', 'Stasiun Tanah Abang', 'stasiun'],
  ['Stasiun Jakarta Kota', 'Stasiun Jakarta Kota', 'stasiun'],
  ['Stasiun Rawa Buntu, Tangerang Selatan', 'Stasiun Rawa Buntu', 'stasiun'],
  ['Stasiun MRT Blok M BCA, Jakarta Selatan', 'Stasiun MRT Blok M', 'stasiun'],
  ['Stasiun MRT Lebak Bulus Grab, Jakarta Selatan', 'Stasiun MRT Lebak Bulus', 'stasiun'],
  // kampus
  ['Universitas Indonesia, Depok', 'Universitas Indonesia', 'kampus'],
  ['Universitas Trisakti, Grogol, Jakarta Barat', 'Universitas Trisakti', 'kampus'],
  ['Binus University Kampus Anggrek, Jakarta Barat', 'Binus Kampus Anggrek', 'kampus'],
  ['Universitas Katolik Atma Jaya, Semanggi, Jakarta Selatan', 'Universitas Atma Jaya', 'kampus'],
  ['UIN Syarif Hidayatullah Jakarta, Ciputat', 'UIN Syarif Hidayatullah', 'kampus'],
  // kantor
  ['Sudirman Central Business District, Jakarta Selatan', 'SCBD', 'kantor'],
  ['Menara BCA, Jakarta Pusat', 'Menara BCA', 'kantor'],
  ['Sarinah, Jalan M.H. Thamrin, Jakarta Pusat', 'Gedung Sarinah', 'kantor'],
  ['Kuningan City, Jakarta Selatan', 'Kuningan City', 'kantor'],
  ['Menara Astra, Jakarta Pusat', 'Menara Astra', 'kantor'],
  ['Gedung Kementerian Perdagangan, Jakarta Pusat', 'Kementerian Perdagangan', 'kantor'],
  // pasar
  ['Pasar Tanah Abang, Jakarta Pusat', 'Pasar Tanah Abang', 'pasar'],
  ['Pasar Senen, Jakarta Pusat', 'Pasar Senen', 'pasar'],
  ['Pasar Mayestik, Jakarta Selatan', 'Pasar Mayestik', 'pasar'],
  ['Pasar Minggu, Jakarta Selatan', 'Pasar Minggu', 'pasar'],
  ['Pasar Santa, Jakarta Selatan', 'Pasar Santa', 'pasar'],
  // rumah sakit
  ['RSUPN Dr. Cipto Mangunkusumo, Jakarta Pusat', 'RSCM', 'rumah-sakit'],
  ['Rumah Sakit Pondok Indah Bintaro Jaya, Tangerang Selatan', 'RS Pondok Indah Bintaro', 'rumah-sakit'],
  ['Siloam Hospitals Semanggi, Jakarta Selatan', 'RS Siloam Semanggi', 'rumah-sakit'],
  // landmark
  ['Monumen Nasional, Jakarta Pusat', 'Monas', 'landmark'],
  ['Bundaran Hotel Indonesia, Jakarta Pusat', 'Bundaran HI', 'landmark'],
  ['Kota Tua Jakarta, Jakarta Barat', 'Kota Tua', 'landmark'],
  ['Taman Impian Jaya Ancol, Jakarta Utara', 'Ancol', 'landmark'],
  ['Taman Mini Indonesia Indah, Jakarta Timur', 'Taman Mini Indonesia Indah', 'landmark'],
  ['Stadion Utama Gelora Bung Karno, Jakarta Pusat', 'Gelora Bung Karno', 'landmark'],
  // hunian
  ['Apartemen Kalibata City, Jakarta Selatan', 'Apartemen Kalibata City', 'hunian'],
  ['Apartemen Casablanca Mansion, Jakarta Selatan', 'Apartemen Casablanca', 'hunian'],
  ['Bintaro Jaya Sektor 9, Tangerang Selatan', 'Bintaro Sektor 9', 'hunian'],
  ['Jalan Kemang Raya, Jakarta Selatan', 'Kemang Raya', 'hunian'],
  ['Jalan Kebon Jeruk Raya, Jakarta Barat', 'Kebon Jeruk', 'hunian'],
  // gudang
  ['Kawasan Industri Pulogadung, Jakarta Timur', 'Kawasan Industri Pulogadung', 'gudang'],
  ['Pergudangan Daan Mogot Baru, Jakarta Barat', 'Pergudangan Daan Mogot Baru', 'gudang'],
  ['Marunda Center, Jakarta Utara', 'Marunda Center', 'gudang'],
]

// Kotak batas Jabodetabek: hasil di luar ini dianggap salah cocok dan ditandai.
const BOUNDS = { minLat: -6.6, maxLat: -6.0, minLng: 106.55, maxLng: 107.2 }

// Koreksi untuk hasil Nominatim yang meleset (dicek manual terhadap lokasi sebenarnya).
const OVERRIDES = {
  'Plaza Senayan': { lat: -6.2252, lng: 106.7997 }, // Nominatim menaruhnya ~1,6 km ke timur
  'Pasar Tanah Abang': { lat: -6.1853, lng: 106.8117, address: 'Jalan K.H. Mas Mansyur, Tanah Abang, Jakarta Pusat' },
}

// Titik yang tidak ditemukan Nominatim; koordinat dari pengetahuan umum (akurasi ~100-300 m, cukup untuk prototipe).
const MANUAL = [
  ['Senayan City', 'mall', -6.2277, 106.7971, 'Jalan Asia Afrika Lot 19, Gelora, Jakarta Pusat', 'Tanah Abang'],
  ['AEON Mall BSD City', 'mall', -6.3025, 106.6425, 'Jalan BSD Raya Barat 1, Sampora, Tangerang', 'Cisauk'],
  ['Stasiun MRT Lebak Bulus', 'stasiun', -6.2894, 106.7746, 'Jalan Lebak Bulus Raya, Lebak Bulus, Jakarta Selatan', 'Cilandak'],
  ['Binus Kampus Anggrek', 'kampus', -6.2017, 106.7819, 'Jalan Kebon Jeruk Raya No. 27, Kebon Jeruk, Jakarta Barat', 'Kebon Jeruk'],
  ['Universitas Atma Jaya', 'kampus', -6.2178, 106.817, 'Jalan Jenderal Sudirman No. 51, Karet Semanggi, Jakarta Selatan', 'Setiabudi'],
  ['UIN Syarif Hidayatullah', 'kampus', -6.3057, 106.752, 'Jalan Ir. H. Juanda, Ciputat, Tangerang Selatan', 'Ciputat'],
  ['RSCM', 'rumah-sakit', -6.1965, 106.8457, 'Jalan Diponegoro No. 71, Kenari, Jakarta Pusat', 'Senen'],
  ['RS Pondok Indah', 'rumah-sakit', -6.2669, 106.7828, 'Jalan Metro Duta Kav. UE, Pondok Pinang, Jakarta Selatan', 'Kebayoran Lama'],
  ['Apartemen Kalibata City', 'hunian', -6.2557, 106.8528, 'Jalan Raya Kalibata, Rawajati, Jakarta Selatan', 'Pancoran'],
  ['Apartemen Casablanca', 'hunian', -6.226, 106.8435, 'Jalan Casablanca Raya, Menteng Dalam, Jakarta Selatan', 'Tebet'],
  ['Pergudangan Daan Mogot Baru', 'gudang', -6.159, 106.71, 'Jalan Daan Mogot Baru, Kalideres, Jakarta Barat', 'Kalideres'],
]

const UA = 'loong-lat-hackathon-gazetteer/1.0 (pembuatan data sekali jalan untuk prototipe)'
const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// Cache hasil mentah agar menjalankan ulang tidak memanggil Nominatim lagi.
const cacheFile = path.resolve(here, 'gazetteer.cache.json')
let cache = {}
try {
  cache = JSON.parse(await readFile(cacheFile, 'utf8'))
} catch {
  // belum ada cache
}
let fromCache = false

async function lookup(q) {
  if (q in cache) {
    fromCache = true
    return cache[q]
  }
  fromCache = false
  const url = new URL('https://nominatim.openstreetmap.org/search')
  url.searchParams.set('q', q)
  url.searchParams.set('format', 'jsonv2')
  url.searchParams.set('addressdetails', '1')
  url.searchParams.set('limit', '1')
  url.searchParams.set('countrycodes', 'id')
  url.searchParams.set('accept-language', 'id')
  const res = await fetch(url, { headers: { 'user-agent': UA } })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  const hit = (await res.json())[0] ?? null
  cache[q] = hit
  return hit
}
const pause = () => (fromCache ? Promise.resolve() : sleep(1200))

const slug = (s) =>
  s
    .toLowerCase()
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-|-$/g, '')

const places = []
const problems = []
for (const [q, name, kind] of QUERIES) {
  let hit
  try {
    hit = await lookup(q)
  } catch (err) {
    problems.push(`${name}: gagal (${err.message})`)
    await pause()
    continue
  }
  if (!hit) {
    problems.push(`${name}: tidak ditemukan (ditangani lewat daftar MANUAL bila ada)`)
    await pause()
    continue
  }
  const fix = OVERRIDES[name]
  const lat = fix ? fix.lat : Number(hit.lat)
  const lng = fix ? fix.lng : Number(hit.lon)
  const a = hit.address ?? {}
  const inBounds = lat >= BOUNDS.minLat && lat <= BOUNDS.maxLat && lng >= BOUNDS.minLng && lng <= BOUNDS.maxLng
  if (!inBounds) problems.push(`${name}: di luar Jabodetabek (${lat}, ${lng})`)
  const area = a.suburb || a.city_district || a.village || a.neighbourhood || a.town || a.city || a.county || ''
  const city = a.city || a.town || a.municipality || a.county || a.state || ''
  const street = [a.road, a.house_number].filter(Boolean).join(' ')
  const address =
    fix?.address ?? [street || a.neighbourhood || name, area !== street ? area : '', city].filter(Boolean).join(', ')
  places.push({
    id: slug(name),
    name,
    // "Daerah Khusus Ibukota Jakarta" terlalu panjang untuk daftar alamat
    address: address.replace(/, Daerah Khusus Ibukota Jakarta$/, ', Jakarta'),
    area: area || city,
    kind,
    position: { lat: Number(lat.toFixed(5)), lng: Number(lng.toFixed(5)) },
    ok: inBounds,
  })
  console.log(`${inBounds ? 'OK ' : '?? '} ${name.padEnd(30)} ${lat.toFixed(4)}, ${lng.toFixed(4)}  ${address}`)
  await pause()
}

for (const [name, kind, lat, lng, address, area] of MANUAL) {
  if (places.some((p) => p.id === slug(name))) continue
  places.push({ id: slug(name), name, address, area, kind, position: { lat, lng }, ok: true })
  console.log(`MN  ${name.padEnd(30)} ${lat.toFixed(4)}, ${lng.toFixed(4)}  ${address}`)
}
places.sort((a, b) => a.kind.localeCompare(b.kind) || a.name.localeCompare(b.name, 'id'))

await writeFile(cacheFile, JSON.stringify(cache, null, 1))

console.log(`\n${places.length} lokasi (dari ${QUERIES.length} kueri + ${MANUAL.length} titik manual).`)
if (problems.length) console.log('Catatan:\n  - ' + problems.join('\n  - '))
if (dry) process.exit(0)

const good = places
  .filter((p) => p.ok)
  .map((p) => ({ id: p.id, name: p.name, address: p.address, area: p.area, kind: p.kind, position: p.position }))
const body = `/**
 * DIHASILKAN oleh scripts/build-gazetteer.mjs dari Nominatim (data © OpenStreetMap contributors, ODbL).
 * Jangan sunting manual; jalankan ulang skrip bila perlu. Berisi ${good.length} titik Jabodetabek.
 */
import type { Place } from '@loong/domain'

export const PLACES: readonly Place[] = ${JSON.stringify(good, null, 2)}
`
await mkdir(path.dirname(outFile), { recursive: true })
await writeFile(outFile, body)
console.log(`Ditulis: ${path.relative(process.cwd(), outFile)} (${good.length} titik)`)
