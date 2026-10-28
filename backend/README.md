# loong.lat backend

Backend Go untuk loong.lat: satu layanan per aplikasi (`console-api`, `backoffice-api`, `driver-api`), satu layanan optimasi (`optimizer`), satu gerbang model AI (`ai`), dan Kafka sebagai jembatan event. Mesin optimasi adalah milik sendiri dan berjalan di sandbox Modal; semua fitur AI memakai model Meta lewat gerbang `ai`.

Rancangan lengkap ada di rencana yang disetujui (`~/.claude/plans/`). Dokumen ini hanya merangkum yang sudah ada di repo dan cara menjalankannya.

## Peta repo

```
backend/
  pkg/
    engine/        mesin optimasi: plan model solver cluster validate analysis exact geo
    contracts/     kontrak data bersama: amplop event, topik, payload, tipe kawat yang mencerminkan frontend
    optimizerapi/  kontrak layanan optimizer (permintaan kuotasi, aliran event) + klien untuk layanan lain
    meta/          klien Meta Model API (chat, alat, gambar, aliran, transkripsi Muse Voice); hanya services/ai yang memakainya
    aiapi/         kontrak gerbang AI + klien untuk console dan backoffice (galat bertipe, fake untuk tes)
    agent/         loop pemanggilan alat atas aiapi.Model: alat hanya-baca, validasi argumen, jejak, batas langkah
    notices/       notifikasi per audiens (tabel, tulis idempoten, list/markRead) yang dipakai ketiga layanan
    platform/      fondasi layanan: konfigurasi, log, server, RPC/SSE, sesi, migrasi
    eventbus/      pembungkus Kafka: outbox, inbox, konsumen, DLQ
    routeplan/     aturan jam rute yang dipakai semua layanan: kecepatan per jam, rebase ke posisi driver, siklus hidup rute, status plan
  services/
    optimizer/     kuotasi rute: cmd/optimizer (HTTP), cmd/solver (isi sandbox), cmd/modalprobe (ukur Modal)
    ai/            gerbang model AI: satu-satunya pemegang kunci Meta; profil per kegunaan, batas laju, tanpa mencatat isi
    console/       layanan dashboard (pelanggan): sesi, wallet, draft, kuotasi, konfirmasi, proyeksi status plan
    backoffice/    layanan admin: armada, antrean plan, dispatch (manual dan otomatis), pengaturan
    driver/        layanan aplikasi driver: sesi, tugas, eksekusi stop, posisi
  e2e/             uji ujung ke ujung (tag `e2e`): tiga layanan + optimizer + Kafka + Postgres dalam satu proses
  deploy/          infrastruktur bersama (Postgres + Redpanda), Dockerfile solver
  scripts/         dev-up, dev-down, publish-solver
  testdata/parity/ pasangan (permintaan, OptionSet) yang juga dibaca tes frontend
```

Aturan batas: `services/X` boleh mengimpor `pkg/*`, tidak boleh mengimpor `services/Y`. `boundaries_test.go` menjaganya.

## Mesin optimasi (`pkg/engine`)

Cluster-first + LNS + fleet reduction, validator independen, pembukti optimal. Fitur khusus loong.lat, semuanya bertes:

| Fitur | Kenapa |
|---|---|
| Rute terbuka (`openRoute`) | Kurir selesai di antar terakhir, tidak kembali ke titik awal |
| Biaya = tarif loong (`perOrder`, `surcharge`, `freeApproach`) | Solver meminimalkan angka yang sama dengan yang dibayar pelanggan |
| `Billed`: jarak objektif mengikuti tagihan | Preset "terpendek" memperpendek jarak tertagih, bukan jarak menjemput |
| Objektif `minimizeSumArrival` dan `minimizeBlend` | Preset "tercepat" (rata-rata waktu antar) dan "seimbang" (bobot bilangan bulat) |
| `solver.SolveContext` / `RefineContext`, `Stats.Truncated` | Pembatalan dan batas waktu yang jujur |

Tes properti membandingkan heuristik dengan pencarian lengkap (`exact`) pada instans acak; untuk pengaturan loong, hingga 1% instans boleh berhenti satu langkah dari optimum (hasil kuotasi kecil tetap dibuktikan optimal, lihat bawah).

