import type {
  AppNotification,
  Driver,
  DriverStatus,
  Effort,
  ISODate,
  LatLng,
  OptionSet,
  PaymentMethod,
  Place,
  Plan,
  PlanStatus,
  Preset,
  Priority,
  Role,
  Route,
  Schedule,
  SavedAddress,
  Session,
  Shipment,
  User,
  Vehicle,
  VehicleType,
  FareConfig,
  VehicleStatus,
} from './types'
import { renderMsg, t, type Msg } from '@loong/i18n'
import type { AdminTopUp, FinanceOverview, TopUp, TopUpFilter, TopUpInput, Wallet, WalletTx } from './wallet'
import type { AiFeatures, AppSettings, AudioInput, DispatchSettings, CopilotEvent, CopilotScope, ImageInput, IntentParse, ReadImageEvent, SpeechParse, SpeechTranscript } from './ai'

/* =====================================================================================
 * Kontrak antara layar (3 aplikasi) dan "backend". Sekarang diimplementasikan oleh mock-api
 * (berjalan di host api.<domain>); nanti oleh backend asli di hostname yang sama.
 * Semua layar HANYA bicara lewat interface ini.
 * ===================================================================================== */

/* ---------- galat ---------- */

export type ApiErrorCode = 'tidak_valid' | 'tidak_ditemukan' | 'ditolak' | 'bentrok' | 'layanan' | 'jaringan'

export class ApiError extends Error {
  readonly code: ApiErrorCode
  readonly status: number
  /** Kolom yang bermasalah, untuk menampilkan galat di bawah field. */
  readonly field?: string
  /** Pesan netral-bahasa; layar merendernya dengan bahasa saat ini (`message` berisi versi Inggris untuk log dan tes). */
  readonly msg?: Msg

  constructor(code: ApiErrorCode, message: string | Msg, opts: { status?: number; field?: string } = {}) {
    super(typeof message === 'string' ? message : renderMsg(message, 'en'))
    this.name = 'ApiError'
    if (typeof message !== 'string') this.msg = message
    this.code = code
    this.field = opts.field
    this.status =
      opts.status ??
      { tidak_valid: 422, tidak_ditemukan: 404, ditolak: 403, bentrok: 409, layanan: 500, jaringan: 0 }[code]
  }
}

/* ---------- peristiwa ---------- */

export type AppEvent =
  | { type: 'plans.changed'; planId?: string }
  | { type: 'fleet.changed' }
  | { type: 'users.changed' }
  | { type: 'notifications.changed' }
  | { type: 'wallet.changed' }
  | { type: 'settings.changed' }
  | { type: 'clock.changed' }
  | { type: 'demo.changed' }
  | { type: 'data.reset' }

export type Unsubscribe = () => void

/* ---------- masukan ---------- */

export interface RegisterInput {
  name: string
  email: string
  phone: string
  password: string
}

export interface PlanFilter {
  status?: PlanStatus[]
  userId?: string
  q?: string
  vehicleType?: VehicleType
  /** Hanya plan yang punya rute tanpa driver. */
  needsDriver?: boolean
  from?: ISODate
  to?: ISODate
}

export interface DraftPatch {
  shipments?: Shipment[]
  schedule?: Schedule
  priority?: Priority
  draftStep?: number
}

/**
 * `step` / `total` adalah tahap besar (1 dari 4); sub-langkah di dalam satu tahap mengulang `step` yang sama.
 * `msg` (pesan netral-bahasa) lebih diutamakan daripada `label` bila ada; layar merendernya dengan `renderMsg`.
 */
export type ComputeEvent =
  | { type: 'progress'; step: number; total: number; label: string; msg?: Msg }
  | { type: 'done'; optionSet: OptionSet }

export interface ComputeOptions {
  /** Seberapa keras mencari; bawaan `fast`. */
  effort?: Effort
}

export interface VehicleInput {
  type: VehicleType
  plate: string
  model: string
  maxWeightKg: number
  maxVolumeM3: number
  maxDimensionCm: number
  fare: FareConfig
  status: VehicleStatus
}

export interface DriverInput {
  name: string
  phone: string
  vehicleId?: string
  /** PIN 6 digit untuk masuk di aplikasi driver. Backend asli mewajibkannya saat membuat driver (dan menggantinya bila diisi saat ubah); tiruan mengabaikannya. */
  pin?: string
}

