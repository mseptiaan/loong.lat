# Pencarian rute: tiga tingkat usaha, peninjau, dan laporan AI

Rancangan ini menambahkan cara kerja klaster dulu, rute kemudian (ronde ulang dengan peninjau, pemolesan `finisher`,
laporan akhir) ke optimizer loong.lat sebagai **pilihan yang dibuat pengirim di wizard**. Aturan lama tetap berlaku:
mesin optimasi milik kita, Mapbox dan Modal hanya alat bantu, dan AI (Meta) **tidak pernah menghitung rute atau angka**.
AI hanya memilih eksperimen dari menu tertutup dan menulis penjelasan dari angka yang sudah dihitung server.

## Tiga tingkat (`contracts.Effort`)

| Effort | Isi | Kapan |
|---|---|---|
| `fast` (bawaan) | Satu job tanpa `Search`: tiap preset dipecahkan sekali (seed tetap). Plan kecil dibuktikan optimal oleh solver eksak | detik |
| `thorough` | Dua job. Job 1: strategi bawaan (effort normal) yaitu `none` (garis dasar, selalu pertama), `kmedoids`, `kmeans`, `agglomerative` (k dari silhouette). Job 2: pemenang (algoritma, k) tiap preset yang berbeda diulang pada effort `thorough` (pass `thorough` pada klaster pemenang), dimulai dari kandidat terbaik tiap preset, lalu **pemolesan warm-start** (`finisher`) | puluhan detik untuk puluhan order |
| `reviewed` | Job 1 sama dengan `thorough`, lalu hingga 3 **ronde**: peninjau mengaudit kandidat terbaik tiap preset dan meminta hingga 3 eksperimen lanjutan (satu job per ronde, mulai dari yang terbaik) atau menyatakan puas (ronde berhenti); satu job pemolesan di akhir | lebih lama; peninjau AI butuh 2 sampai 8 detik per ronde |

`fast` selalu ada di dalam dua lainnya: percobaan `none` dengan effort `normal` identik dengan jalur `fast`
(seed, iterasi, tanpa klaster), jadi hasil `thorough` dan `reviewed` **tidak pernah lebih buruk** dari `fast`.

**Laporan AI** terpisah dari tingkat usaha (`report: true`): penjelasan untuk pengirim, ditulis setelah opsi muncul.

Catatan jujur: wizard membatasi 6 shipment dan solver eksak membuktikan optimal sampai 5 order, jadi pada plan sekecil itu
ketiga tingkat memberi jawaban yang sama. Manfaat klaster dan ronde baru terasa pada plan puluhan sampai ratusan order
(batching lintas-plan, re-plan armada). Mesinnya diuji pada instans sintetis sebesar itu.

## Alur

```
console  plans.computeOptions {id, effort}
   │  optimizerapi.QuoteRequest{Effort, AIReview}            (AIReview = flag AI admin DAN AI_URL terpasang)
   ▼
optimizer  quote.Planner.Compute  →  orchestrate.Orchestrator.Run(effort, job dasar tanpa Search)
   fast      → 1 job, Search=nil; hasilnya dipakai apa adanya
   thorough  → job 1 {Experiments: DefaultExperiments()}
               job 2 {Experiments: pemenang unik (algoritma,k) tiap preset @ thorough,
                      From: terbaik per preset, Polish: thorough}
   reviewed  → job 1 {Experiments: DefaultExperiments()}
               ulangi ≤ Rounds kali (OPTIMIZER_ROUNDS, bawaan 3):
                   Reviewer.Review(Brief) → Decision{Satisfied | Experiments ≤3, Note, By}
                   puas → berhenti;  tidak → job {Experiments, From: terbaik per preset}
               akhir:  job {Polish: thorough, From: terbaik}
   (tiap job = satu sandbox Modal atau LocalRunner; keadaan antar job dipegang layanan optimizer)
   │  gabungkan: kandidat terbaik per preset lewat Candidate.Score, assemble → OptionSet{Method}
   ▼
console  menyimpan OptionSet (+Method); UI menampilkan opsi
console  ai.explainOptions {planId} (aliran CopilotEvent: token…, done) → profil "report" di gateway ai → OptionSet.Report
```

`DefaultExperiments` adalah strategi bawaan (effort normal) dengan `{none, normal}` **di urutan
pertama**: solver menjamin percobaan itu mereproduksi solve biasa, jadi pencarian tidak pernah lebih buruk dari `fast`.

