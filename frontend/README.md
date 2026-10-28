# loong.lat frontend

Platform kurir yang menyusun **rute pengiriman termurah, terpendek, atau tercepat** untuk banyak paket sekaligus, lalu menjelaskan pilihannya dengan angka yang bisa ditelusuri. Repositori ini berisi tiga aplikasi yang **berjalan penuh tanpa backend**: data, engine optimasi rute, dan AI dipalsukan di browser di balik satu interface (`ApiClient`), sehingga backend asli tinggal dicolokkan nanti.

| Aplikasi | Untuk siapa | Inti |
|---|---|---|
| **Dashboard** (`console.`) | Pengirim (UMKM, individu) | Saldo loong.lat (saldo di bilah atas dan Beranda, halaman riwayat, top up lewat virtual account atau QRIS, pembayaran pesanan memotong saldo). Wizard 4 langkah: rute dan jadwal (termasuk batas waktu per pengiriman dan kalimat bebas "Ceritakan kebutuhan Anda"), barang (manual, foto, suara), pilih opsi dengan peta, kedalaman pencarian (Cepat, Teliti, atau Teliti dengan tinjauan AI), ringkasan AI, dan Copilot, tinjau dan konfirmasi. Lacak pengiriman live, dengan bukti pengantaran (kode OTP atau foto serah-terima beserta ringkasan AI) di bawah tiap titik antar yang selesai. |
| **Backoffice** (`backoffice.`) | Admin dan dispatcher | Pusat komando dengan antrean plan (berisi yang perlu tindakan), penugasan driver otomatis dengan penugasan manual sebagai pengecualian, peta live, CRUD kendaraan dan driver, halaman Saldo & top up (total saldo pengguna, semua top up dengan filter status/cara bayar, riwayat saldo per user), analitik dengan ringkasan AI. Palet perintah ⌘K. |
| **Driver** (`driver.`) | Kurir di jalan | Tugas dan urutan ambil/antar, kartu "Berikutnya", geser untuk tiba dan selesai, bukti OTP atau foto, lapor masalah. PWA. |

## Alamat

Tiap aplikasi punya hostname sendiri, **bukan folder path**:

| Aplikasi | Produksi | Dev |
|---|---|---|
| Halaman depan (`landing/`) | `loong.lat` | `loong.localhost` |
| Dashboard pengirim | `console.loong.lat` | `console.loong.localhost` |
| Backoffice | `backoffice.loong.lat` | `backoffice.loong.localhost` |
| Driver | `driver.loong.lat` | `driver.loong.localhost` |
| Backend tiruan | `api.loong.lat` | `api.loong.localhost` |

Domain induk **tidak di-hardcode**: kode menurunkannya dari hostname saat berjalan (`rootHost()` / `appOrigin()` di `packages/domain/src/apps.ts`). Domain apa pun bekerja selama subdomainnya `console.`, `backoffice.`, `driver.`, `api.`.

Pengecualian: build produksi halaman depan (`landing/`) menautkan ke alamat yang ditulis di `landing/.env.production` (`https://console.loong.lat`, `https://backoffice.loong.lat`, `https://driver.loong.lat`), dan `landing/.env` mengisi canonical/og:url `https://loong.lat`. Keduanya bukan rahasia dan ikut repo. Saat dev tautan tetap diturunkan dari hostname (`console.loong.localhost`, dst.); `npm run preview` memakai build produksi, jadi tautannya menuju domain produksi.

Mengapa ada `api.`: beda hostname berarti beda origin, jadi `localStorage` dan `BroadcastChannel` tidak dibagi antar aplikasi. `api.` adalah backend tiruan yang dimuat sebagai iframe tersembunyi oleh ketiga app (RPC lewat `postMessage` dengan daftar origin yang diizinkan); ia menyimpan state dan membaginya, sehingga plan yang dibuat di `console.` langsung muncul di `backoffice.` dan `driver.`. Nanti hostname yang sama dipakai backend asli.

## Menjalankan

```bash
cd frontend
npm install        # juga menyalin worker MapLibre ke public/ tiap app
npm run dev        # gateway + 4 dev server
```

Buka **http://loong.localhost** (halaman depan, EN/ID, berisi tautan ke ketiga aplikasi dan akun demo), atau langsung `http://console.loong.localhost`.

- `*.localhost` otomatis mengarah ke komputer ini (tanpa mengubah `/etc/hosts`) dan dianggap **konteks aman** oleh Chrome dan Firefox, jadi mikrofon, GPS, dan PWA tetap jalan di dev.
- Gateway memakai **port 80** (tanpa `:port` di URL). Bila port 80 tidak tersedia, otomatis pindah ke 4000 (`http://console.loong.localhost:4000`). Paksa dengan `PORT=4000 npm run dev`.
- Ingin tetap memakai domain `.local`? Tambahkan ke `/etc/hosts` (`127.0.0.1 console.hackathon.local ...`). Catatan: `.local` bukan konteks aman, jadi mikrofon dan GPS butuh HTTPS (mis. sertifikat `mkcert`).
- `.lat` tidak memaksa HTTPS seperti `.app`, jadi di produksi (`https://loong.lat`) aktifkan HTTPS dan pengalihan http -> https di hosting. Tanpa HTTPS, mikrofon, GPS, dan PWA tidak jalan.

