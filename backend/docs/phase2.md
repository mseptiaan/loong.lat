# Fase 2: melengkapi backend

Dokumen kerja untuk menutup fitur yang masih ditolak backend ("Fitur ini belum tersedia"). Aturan umum layanan tetap di `services/README.md`; ini menambah keputusan yang berlaku lintas layanan. Spesifikasi perilaku adalah **mock di frontend** (`frontend/packages/mock-api/src/server/*.ts`): kunci pesan, urutan aturan, dan teks default berasal dari sana, supaya layar bekerja sama di kedua mode.

| # | Fitur | Pemilik utama |
|---|---|---|
| 1 | Pembatalan plan (saga dua fase) | console (koordinator), driver (pemungutan suara), backoffice (membekukan) |
| 2 | Jadwal ulang plan | console, backoffice (mengembalikan rute ke antrean) |
| 3 | Rute dilepas saat driver offline atau diam | backoffice |
| 4 | Deteksi keterlambatan (`route.delayed`) | driver |
| 5 | Notifikasi (pelanggan, admin, driver) | console, backoffice, driver (`pkg/notices`) |
| 6 | Pengguna dan keuangan (admin) | backoffice |
| 7 | Akun dan alamat tersimpan | console |
| 8 | Pengaturan terbaca semua layanan | console, driver |
| 9 | AI: Copilot, wawasan, baca foto, suara, maksud | `services/ai` + console + backoffice |

## Status

Semua sembilan butir selesai dan terverifikasi: tes per paket (`go test -race ./...`), sembilan skenario e2e (`e2e/flow_test.go`, `e2e/phase2_flow_test.go`), lima cerita langsung dari klien frontend terhadap layanan yang berjalan (`frontend/packages/http-api/src/live.test.ts`), dan dua cerita AI terhadap model Meta sungguhan (`LOONG_LIVE_AI=1`). Penyimpangan dan tambahan terhadap rancangan di bawah:

- **Saga batal**: driver-api juga menyimpan (parkir) `route.assigned`/`route.unassigned` yang tiba di belakang tombstone dan menerapkannya bila permintaan itu di-abort; tanpa itu penugasan yang sudah sampai ke backoffice sebelum pembekuan akan hilang. Versi plan di console naik pada requested, aborted, cancelled, dan rescheduled; suara dan `plan.cancel.asked` bukan bagian dari keluarga versi itu.
- **`topup.cancelled`** ditambahkan (top up yang dibatalkan pelanggan tadinya tak diumumkan, sehingga keuangan backoffice menampilkannya menunggu sampai kedaluwarsa).
- **Penghematan**: `Plan.SavingsRp`/`SavingsPct` (opsional) ada di snapshot `plan.confirmed` (alternatif opsi tetap tidak dibagikan), sehingga baris daftar backoffice, Analytics, dan wawasan AI memakai angka penghematan opsi terpilih.
- **AI**: `ai.insights` berjalan di profil `insights` dengan angka hasil hitungan server sebagai satu pesan data (gerbang menolak alat pada profil itu); audio harus WAV PCM mono 16/24 kHz (frontend mengodekannya); flag `ai.*` di `settings.get` console dan driver = flag admin DAN gerbang terkonfigurasi, sedangkan backoffice menampilkan flag admin apa adanya dan menolak di tingkat metode.
- **AI membaca foto bukti antar**: foto yang diambil driver saat menutup antar dianalisis model (profil `vision`) setelah event diproses, bukan di dalamnya: proyeksi `stop.completed` di console menambah satu baris `proof_jobs` (migrasi `0008`, satu transaksi dengan bukti, idempoten) dan worker `internal/proofs` (hanya bila `AI_URL` terisi; `PROOF_AI_POLL`) menyimpan hasilnya di `proof.analysis` pada stop tanpa menaikkan versi plan atau rute, lalu memberi tahu dashboard lewat SSE `plans.changed`. Teksnya dua bahasa dalam satu pesan `srv.ai.text` (ditambah `srv.ai.proof.noNote`). Saklar `vision` mati berarti foto dilewati untuk selamanya (nyala lagi tidak menyusul); kegagalan sementara diulang dengan jeda 30 dtk, 2 mnt, 10 mnt hingga empat percobaan, foto yang tak bisa dibaca gagal seketika. Tes langsung: `LOONG_LIVE_META=1 LOONG_LIVE_PHOTO=<berkas> go test -run LiveAnalyzeProof ./services/console/internal/ai`.
- **Ditemukan lewat uji browser** (Chrome, tiga aplikasi dalam mode http terhadap layanan sungguhan): wizard memanggil `fleet.vehicles.list`, yang tidak disajikan console (tanpa penanganan galat halaman membeku dalam badai render). Daftar armada ternyata data sensitif untuk pelanggan, jadi metodenya sengaja **tidak** ditambahkan ke console: di backend asli wizard tidak memeriksa muat di klien (backend menolak muatan terlalu besar saat opsi dihitung), dan bila daftar gagal dimuat di mode tiruan wizard menampilkan galat dengan "Coba lagi"; driver yang tabnya tersembunyi berhenti melapor lalu tugasnya dicabut berulang (klien kini menahan layar menyala dan melapor seketika saat tampil); pelepasan karena diam kini bertuliskan "berhenti melapor", bukan "offline"; opsi pembayaran selain saldo, akun demo, dan tombol simulasi pembayaran tidak lagi ditawarkan bila backend tak mendukungnya.
- **Pengaturan driver**: `settings.changed` belum didorong lewat SSE ke driver (terbaca pada `settings.get` berikutnya).
- **Tersisa**: `places` di sisi server; pembayaran sungguhan; penugasan ulang otomatis berdasarkan keterlambatan; tombstone untuk rute yang tak pernah muncul tidak dibersihkan (sengaja); keterlambatan diukur per ruas, bukan kumulatif.