## Layanan optimizer

```
POST /v1/quote   (NDJSON: progress 1..4, lalu done atau error)
        │
   internal/quote    validasi → data jalan → job → hasil → opsi (waktu per ruas, tarif, alasan Msg)
        │                    │
   roads.Provider      runner.Runner ── Modal sandbox (prod) | solver binary | in-process
   (Mapbox + cache)           │
                       internal/solve  (satu pencarian per preset, paralel; dibuktikan optimal bila ≤5 pengiriman)
```

- Job v1 (`internal/job`) hanya membawa koordinat, bobot, dan matriks jarak: tanpa nama, telepon, atau alamat. Sandbox berjalan tanpa jaringan dan tanpa secret.
- Hasil deterministik: job yang sama menghasilkan keluaran yang sama (per arsitektur). Karena itu opsi bisa di-cache per `inputHash`.
- Kuotasi ≤5 pengiriman juga diselesaikan secara lengkap (`exact`) dan bertanda `proof: optimal`; 6 pengiriman memakan ±7 detik, jadi batasnya 5 (`OPTIMIZER_EXACT_MAX`).
- Distance Mapbox hanya alat bantu data jalan. Tanpa `MAPBOX_TOKEN`, jarak adalah estimasi garis lurus × 1,28.
- **Tanpa tol.** Tidak ada kendaraan kita yang boleh lewat jalan tol, jadi tabel jarak dan garis rute diukur dengan Directions API Mapbox memakai `exclude=toll` dan `continue_straight=false` (`internal/roads/mapbox_tollfree.go`). Matrix API tidak bisa mengecualikan tol (tidak punya `exclude`; permintaan yang memuatnya ditolak HTTP 422), jadi tabel disusun dari leg-leg Directions: satu permintaan lewat waypoint `a, b, a, c, …` (maksimum 25) mengembalikan jarak tiap leg, sehingga satu permintaan mengukur sampai 24 pasangan terarah dan tabel 12 tempat hanya butuh sekitar 6 permintaan, bukan 132. `continue_straight=false` wajib: diukur terhadap Mapbox asli, dengan bawaan (`true`) sepertiga leg berbeda dari permintaan dua titik, dengan `false` tidak ada yang berbeda (dijaga tes hidup `LOONG_LIVE_MAPBOX=1`). Pasangan tanpa rute diperkirakan dari garis lurus dan dihitung (`Matrix.Estimated`). `ROAD_ALLOW_TOLLS=true` mengembalikan Matrix API (dan tol). Tanpa token, jarak perkiraan garis lurus tidak mengenal tol sama sekali.
- Tarif belum punya komponen biaya tol; karena tol dikecualikan, memang tidak ada yang ditagihkan.
- `effort` pada permintaan (`fast` bawaan, `thorough`, `reviewed`) memilih seberapa keras mencari: `internal/orchestrate` menjalankan beberapa job berurutan dan menggabung hasilnya per `Score` (rancangan dan aturan peninjau di `docs/optimizer-search.md`). Kuotasi dalam memakai `OPTIMIZER_TIMEOUT_DEEP` (150 dtk) dan `OPTIMIZER_ROUNDS` (3); peninjau AI lewat `AI_URL`/`AI_TOKEN`, tanpa itu peninjau aturan. Langkah pencarian mengalir sebagai event `progress` ber-`msg` di tahap 3, dan `GET /v1/info` menyebut effort yang didukung dan apakah peninjau AI terpasang. Ukuran nyata (3, 12, 24 shipment) dan batasnya ada di bagian "Hasil pengukuran" `docs/optimizer-search.md`.

### Menjalankan lokal

```bash
cd backend
cp services/optimizer/.env.example services/optimizer/.env      # lalu isi MAPBOX_TOKEN bila ada
go run ./services/optimizer/cmd/optimizer                        # RUNNER=local, OPTIMIZER_DEV=true
curl -s -X POST localhost:8090/v1/quote -H 'Content-Type: application/json' \
  -d @testdata/parity/batching.request.json
```

`RUNNER=exec SOLVER_BIN=bin/solver` menjalankan binary solver sebagai proses anak (jalur kawat yang sama dengan sandbox, tanpa jaringan).

### Modal