**Penggabungan.** Per preset disimpan kandidat terbaik dari semua job; dibandingkan leksikografis lewat `Candidate.Score`
(lebih kecil lebih baik; hanya kandidat valid, `Facts.Valid`; seri mempertahankan yang lebih awal, jadi solve biasa
menang bila tak ada yang lebih baik). Yang digabung: `Trials` (semua percobaan, `Won` hanya pada job pemenang),
`Stats` (jumlah `Trials` dan `Iterations`, `ExactRan` salah satu), `Truncated` (salah satu job). `Method` diisi dari
itu: `Effort`, `Trials`, `Rounds` (ronde yang menjalankan job), `Polished` (solver melaporkan pemolesan), `Reviewer`,
`Proven` (dari `assemble`, semua kandidat terbukti optimal) dan `RoadSource`. `fast` juga mengisi `Method`.

**Kegagalan.** Job 1 gagal berarti kuotasi gagal (galat runner dikembalikan apa adanya: `job.Error` invalid menjadi
`tooBig`, lainnya `srv.engine.unavailable`). Kegagalan setelah job 1 (sandbox hilang, waktu habis sebelum ronde atau
pemolesan) **tidak** menggagalkan kuotasi: pencarian berhenti, yang terbaik sejauh ini dipakai, `Method` hanya
mencerminkan yang benar-benar berjalan, dan hasilnya `truncated` agar tidak di-cache sebagai kanonik.

**Progres.** Planner melaporkan empat tahap seperti dulu; di dalam tahap 3 (`StageCombine`) pencarian dalam juga
mengirim event `progress` dengan `Msg` (tahap tetap 3, total 4). Kuotasi `fast` hanya mengirim empat event itu.

| Kunci | Param | Kapan |
|---|---|---|
| `srv.engine.search.experiments` | `n` | sebelum job yang menjalankan `n` percobaan (job 1, dan job 2 `thorough`) |
| `srv.engine.search.polish` | | sebelum job pemolesan |
| `srv.engine.search.round` | `round` | awal ronde peninjauan, dari 1 |
| `srv.engine.search.reviewAI` | | peninjau AI ditanya |
| `srv.engine.search.reviewRules` | | peninjau aturan ditanya (juga bila AI gagal dan aturan mengambil alih ronde itu) |
| `srv.engine.search.satisfied` | | peninjau puas: ronde berhenti |
| `srv.engine.search.more` | `n` | peninjau meminta `n` eksperimen lagi; job ronde berjalan |

Urutan `reviewed` dengan AI yang sekali meminta lagi: `experiments{4}`, `round{1}`, `reviewAI`, `more{2}`, `round{2}`,
`reviewAI`, `satisfied`, `polish`.

Peninjau (`Reviewer`) ada dua: **rules** (aturan peninjau, tanpa jaringan) dan **ai** (profil `review`,
keluaran JSON bersyarat skema, tanpa tool call). Fakta untuk AI disusun server sebagai JSON ringkas dengan angka yang
sudah berformat (per preset: percobaan pemenang, validitas, penyebab tak-terlayani, overlap, kemurnian, daftar percobaan
dengan selisihnya terhadap yang terbaik, nomor ronde); teks dari engine diperlakukan sebagai data (dibatasi, tanpa
karakter kontrol, id shipment tidak ikut). Keputusan AI divalidasi: algoritma dari daftar, `k` 0..8 dan tidak lebih
dari jumlah shipment, effort dari daftar, paling banyak 3 eksperimen, tanpa percobaan yang sudah pernah dijalankan di
job mana pun (yang diminta dan yang dipakai, termasuk `k` pilihan silhouette); yang tidak sah dibuang, dan bila tidak
puas tetapi tak ada yang sah tersisa, dianggap puas. Galat transport atau format dari AI membuat **peninjau rules**
mengambil ronde itu (`By = rules`). `Method.Reviewer` = `"ai"` hanya bila setiap keputusan berasal dari model, selain
itu `"rules"`; bila peninjau puas di pertanyaan pertama (nol ronde), tetap dilaporkan peninjau yang ditanya.
`AIReview=false` atau tanpa gateway (`AI_URL` kosong) memakai rules. `Decision.Note` hanyalah catatan model untuk log:
tidak pernah ditampilkan sebagai fakta.

Aturan peninjau (dipakai rules, dan menjadi isi prompt AI):
- Kandidat tidak valid: minta eksperimen konkret (algoritma, k, effort).
- Shipment tak terlayani karena sifat plan (`no_vehicle_skill`, `too_heavy`, `window_in_break`, `window_unreachable`,
  `shift_too_short`, `max_working_duration`) tidak bisa diperbaiki eksperimen: jangan minta apa pun karenanya.
  Penyebab lain (`not_placed`, `schedule_conflict`, `driver_time_full`, `vehicle_full`) boleh.