> Gunakan **npm**. `~/package.json` memaksa `yarn@1` lewat corepack, sehingga `pnpm` dan `yarn` gagal di bawah folder ini.

### Jalan sungguhan (Mapbox, opsional)

Tanpa konfigurasi apa pun semua jalan: jarak = garis lurus x 1,28 dan rute digambar sebagai lengkung halus. Untuk rute yang mengikuti jalan:

```bash
cp .env.example .env.local     # lalu isi VITE_MAPBOX_TOKEN dengan token publik Mapbox (pk.…)
npm run dev                    # restart bila sudah berjalan
```

- Mapbox hanya **alat bantu**: **Matrix API** untuk jarak lewat jalan antar titik plan (1 permintaan per hitung) dan **Directions API** untuk jalur yang digambar (1 per rute). Urutan stop, pembagian kendaraan, biaya, dan opsi tetap dihitung engine sendiri (`packages/mock-api/src/engine`); penyedia jalan hanya menggantikan fungsi jarak (`RoadKm`) dan memberi `route.geometry`.
- Token dibaca **hanya oleh host `api.`** (`api/vite.config.ts` mengatur `envDir`), jadi tidak masuk bundel console, backoffice, atau driver. Tetap saja token `pk.` terlihat di tab Network, jadi **batasi URL-nya** di dashboard Mapbox (`loong.lat`, `*.loong.lat`, `*.loong.localhost`). `.env.local` diabaikan git.
- Plan baru: jarak, ETA, dan biaya memakai jarak jalan (pada sampel yang dicoba, Kemang ke Senayan, jarak jalan sekitar 30-45% lebih panjang dari perkiraan x 1,28, sebagian karena jalan satu arah). Data seed dan plan buatan generator **tidak diubah angkanya**; hanya jalurnya yang diisi di latar belakang oleh instance host yang memegang lock autopilot (rute berjalan lebih dulu).
- Hasil disimpan di `localStorage` origin `api.` (cache ±40 KB), jadi alamat yang sama tidak meminta lagi dan demo tetap jalan saat offline setelah dijalankan sekali.
- Gagal apa pun (tanpa token, offline, 401/403, 429, timeout) tidak merusak alur: kembali ke perkiraan dan lengkung halus; alasan terakhir tampil di halaman `api.<domain>` baris "Data jalan". Batas Mapbox: 300 permintaan/menit (Directions), 60/menit (Matrix), 25 titik per permintaan.
- Peta menampilkan "Rute © Mapbox" di atribusi bila jalurnya berasal dari Mapbox. Sebelum produksi, baca Mapbox Terms of Service (kewajiban menampilkan kebijakan Directions kepada pengguna, aturan memakai hasilnya di peta non-Mapbox, dan penyimpanan/cache hasil).

### Dengan backend asli (opsional)

Bawaannya tiruan di browser. Untuk memakai backend Go di `../backend` (satu API per aplikasi, **tanpa gateway**: driver `:8081`, backoffice `:8082`, console `:8083`):

```bash
# 1. backend: infrastruktur dan keempat layanan (lihat ../backend/README.md)
cd ../backend && scripts/dev-up.sh
ADMIN_EMAIL=admin@loong.test ADMIN_PASSWORD=<sandi-dev> SIMULATE_PAYMENTS=true \
  sh -c 'for s in optimizer console backoffice driver; do docker compose -f services/$s/compose.yaml up -d --build --wait; done'

# 2. frontend, mode http
cd ../frontend && npm run dev:http        # = VITE_API_MODE=http npm run dev
```