Terverifikasi langsung pada akun Modal (Mac arm64 → sandbox amd64 di Modal, hasilnya sama persis dengan solver lokal):

| | Waktu |
|---|---|
| Sandbox dingin, satu job | ±4,5 detik (permintaan sampai keluaran) |
| Sandbox hangat, job lewat `Exec` | ±1,0 detik, p95 1,25 detik |
| Kuotasi utuh dengan Mapbox, hangat | ±1,1 sampai 1,25 detik |
| Kuotasi pertama (sandbox dingin) | ±7 detik |
| Biaya per job hangat (2 core, 1 GiB) | ±$0,00009 |
| Sandbox hangat menganggur | ±$0,31 per jam sampai `MODAL_IDLE_TIMEOUT` mengakhirinya |

Karena itu runner memakai kolam sandbox hangat (`MODAL_POOL_SIZE`, bawaan 2): tiap sandbox mengerjakan satu job pada satu waktu, sisanya antre. Sandbox yang menganggur melewati batasnya dibuang dan diganti; sandbox yang mati di tengah dipakai ulang lewat satu percobaan ulang. Menyalin biner ke tiap sandbox lewat API terlalu lambat (6 sampai 30 detik), jadi biner ditaruh di Modal Volume.

1. Kredensial Modal di `backend/.env` (`MODAL_TOKEN_ID`, `MODAL_TOKEN_SECRET`; gitignored, tidak pernah masuk repo atau log).
2. `scripts/publish-solver.sh` membangun biner linux/amd64, mengunggahnya ke Volume privat `loong-solver` sebagai `solver-<versi>`, lalu membuktikannya: sandbox baru memasang Volume read-only tanpa jaringan dan biner harus melaporkan versi itu. Salin versi yang dicetak ke `MODAL_SOLVER_VERSION`.
3. `RUNNER=modal go run ./services/optimizer/cmd/optimizer`. Ukur kapan saja dengan `go run ./services/optimizer/cmd/modalprobe -n 10` (latensi dingin/hangat, 4 job bersamaan, biaya, dan apakah hasil sama dengan solver lokal).

Alternatif tanpa Volume: image di registry **privat** (`MODE=registry scripts/publish-solver.sh <registry>/<repo>`, `MODAL_IMAGE`, `MODAL_REGISTRY_SECRET`).

Sandbox gagal dimulai ⇒ `Fallback` memakai runner lokal (`OPTIMIZER_FALLBACK=none` mematikannya). Job yang ditolak solver (galat input) tidak pernah dialihkan.

## Layanan ai (gerbang model Meta)

`services/ai` (port 8091) satu-satunya tempat kunci Meta Model API berada. Ia tanpa basis data dan tanpa data bisnis: console-api, backoffice-api, dan optimizer (peninjau AI) memanggilnya lewat `pkg/aiapi` (`AI_URL`, `AI_TOKEN`; tiap pemanggil punya token sendiri) dan console serta backoffice menjalankan sendiri loop alat atas data milik mereka (`pkg/agent`); gerbang hanya meneruskan panggilan model.

