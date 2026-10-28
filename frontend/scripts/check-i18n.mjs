#!/usr/bin/env node
/**
 * Mencari sisa teks Indonesia yang masih tertanam di kode (seharusnya lewat t()/katalog).
 * Heuristik, bukan bukti: mencari kata Indonesia umum di literal string dan teks JSX, mengabaikan komentar.
 *
 *   node scripts/check-i18n.mjs                 seluruh sumber aplikasi dan paket
 *   node scripts/check-i18n.mjs dashboard/src   hanya jalur tertentu (boleh beberapa)
 *
 * Tambahkan `// i18n-ignore` di baris yang memang harus Indonesia (data, contoh, kata yang sama di dua bahasa).
 * Berkas katalog (i18n/ dan messages/), tes, dan data seed/gazetteer dilewati.
 */
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative, sep } from 'node:path'

const root = new URL('..', import.meta.url).pathname
const defaults = ['landing/src', 'dashboard/src', 'backoffice/src', 'mobile/src', 'api/src', 'packages/ui/src', 'packages/shell/src', 'packages/data/src', 'packages/domain/src', 'packages/mock-api/src', 'packages/i18n/src/format.ts']
const SKIP_DIRS = new Set(['node_modules', 'dist', 'i18n', 'messages', 'dev', 'gazetteer'])
const SKIP_FILE = /(\.test\.tsx?|\.d\.ts)$/
const SKIP_PATH = /packages\/(mock-api\/src\/seed\/(people|catalog)|gazetteer\/src\/places)\.ts$/

const STRONG = new Set(
  `yang dan atau dengan untuk tidak belum sudah anda silakan pada akan bisa harus saat jika bila karena semua lebih sebelum setelah antara
   kembali lanjut simpan batal hapus ubah tambah pilih cari tampilkan ada baru lagi coba tunggu memuat menunggu mencari selesai kosong gagal
   berhasil pengiriman pesanan rute barang alamat saldo pengguna kendaraan jadwal sekarang nanti tujuan titik ambil antar terlambat tepat waktu
   dalam dari ke sebagai oleh kami kita saya mereka dia kamu masuk keluar daftar kata sandi nomor catatan kirim terkirim dikirim jumlah biaya
   hemat murah cepat pendek jarak menit jam hari besok kemarin minggu bulan tahun bagian langkah opsi ringkasan tinjau konfirmasi buat
   membuat dibuat diubah dihapus disimpan ditambahkan terjadi kesalahan periksa pastikan hubungi bantuan belum ada punya memiliki sedang
   telah sampai tiba mulai menuju menuju lokasi penerima pengirim perjalanan bermasalah dibatalkan menunggu ditugaskan diblokir aktif nonaktif`.split(/\s+/),
)
// kata yang hampir selalu UI Indonesia bila ditulis kapital di awal literal
const WORDS_CAP = new Set(['Simpan', 'Batal', 'Hapus', 'Lanjut', 'Kembali', 'Tutup', 'Cari', 'Pilih', 'Tambah', 'Ubah', 'Selesai', 'Beranda', 'Pengiriman', 'Alamat', 'Saldo', 'Akun', 'Keluar', 'Masuk', 'Daftar', 'Kirim', 'Terapkan', 'Abaikan', 'Salin', 'Memuat', 'Coba', 'Ya', 'Tidak', 'Lihat', 'Buka', 'Detail', 'Riwayat', 'Pendapatan', 'Profil', 'Bantuan'])
// kata yang sama di kedua bahasa atau terlalu umum untuk dihitung sebagai bukti
const SAFE_SINGLE = new Set(['ID', 'EN'])

function* walk(path) {
  const st = statSync(path)
  if (st.isFile()) return void (yield path)
  for (const name of readdirSync(path)) {
    if (SKIP_DIRS.has(name)) continue
    yield* walk(join(path, name))
  }
}

function stripComments(src) {
  // tetapkan spasi pada tempat komentar agar nomor baris dan kolom tetap
  return src
    .replace(/\/\*[\s\S]*?\*\//g, (m) => m.replace(/[^\n]/g, ' '))
    .replace(/(^|[^:'"`\\])\/\/[^\n]*/g, (m, p) => p + ' '.repeat(m.length - p.length))
}

function candidates(line) {
  const out = []
  for (const m of line.matchAll(/'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)"|`((?:[^`\\]|\\.)*)`/g)) out.push(m[1] ?? m[2] ?? m[3] ?? '')
  for (const m of line.matchAll(/>([^<>{}=]*[A-Za-z]{2,}[^<>{}=]*)</g)) out.push(m[1])
  // teks JSX yang berlanjut ke baris berikutnya (tanpa tag penutup di baris yang sama)
  const bare = line.trim()
  if (/^[A-Z][^<>{}=;]*[a-z]/.test(bare) && !/[;=(]$/.test(bare) && !bare.includes('=>')) out.push(bare)
  return out
}

function indonesian(text) {
  const t = text.trim()
  if (t.length < 3 || /^[\w./:#@%-]+$/.test(t) && !/^[A-Z][a-z]+$/.test(t)) return false // pengenal, jalur, kelas CSS
  if (/^[a-z]+(_[a-z]+)+$/.test(t)) return false // nilai enum
  const words = t.toLowerCase().match(/[a-zà-ÿ]+/g) ?? []
  if (words.length === 0) return false
  if (words.length === 1) return WORDS_CAP.has(t) && !SAFE_SINGLE.has(t)
  return words.some((w) => STRONG.has(w))
}

const targets = (process.argv.slice(2).length ? process.argv.slice(2) : defaults).map((p) => join(root, p))
let total = 0
for (const target of targets) {
  let files
  try {
    files = [...walk(target)]
  } catch {
    continue
  }
  for (const file of files) {
    if (!/\.(ts|tsx)$/.test(file) || SKIP_FILE.test(file) || SKIP_PATH.test(file.split(sep).join('/'))) continue
    const raw = readFileSync(file, 'utf8')
    const rawLines = raw.split('\n')
    const lines = stripComments(raw).split('\n')
    lines.forEach((line, i) => {
      if (rawLines[i].includes('i18n-ignore') || /^\s*(import|export)\b.*\bfrom\b/.test(line)) return
      for (const text of candidates(line)) {
        if (indonesian(text)) {
          total++
          console.log(`${relative(root, file)}:${i + 1}: ${text.trim().slice(0, 90)}`)
          break
        }
      }
    })
  }
}
console.log(total === 0 ? 'Tidak ada sisa teks Indonesia yang terdeteksi.' : `\n${total} baris masih memuat teks yang tampak Indonesia.`)
process.exitCode = total === 0 ? 0 : 1
