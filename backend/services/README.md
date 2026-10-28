# Konvensi layanan

Berlaku untuk `console-api`, `backoffice-api`, dan `driver-api`. Rancangan penuh ada di rencana yang disetujui (`~/.claude/plans/aku-ingin-mengikuti-hackathon-async-sunset.md`, bagian "Arsitektur microservices"); dokumen ini memutuskan hal-hal yang harus sama di ketiganya.

## Susunan

```
services/<nama>/
  cmd/<nama>-api/main.go     tipis: muat env, panggil app.Start, tunggu sinyal
  app/                       perakitan: Config, Start, App (satu-satunya paket yang boleh mengenal semuanya)
  internal/<domain>/         logika per domain; tidak mengimpor layanan lain
  migrations/*.sql           DDL milik layanan ini (//go:embed), diterapkan dengan platform.Migrate
```

`app.Start` harus bisa dipanggil dari tes tanpa proses terpisah:

```go
type Config struct {
    HTTPAddr     string        // ":0" memilih port bebas
    DatabaseURL  string        // database layanan ini saja
    KafkaBrokers []string
    GroupPrefix  string        // grup konsumen "<GroupPrefix>.<tujuan>"; bawaan: nama layanan
    SessionTTL   time.Duration // bawaan 720h
    CORSOrigins  []string      // asal browser yang boleh memanggil; bawaan: aplikasinya sendiri di loong.localhost
    // ...pengaturan khusus layanan
}
func ConfigFromEnv(env *platform.Env) Config
func Start(ctx context.Context, cfg Config, log *slog.Logger) (*App, error) // kembali setelah listener HTTP terikat dan relay/konsumen berjalan
func (a *App) Addr() string
func (a *App) Wait() error // sampai ctx dibatalkan dan semuanya berhenti rapi
```

Migrasi: `platform.Migrate(ctx, pool, slices.Concat(platform.Migrations(), eventbus.Migrations(), own)...)`. Port bawaan: driver 8081, backoffice 8082, console 8083, optimizer 8090, ai 8091. Tidak ada gateway: tiap API dijangkau langsung oleh aplikasinya dan menjawab preflight CORS sendiri (`CORS_ORIGINS`, `platform.CORS`).

## Aturan data dan event

1. **Satu database per layanan.** Tidak ada baca-tulis lintas database. Tidak ada impor `services/<lain>/...` (dijaga `boundaries_test.go`). Data bersama hanya lewat `pkg/contracts`.
2. **Penerbitan hanya lewat outbox**: `Publisher.Enqueue(ctx, tx, Event{...})` di dalam transaksi yang sama dengan perubahan data. Jangan memproduksi ke Kafka langsung.
3. **Konsumsi hanya lewat `eventbus.Consumer`**; handler idempoten dan memperbarui proyeksi di `tx` yang diberikan. Galat sementara (mis. baris induk belum tiba) dikembalikan biasa agar dicoba ulang; input yang tidak akan pernah valid dibungkus `eventbus.Permanent`.
4. **`AggregateVersion`**: tiap agregat punya penghitung naik monoton milik pemiliknya. Proyeksi menyimpan versi terakhir yang diterapkan per (agregat, keluarga event) dan membuang event dengan versi ≤ itu.
   - plan (console): `plan.version`, naik tiap perubahan komersial.
   - penugasan rute (backoffice): `routes.assign_version`, naik tiap assign/unassign. Dipakai pada `route.assigned/unassigned`.
   - eksekusi rute (driver): `routes.exec_version`, naik tiap event eksekusi (started/arrived/completed/issue/completed-route). Dipakai pada `loong.execution.v1`.
   - driver: `driver.version`; posisi tidak berversi (hanya yang terbaru yang berarti, bandingkan `recordedAt`).
   Proyeksi rute di console dan backoffice menyimpan `assign_version` dan `exec_version` terpisah.
5. **Kunci Kafka** mengikuti tabel topik di rencana (planId, routeId, driverId, vehicleId, customerId). **Grup konsumen tidak boleh dibagi antar layanan**: `<GroupPrefix>.<tujuan>` yang sama di dua layanan membuat keduanya berbagi partisi dan saling mencuri event, jadi `GroupPrefix` tiap layanan harus berbeda (bawaannya nama layanan).
6. **PII minimal**; sandi/PIN tidak pernah di event kecuali hash argon2id di `driver.provisioned/updated`. `deliveryCode` ada di snapshot `plan.confirmed` dan `route.assigned`: driver-api menyimpannya di server untuk memverifikasi OTP, tetapi **tidak pernah mengirimnya ke aplikasi driver** (hilangkan dari respons `driver.*`).
7. **Status rute dan plan**: `routeplan.StatusOf(routes)` menurunkan status plan dari rute. Proyeksi memperbarui rute dengan fungsi bersama di `pkg/routeplan` (jangan menulis ulang aturannya), memakai waktu yang dibawa event, lalu menghitung status plan lagi:
   - `route.assigned` ⇒ ganti seluruh rute dengan snapshot `Route` di payload (sudah di-rebase ke posisi driver).
   - `route.unassigned` ⇒ `routeplan.Unassign`.
   - `route.started` ⇒ `routeplan.Start(route, startedAt)`.
   - `stop.arrived` ⇒ `routeplan.Arrive`. `stop.completed` ⇒ `routeplan.Close(..., StopStatusDone, proof, nil)`. `stop.issue` ⇒ `routeplan.Close(..., StopStatusProblem, nil, &issue)`.
   - `route.completed` ⇒ tidak mengubah apa pun bila rute sudah `selesai` (penutupan stop terakhir sudah menyelesaikannya).
   Kegagalan transisi (`ErrNotActiveStop`, dst.) pada event yang versinya baru berarti urutan antar topik terbalik: kembalikan galat biasa agar dicoba ulang (event induk akan tiba), jangan `Permanent`.