- Pemanggil menyebut **profil** (`copilot`, `vision`, `intent`, `insights`, `review`, `report`, `transcribe`); gerbang menetapkan model, upaya penalaran, anggaran token, batas waktu dan ukuran permintaan per profil, membatasi laju tiap pemanggil (token), dan **tidak pernah mencatat isi** pesan, gambar, atau audio (hanya ukuran, hitungan token, dan latensi). Token penalaran memakan anggaran token, jadi tiap profil memakai `reasoning_effort` rendah dan anggaran besar.
- Endpoint: `GET /healthz`, `GET /readyz` (melaporkan `meta.configured`), `GET /v1/info`, `POST /v1/chat` (JSON, atau NDJSON delta dengan `stream:true`), `POST /v1/transcribe` (multipart, satu berkas WAV PCM mono 16/24 kHz; format Meta).
- Tanpa `META_API_KEY` layanan tetap hidup dan menjawab semua panggilan AI dengan 503; console dan backoffice menjawab `srv.api.aiError`, dan bendera `ai.*` di `settings.get` console dan driver mati (bendera admin DAN gerbang terkonfigurasi), sehingga layar tidak menawarkan yang pasti gagal.
- Fitur (semuanya hanya mengusulkan; tak satu alat pun mengubah data): console menyajikan `ai.copilot` (rencana milik pelanggan itu: ringkasan, opsi, perbandingan yang dihitung di Go, saldo, usulan tindakan), `ai.readImage`, `ai.parseSpeech`, `ai.parseIntent`, `ai.transcribe`, dan `ai.explainOptions` (laporan atas opsi hasil hitung, profil `report`: fakta berformat disusun server, teks diperiksa dulu terhadap fakta lalu dikirim utuh, disimpan di `optionSet.report`); backoffice menyajikan `ai.copilot` (operasional) dan `ai.insights`; optimizer memakai profil `review` untuk peninjau AI pada effort `reviewed`. Angka dalam jawaban hanya yang dikembalikan alat atau yang ada di fakta; teks buatan pengguna diperlakukan sebagai data, bukan instruksi. Laporan dan peninjau AI memakai saklar `copilot` yang sudah ada (tanpa pengaturan admin baru).
- Foto bukti antar dibaca AI di console (profil `vision`, tanpa pengaturan admin baru: memakai saklar `vision`): saat driver menutup antar dengan foto, proyeksi menambah satu pekerjaan di `proof_jobs` dalam transaksi yang sama dengan bukti (idempoten per stop), lalu worker `proofs` (hanya berjalan bila `AI_URL` terisi; `PROOF_AI_POLL`, bawaan 2 dtk) meminta model menjelaskan isi foto di luar transaksi mana pun dan menyimpannya di `proof.analysis` pada stop, yang tampil sebagai kartu "Cek AI" di dashboard pemilik. Model hanya menjelaskan apa yang terlihat (tidak mengenali orang), jawabannya divalidasi dan disimpan dalam dua bahasa (`srv.ai.text`); versi plan dan rute tidak berubah. Saklar `vision` mati berarti foto dilewati untuk selamanya; kegagalan sementara diulang (30 dtk, 2 mnt, 10 mnt) hingga empat percobaan, setelah itu hanya kartunya yang tidak ada.
- Env gerbang: `AI_ADDR` (`:8091`), `AI_TOKEN` (boleh beberapa, dipisah koma; satu per pemanggil), `AI_DEV`, `META_API_KEY`, `META_BASE_URL`, `META_MODEL`, `META_TRANSCRIBE_MODEL`, `AI_RATE_PER_MINUTE` (120), `AI_RATE_BURST` (30), `AI_MAX_IN_FLIGHT` (16), dan per profil `AI_<PROFIL>_{MODEL,EFFORT,MAX_TOKENS,TIMEOUT,MAX_BODY_BYTES}`. Env pemanggil: `AI_URL`, `AI_TOKEN`, dan di console `AI_USER_RATE_PER_MINUTE` (20) / `AI_USER_RATE_BURST` (10) per pelanggan.
- Jalankan: `docker compose -f services/ai/compose.yaml up -d --build` dengan `META_API_KEY` dari shell atau `--env-file ../../.env` (kunci tidak pernah masuk image). Tes langsung ke Meta: `LOONG_LIVE_META=1 go test -run Live ./pkg/meta ./services/console/...` setelah memuat `.env` ke lingkungan; tanpa `LOONG_LIVE_META` dilewati.

## Layanan per aplikasi

Aturan bersama ada di `services/README.md` (susunan, `app.Start`, kepemilikan data, versi agregat, bentuk badan RPC, ID). Ringkasnya:

| Layanan | Port | Memiliki | Menerbitkan | Mengonsumsi |
|---|---|---|---|---|
| console-api | 8083 | pelanggan, wallet (ledger), draft dan plan komersial, alamat tersimpan, notifikasi pelanggan | `plan.confirmed`, `plan.cancel.requested/aborted`, `plan.cancelled`, `plan.rescheduled`, `customer.*`, `topup.*`, `wallet.*` | fleet, dispatch, execution, settings, customer (blokir), plan (suara dan permintaan batal) |
| backoffice-api | 8082 | admin, armada, penugasan, pengaturan, antrean plan, proyeksi pengguna dan keuangan, notifikasi admin | `vehicle.*`, `driver.provisioned/updated`, `route.assigned/unassigned`, `settings.updated`, `customer.blocked/unblocked`, `plan.cancel.asked` | plan, customer, driver, position, execution, wallet |
| driver-api | 8081 | kredensial driver, tugas, status eksekusi, posisi, notifikasi driver | `driver.online/offline/position`, `route.started`, `stop.*`, `route.completed`, `route.delayed`, `plan.cancel.accepted/rejected` | dispatch, driver, fleet, plan, settings |