## Yang sudah disediakan (jangan dibuat ulang)

- `pkg/contracts`: semua payload event di bawah (lihat `events.go`, `payloads.go`), `Plan.PendingAction`, `AppNotification`, alasan `RouteUnassigned.Reason`.
- `pkg/platform.LocaleMiddleware` / `LocaleFrom(ctx)`: bahasa dari `Accept-Language` (`en`/`id`). Sudah terpasang di rantai ketiga layanan. Dipakai hanya untuk teks yang disusun server (jawaban AI, alasan batal bawaan); selebihnya server memakai kunci pesan.
- `pkg/notices`: tabel `notifications`, `Add` (idempoten lewat `Key`, ditulis di transaksi handler), `List`, `MarkRead`, dan `Mount` untuk `notifications.list/markRead`. Tambahkan `notices.Migrations()` ke migrasi layanan.

## 1. Pembatalan plan

Satu-satunya tulis-sumber status komersial adalah console, jadi console menjadi koordinator. Pembatalan dua fase supaya driver yang sudah berangkat tidak pernah dibatalkan diam-diam dan uang tidak dikembalikan untuk jasa yang sudah berjalan.

```
customer ─ plans.cancel ─▶ console                       admin ─ plans.cancel ─▶ backoffice
                              │                                                    │ plan.cancel.asked (+locale)
                              │◀───────────────────────────────────────────────────┘
        validasi, plan.pendingAction='cancel', simpan permintaan (request_id)
                              │ plan.cancel.requested {requestId, requestedBy, routeIds=SEMUA rute plan}
              ┌───────────────┴───────────────┐
         backoffice                        driver-api
   membekukan semua rute plan        satu suara per rute:
   (dispatch tidak menugaskannya)     - rute dikenal, belum dimulai  → tahan (hold) + plan.cancel.accepted
                                      - rute dikenal, sudah berjalan/selesai → plan.cancel.rejected {reason}
                                      - rute belum dikenal → catat tombstone (requestId) + accepted;
                                        route.assigned yang datang belakangan untuk rute itu diabaikan
                              │ votes (topik plan, kunci planId)
                              ▼
                           console mengumpulkan suara per requestId
        semua accepted ──▶ plan.cancelled (komit)     ada rejected / lewat tenggat ──▶ plan.cancel.aborted
```