- Paket `packages/http-api` mengimplementasikan `ApiClient` yang sama lewat HTTP (`POST /v1/rpc/<metode>`, aliran NDJSON untuk `plans.computeOptions`, SSE di `/v1/events`). Layar tidak tahu bedanya; `main.tsx` memilih lewat `createApi('console' | 'backoffice' | 'driver')`.
- **Alamat API** diturunkan dari hostname halaman (`console.loong.localhost` memanggil `http://loong.localhost:8083`). Timpa dengan `VITE_API_URL` di `<app>/.env.local`. Tiap layanan menjawab CORS sendiri dan hanya untuk origin yang terdaftar di `CORS_ORIGINS`; bila gateway dev jatuh ke port 4000, tambahkan origin berport itu.
- **Sesi** berupa token bearer di `localStorage` (`loong:http:<app>`); 401 dari backend menghapusnya dan membawa pengguna ke halaman masuk.
- **Driver** harus mengirim posisi supaya bisa ditugaskan: aplikasi driver mengirimnya tiap 8 detik selama online (GPS peramban). Driver yang diam lebih dari 2 menit dianggap offline oleh dispatch. Di desktop tanpa GPS, isi `VITE_DRIVER_FALLBACK_POSITION="-6.261,106.813"` di `mobile/.env.local`. **PIN** driver (6 digit) diisi admin saat membuat driver (Backoffice > Driver).
- **Bahasa di kawat.** Setiap permintaan (panggilan, aliran NDJSON, dan sambungan SSE) membawa `Accept-Language: en|id` sesuai bahasa layar saat itu. Backend memakainya untuk teks yang disusun server (jawaban AI, alasan batal bawaan); selebihnya ia mengirim kunci pesan yang dirender layar.
- **Yang dilayani backend sekarang:** akun dan alamat tersimpan, notifikasi, pengaturan, batal dan jadwal ulang plan, daftar dan detail pengguna serta blokir, Saldo & top up di Backoffice, Copilot dan ringkasan AI, serta baca foto, suara, dan maksud. Pembatalan plan berjalan **dua tahap**: plan kembali dengan `pendingAction: 'cancel'` selama menunggu jawaban driver, layar menampilkan "Membatalkan…" dan menjeda tindakan lain, lalu plan jadi `dibatalkan` atau kembali normal bila driver menolak.
- **Foto dan suara di kawat.** `ImageInput.data` dan `AudioInput.data` adalah `ArrayBuffer` di layar, tetapi badan RPC berupa JSON, jadi dikirim sebagai base64: `ai.readImage` -> `{"input":{"name","type","size","data":"<base64>"}}` dan `ai.transcribe` -> `{"input":{"type":"audio/wav","size","data":"<base64>"}}` (`size` dan `type` adalah byte yang benar-benar dikirim). Foto besar diperkecil dulu di peramban (sisi terpanjang 1280 px, JPEG 0,85; turun bertahap bila perlu) agar badan di bawah ±3 MB; foto yang tak bisa diperkecil dan terlalu besar dijawab mesin lokal. **Input suara:** mode http merekam dengan `MediaRecorder` (maksimal 1 menit), lalu **rekaman dikodekan sebagai WAV 16 kHz mono sebelum dikirim** (didekode dengan `AudioContext`, dijadikan mono, diturunkan ke 16 kHz, PCM 16-bit; Muse Voice hanya menerima WAV PCM mono 16 atau 24 kHz, dan satu menit WAV 16 kHz sekitar 1,9 MB atau 2,6 MB sebagai base64). WAV itu dikirim ke `ai.transcribe` (Muse Voice), lalu transkripnya diperiksa pengguna dan diurai `ai.parseSpeech`; bila merekam, mendekode, atau mentranskripsi gagal (termasuk peramban yang tidak bisa mendekode rekamannya), mikrofon jatuh ke Web Speech API peramban, lalu ke mengetik. Mode tiruan tetap memakai Web Speech API (`ai.transcribe` di tiruan menjawab "belum tersedia").
- **Cadangan AI.** `ai.readImage`, `ai.parseSpeech`, dan `ai.parseIntent` mencoba backend dulu. Bila backend menjawab galat "layanan mati" (`jaringan` atau `layanan`, mis. gateway AI jatuh atau belum dikonfigurasi), klien memakai mesin deterministik lokal (`packages/mock-api/src/ai`) sehingga wizard tetap jalan. Penolakan seperti `ditolak` (fitur dimatikan admin) dan galat validasi diteruskan apa adanya, tidak diulang di klien. Copilot, ringkasan AI, dan transkripsi tidak punya cadangan; layar menyembunyikannya lewat bendera `settings.get`, yang oleh backend dimatikan bila AI tidak tersedia.
- **Yang tetap di klien:** pencarian tempat (`places.*`, gazetteer) dan jam nyata (`demo.state`, untuk ETA dan posisi). **Panel Demo hanya untuk mode tiruan**: di mode http ia tidak dirender (`RemoteApi.kind` bernilai `'http'`), jadi tidak ada layar yang memanggil `demo.*` selain jam nyata itu. Selain itu tidak ada lagi fitur yang dijawab kosong atau ditolak dengan "Fitur ini belum tersedia".
- **Armada.** Aplikasi pelanggan tidak mendapat data armada atau driver dari backend asli (plat, model, tarif, driver adalah data perusahaan): wizard tidak memeriksa muat di klien (petunjuk "muat di motor" tidak muncul) dan backend menolak muatan yang tak muat di kendaraan mana pun saat opsi dihitung.
- **Pembayaran dan akun demo.** Backend asli baru menerima pembayaran dari saldo, jadi langkah tinjau hanya menawarkan "Saldo" (transfer dan tunai hanya di backend tiruan), dan layar masuk tidak menawarkan akun demo (akun itu hanya ada di backend tiruan). Tombol "Simulasikan pembayaran" di halaman top up hanya muncul bila klien dibangun dengan `VITE_SIMULATE_PAYMENTS=1` (skrip `npm run dev:http` menyalakannya) dan backend dijalankan dengan `SIMULATE_PAYMENTS=true`; tanpa itu top up lunas hanya lewat konfirmasi penyedia pembayaran. Bendera dibaca saat build (`REAL_BACKEND`, `SIMULATED_PAYMENTS` di `@loong/data`).
- **Aplikasi driver harus tetap hidup.** Peramban membekukan tab yang tersembunyi dan layar ponsel yang mati menghentikan JavaScript, sedangkan backend menganggap driver yang diam lebih dari 2 menit sudah pergi dan mencabut tugas yang akan berangkat. Karena itu klien menahan layar tetap menyala (Screen Wake Lock) selama driver online dan mengirim posisi seketika saat halaman tampil lagi. Di desktop, biarkan tab driver terlihat; GPS di latar belakang dan layar mati butuh aplikasi native.
- **Posisi driver.** Peramban melaporkan lokasi *sebenarnya* bila izin GPS diberikan, dan dispatch memakai radius 15 km dari titik ambil: driver yang berada di luar radius tidak mendapat pesanan "sekarang". `VITE_DRIVER_FALLBACK_POSITION` hanya dipakai bila GPS tidak tersedia atau ditolak.
- **Kedalaman pencarian dan ringkasan AI di kawat.** `plans.computeOptions(id, { effort })` mengirim badan `{"id","effort"}` (`effort`: `fast` bawaan, `thorough`, `reviewed`; opsi disebar oleh `toBody`, lihat `...opts` di `wire.ts`). Peristiwa `progress` boleh membawa `msg` (kunci pesan, dirender layar dan didahulukan atas `label`); sub-langkah pencarian mengulang `step` tahap 3, jadi layar tetap menampilkan empat tahap. `OptionSet.method` (`effort`, `trials`, `rounds`, `polished`, `reviewer`, `proven`, `roadSource`) dan `OptionSet.report` (`text`, `locale`, `at`) dirapikan di `normalize.ts`; kalimat "cara menemukannya" hanya menyebut bagian yang ada dan menyebut "optimal" hanya bila `method.proven`. `ai.explainOptions(planId)` (hanya console) adalah aliran `{"planId"}`: satu `token` berisi seluruh teks lalu `done`; tidak punya cadangan lokal, jadi layar menampilkan galatnya (`srv.ai.report.noOptions`, `srv.ai.report.unverified`, atau galat AI umum).
- **Tes ujung ke ujung** dari sisi layar, memakai klien yang sama: `LOONG_LIVE=1 npx vitest run packages/http-api/src/live.test.ts` (butuh backend di atas; `LOONG_ADMIN_EMAIL`/`LOONG_ADMIN_PASSWORD` bila berbeda). Menulis data ke database dev; dilewati tanpa `LOONG_LIVE`. Cerita laporan AI (kuotasi `reviewed`, `ai.explainOptions`) hanya jalan dengan `LOONG_LIVE_AI=1` di samping `LOONG_LIVE=1` dan butuh gerbang AI terkonfigurasi.

