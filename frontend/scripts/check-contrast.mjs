// Uji kontras APCA untuk token di packages/ui/src/styles/tokens.css.
// Membaca token langsung dari CSS (sumber tunggal), menyelesaikan var(), mengonversi OKLCH -> sRGB.
// Ambang (lihat rencana): teks isi Lc >= 75, teks sekunder/keterangan >= 60, UI non-teks >= 45.
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import Color from 'colorjs.io'
import { calcAPCA } from 'apca-w3'

const here = path.dirname(fileURLToPath(import.meta.url))
const tokensFile = path.resolve(here, '../packages/ui/src/styles/tokens.css')

const css = (await readFile(tokensFile, 'utf8')).replace(/\/\*[\s\S]*?\*\//g, '')
const rootBlock = css.slice(css.indexOf(':root'), css.indexOf('\n}', css.indexOf(':root')))
const vars = new Map()
for (const m of rootBlock.matchAll(/--([\w-]+):\s*([^;]+);/g)) vars.set(m[1], m[2].trim())

function resolve(name, seen = new Set()) {
  if (seen.has(name)) throw new Error(`Siklus token: ${name}`)
  seen.add(name)
  const raw = vars.get(name)
  if (raw === undefined) throw new Error(`Token tidak ditemukan: --${name}`)
  const ref = raw.match(/^var\(--([\w-]+)\)$/)
  return ref ? resolve(ref[1], seen) : raw
}

function hex(name) {
  const value = resolve(name)
  if (/\/\s*[\d.]+\)/.test(value)) throw new Error(`--${name} punya alpha; uji pasangan opak saja`)
  const c = new Color(value).to('srgb').toGamut({ space: 'srgb', method: 'oklch.c' })
  return c.toString({ format: 'hex', collapse: false })
}

const lc = (fg, bg) => Math.abs(calcAPCA(hex(fg), hex(bg)))

const BODY = 75
const SECONDARY = 60
const NONTEXT = 45

const PAIRS = [
  // [teks/ikon, latar, ambang, keterangan]
  ['text', 'surface', BODY, 'teks utama di kartu'],
  ['text', 'bg', BODY, 'teks utama di halaman'],
  ['text-muted', 'surface', BODY, 'teks sekunder di kartu'],
  ['text-muted', 'bg', BODY, 'teks sekunder di halaman'],
  ['text-subtle', 'surface', SECONDARY, 'keterangan kecil di kartu'],
  ['text-subtle', 'bg', SECONDARY, 'keterangan kecil di halaman'],
  ['accent', 'surface', BODY, 'tautan/teks aksen di kartu'],
  ['accent', 'bg', BODY, 'tautan/teks aksen di halaman'],
  ['on-accent', 'accent', BODY, 'label tombol utama'],
  ['on-accent', 'accent-hover', BODY, 'label tombol utama (hover)'],
  ['on-accent', 'danger-fg', BODY, 'label tombol hapus/destruktif di dialog konfirmasi'],
  ['success-fg', 'surface', BODY, 'teks status sukses'],
  ['warning-fg', 'surface', BODY, 'teks status peringatan'],
  ['danger-fg', 'surface', BODY, 'teks error'],
  ['accent', 'bg', NONTEXT, 'rute/indikator aksen di atas halaman'],
  ['success', 'surface', NONTEXT, 'titik status sukses'],
  ['warning', 'surface', NONTEXT, 'titik status peringatan'],
  ['danger', 'surface', NONTEXT, 'titik status bahaya'],
  ['border-input', 'surface', NONTEXT, 'batas input (WCAG 1.4.11)'],
]

let failed = 0
console.log('\nKontras APCA (Lc, mutlak)\n')
for (const [fg, bg, min, note] of PAIRS) {
  if (!vars.has(fg) || !vars.has(bg)) {
    console.log(`  SKIP  --${fg} / --${bg}  (token belum ada)  ${note}`)
    failed++
    continue
  }
  const value = lc(fg, bg)
  const ok = value >= min
  if (!ok) failed++
  console.log(
    `  ${ok ? 'OK  ' : 'GAGAL'}  Lc ${value.toFixed(1).padStart(5)}  (min ${min})  --${fg} di atas --${bg}  ${note}`,
  )
}

console.log(`\nHex penting: accent ${hex('accent')}  bg ${hex('bg')}  surface ${hex('surface')}`)
console.log(failed ? `\n${failed} pasangan belum lolos.\n` : '\nSemua pasangan lolos.\n')
process.exit(failed ? 1 : 0)
