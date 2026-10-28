# @loong/i18n

Inggris (bawaan) dan Indonesia untuk semua aplikasi. Tanpa dependensi luar: katalog bertipe, plural, pemformat
angka/tanggal/durasi, pengganti bahasa.

```
import { t } from '../i18n'            // di aplikasi: t bertipe (katalog aplikasi + bersama)
t('bo.users.title')                    // "Users"        (id: "User")
t('bo.users.summary', { count: 3, total: 120000 })   // "3 users · total balance Rp 120,000"
```

## Aturan emas

1. **Inggris adalah sumber kebenaran.** Kunci dan `{parameter}` diturunkan dari teks Inggris; katalog Indonesia harus
   punya kunci yang sama (dicek tipe + tes). Teks Indonesia = teks lama di kode, dipindahkan **apa adanya**
   (jangan "memperbaiki" kalimatnya; hanya ubah bagian dinamis menjadi `{parameter}`).
2. **Bahasa bawaan Inggris**, tidak menebak dari peramban. Pilihan disimpan per aplikasi (`loong:locale`); `?lang=id`
   menetapkan bahasa lalu dihapus dari alamat. Pengganti: `<LocaleSwitcher />` dari `@loong/shell`.
3. **Jangan menyusun kalimat dari potongan terjemahan** ("Pilih " + label + " sekarang"): urutan kata beda antar
   bahasa. Satu kalimat = satu kunci dengan `{parameter}`.
4. **Angka, uang, tanggal, jam, jarak, durasi tidak diformat tangan.** Pakai `formatRupiah/formatKm/formatKg/formatTime/
   formatDate/formatDuration/formatAgo/formatList...` (dari `@loong/domain`, atau langsung `@loong/i18n`), atau
   pemformat bernama di dalam teks: `{total|rupiah}`.
5. **Jangan terjemahkan data**: nama orang, nama tempat/alamat gazetteer, isian pengguna (catatan, nama barang yang
   diketik), nama produk/merek (loong.lat, loong.lat, Mapbox, Muse Spark, BCA, QRIS), kode plan (PL-2041), slug rute
   dan nama parameter URL (`/wallet`, `/plan`, `?step=`; selalu English), nilai enum (`'menunggu_driver'`). Nilai enum ditampilkan lewat label domain.
6. **Semua teks yang terlihat atau terbaca** diterjemahkan: isi, `aria-label`, `title`, `placeholder`, `alt`, toast,
   judul dokumen, pesan validasi, teks `sr-only`, kosong/galat/memuat.

## Kunci

- Format: `<pemilik>.<area>.<nama>` huruf/angka dan titik, camelCase pada nama akhir. Boleh `_` hanya pada akhiran
  jamak atau kunci yang memang mengikuti nilai enum (`dom.planStatus.menunggu_driver`).
- Pemilik: `dash.*` (console), `bo.*` (Backoffice), `drv.*` (Driver), `ui.*` (kit UI), `shell.*` (kerangka + data),
  `srv.*` (teks netral-bahasa dari backend tiruan), `dom.*` / `cat.*` / `fmt.*` (domain, katalog barang, pemformat),
  `ai.*` (teks AI tiruan di mock-api).
- Plural: `key_one` / `key_other` dengan parameter `count` (`{count} stops`). Indonesia cukup `key_other`.
- Teks dengan tag: `'Butuh bantuan? <link>Hubungi kami</link>'` dipanggil dengan `tr(key, { link: (c) => <Link ...>{c}</Link> })`.
- Parameter bersarang: nilai parameter boleh `Msg` (`{ key, params }`) yang dirender dalam bahasa yang sama.
- Pemformat bernama: `{x|rupiah}` `{x|number}` `{x|km}` `{x|kg}` `{x|pct}` `{x|duration}` (menit) `{x|time}` `{x|date}`
  `{x|datetime}` `{x|dateLong}` `{x|day}` `{x|ago}` `{x|decimal}` `{x|decimal2}` `{x|float}` `{x|phone}`.
  Angka tanpa pemformat ditulis apa adanya (`String(n)`); jangan taruh angka mentah yang butuh pemisah ribuan.
- Hindari kurung kurawal dan `<` literal di teks. Penampung yang tak terpakai di salah satu bahasa ditolak tes.

## Berkas