### Akun demo

| Peran | Masuk dengan |
|---|---|
| Pengirim | `dina@contoh.id` / `demo1234` |
| Admin | `admin@loong.lat` / `admin1234` |
| Driver | `0812-3456-7801` / PIN `123456` |

Di layar masuk tiap aplikasi ada kotak **"Trying the demo?"** (Indonesia: "Mencoba demo?") yang mengisinya otomatis.

### Saldo dan top up (tiruan)

- Console: saldo tampil di bilah atas dan kartu Beranda; `/wallet` berisi riwayat transaksi dan top up yang menunggu; `/wallet/top-up` untuk jumlah (Rp 10.000 sampai Rp 5.000.000) dan cara bayar (virtual account BCA/Mandiri/BNI/BRI atau QRIS).
- Pembayaran top up **disimulasikan**: halaman instruksi punya tombol "Simulasikan pembayaran" yang menganggap uang sudah diterima. Tidak ada uang sungguhan, dan kode QRIS hanya gambar contoh. Backend asli menerima konfirmasi dari penyedia pembayaran.
- Konfirmasi pesanan dengan metode **Saldo** memotong saldo sebesar biaya; saldo kurang ditolak dengan pesan jelas dan tombol "Top up" yang membawa pengguna kembali ke pesanannya (`?next=` hanya menerima jalur internal). Pembatalan mengembalikan saldo penuh sekali saja. Transfer dan tunai tidak menyentuh saldo.
- Saldo = jumlah buku besar (`db.walletTx`), diubah hanya lewat `postTx` di `packages/mock-api/src/server/wallet.ts`. Backoffice: halaman **Saldo & top up** (`/balances`, juga lewat ⌘K) merangkum total saldo pengguna, top up menunggu, dan top up masuk hari ini/30 hari; daftar user punya kolom Saldo dan total; detail user memuat daftar top up dan riwayat saldo. Semuanya baca-saja (admin belum bisa menyesuaikan saldo).

### Panel Demo

Tambahkan `?demo` ke alamat mana pun (mis. `http://console.loong.localhost/?demo`) untuk memunculkan tombol Panel Demo (hanya di mode tiruan; di mode http panel tidak pernah dirender). Panel ini mengatur: kecepatan jam virtual (1x, 10x, 60x) dan lompat maju 5/30 menit, autopilot driver, kegagalan tiruan (error, lambat, offline, data kosong) untuk menguji semua keadaan layar, membuat plan baru ke antrean, memicu driver terlambat, dan mengembalikan data ke awal. Panel tersembunyi tanpa flag itu.

## Bahasa (Inggris dan Indonesia)

Semua aplikasi tersedia dalam **Inggris (bawaan)** dan **Indonesia**. Pengganti **EN | ID** ada di halaman Akun (Dashboard), bilah atas dan Pengaturan (Backoffice), serta halaman masuk dan Profil (Driver). Pilihan disimpan per aplikasi; `?lang=id` atau `?lang=en` di alamat mana pun menetapkannya (lalu dihapus dari alamat). Bahasa bawaan selalu Inggris, tidak menebak dari peramban.