8. **Event SSE** (`platform.Hub`, `GET /v1/events`, bearer token): kirim `{type: ...}` bernama seperti `AppEvent` frontend (`plans.changed` dengan `planId`, `fleet.changed`, `wallet.changed`, dst.) kepada audiens yang relevan setiap kali proyeksi atau data berubah. Event tidak membawa data; klien mengambil ulang.

## ID dan kode

- Entitas: `<awalan>-<uuid v7>` (`cus-`, `pln-`, `rte-` hanya bila perlu, `veh-`, `drv-`, `adm-`). Rute dan stop mengikuti `routeplan.RoutesFromOption`: `<planId>:r1`, `<planId>:r1:s1`.
- Kode plan: `PL-<nomor>` dari sequence database console, mulai 2001.
- Waktu di kawat: `contracts.Time` (ISO-8601 UTC, milidetik). Uang: bilangan bulat rupiah.

## RPC

- `POST /v1/rpc/{metode}`; nama metode persis seperti `ApiClient` di `frontend/packages/domain/src/api.ts` (mis. `plans.saveDraft`).
- **Badan = objek bernama menurut nama parameter TypeScript**, selalu objek: `plans.saveDraft(id, patch)` → `{"id":"...","patch":{...}}`; `auth.register(input)` → `{"input":{...}}`; metode tanpa parameter → badan kosong. Tipe masuk/keluar mencerminkan `packages/domain/src/types.ts` (sudah dicerminkan di `pkg/contracts`).
- Metode `AsyncIterable` (`plans.computeOptions`) memakai `RPC.Stream` (NDJSON): `{"type":"progress","step":n,"total":4,"label":"..."}` lalu `{"type":"done","optionSet":{...}}`. Badannya `{"id":"...","effort":"fast|thorough|reviewed"}` (`effort` boleh dihilangkan: `fast`; nilai lain ditolak `tidak_valid` pada field `effort`, kunci `srv.api.badEffort`); pada effort dalam, baris `progress` boleh membawa `msg` (kunci `srv.engine.search.*`) dan `label` tetap teks Inggris biasa. Console menunggu quote sampai `OPTIMIZER_CALL_TIMEOUT` (bawaan 3 menit, lebih panjang dari `OPTIMIZER_TIMEOUT_DEEP` optimizer, 150 detik). `ai.explainOptions` (console, `{"planId":"..."}`) juga aliran: satu event `token` berisi seluruh teks laporan, lalu `done{actions:[],requestId}`; laporan tersimpan di `optionSet.report`.
- Metode di luar cakupan layanan itu tidak didaftarkan (jawabannya 404 `tidak_ditemukan`); jangan membuat stub yang berpura-pura berfungsi.
- Galat: `platform.Invalid/NotFound/Denied/Unauthenticated/Conflict/Unavailable` dengan `contracts.Msg` berkunci katalog frontend yang sudah ada (`srv.api.*`, `srv.engine.*`, `srv.ops.*`, `srv.rpc.*`); kunci baru ditulis di daftar yang dilaporkan ke saya, tidak ditambah sendiri ke frontend.
- Operasi uang (`plans.confirm`, `wallet.createTopUp`) lewat `platform.Idempotency`.
- Hasil `auth.register`, `auth.login*` adalah `Session` ditambah field `token` (bearer); kontrak frontend belum punya tempat untuk token.
- Login: `platform.NewLoginLimiter`, sandi/PIN `platform.HashSecret/VerifySecret`, sesi `platform.Sessions` dengan `NewPgSessionStore`.

## Pengujian

- `go vet`, `gofmt`, dan `go test -race ./services/<nama>/...` bersih. Tes yang butuh Postgres memakai `platformtest.Pool(t)` dan di-skip bila `TEST_DATABASE_URL` kosong; Kafka diuji dengan `eventbustest` (kfake), tanpa Docker.
- Infrastruktur dev sudah menyala (`scripts/dev-up.sh`): `export TEST_DATABASE_URL='postgres://console:loong_dev@127.0.0.1:55432/console?sslmode=disable'`. **Jangan menjalankan `dev-down`**; layanan lain sedang memakainya.
- Tes harus membuktikan perilaku, bukan sekadar menjalankan kode: proyeksi menolak event lama (versi), event ganda berdampak sekali, peran yang salah ditolak, saldo kurang menggagalkan konfirmasi tanpa mengubah plan, dst.

## Larangan

Jangan mengubah `pkg/*` (kekurangan dilaporkan, bukan ditambal), jangan menambah dependensi baru tanpa alasan kuat (`go get <modul>@<versi>` satu per satu, tanpa `go mod tidy`), jangan membaca atau menulis rahasia, jangan `git commit`.