- Penerbitan lewat outbox dalam transaksi yang sama dengan datanya; konsumsi idempoten lewat inbox; event lama dibuang berdasarkan `AggregateVersion`.
- Satu grup konsumen per layanan dan tujuan (`<GroupPrefix>.<tujuan>`). **Dua layanan tidak boleh berbagi `GroupPrefix`**: mereka akan membagi partisi dan masing-masing kehilangan separuh event. Bawaannya nama layanan.
- Hasil `auth.*` menambahkan field `token` (bearer) pada `Session`; kontrak frontend tidak punya tempat untuk token, jadi klien `http-api` frontend (`../frontend/packages/http-api`) menyimpannya dan mengirimnya sebagai `Authorization: Bearer`.
- **Kehadiran driver.** Aplikasi driver mengirim `driver.ping` tiap beberapa detik selama online. Driver yang online tetapi diam lebih dari `fleet.PresenceTTL` (2 menit) dianggap offline: tidak dicalonkan dispatch dan hilang dari peta live, sampai pingnya datang lagi. Driver yang sedang bertugas tidak pernah kedaluwarsa (jalur tanpa sinyal tidak mencabut rutenya). PIN driver (6 digit) diisi admin saat membuat atau mengubah driver; hanya hash argon2id yang meninggalkan backoffice.
- **Rute dilepas.** Driver yang menekan offline mengembalikan semua rutenya yang belum dimulai ke antrean (`route.unassigned{reason: driver_offline}`); driver yang diam lewat `PresenceTTL` kehilangan rute yang berangkat dalam satu jam (`driver_silent`); rute terjadwal jauh di depan dan rute yang sedang berjalan tidak disentuh. Dispatch otomatis mencari driver lain.
- **Keterlambatan.** driver-api menyapu rute yang berjalan (`DELAY_SWEEP_INTERVAL`, 30 dtk) dan menerbitkan `route.delayed` saat keterlambatan melewati 5 menit lalu tiap kelipatan 5 menit; backoffice dan console menampilkannya (status, anomali, notifikasi).
- **Notifikasi.** Tiap layanan menyimpan notifikasi audiensnya sendiri (`pkg/notices`: pelanggan di console, admin di backoffice, driver di driver-api), ditulis dalam transaksi event yang menyebabkannya dengan kunci pesan katalog frontend, dan memberi tahu klien lewat SSE `notifications.changed`.
- **Pengguna dan keuangan** (admin) diproyeksikan di backoffice dari topik customer, wallet, dan plan; memblokir pelanggan menerbitkan `customer.blocked`, console menolak masuk dan mencabut sesinya.
- **Armada bukan untuk pelanggan.** console-api tidak menyajikan `fleet.vehicles.list` maupun `fleet.drivers.list` (plat, model, tarif, jumlah armada, dan data pengemudi adalah data perusahaan dan orang di jalan; tes menjaganya tetap 404). Pelanggan mengetahui plat dan pengemudi pesanannya sendiri dari pesanan itu. Muatan yang tak muat di kendaraan mana pun ditolak saat opsi dihitung (`srv.engine.tooBig`, 422, kolom `shipments`).
- **Bahasa.** Klien mengirim `Accept-Language` (`en`/`id`); server memakainya hanya untuk teks yang disusunnya sendiri (jawaban AI, alasan batal bawaan). Selebihnya server mengirim kunci pesan.

### Pembatalan dan jadwal ulang

Pembatalan plan yang sudah dikonfirmasi adalah commit dua fase yang dikoordinasi console, supaya driver yang sudah berangkat tidak pernah dibatalkan diam-diam dan uang tidak kembali untuk jasa yang sudah berjalan:

1. `plans.cancel` (pelanggan di console, atau admin di backoffice yang menerbitkan `plan.cancel.asked`) memvalidasi, menandai plan `pendingAction: "cancel"`, dan menerbitkan `plan.cancel.requested` untuk **semua** rute.
2. backoffice membekukan rute-rutenya (dispatch tidak menugaskannya); driver-api memberi satu suara per rute: **accepted** (belum dimulai; rute ditahan, atau rute belum dikenal sehingga dicatat sebagai tombstone dan penugasan yang datang belakangan diabaikan) atau **rejected** (sudah berjalan atau selesai).
3. Semua accepted: `plan.cancelled` (komit: status `dibatalkan`, refund penuh bila dibayar dengan saldo tepat sekali, tugas driver ditutup, driver bebas). Ada yang rejected atau suara tidak lengkap dalam `CANCEL_TIMEOUT` (2 menit): `plan.cancel.aborted`, semua tahanan dilepas, plan berjalan seperti semula.

`plans.reschedule` (pelanggan; hanya `menunggu_driver`/`ditugaskan`, jadwal terjadwal minimal 20 menit ke depan) menerbitkan `plan.rescheduled`; backoffice mengembalikan rute yang sudah ditugaskan ke antrean dan dispatch mencari driver untuk jam baru.

- `deliveryCode` (OTP serah-terima) disimpan di server driver untuk verifikasi dan tidak pernah dikirim ke aplikasi driver; pelanggan melihatnya di plan miliknya.

### Port dan pemisahan

Tidak ada gateway. Setiap API punya port sendiri dan dijangkau langsung oleh aplikasinya, jadi tiap layanan bisa dijalankan, di-deploy, dan di-compose terpisah:

| API | Port | Aplikasi | Asal browser yang diizinkan (bawaan) |
|---|---|---|---|
| driver-api | 8081 | mobile (driver) | `http://driver.loong.localhost` |
| backoffice-api | 8082 | backoffice | `http://backoffice.loong.localhost` |
| console-api | 8083 | dashboard | `http://console.loong.localhost` |
| optimizer | 8090 | (internal, tanpa browser) | tanpa CORS |

Karena tidak ada gateway di depannya, tiap layanan menjawab preflight CORS sendiri (`CORS_ORIGINS`, daftar asal yang dicocokkan persis; token ada di header `Authorization`, bukan cookie). Di produksi isi `CORS_ORIGINS` dengan asal sebenarnya, misalnya `https://console.loong.lat`.

Setiap layanan punya `Dockerfile` dan `compose.yaml` sendiri di `services/<nama>/`. File compose itu tidak menyalakan Postgres atau Kafka: ia hanya bergabung ke jaringan Docker `loong` milik `deploy/docker-compose.yml` untuk menjangkau `postgres:5432` dan `redpanda:9092`. Untuk infrastruktur lain, isi `DATABASE_URL`, `KAFKA_BROKERS`, dan (untuk console) `OPTIMIZER_URL` lewat shell atau `.env` di folder layanan itu, lalu hapus bagian jaringan.

```bash
scripts/dev-up.sh                                         # infrastruktur bersama (sekali)
docker compose -f services/optimizer/compose.yaml up -d --build
docker compose -f services/driver/compose.yaml up -d --build
ADMIN_EMAIL=admin@loong.test ADMIN_PASSWORD=... docker compose -f services/backoffice/compose.yaml up -d --build
docker compose -f services/console/compose.yaml up -d --build
```

Kunci untuk optimizer (Modal, Mapbox) diteruskan dari shell atau `--env-file ../../.env`; rahasia tidak pernah masuk image (`.dockerignore`).

**Penting: bawaannya optimizer berjalan lokal.** Tanpa `RUNNER=modal` solver berjalan di dalam proses dan tanpa `MAPBOX_TOKEN` jarak adalah perkiraan (garis lurus x 1,28, tanpa jalur jalan). Jadi `docker compose up` biasa, tes e2e Go, dan cerita live frontend tidak menyentuh Modal maupun Mapbox (tidak ada biaya, tidak ada kunci). Untuk jalur sungguhan:

```bash
RUNNER=modal docker compose -f services/optimizer/compose.yaml --env-file "$PWD/.env" up -d --build --wait
docker logs loong-optimizer-optimizer-1 | grep -E "optimizer ready|job done|quote done"   # runner=modal, roads=Mapbox, sandbox sb-..., ms
```