- **Cara kerja:** `packages/i18n` (tanpa dependensi luar) memuat `t()` bertipe (kunci, `{parameter}`, plural `_one/_other`, teks bertag), pemformat angka, uang, jam, tanggal, durasi, dan "x menit lalu" yang mengikuti bahasa (zona waktu tetap WIB), serta `I18nBoundary` yang memasang ulang tampilan saat bahasa berganti. Teks Inggris adalah sumber kunci; tes memastikan katalog Indonesia punya kunci dan `{parameter}` yang sama. Panduan lengkap penulisan kunci dan teks ada di `packages/i18n/README.md`.
- **Backend tiruan ikut dua bahasa.** Teks yang disimpan (peristiwa plan, notifikasi, alasan opsi, rincian tarif, catatan saldo, galat) disimpan sebagai pesan netral-bahasa (`Msg`: kunci + angka mentah) dan dirender di layar dengan bahasa saat itu, jadi data lama ikut berganti bahasa. Teks sesaat (Copilot, baca foto, suara, maksud, insight) dibuat server dalam bahasa permintaan (setiap panggilan RPC membawa `locale`). Pengklasifikasi AI tiruan memahami masukan Inggris dan Indonesia apa pun bahasa layarnya ("Why is it late?" dan "Kenapa terlambat?").
- **Yang sengaja tidak diterjemahkan:** nama orang, tempat, dan alamat (data), isian pengguna (catatan, nama barang yang diketik), nama merek, slug URL dan nama parameter (`/wallet`, `/plan`, `?step=`; selalu English), dan pesan diagnostik developer untuk Mapbox di halaman `api.<domain>`. Nama barang katalog dan label alamat bawaan (Rumah/Toko/Kantor) mengikuti bahasa.
- **Format:** `Rp 46,500`, `1.8 km`, `14:20` (Inggris) dan `Rp 46.500`, `1,8 km`, `14.20` (Indonesia).
- **Menambah teks:** tambahkan kunci Inggris di katalog bagian yang bersangkutan dan padanannya di Indonesia; `npm test` dan `npm run typecheck` menolak kunci atau parameter yang tidak cocok. `npm run check:i18n` membantu menemukan teks yang terlewat.

## Skenario demo lintas aplikasi (5 menit)

Nama tombol di skenario ini ditulis dalam bahasa Indonesia. Pilih **ID** di pengganti bahasa tiap aplikasi (atau buka dengan `?lang=id`) agar sama persis; di Inggris, mis. "Buat pengiriman" = New delivery, "Perlu tindakan" = Needs attention, "Tugaskan manual" = Assign manually, "Panel Demo" = Demo panel.

Buka tiga tab: `console.`, `backoffice.`, `driver.` (tambahkan `?demo` pada satu atau lebih).

1. **Console** (Dina): "Buat pengiriman". Pilih ambil di Kemang, tambah dua tujuan. Di kartu **Ceritakan kebutuhan Anda** (bawah langkah rute) tulis "paling hemat tapi sampai sebelum jam 5" dan tekan **Pahami**; batas waktu juga bisa diisi per pengiriman. Di langkah barang tekan **Foto barang** dan unggah gambar bernama `foto-laptop.jpg`, atau **Ucapkan** lalu pilih contoh ucapan. Di langkah opsi bandingkan kartu, lihat rute tergambar di peta, tanya Copilot "Bandingkan semua opsi". Konfirmasi.
2. **Backoffice** (admin): sistem menugaskan driver sendiri begitu pesanan dikonfirmasi (driver terdekat untuk pesanan sekarang, driver yang kosong pada jamnya untuk pesanan terjadwal); alasannya tercatat di riwayat plan. Plan hanya masuk antrean **Perlu tindakan** bila belum ada driver yang cocok: tekan **Tugaskan manual** untuk melihat saran dan menugaskan sendiri. Matikan **Tugaskan driver otomatis** di Pengaturan untuk mendemokan alur manual.
3. **Driver** (Budi atau driver yang ditugaskan): kartu **Tugas baru** muncul. **Mulai rute**, geser untuk tiba, masukkan kode OTP penerima (di mode demo kode ditampilkan), geser untuk selesai. Di mode demo, pilih **Recipient has no code? Use a proof photo** lalu ketuk foto contoh: foto dikecilkan dan dikirim, dan ringkasan AI-nya muncul di console pada titik antar itu. Ulangi sampai rute selesai.
4. Kembali ke **Console**: peta dan perkiraan tiba bergerak live, status berubah sampai selesai, struk tersedia. Di **Backoffice**, peta langsung dan analitik ikut berubah.
5. Dari Panel Demo tekan **Driver telat 15 mnt**: antrean menandai anomali, Copilot menjelaskan penyebab dan langkah yang disarankan, Ringkasan AI di Analitik menyebutnya.

## Fitur AI (tiruan, siap diganti Meta)

Semua fitur AI memakai `ApiClient.ai` dan hanya **mengusulkan**; tidak ada yang mengubah data tanpa tindakan pengguna. Admin bisa mematikannya per fitur di Backoffice > Pengaturan (berlaku untuk semua pengguna; layar pengguna otomatis kembali ke isian manual).