/* ---------- keluaran ringkas ---------- */

export interface PlanSummary {
  id: string
  code: string
  userId: string
  customerName: string
  status: PlanStatus
  createdAt: ISODate
  updatedAt: ISODate
  scheduleMode: Schedule['mode']
  startAt?: ISODate
  preset: Preset
  shipments: number
  stops: number
  /** "Kemang Village ... Plaza Senayan" untuk baris tabel. */
  fromLabel: string
  toLabel: string
  totalCostRp?: number
  distanceKm?: number
  durationMin?: number
  /** Satu entri per rute (dua rute motor = ['motor', 'motor']). */
  vehicleTypes: VehicleType[]
  driverNames: string[]
  routesWithoutDriver: number
  /** Stop selesai / total stop pada rute yang berjalan. */
  progress: { done: number; total: number }
  savingsPct?: number
  /** Penghematan rupiah terhadap kirim terpisah (bisa negatif bila opsi lebih mahal tapi lebih cepat). */
  savingsRp?: number
  draftStep: number
  /** Keterlambatan terbesar (menit) di antara rute yang belum selesai. */
  delayMin: number
  /** Jumlah stop yang ditandai bermasalah oleh driver. */
  problemStops: number
  hasAnomaly: boolean
  /** Terisi selama pembatalan menunggu jawaban driver (lihat `Plan.pendingAction`); hanya backend asli yang mengisinya. */
  pendingAction?: 'cancel'
}

export interface UserSummary {
  id: string
  name: string
  email: string
  phone: string
  createdAt: ISODate
  blocked: boolean
  plansCount: number
  completedCount: number
  totalSpendRp: number
  /** Saldo loong.lat saat ini. */
  balanceRp: number
  lastPlanAt?: ISODate
}

export interface UserDetail {
  user: User
  summary: UserSummary
  plans: PlanSummary[]
}

export interface DriverSuggestion {
  driverId: string
  driverName: string
  vehicleId: string
  vehicleType: VehicleType
  plate: string
  rating: number
  distanceToPickupKm: number
  etaToPickupMin: number
  fits: boolean
  /** 0..100, makin tinggi makin cocok. */
  score: number
  reasons: Msg[]
}

export interface DriverRoute {
  planId: string
  planCode: string
  customerName: string
  route: Route
}

export interface DriverHistoryItem {
  planId: string
  planCode: string
  routeId: string
  finishedAt: ISODate
  stops: number
  distanceKm: number
  durationMin: number
  earningsRp: number
}

export interface DriverEarnings {
  todayRp: number
  weekRp: number
  tripsToday: number
  tripsWeek: number
  byDay: { date: string; amountRp: number; trips: number }[]
}

export type IssueKind = 'tidak_ada_orang' | 'barang_rusak' | 'alamat_salah' | 'ditolak_penerima' | 'lainnya'

export const ISSUE_LABEL: Record<IssueKind, string> = {
  get tidak_ada_orang() {
    return t('dom.issue.tidak_ada_orang')
  },
  get barang_rusak() {
    return t('dom.issue.barang_rusak')
  },
  get alamat_salah() {
    return t('dom.issue.alamat_salah')
  },
  get ditolak_penerima() {
    return t('dom.issue.ditolak_penerima')
  },
  get lainnya() {
    return t('dom.issue.lainnya')
  },
}

export interface StopProof {
  kind: 'foto' | 'otp' | 'tanpa'
  /** Kode OTP atau nama berkas foto. */
  value?: string
  /** Isi foto (data URL JPEG/PNG/WebP yang sudah diperkecil); wajib untuk bukti foto di stop antar. */
  photo?: string
}

export interface ActiveRouteLive {
  planId: string
  planCode: string
  customerName: string
  driverName?: string
  route: Route
}

/* ---------- Demo ---------- */

export type FaultMode = 'none' | 'error' | 'slow' | 'offline' | 'empty'

export interface ClockState {
  /** Kecepatan waktu virtual: 1 = nyata, 10, 60. */
  speed: number
  baseReal: number
  baseVirtual: number
}

export interface DemoState {
  clock: ClockState
  /** Waktu virtual sekarang (ms). */
  now: number
  fault: FaultMode
  autopilot: boolean
  seededAt: ISODate
  scenario: string
}

/* ---------- ApiClient ---------- */