**Console** (`plans.cancel{id, reason}`, pelanggan pemilik):
- Aturan persis mock: status `berjalan` → `bentrok` `srv.api.cancelRunning`; `selesai`/`dibatalkan` → `bentrok` `srv.api.planInactive`; plan yang masih `pendingAction` → `bentrok` (kunci baru, laporkan).
- Plan yang belum dikonfirmasi (draft, opsi_siap): batal lokal langsung (tanpa saga, tanpa refund).
- Plan terkonfirmasi (`menunggu_driver`, `ditugaskan`): set `pendingAction='cancel'`, `version++`, simpan baris permintaan (request_id, requested_by, reason, rute yang ditunggu, tenggat), enqueue `plan.cancel.requested` dengan **semua** rute plan. Plan dikembalikan dengan `pendingAction`.
- Konsumen topik plan (grup sendiri) menerima `plan.cancel.asked` (dari admin; validasi yang sama; bila tak boleh, terbitkan `plan.cancel.rejected` dengan `RouteID` kosong dan alasannya) dan suara `accepted/rejected` (dedup per requestId+routeId; suara requestId lain atau permintaan yang sudah selesai diabaikan).
- Semua rute `accepted` → komit: status `dibatalkan`, `pendingAction` dihapus, `cancelReason` (alasan pengguna, atau teks bawaan "Dibatalkan oleh pengirim"/"Dibatalkan oleh admin" dalam bahasa permintaan; untuk jalur admin pakai `Locale` dari `plan.cancel.asked`), refund penuh ke wallet bila dibayar dengan saldo (`wallet.refunded` + baris ledger + `RefundRp` di `plan.cancelled`), baris linimasa plan, notifikasi pelanggan. Ada satu `rejected` → batalkan: `pendingAction` dihapus, `plan.cancel.aborted{reason}`, notifikasi pelanggan.
- Penyapu: permintaan yang tidak lengkap lewat `CANCEL_TIMEOUT` (bawaan 2m) → aborted dengan alasan "tidak ada jawaban".
- Event plan yang diterbitkan console memakai `plan.version`; suara dan `plan.cancel.asked` bukan bagian dari keluarga versi itu (dedup lewat inbox dan requestId).

**Backoffice**:
- `plans.cancel{id, reason}` (admin): periksa proyeksi (status `berjalan`/`selesai`/`dibatalkan` → konflik langsung seperti mock), tandai baris plan `pending_action='cancel'`, enqueue `plan.cancel.asked{planId, reason, askedBy: nama admin, locale}`, kembalikan plan (dengan `pendingAction`).
- Konsumen topik plan: `requested` → bekukan semua rute plan (kolom hold berisi requestId; worker dispatch dan `dispatch.assign/autoAssign/suggest` melewatinya; penugasan manual → `bentrok` kunci baru); `cancelled` → komit: status plan `dibatalkan` (mengalahkan `routeplan.StatusOf`), rute yang sudah ditugaskan dikembalikan ke antrean tanpa driver (tanpa menerbitkan `route.unassigned`; driver-api menutup tugasnya dari `plan.cancelled` itu sendiri) sehingga driver kembali bebas, `pending_action` dihapus, notifikasi admin bila diminta pelanggan; `aborted` → lepas pembekuan, hapus `pending_action`, `Kick` dispatch; `rejected` dengan `RouteID` kosong (console menolak permintaan admin) → hapus `pending_action`, notifikasi admin.

**Driver**:
- Konsumen topik plan: `requested` → suara per rute seperti diagram (hold berisi requestId; tombstone untuk rute tak dikenal), `cancelled` → rute yang di-hold requestId itu ditutup sebagai batal dan hilang dari daftar tugas (`driver.routes`), driver diberi notifikasi (`srv.api.notif.taskCancelled.*`), `aborted` → lepas hold dan hapus tombstone requestId itu. `driver.startRoute` pada rute ber-hold → `bentrok`.
- Suara diterbitkan lewat outbox ke `loong.plan.v1` berkunci planId. Penerbit `source` = `driver-api`.