```
packages/i18n/src/messages/{en,id}/   katalog bersama: format, domain, ui, shell, srv/{rpc,engine,ops,api}
<app>/src/i18n/{en,id}/<bagian>.ts    katalog aplikasi, satu berkas per bagian; index.ts menggabungkan
<app>/src/i18n/index.ts               mendaftarkan katalog; mengekspor t dan tr bertipe
```

Katalog Inggris ditulis `export const enX = { ... } as const`; Indonesia `export const idX: Translation<typeof enX> = { ... }`.
Tes `catalogProblems(en, id)` (tiap aplikasi punya `i18n/i18n.test.ts`) memastikan kunci, `{parameter}`, dan `<tag>` sama.
Kunci yang dibuat dinamis (`` t(`x.${status}` ``) tidak diperiksa tipe: tulis tes yang menelusuri semua nilainya, atau
pakai peta label dengan getter seperti `PRESET_LABEL` di domain.

## Teks bersama dan tidak bersama

- Label domain (status, preset, kendaraan, top up, katalog barang) sudah bersama: `PLAN_STATUS_LABEL[s]`,
  `PRESET_LABEL[p]`, `VEHICLE_TYPE_LABEL[v]`, `TOPUP_METHOD_LABEL[m]`, `ISSUE_LABEL[i]`, `catalogName(key)`,
  `itemLabel(item)`. Label ini getter: selalu mengikuti bahasa saat dibaca.
- Butuh teks yang sudah ada di katalog pemilik lain? Jangan edit berkas orang lain; buat kunci di katalog Anda
  (duplikasi kecil dapat diterima).
- Konstanta tingkat modul yang berisi teks (`const NAV = [{ label: 'Beranda' }]`) dievaluasi sekali, jadi tidak
  mengikuti bahasa. Simpan **kunci** (`labelKey`) atau bungkus dengan fungsi, lalu panggil `t()` saat render.

## Rendering dan perubahan bahasa

`AppRoot` (shell) memasang `I18nBoundary`: saat bahasa berganti seluruh pohon di bawahnya dipasang ulang dan semua
query dimuat ulang. Karena itu `t()` boleh dipanggil di mana saja saat render (tanpa hook) dan tidak ada teks basi.
Jangan menyimpan hasil `t()` di state/ref/modul yang hidup lintas perubahan bahasa; simpan kunci atau nilainya.

## Teks dari backend tiruan (`mock-api`)

Backend berjalan di iframe `api.<domain>` (origin lain), jadi tidak tahu bahasa layar. Dua mekanisme:

1. **Teks yang disimpan** (peristiwa plan, notifikasi, alasan opsi, rincian tarif, catatan saldo, galat) disimpan
   sebagai `Msg` = `{ key, params }` dengan parameter **mentah** (angka, ISO, bukan teks berformat) dan dirender di
   layar dengan `renderMsg(msg)` dalam bahasa saat itu. Kunci ada di katalog bersama `srv.*` (`msg('srv.x', {...})`
   dari `@loong/i18n` bertipe). `ApiError` menerima `string | Msg`; `errorMessage()` dari `@loong/data` merendernya.
2. **Teks sesaat** (jawaban Copilot, hasil baca foto/suara, urai maksud, insight, progres hitung) dibuat server
   dalam bahasa permintaan: klien menyertakan `locale` di setiap panggilan RPC, `createApi(server, ctx, locale)`
   meneruskannya, dan kode memakai `translatorFor(locale)` / `format*(x, locale)` (bukan `t` global, karena satu
   server melayani banyak bahasa bergantian).

## Pengujian

Bahasa bawaan di Vitest (Node) adalah Inggris. Tes yang membandingkan teks sebaiknya merender `Msg`
(`renderMsg(m, 'en')`) atau memeriksa kunci, bukan teks Indonesia. Untuk tes khusus Indonesia panggil
`setLocale('id')` dan kembalikan di `afterEach`.

## Menambah bahasa

Tambah kode di `LOCALES`, `LOCALE_NAME`, `LOCALE_SHORT`, `INTL_TAG` (`locale.ts`), buat berkas katalog
`messages/<kode>/` dan `<app>/src/i18n/<kode>/`, lalu daftarkan di `messages/index.ts` dan `i18n/index.ts` tiap aplikasi.
Jika bahasanya punya bentuk jamak selain satu/lainnya, `Translation` dan `check.ts` perlu dilonggarkan untuk akhiran itu.