| Fitur | Di mana | Cara kerja tiruan | Nanti (Meta) |
|---|---|---|---|
| Ringkas foto bukti antar | Console, detail pengiriman (di bawah titik antar yang selesai) | Hasil deterministik per berkas: kata di nama berkas atau hash memilih satu dari tiga skenario (penerima memegang paket, paket di depan pintu, foto contoh). Tidak membaca piksel, jadi di demo pakai foto contoh dari tombol demo Driver; pada plan seed PL-2026 (`pln-26`) ada satu foto contoh dengan ringkasannya | `muse-spark-1.3` (gambar + keluaran terstruktur) |
| Baca foto barang | Console, langkah 2 | Hasil deterministik per berkas (nama dan ukuran), dari katalog barang; barang tambahan sengaja berkeyakinan lebih rendah sehingga alur "periksa dulu" terlihat | `muse-spark-1.3` (gambar + keluaran terstruktur) |
| Input suara | Console, langkah 2 | Pengenalan suara peramban (Chrome/Edge) bila ada; selain itu ketik atau pilih contoh. Parser aturan untuk jumlah, jenis, berat, dan "mudah pecah". Dengan backend asli: rekaman dikodekan sebagai WAV 16 kHz mono dan dikirim ke `ai.transcribe` (Muse Voice), transkripnya dikoreksi pengguna lalu diurai Spark | `muse-voice-transcribe-1.0` + Spark |
| Kalimat bebas ke prioritas | Console, langkah 1 | Kata kunci menjadi chip prioritas dan batas waktu yang bisa dibuang satu per satu; batas waktu dipasang ke semua pengiriman (bisa diubah per pengiriman di kartunya) | Spark, keluaran terstruktur |
| Copilot | Console (langkah 3 dan detail), Backoffice (detail plan) | Jawaban dirakit dari data plan sungguhan sehingga angkanya selalu sama dengan kartu di layar; jejak alat terlihat, jawaban mengalir, bisa dihentikan, disalin, diulang, dan dinilai | Spark + tool calling |
| Saran driver | Backoffice | Skor dari jarak ke titik ambil, muat, dan rating; kandidat di bawah skor 40 tidak disebut "disarankan" | Spark + `run_optimizer` |
| Ringkasan opsi | Console, langkah 3 (kartu "Ringkasan AI", hanya bila fitur Copilot menyala) | Teks biasa yang dirakit deterministik dari angka OptionSet (opsi yang direkomendasikan, hemat, yang termurah dan tercepat, cara pencarian dari `method`; "belum terbukti optimal" karena tiruan tidak pernah membuktikan), dalam bahasa layar, disimpan sebagai `optionSet.report` | Spark; server memeriksa tiap deretan angka terhadap fakta lalu mengirim teks utuh |
| Pemeriksaan foto bukti antar | Console, detail plan (kartu "AI check" pada stop antar yang selesai dengan foto) | Setelah driver melampirkan foto serah-terima, console menganalisisnya di latar belakang dan mengisi `proof.analysis`: ringkasan, tiga pemeriksaan (penerima, paket, kualitas foto) dan keyakinan. Hanya menjelaskan isi foto, tidak mengenali orang, keputusan tetap di manusia. Hasilnya tiba beberapa detik setelah foto lewat sambungan SSE; bila AI mati atau gagal kartu tidak muncul dan fotonya tetap tampil. Teks model disimpan dalam dua bahasa (`srv.ai.text`, pemformat `localized`), jadi ikut bahasa layar. Mode tiruan memakai penganalisis deterministik | Spark vision |
| Ringkasan analitik | Backoffice > Analitik | Narasi dari data plan dengan angka yang bisa ditelusuri | SQL menghitung, Spark menarasikan |

**Engine optimasi rute adalah milik kita** (`packages/mock-api/src/engine`): heuristik pickup-delivery dengan kandidat partisi (gabung semua, pisah per jenis kendaraan, k-means), tiga mode urutan (jarak terpendek, antar lebih awal, dan pengejar batas waktu yang hanya aktif bila ada batas waktu), empat preset, baseline "kirim terpisah" untuk menghitung penghematan, dan **batas waktu per pengiriman** (isian di langkah 1, tiap titik antar bisa berbeda) yang menyingkirkan opsi yang tidak sempat memenuhi semuanya; bila tak ada yang memenuhi, alasan tiap opsi menyebut pengiriman mana yang terlambat. Layanan pihak ketiga hanya pembantu (peta, pencarian lokasi).

**Kedalaman pencarian** (`computeOptions(id, { effort })`, dipilih pengirim di langkah 3). `fast` adalah perilaku di atas. `thorough` menambah cara pengelompokan lain (k-means yang dimulai dari tiap pengiriman, pengelompokan bertahap, k sampai 5) lalu memoles: urutan stop tiap pemenang dicoba ulang dari urutan acak bersemai tetap. `reviewed` menunda pemolesan, lalu hingga 3 ronde: peninjau (aturan bawaan; tiruan tidak punya peninjau AI, jadi `method.reviewer` selalu `rules`) meminta percobaan ulang pada pemenang atau menyatakan puas bila dua ronde berturut-turut memperbaiki sasaran kurang dari 1 persen; pemolesan di akhir. Kandidat `fast` selalu ikut dan percobaan hanya menambah calon, jadi hasilnya tidak pernah lebih buruk. `method.trials` adalah jumlah penyelesaian yang benar-benar dijalankan; tiruan tidak membuktikan optimal sehingga `proven` tidak pernah diisi. Jeda antar catatan progres lebih pendek daripada backend asli (`SEARCH_PAUSE_MS` di `server/api.ts`). Pada satu pengiriman tidak ada yang bisa dicoba, jadi ketiga tingkat menjawab sama.