- Puas bila valid, tak ada tak-terlayani yang bisa diperbaiki, overlap ≤ 0,15 dan kemurnian klaster ≥ 0,85
  (kemurnian 0 berarti tanpa klaster dan tidak dihitung).
- Overlap dan kemurnian hanya diagnosis sekunder: jangan minta eksperimen yang tujuannya cuma menurunkan overlap.
- Puas bila dua ronde berturut-turut memperbaiki objektif terbaik kurang dari 1 persen, atau bila beberapa klaster
  berbeda memberi nilai objektif sama (itu hanya memaafkan overlap/kemurnian, bukan kandidat tidak valid).
- Ukuran 1 persen: pada entri `Score` pertama yang berbeda antara sebelum dan sesudah ronde, `(lama - baru) / lama`
  (entri berbentuk hitungan seperti jumlah tak terlayani atau detik terlambat memakai penyebut minimal 1, jadi dari satu
  tak terlayani menjadi nol adalah 100 persen); per ronde diambil preset yang paling membaik. Tidak berlaku selama ada
  preset tanpa kandidat valid.

Eksperimen yang diminta rules (maksimal 3, tidak pernah mengulang `(algoritma, k, effort)` yang sudah jalan): `kmedoids`
dengan k satu di bawah dan satu di atas k pemenang (atau k klaster terbaik bila pemenangnya `none`, atau 3 bila belum ada
klaster), lalu `agglomerative` dan `kmeans` dengan k terdekat yang belum dicoba, lalu klaster pemenang diulang pada effort
`thorough`. k dibatasi 2..min(8, jumlah shipment). Tak ada yang tersisa: puas.

## Kontrak

- `contracts`: `Effort`, `Method{effort,trials,rounds,polished,reviewer,proven,roadSource}`, `Report{text,locale,at}`,
  `OptionSet.Method`, `OptionSet.Report` (aditif).
- `optimizerapi`: `QuoteRequest.Effort/AIReview`; `Event.Msg` pada `progress` untuk catatan di dalam tahap 3;
  `Info{engine, runner, efforts, aiReview}` di `GET /v1/info` (tingkat usaha yang didukung dan apakah peninjau AI terpasang).
- `job` (versi kawat tetap 1, aditif): `Job.Search{Experiments, From, Polish}`, `Candidate.Score/Trials/Facts`,
  `Stats.Trials/Polished`. `Score` memungkinkan penggabungan hasil beberapa job tanpa mesin.
- `aiapi`: profil `review` dan `report`. Optimizer menjadi pemanggil ketiga gateway (token sendiri di `AI_TOKEN`).

## Laporan AI

Fakta untuk laporan disusun server dan **sudah berformat** ("Rp 45.000", "10,4 km", "32 menit"); model hanya boleh
mengutipnya. Isi: opsi (label, biaya, jarak, durasi, ETA, hemat, alasan), cara menemukannya (`Method`, pemenang
percobaan, catatan peninjau, sumber data jalan, terbukti optimal atau tidak), dan peringatan (tenggat terlewat, jarak
perkiraan). Keluaran ≤ 180 kata dalam bahasa permintaan (`Accept-Language`). Setiap deretan angka dalam keluaran
harus ada di fakta; bila tidak, ulangi sekali, lalu gagal dengan pesan "ringkasan AI tidak tersedia" (opsi tetap utuh).
Karena diperiksa dulu, teks tidak diteruskan dari Meta token demi token: server memanggil tanpa stream, memeriksa, lalu
mengirim teks utuh sebagai satu event `token` dan `done` (bentuk `CopilotEvent`, `actions` kosong). `plans.get` kemudian
mengembalikan `optionSet.report`. Laporan dihasilkan atas permintaan (tombol/saklar di wizard), bukan otomatis.

## Batas waktu dan cache

`fast` tetap 45 detik (`OPTIMIZER_TIMEOUT`). `thorough` dan `reviewed` memakai `OPTIMIZER_TIMEOUT_DEEP` (bawaan 150 detik).
Tiap job mendapat `Limits.MaxSeconds` sendiri: sisa waktu sampai tenggat pencarian dibagi rata dengan jumlah job yang masih
akan berjalan (`Limits.MaxSeconds` dari konfigurasi, bila ada, menjadi batas atasnya; tanpa tenggat dipakai 40 detik).
Pada `thorough` dan `reviewed` tenggat pencarian disisakan sebesar `GeometryTimeout` + 1 detik agar penggambaran rute tidak kehabisan waktu (`fast` tidak berubah). Job yang
terpotong ditandai `truncated`; ronde atau pemolesan yang tidak sempat dimulai karena waktu (kurang dari 3 detik)
membuat hasilnya `truncated` juga. Sandbox Modal tetap satu job per eksekusi (ronde berarti beberapa eksekusi di sandbox
hangat yang sama).