export interface ApiClient {
  auth: {
    session(): Promise<Session | null>
    loginCustomer(email: string, password: string): Promise<Session>
    register(input: RegisterInput): Promise<Session>
    loginAdmin(email: string, password: string): Promise<Session>
    loginDriver(phone: string, pin: string): Promise<Session>
    logout(): Promise<void>
  }

  places: {
    search(query: string, limit?: number): Promise<Place[]>
    /** Titik terdekat dari koordinat, dipakai sebagai "reverse geocode" tiruan saat pin digeser. */
    describe(position: LatLng): Promise<{ name: string; address: string; placeId?: string }>
  }

  account: {
    profile(): Promise<User>
    update(patch: Partial<Pick<User, 'name' | 'phone' | 'email'>>): Promise<User>
    addresses: {
      add(input: Omit<SavedAddress, 'id'>): Promise<SavedAddress>
      update(id: string, patch: Partial<Omit<SavedAddress, 'id'>>): Promise<SavedAddress>
      remove(id: string): Promise<void>
    }
  }

  /** Saldo pengirim: dompet, riwayat transaksi, dan top up (pembayaran top up disimulasikan di mock). */
  wallet: {
    get(): Promise<Wallet>
    transactions(): Promise<WalletTx[]>
    createTopUp(input: TopUpInput): Promise<TopUp>
    getTopUp(id: string): Promise<TopUp>
    /** Hanya mock: menandai top up sudah dibayar. Backend asli menerima konfirmasi dari penyedia pembayaran. */
    simulatePay(id: string): Promise<TopUp>
    cancelTopUp(id: string): Promise<TopUp>
  }

  /** Admin: uang di dompet pengguna (saldo total, semua top up, riwayat per pengguna). */
  finance: {
    overview(): Promise<FinanceOverview>
    topUps(filter?: TopUpFilter): Promise<AdminTopUp[]>
    transactions(userId: string): Promise<WalletTx[]>
  }

  notifications: {
    list(): Promise<AppNotification[]>
    markRead(ids?: string[]): Promise<void>
  }

  plans: {
    list(filter?: PlanFilter): Promise<PlanSummary[]>
    get(id: string): Promise<Plan>
    createDraft(): Promise<Plan>
    saveDraft(id: string, patch: DraftPatch): Promise<Plan>
    /**
     * Mengalir: beberapa `progress`, lalu `done`. Menghitung ulang menggantikan optionSet dan menghapus opsi terpilih.
     * Di kawat badan RPC-nya `{id, effort}` (lihat http-api/wire.ts).
     */
    computeOptions(id: string, opts?: ComputeOptions): AsyncIterable<ComputeEvent>
    chooseOption(id: string, optionId: string): Promise<Plan>
    confirm(id: string, paymentMethod: PaymentMethod): Promise<Plan>
    cancel(id: string, reason?: string): Promise<Plan>
    reschedule(id: string, schedule: Schedule): Promise<Plan>
    remove(id: string): Promise<void>
  }

  fleet: {
    vehicles: {
      list(): Promise<Vehicle[]>
      create(input: VehicleInput): Promise<Vehicle>
      update(id: string, patch: Partial<VehicleInput>): Promise<Vehicle>
      remove(id: string): Promise<void>
    }
    drivers: {
      list(): Promise<Driver[]>
      create(input: DriverInput): Promise<Driver>
      update(id: string, patch: Partial<DriverInput> & { status?: DriverStatus }): Promise<Driver>
    }
  }

  users: {
    list(q?: string): Promise<UserSummary[]>
    get(id: string): Promise<UserDetail>
    setBlocked(id: string, blocked: boolean): Promise<UserSummary>
  }

  dispatch: {
    suggest(planId: string, routeId: string): Promise<DriverSuggestion[]>
    assign(planId: string, routeId: string, driverId: string): Promise<Plan>
    unassign(planId: string, routeId: string): Promise<Plan>
    /** Menerapkan saran terbaik untuk semua rute plan yang belum punya driver. */
    autoAssign(planId: string): Promise<Plan>
  }