Satu sandbox hangat melayani semua kuotasi (kolam, `MODAL_POOL_SIZE`, berhenti sendiri setelah `MODAL_IDLE_TIMEOUT`, dan dihentikan saat optimizer dimatikan), jadi di dasbor Modal tampak satu sandbox dengan banyak eksekusi, bukan satu sandbox per kuotasi. Kuotasi pertama ±4 dtk (jalan Mapbox dan sandbox), berikutnya ±1 dtk (cache jalan, sandbox hangat).

### Menjalankan stack lokal

```bash
scripts/dev-up.sh                       # Postgres (55432) + Redpanda (19092), membuat topik
export TEST_DATABASE_URL='postgres://console:loong_dev@127.0.0.1:55432/console?sslmode=disable'
go test ./...                           # tes basis data memakai skema per tes, jadi aman dijalankan berdampingan
LOONG_E2E=1 go test -tags e2e -count=1 -v ./e2e   # menghapus database dev dan topik loong.* lebih dulu
scripts/dev-down.sh                     # menghentikan infrastruktur; volume dipertahankan
```

Uji ujung ke ujung (sembilan skenario, ±1 menit) memeriksa:
- **Alur utama**: pelanggan membayar kuotasi dengan saldo, plan masuk antrean backoffice, dispatch otomatis menugaskan driver, driver menyelesaikan semua stop dengan OTP, status `selesai` tiba di console dan backoffice, tidak ada event yang masuk DLQ. Backoffice yang mati saat plan dikonfirmasi mengejar ketinggalan begitu hidup lagi.
- **Pembatalan**: pelanggan membatalkan plan yang sudah ditugaskan (refund tepat sekali, tugas driver hilang, driver bebas, notifikasi ketiga audiens); admin membatalkan dari backoffice; pembatalan ditolak setelah driver berangkat dan tidak menggerakkan uang.
- **Jadwal ulang** mengembalikan rute lewat dispatch; **driver offline** melepas rutenya dan dispatch menugaskannya lagi saat ia kembali.
- **Pengguna dan keuangan** mengikuti wallet; **pemblokiran** menolak masuk dan mencabut sesi; **pengaturan** admin sampai ke console lewat Kafka; **akun dan alamat** hidup di console dan nama baru sampai ke backoffice.

Dari sisi frontend, `LOONG_LIVE=1 npx vitest run packages/http-api/src/live.test.ts` menjalankan cerita yang sama lewat klien `http-api` terhadap layanan yang berjalan (butuh `SIMULATE_PAYMENTS=true` di console dan admin dev di backoffice; lihat `../frontend/README.md`).

Belum ada di irisan ini: `places` di sisi server (pencarian tempat masih di klien dengan gazetteer), pembayaran sungguhan (virtual account/QRIS; saat ini `SIMULATE_PAYMENTS` untuk pengembangan), dan penugasan ulang otomatis berdasarkan keterlambatan (keterlambatan hanya ditampilkan).

## Tes

```bash
go vet ./... && go test -race ./...
# properti vs pembukti optimal, lebih lebar:
VRP_EXACT_INSTANCES=600 VRP_EXACT_FIRST_SEED=9000 go test ./pkg/engine/exact -run Loong -v
# pasangan paritas dengan frontend (setelah mengubah engine atau tarif):
UPDATE_PARITY=1 go test ./services/optimizer/internal/quote -run Parity
(cd ../frontend && npx vitest run packages/mock-api/src/engine/backend-parity.test.ts)
```

Tes paritas frontend memeriksa aturan yang diandalkan wizard, bahwa semua kunci pesan terdaftar di katalog EN dan ID, dan bahwa engine tidak pernah kalah dari mock yang digantikannya.

## Keamanan

- Token Modal, Mapbox, dan Meta hanya di `.env` (gitignored) atau variabel lingkungan; tidak pernah di repo, bundel frontend, atau log (galat Mapbox tidak memuat URL bertoken).
- Layanan optimizer meminta bearer token bersama (`OPTIMIZER_TOKEN`) dari layanan pemanggil; tanpa token hanya bila `OPTIMIZER_DEV=true`.
- Kunci Meta yang pernah ditempel di chat sebaiknya dirotasi.