## 2. Jadwal ulang

`plans.reschedule{id, schedule}` di console (pelanggan pemilik), aturan persis mock: hanya `menunggu_driver`/`ditugaskan` (`srv.api.rescheduleLate`), `terjadwal` harus ≥ 20 menit dari sekarang (`srv.api.scheduleTooSoon`, field `schedule`); ditolak bila `pendingAction`. Console menimpa `schedule`, mengatur ulang waktu mulai rute (`routeplan.RebaseStart`; sekarang = +6 mnt, terjadwal = `window.from`), `version++`, baris linimasa (`srv.api.event.rescheduled`/`rescheduledNow`), enqueue `plan.rescheduled{planId, schedule, routes}`.
Backoffice menerapkan jadwal dan waktu baru ke proyeksinya, lalu **mengembalikan rute yang sudah ditugaskan ke antrean** (`route.unassigned{Reason: rescheduled}` per rute, `assign_version++`) dan `Kick` dispatch: driver lama mungkin tidak kosong pada jam baru, jadi penugasan diulang oleh mesin yang sama (admin diberi tahu bila penugasan otomatis mati).

## 3. Rute dilepas saat driver offline atau diam

Di backoffice:
- `driver.offline` → semua rute driver itu yang masih `ditugaskan` (belum dimulai) dikembalikan ke antrean: `route.unassigned{Reason: driver_offline}`, `Kick`, notifikasi admin (kunci `srv.api.notif.driverOffline.*` dari mock bila ada).
- Penyapu di worker dispatch: driver yang berstatus online tetapi tak ada kabar lebih dari `fleet.PresenceTTL` **dan** punya rute `ditugaskan` yang berangkat dalam `dispatch.CommitLead` → rute itu dilepas dengan `Reason: driver_silent`. Rute terjadwal yang masih jauh tidak disentuh (aplikasi driver yang tertutup itu wajar).
- Rute yang sudah `berjalan` tidak pernah dilepas.
- driver-api: `route.unassigned` untuk rute yang sudah dimulai diabaikan; selain itu tugas dihapus dan driver diberi notifikasi sesuai `Reason`.

## 4. `route.delayed`

driver-api, penyapu berkala atas rute `berjalan`: keterlambatan = menit sejak ETA stop berikutnya yang belum selesai terlewat (`now − stop.eta`, jam rute sesuai `pkg/routeplan`; tidak negatif). Terbitkan `route.delayed{planId, routeId, delayMin}` (versi eksekusi naik) saat keterlambatan melewati ambang 5 menit lalu setiap kelipatan 5 menit berikutnya; tidak terbit ulang pada nilai yang sama; kembali tepat waktu tidak menerbitkan apa pun. Konsumen: backoffice (sudah ada: `delayMin`, `hasAnomaly`; tambahkan notifikasi admin per mock `srv.api.notif.delay.*`), console (notifikasi pelanggan; proyeksi `delayMin` bila belum).

## 5. Notifikasi

`pkg/notices` per layanan; handler event menulis notifikasi di transaksi yang sama dengan proyeksinya, `Key` = id event (+ sufiks bila satu event menghasilkan beberapa). Metode: `notifications.list`, `notifications.markRead{ids?}`. Setelah menulis, umumkan `notifications.changed` ke audiens itu lewat jalur SSE layanan masing-masing. **Isi dan penerima mengikuti pemanggilan `server.notify(...)` di mock** (`grep -n "notify(" frontend/packages/mock-api/src/server/*.ts`): judul dan isi berupa `Msg` dengan kunci `srv.api.notif.*` yang sudah ada di katalog frontend, parameter sama. Kunci yang belum ada di katalog dilaporkan, tidak ditambah sendiri.
- console: audiens `customer:<id>` (konfirmasi, ditugaskan, mulai, stop selesai/bermasalah, selesai, dibatalkan, tak bisa dibatalkan, top up lunas, refund, terlambat).
- backoffice: audiens `admin` (plan baru yang butuh tindakan, dibatalkan pelanggan, anomali/terlambat, stop bermasalah, driver offline, rute dikembalikan ke antrean).
- driver: audiens `driver:<id>` (tugas baru, tugas dicabut/dibatalkan).