  driver: {
    me(): Promise<{ driver: Driver; vehicle?: Vehicle }>
    setOnline(online: boolean): Promise<Driver>
    routes(): Promise<DriverRoute[]>
    startRoute(planId: string, routeId: string): Promise<Plan>
    arrive(planId: string, routeId: string, stopId: string): Promise<Plan>
    complete(planId: string, routeId: string, stopId: string, proof?: StopProof): Promise<Plan>
    reportIssue(planId: string, routeId: string, stopId: string, issue: IssueKind, note?: string): Promise<Plan>
    history(): Promise<DriverHistoryItem[]>
    earnings(): Promise<DriverEarnings>
  }

  live: {
    /** Rute yang sedang ditugaskan/berjalan; UI menganimasikan posisi lewat routePositionAt(). */
    routes(): Promise<ActiveRouteLive[]>
    /** Driver yang online tanpa tugas (titik diam di peta). */
    idleDrivers(): Promise<Driver[]>
  }

  demo: {
    state(): Promise<DemoState>
    reset(scenario?: string): Promise<void>
    setSpeed(speed: number): Promise<void>
    /** Loncat maju di waktu virtual (menit). */
    advance(minutes: number): Promise<void>
    setAutopilot(on: boolean): Promise<void>
    setFault(mode: FaultMode): Promise<void>
    /**
     * Menambah keterlambatan pada rute yang berjalan dan memicu notifikasi anomali.
     * Tanpa `routeId`, dipilih rute berjalan pertama yang belum telat; mengembalikan kode plan yang terdampak.
     */
    triggerDelay(routeId: string | undefined, minutes: number): Promise<{ planCode: string }>
    /** Membuat plan acak yang sudah dikonfirmasi agar antrean Backoffice terisi live. */
    spawnPlan(): Promise<Plan>
  }

  settings: {
    get(): Promise<AppSettings>
    /** Hanya admin. */
    update(patch: { ai?: Partial<AiFeatures>; dispatch?: Partial<DispatchSettings> }): Promise<AppSettings>
  }

  /** Fitur AI. Semuanya hanya mengusulkan; tidak ada yang mengubah data tanpa tindakan eksplisit pengguna. */
  ai: {
    /** Mengalir: beberapa `progress`, lalu `done` berisi barang yang terdeteksi. */
    readImage(input: ImageInput): AsyncIterable<ReadImageEvent>
    /** Rekaman suara -> teks (backend asli: Muse Voice). Tiruan menolak dengan "belum tersedia"; layar memakai Web Speech API peramban. */
    transcribe(input: AudioInput): Promise<SpeechTranscript>
    parseSpeech(transcript: string): Promise<SpeechParse>
    parseIntent(text: string): Promise<IntentParse>
    /** Mengalir: jejak alat, potongan jawaban, lalu `done` beserta tindakan yang bisa diterapkan. */
    copilot(scope: CopilotScope, question: string): AsyncIterable<CopilotEvent>
    /** Narasi analitik untuk admin; format aliran sama dengan copilot. */
    insights(): AsyncIterable<CopilotEvent>
    /**
     * Penjelasan AI atas opsi sebuah plan (hanya console). Teks sudah diperiksa terhadap angka di server, jadi datang utuh:
     * satu `token` berisi seluruh teks lalu `done` tanpa tindakan. Hasilnya juga disimpan sebagai `optionSet.report`.
     */
    explainOptions(planId: string): AsyncIterable<CopilotEvent>
  }

  events: {
    subscribe(listener: (event: AppEvent) => void): Unsubscribe
  }
}

/** Metode yang mengembalikan AsyncIterable (diteruskan sebagai aliran lewat RPC). */
export const STREAM_METHODS: ReadonlySet<string> = new Set(['plans.computeOptions', 'ai.readImage', 'ai.copilot', 'ai.insights', 'ai.explainOptions'])

/** Kredensial demo yang ditampilkan sebagai petunjuk di layar masuk. */
export const DEMO_CREDENTIALS: Record<Role, { id: string; secret: string; readonly label: string }> = {
  customer: {
    id: 'dina@contoh.id',
    secret: 'demo1234',
    get label() {
      return `Dina Pramesti (${t('dom.role.customer')})`
    },
  },
  admin: {
    id: 'admin@loong.lat',
    secret: 'admin1234',
    get label() {
      return `Rani Wulandari (${t('dom.role.admin')})`
    },
  },
  driver: {
    id: '0812-3456-7801',
    secret: '123456',
    get label() {
      return `Budi Santoso (${t('dom.role.driver')})`
    },
  },
}