Optimizer tidak punya cache kuotasi (hanya cache matriks jalan, yang tidak bergantung pada effort). Yang ada:
`OptionSet.InputHash` memasukkan effort bila bukan `fast` (hash `fast` tidak berubah), dan `QuoteResult.Truncated` memberi
tahu pemanggil agar tidak menyimpan hasil terpotong sebagai kanonik.

## Pembagian pekerjaan

1. Engine/solver: klaster, percobaan, pemolesan, incumbent `From`, `Score`, `Trials`, `Facts` (`services/optimizer/internal/solve`, `cmd/solver`).
2. Orkestrasi optimizer: ronde, peninjau rules dan AI, `Planner`/handler/app, progres per catatan (`services/optimizer/internal/orchestrate`, `quote`, `api`, `app`).
3. Gateway AI dan console: profil `review`/`report`, `ai.explainOptions`, `plans.computeOptions {id, effort}`, penyimpanan `Method`/`Report`.
4. Frontend: tipe domain, mock, `http-api`, pemilih tingkat usaha dan kartu laporan di wizard, i18n EN/ID.

## Hasil pengukuran (Modal + Mapbox + Meta, solver `nogit-20261022001335`)

Diukur lewat tumpukan nyata (optimizer `runner=modal`, jalan Mapbox, peninjau dan laporan lewat gerbang ai ke Meta).

| Plan | Effort | Waktu | Catatan |
|---|---|---|---|
| 3 shipment (cerita live) | fast | 1,2 dtk | 4 percobaan, terbukti optimal |
| 3 shipment | reviewed, peninjau AI | 7,7 dtk | 2 job di sandbox yang sama, 20 percobaan; AI puas di ronde 1 (panggilan `review` 3,7 dtk); laporan AI (`report`) 6,5 dtk |
| 12 shipment | fast | 1 dtk (matriks Mapbox sudah di cache) | termurah Rp 294.500 |
| 12 shipment | thorough | 21 dtk | job 1 (4 pengelompokan × 4 preset) 10,7 dtk, job 2 (menang diulang di effort thorough + polish) 10,5 dtk; 24 percobaan; hasil sama dengan fast |
| 24 shipment | reviewed, peninjau AI | 73 dtk | hasil ditandai `truncated` (batas waktu per job memotong pemolesan) tapi valid; pengukuran jalan (saat itu masih lewat Matrix, yang bisa memakai tol) melewati `ROAD_TIMEOUT` 8 dtk dan jatuh ke perkiraan |

Dari uji skala solver (CPU per preset, bawaan produksi): 30 shipment ±60 dtk, 60 shipment ±240 dtk (biaya −1,6% dibanding satu kali solve), 120 shipment ±830 dtk (−2,9%). Di atas ±30 shipment pencarian dalam akan terpotong oleh batas waktu; hasilnya tetap valid dan tidak pernah lebih buruk dari `fast` karena percobaan dasar berjalan paling dulu.

Yang perlu diingat:
- Pada plan sekecil wizard (≤ 6 shipment) hasil ketiga tingkat nyaris selalu sama; manfaatnya muncul pada plan yang jauh lebih besar (batching lintas-plan, re-plan armada).
- Tabel jalan diukur tanpa tol lewat Directions (lihat README, "Tanpa tol"): sekitar 6 permintaan untuk 12 tempat dan 24 permintaan untuk 24 tempat (diukur 2,6 dtk dingin dengan 8 permintaan paralel). Plan yang sangat besar tetap bisa melewati `ROAD_TIMEOUT`; naikkan di sana, bila tidak kuotasi memakai jarak perkiraan (tercatat di `Method.roadSource`). Yang sudah terukur tetap masuk cache, jadi percobaan berikutnya lebih cepat.
- Solver di sandbox berubah setiap kali mesin berubah: jalankan `scripts/publish-solver.sh` lalu isi `MODAL_SOLVER_VERSION`.
- Stream progres optimizer dulu tertahan sampai akhir (handler memeriksa `http.Flusher` pada writer yang dibungkus middleware); sekarang memakai `http.ResponseController` dan dijaga tes `TestProgressReachesTheClientWhileTheSolverIsStillWorking`.