## 6. Pengguna dan keuangan (backoffice)

Backoffice sudah punya proyeksi pelanggan (`customer.registered/updated`) dan plan. Tambahkan konsumen topik wallet (grup sendiri) dan tabel proyeksi: saldo per pelanggan, buku besar (`WalletTx`: topup, pembayaran, refund; baris top up dari `topup.paid` memakai `TxID`/`BalanceAfterRp`), dan daftar top up (`topup.created/paid/expired`). Event wallet berkunci customerId; pakai versi agregat bila payload/envelope membawanya, dedup lewat inbox.
- `users.list(q?)` → `UserSummary[]` (plansCount, completedCount, totalSpendRp dari plan terkonfirmasi, balanceRp, lastPlanAt); `users.get(id)` → `UserDetail{user, summary, plans}` (`user.savedAddresses` kosong: alamat milik console); `users.setBlocked(id, blocked)` → terbitkan `customer.blocked/unblocked`, perbarui proyeksi, kembalikan `UserSummary`. Console sudah/ harus menghentikan layanan pelanggan terblokir dan mencabut sesinya (periksa; lengkapi bila belum).
- `finance.overview` → `FinanceOverview`; `finance.topUps(filter?)` → `AdminTopUp[]` (filter status/cara bayar/q sesuai `TopUpFilter`); `finance.transactions(userId)` → `WalletTx[]`. Bentuk persis ada di `frontend/packages/domain/src/wallet.ts` dan mock `wallet.ts` (`createFinanceApi`).

## 7. Akun (console)

`account.profile` (User dengan `savedAddresses`), `account.update{patch}` (name/phone/email; validasi dan keunikan seperti `auth.register`; terbitkan `customer.updated`), `account.addresses.add/update/remove` (tabel alamat milik console; validasi seperti mock). `SavedAddress` ada di `pkg/contracts` (cermin tipe frontend).

## 8. Pengaturan

console-api dan driver-api mengonsumsi `loong.settings.v1` (dipadatkan) dan melayani `settings.get` dari proyeksinya (bawaan `DEFAULT_SETTINGS` bila belum ada). Bendera `ai.*` yang dikembalikan = bendera admin **DAN** gateway AI terkonfigurasi (`AI_URL` terisi); tanpa gateway semua bendera AI mati, supaya layar tidak menawarkan yang pasti gagal.

## 9. AI (Meta)

Semua panggilan model lewat satu gateway tanpa data bisnis, `services/ai` (port 8091), yang memegang kunci Meta (`META_API_KEY`, `META_BASE_URL`, `META_MODEL` dari `backend/.env`; **tidak pernah** dicetak, dicatat, atau ditulis ke berkas yang di-commit). console dan backoffice memanggilnya dengan `AI_URL` + `AI_TOKEN` (bearer bersama, seperti optimizer) melalui `pkg/aiapi`.