> **Kunci API:** kunci Meta tidak pernah ditulis ke repositori. Bila integrasi live dikerjakan, kunci hanya ada di backend atau `.env` yang di-gitignore, tidak masuk bundel frontend. Kunci yang pernah ditempel di obrolan sebaiknya diganti (rotate).

## Perintah

| Perintah | Fungsi |
|---|---|
| `npm run dev` | Gateway (80) + console (4171) + backoffice (4172) + driver (4173) + api (4174) |
| `npm run dev:http` | Sama, tetapi aplikasi memakai backend asli (`VITE_API_MODE=http`; lihat "Dengan backend asli") |
| `npm run build` | Build semua app lalu rakit ke `dist/` (`console`, `backoffice`, `driver`, `api`) |
| `npm run preview` | Sajikan `dist/` lewat gateway (butuh `build` dulu) |
| `npm run typecheck` | `tsc --noEmit` di semua workspace |
| `npm run lint` | ESLint (termasuk aturan React Compiler) |
| `npm test` | Vitest: engine rute, seed, API tiruan, klien http-api (rute, bahasa, base64 dan pengecilan foto, cadangan AI), paritas dengan engine Go, AI (dua bahasa), katalog i18n, parser markdown, model wizard (termasuk kedalaman pencarian dan ringkasan AI), perekam suara (±670 tes; tes live dilewati) |
| `npm run build:brand` | Menulis ulang logo (`brand/*.svg`, favicon tiap app, ikon PWA) dari `packages/ui/src/brand.ts` |
| `npm run check:contrast` | Uji kontras APCA untuk semua token |
| `npm run check:i18n` | Mencari sisa teks Indonesia yang masih tertanam di kode (heuristik; abaikan baris dengan `// i18n-ignore`) |

Deploy produksi: arahkan tiap subdomain ke subfolder `dist/` yang sesuai (`console.loong.lat` -> `dist/console`, dst.).

## Struktur

```
dashboard/   console.<domain>     Dashboard pengirim (folder tetap bernama dashboard)
backoffice/  backoffice.<domain>  Backoffice
mobile/      driver.<domain>      Driver (PWA: manifest + service worker, aktif di build produksi)
api/         api.<domain>         Backend tiruan (host iframe + hub RPC)
packages/ui/        token, komponen, peta, palet perintah, Markdown dan jejak alat AI; `src/brand.ts` = sumber geometri logo
packages/i18n/      Inggris + Indonesia: katalog bertipe, plural, pemformat angka/tanggal, pengganti bahasa (lihat README-nya)
packages/domain/    tipe domain, kontrak ApiClient, geo, label status, helper hostname
packages/data/      hook TanStack Query + provider; peristiwa backend meng-invalidasi query
packages/shell/     AppRoot, Panel Demo, lonceng notifikasi, Copilot, Ringkasan AI
packages/mock-api/  implementasi ApiClient in-memory: seed, engine rute, AI tiruan, RPC, penyedia jalan (Mapbox)
packages/http-api/  implementasi ApiClient lewat HTTP ke backend Go (RPC, NDJSON, SSE, sesi bearer, posisi driver)
packages/gazetteer/ titik lokasi Jabodetabek untuk pencarian tanpa API key
brand/              logo SVG (`loong-lat.svg` utama, `loong-lat-mark.svg` tanda saja); jangan disunting tangan, lihat `npm run build:brand`
scripts/            gateway hostname, perakit dist, uji kontras, sinkron worker peta
```

Konteks desain ada di `../.ui-craft/` (`brief.md`, `tokens.md`).

## Keputusan teknis yang perlu diketahui

- **Satu interface, dua implementasi.** Semua layar hanya bicara lewat `ApiClient` (`packages/domain/src/api.ts`). Paket `http-api` mengimplementasikan interface yang sama untuk backend asli (pilih dengan `VITE_API_MODE=http`); `mock-api` menjelaskan bentuk masukan dan keluaran yang diharapkan.
- **Jam virtual.** Posisi kendaraan adalah fungsi murni dari waktu mulai ruas dan jam virtual (`routePositionAt`), jadi semua tab melihat posisi yang sama tanpa simulator dan Panel Demo bisa mempercepat waktu.
- **Angka konsisten.** Kartu opsi, peta, penjelasan Copilot, dan struk membaca data yang sama; tes memeriksa bahwa angka jawaban AI sama persis dengan angka di kartu.
- **Sesi per aplikasi.** Login disimpan per origin; tiap panggilan membawa sesi sebagai `ctx` dan backend tiruan memeriksa peran di setiap metode.
- **Kegagalan bisa diuji.** Mode error, lambat, offline, dan kosong dari Panel Demo berlaku untuk semua metode, termasuk AI; tiap wilayah data punya keadaan loading, kosong, galat dengan coba lagi.