- API Meta (OpenAI-kompatibel, dokumentasi: Context7 `/websites/dev_meta_ai`): `POST {base}/chat/completions` (SSE bila `stream:true`, `tools`/`tool_calls`, `image_url` data-URL untuk gambar, `response_format: json_schema`), `POST {base}/asr/transcribe` (Muse Voice, `muse-voice-transcribe-1.0`). **Token penalaran memakan anggaran**: set `reasoning_effort` ("minimal"/"low") dan `max_completion_tokens` besar (≥ 1500), kalau tidak `content` bisa kosong dengan `finish_reason: length`. Satu panggilan ±2,5 dtk.
- `pkg/meta`: klien Go; `services/ai`: gateway (profil per kegunaan: copilot, vision, intent, insights, review, report, transcribe; model, upaya penalaran, batas token, timeout per profil), batas laju, tanpa mencatat isi percakapan; `pkg/aiapi`: klien dan tipe kawat untuk console/backoffice; `pkg/agent`: loop pemanggilan alat (daftar alat, skema argumen, validasi argumen sebelum dijalankan, langkah maksimum, timeout per alat, jejak `tool`/`token`/`done`).
- Copilot (`ai.copilot(scope, question)`, aliran): jejak alat (`CopilotToolTrace`, `title`/`target`/`detail` berupa teks biasa dalam bahasa `LocaleFrom(ctx)`), potongan jawaban, lalu `done{actions, requestId}`. Alat hanya membaca data pemanggil (console: plan milik pelanggan itu; backoffice: antrean, driver, saran). Angka dalam jawaban hanya yang dikembalikan alat. Tindakan (`CopilotAction`: `pilih_opsi`, `tugaskan_terbaik`, `buka_plan`) hanya diusulkan lewat alat `propose_action` yang memvalidasi terhadap data nyata; tidak ada alat yang mengubah data.
- Isi alat adalah **data**, bukan instruksi: teks buatan pengguna (nama barang, alamat, catatan) bisa memuat perintah; prompt sistem menyatakannya, dan karena alat hanya membaca dan tindakan hanya usulan, tak ada yang bisa dilakukan lewat teks itu.
- `ai.insights()` (backoffice): narasi analitik dari angka yang dihitung server. `ai.readImage` (console): gambar → barang (`DetectedItem[]`) lewat `response_format: json_schema`, aliran `progress` lalu `done{items, summary}`. `ai.parseSpeech(transcript)` dan `ai.parseIntent(text)` (console): teks → `SpeechParse`/`IntentParse` lewat json_schema; chip prioritas dan batas waktu divalidasi server. Transkripsi audio (Muse Voice) lewat `ai.transcribe` (tambahan aditif; lihat tugas frontend). `ai.explainOptions(planId)` (console, aliran): laporan atas opsi plan milik pelanggan itu lewat profil `report`; fakta (opsi, perbandingan, cara menemukan dari `OptionSet.Method` dan catatan quote) disusun server dengan setiap angka sudah berformat, model dipanggil tanpa alat dan tanpa stream, setiap deretan angka pada teksnya harus ada di fakta (diulang sekali dengan catatan koreksi, lalu `srv.ai.report.unverified`), teks disimpan di `optionSet.report` hanya bila opsi belum berubah, lalu dikirim utuh sebagai satu `token` dan `done` tanpa tindakan.
- Hormati `settings.ai.*`: fitur yang dimatikan admin ditolak dengan `ditolak` (`srv.api.aiDisabled` bila ada di katalog, kalau tidak laporkan kunci baru).

## Aturan kerja

- Tiap agen hanya mengubah direktori yang jadi miliknya (lihat pembagian di tugasnya). Perlu perubahan di `pkg/contracts`, `pkg/platform`, atau milik agen lain: **jangan**; tulis di laporan akhir apa yang dibutuhkan dan mengapa.
- Ikuti `services/README.md` (outbox, inbox, versi agregat, grup konsumen unik, RPC bernama parameter, `Msg` berkunci katalog).
- Setiap perilaku baru bertes (tes basis data dengan `platformtest.Pool`, Kafka dengan `eventbustest`); buktikan perilaku, bukan sekadar menjalankan kode. `go vet`, `gofmt -l`, `go test -race` untuk paket yang disentuh harus bersih. Infrastruktur dev (Postgres) sudah menyala: `export TEST_DATABASE_URL='postgres://console:loong_dev@127.0.0.1:55432/console?sslmode=disable'`. **Jangan** menjalankan `dev-down`, e2e (menghapus database dev), atau `docker compose` apa pun.
- Jangan membaca atau mencetak `backend/.env` kecuali tugasmu memerlukannya (hanya agen AI untuk uji langsung terbatas; nilainya tidak boleh muncul di keluaran, log, berkas, atau laporan).
- Jangan `git commit`.