## Penugasan driver

Mesin penugasan ada di `packages/mock-api/src/server/dispatch.ts` (aturan dari data armada dan jadwal, bukan AI). Berjalan saat pesanan dikonfirmasi, saat driver online, dan tiap detik oleh host `api.` untuk plan yang masih menunggu.

- **Pesanan sekarang:** driver yang online dan kosong, kendaraannya aktif, sesuai jenis, dan muat, dalam radius 15 km jalan dari titik ambil; yang terdekat menang (rating memecah seri).
- **Pesanan terjadwal:** driver yang tidak bentrok pada rentang waktunya (jeda aman 15 menit), termasuk yang sedang bertugas sekarang bila kosong pada jam itu. Skor: rating tinggi, beban hari itu yang ringan, lalu kedekatan. Ditugaskan saat konfirmasi; bila belum ada yang cocok, dicoba lagi terus.
- Tugas yang masih jauh di depan (lebih dari 60 menit) belum menandai driver "bertugas", sehingga ia tetap bisa menerima pesanan sekarang yang tidak bentrok. Status berubah menjadi bertugas saat tugasnya mendekat atau dimulai.
- Driver yang offline mengembalikan tugasnya yang belum dimulai ke antrean, lalu dicarikan driver lain. Admin mendapat notifikasi.
- Admin tetap bisa menugaskan atau mengganti driver secara manual, dan tombol "Terapkan saran terbaik" memakai mesin yang sama. Pengaturan > "Tugaskan driver otomatis" mematikan penugasan sendiri.
- Belum ada: penugasan ulang bila driver telat, tawaran ke beberapa driver sekaligus (driver bisa menolak), dan posisi driver yang bergerak nyata (posisi di mock tetap sampai tugas dimulai).

## Batasan yang diketahui

- Mode bawaan tanpa backend: data hidup di `localStorage` origin `api.` dan hilang bila dihapus (atau lewat Panel Demo > Reset).
- Jarak dan jalur jalan butuh internet dan token Mapbox (lihat "Jalan sungguhan"); tanpa itu rute berupa lengkung halus. Rute seed menampilkan jarak lama (x 1,28) di atas jalur jalan sungguhan, jadi angkanya bisa lebih pendek dari yang terlihat di peta.
- Tile peta dari OpenFreeMap tanpa SLA; layar yang memakai peta selalu punya padanan daftar. Siapkan cadangan sebelum demo penting.
- Pengenalan suara di mode tiruan memakai Web Speech API yang hanya ada di Chromium; peramban lain memakai ketik atau contoh ucapan. Di mode http perekaman memakai `MediaRecorder` dan `AudioContext` (hampir semua peramban modern), rekaman dikodekan sebagai WAV 16 kHz mono di peramban, dan transkripsi dilakukan backend.
- Baca foto dan ringkasan foto bukti adalah tebakan deterministik dari nama dan ukuran berkas, bukan visi komputer sungguhan.
- Analitik dihitung di klien dari daftar plan; dengan backend sungguhan ia sebaiknya dihitung di server.
- Bundel sudah dipecah (`peta`, `react`, `vendor`, kode aplikasi 110-200 KB), tetapi MapLibre sendiri sekitar 1,1 MB (296 KB gzip) dan dimuat di semua aplikasi; belum ada pemuatan malas per rute untuk halaman tanpa peta.
- Notifikasi dan preferensi di Backoffice > Pengaturan (selain fitur AI) hanya tersimpan di perangkat dan belum mengirim apa pun.
- Di mode http, hanya `places.*` (gazetteer) dan jam nyata yang masih di klien, dan Panel Demo hanya ada di mode tiruan. Bendera AI di Pengaturan Backoffice yang dikembalikan backend adalah bendera admin DAN gateway AI terkonfigurasi, jadi tanpa gateway semua saklar AI tampak mati meski admin menyalakannya.
- Hasil tinjauan aksesibilitas dan desain sudah diterapkan untuk temuan yang berdampak besar (kontrol geser punya alternatif ketuk dua kali dan Enter, tautan lewati konten, judul halaman, pengumuman jawaban AI untuk pembaca layar, anggaran aksen di antrean). Yang masih terbuka: pemindahan fokus saat berpindah halaman, tombol kecil 32 px di Backoffice desktop, asosiasi label untuk kolom non-input, dan alur stop pengantaran yang masih butuh beberapa langkah (geser tiba, OTP, geser selesai) karena OTP adalah syarat bukti antar.
- Pilihan bahasa disimpan per aplikasi (origin berbeda): mengganti bahasa di Dashboard tidak mengganti Backoffice dan Driver. Data contoh yang berupa isian pengguna (catatan titip ke satpam, alasan batal seed, nama orang dan tempat) tetap dalam bahasa aslinya di kedua bahasa. Hanya dua bahasa; bentuk jamak dan kata kunci pencarian dirancang untuk Inggris dan Indonesia.
- Belum ada tema gelap maupun uji end-to-end otomatis lintas aplikasi; alur lintas aplikasi diuji lewat tes API dan dijalankan manual di Chrome.
