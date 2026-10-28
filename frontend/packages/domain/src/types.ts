/**
 * Tipe domain bersama untuk tiga aplikasi. Semua uang dalam rupiah bulat, jarak km, durasi menit,
 * waktu ISO 8601. Backend asli nanti cukup mengembalikan bentuk yang sama.
 */

import { t, type Msg } from '@loong/i18n'

export type ISODate = string

/* ---------- geografi ---------- */

export interface LatLng {
  lat: number
  lng: number
}

/** Urutan [lng, lat], sesuai GeoJSON/MapLibre. */
export type LngLat = [number, number]

export type PlaceKind =
  | 'mall'
  | 'stasiun'
  | 'kampus'
  | 'kantor'
  | 'pasar'
  | 'rumah-sakit'
  | 'landmark'
  | 'hunian'
  | 'gudang'

export interface Place {
  id: string
  name: string
  address: string
  /** Kecamatan/kawasan, mis. "Kebayoran Baru". */
  area: string
  kind: PlaceKind
  position: LatLng
}

export interface PlaceRef {
  name: string
  address: string
  position: LatLng
  placeId?: string
}

/* ---------- armada ---------- */

export type VehicleType = 'motor' | 'blindvan'
export type VehicleStatus = 'aktif' | 'servis' | 'nonaktif'

export interface FareConfig {
  /** Tarif dasar per rute. */
  base: number
  perKm: number
  perMin: number
  /** Biaya tambahan per stop setelah pasangan ambil-antar pertama. */
  perExtraStop: number
  /** Surcharge per kg di atas ambang berat. */
  surchargePerKg: number
  surchargeAboveKg: number
}

export interface Vehicle {
  id: string
  type: VehicleType
  plate: string
  model: string
  maxWeightKg: number
  maxVolumeM3: number
  /** Dimensi terbesar satu barang yang masih muat (cm). */
  maxDimensionCm: number
  fare: FareConfig
  status: VehicleStatus
  driverId?: string
}

export type DriverStatus = 'online' | 'bertugas' | 'offline'

export interface Driver {
  id: string
  name: string
  phone: string
  rating: number
  vehicleId?: string
  status: DriverStatus
  position?: LatLng
  lastSeen?: ISODate
  joinedAt: ISODate
  completedTrips: number
}

/* ---------- pengguna ---------- */

export interface SavedAddress {
  id: string
  label: string
  place: PlaceRef
  contactName: string
  contactPhone: string
}

export interface User {
  id: string
  name: string
  email: string
  phone: string
  createdAt: ISODate
  blocked: boolean
  savedAddresses: SavedAddress[]
}

export type Role = 'customer' | 'admin' | 'driver'

export interface Session {
  role: Role
  userId: string
  name: string
}

/* ---------- barang & pengiriman ---------- */

export type ItemSource = 'manual' | 'foto' | 'suara'

export interface Item {
  id: string
  name: string
  qty: number
  /** Berat per satuan. */
  weightKg: number
  lengthCm: number
  widthCm: number
  heightCm: number
  fragile: boolean
  source: ItemSource
  /** 0..1, hanya untuk barang hasil AI. */
  confidence?: number
  /** Kunci katalog (ITEM_CATALOG) bila barang dibuat dari katalog: namanya mengikuti bahasa tampilan, lihat `itemLabel`. */
  catalogKey?: string
}

export interface Waypoint extends PlaceRef {
  contactName: string
  contactPhone: string
  note?: string
}

export interface Shipment {
  id: string
  pickup: Waypoint
  dropoff: Waypoint
  items: Item[]
  /** Paling lambat tiba di titik antar (hasil isian di langkah rute atau hasil urai AI). Tiap pengiriman punya sendiri. */
  deadline?: ISODate
}

export interface TimeWindow {
  from: ISODate
  to: ISODate
}

export type ScheduleMode = 'sekarang' | 'terjadwal'

export interface Schedule {
  mode: ScheduleMode
  window?: TimeWindow
}

export type Preset = 'termurah' | 'terpendek' | 'tercepat' | 'seimbang'

export interface Priority {
  preset: Preset
  /** Kalimat bebas dari user, mis. "paling hemat tapi sampai sebelum jam 5". */
  text?: string
}

/* ---------- rute & opsi ---------- */

export type StopKind = 'ambil' | 'antar'
export type StopStatus = 'menunggu' | 'menuju' | 'tiba' | 'selesai' | 'bermasalah'

/** Satu baris barang yang ditampilkan ke driver saat mengambil/mengantar (untuk dicek, bukan untuk dihitung). */
export interface StopItem {
  name: string
  /** Salinan `Item.catalogKey`; lihat `itemLabel`. */
  catalogKey?: string
  qty: number
  /** Berat total dan dimensi, mis. "4 kg · 40×30×30 cm" (`dom.stopItem.detail`). */
  detail: Msg
  fragile: boolean
}

/** Aspek yang diperiksa AI pada foto bukti antar. */
export type ProofCheckKey = 'recipient' | 'parcel' | 'quality'
/** Hasil satu pemeriksaan: terlihat jelas, tidak bisa dipastikan, atau tidak ada. */
export type ProofCheckResult = 'ok' | 'unclear' | 'missing'

export interface ProofCheck {
  key: ProofCheckKey
  result: ProofCheckResult
  /** Temuan singkat untuk pemeriksaan ini (pesan netral-bahasa). */
  note: Msg
}

/** Ringkasan AI atas foto bukti antar. Hanya menjelaskan isi foto; keputusan tetap di manusia. */
export interface ProofAnalysis {
  /** Satu-dua kalimat tentang apa yang terlihat di foto. */
  summary: Msg
  checks: ProofCheck[]
  /** 0..1: keyakinan model atas ringkasan. */
  confidence: number
  /** Nama model pembuat ringkasan (tiruan di demo; Muse Spark bila backend asli terhubung). */
  model: string
  analyzedAt: ISODate
}

/** Bukti yang dicatat saat driver menyelesaikan stop antar: kode OTP penerima atau foto serah-terima. */
export interface DeliveryProof {
  kind: 'otp' | 'foto'
  at: ISODate
  /** Foto yang sudah diperkecil sebagai data URL (hanya untuk `kind: 'foto'`). */
  photo?: string
  analysis?: ProofAnalysis
}

export interface RouteStop {
  id: string
  seq: number
  kind: StopKind
  shipmentId: string
  waypoint: Waypoint
  /** Jarak dan waktu dari titik sebelumnya (atau titik awal untuk stop pertama). */
  legKm: number
  legMin: number
  /** Menit sejak startAt rute. */
  etaMin: number
  arriveAt: ISODate
  status: StopStatus
  arrivedAt?: ISODate
  completedAt?: ISODate
  /** Masalah yang dilaporkan driver (pesan netral-bahasa: jenis masalah + catatan). */
  issue?: Msg
  /** Ringkasan singkat barang, mis. "2 barang · 4 kg" (pesan netral-bahasa). */
  itemsSummary: Msg
  /** Rincian barang pengiriman ini; kosong pada data lama (pakai `itemsSummary`). */
  items?: StopItem[]
  /** Kode OTP 4 digit untuk stop 'antar': diberikan ke penerima, dimasukkan driver sebagai bukti antar. */
  deliveryCode?: string
  /** Bukti antar yang dicatat driver; terisi untuk stop antar yang selesai. */
  proof?: DeliveryProof
}

export type RouteStatus = 'menunggu_driver' | 'ditugaskan' | 'berjalan' | 'selesai'

export interface FareLine {
  label: Msg
  amountRp: number
}

export interface Route {
  id: string
  planId: string
  vehicleType: VehicleType
  vehicleId?: string
  driverId?: string
  status: RouteStatus
  startPosition: LatLng
  startAt: ISODate
  stops: RouteStop[]
  distanceKm: number
  durationMin: number
  costRp: number
  fare: FareLine[]
  totalWeightKg: number
  /** Opsional: backend asli mengirim jalan sungguhan. Bila kosong dipakai buildRouteGeometry(). */
  geometry?: LngLat[]
  assignedAt?: ISODate
  startedAt?: ISODate
  finishedAt?: ISODate
  /**
   * Kapan kendaraan mulai menempuh ruas menuju stop aktif (waktu virtual). Posisi kendaraan adalah fungsi murni
   * dari ini + jam virtual (lihat routePositionAt), jadi semua tab melihat posisi yang sama tanpa simulator.
   */
  legStartedAt?: ISODate
  /** Total keterlambatan yang disuntikkan (menit), untuk anomali/analitik. */
  delayMin?: number
}

export interface Totals {
  costRp: number
  distanceKm: number
  /** Waktu sampai paket terakhir selesai (rute terlama), dari jam berangkat. */
  durationMin: number
  /** Rata-rata menit sampai tiap paket tiba di tujuan, dari jam berangkat. */
  avgDeliveryMin: number
  stops: number
  motor: number
  blindvan: number
}

export interface Savings {
  costRp: number
  pct: number
  distanceKm: number
}

export interface PlanOption {
  id: string
  preset: Preset
  /** Label tampilan, mis. "Seimbang" atau "Seimbang · Termurah" bila hasilnya sama. */
  label: Msg
  recommended: boolean
  headline: Msg
  routes: Route[]
  totals: Totals
  savings: Savings
  /** Alasan yang bisa ditelusuri, dipakai juga oleh Copilot AI. */
  reasons: Msg[]
}

export interface OptionSet {
  computedAt: ISODate
  baseline: Totals
  options: PlanOption[]
  /** Sidik jari input; berubah bila barang/lokasi/jadwal berubah sehingga opsi lama basi. */
  inputHash: string
  /** Cara opsi ini dicari (angka dari engine sendiri). Tidak ada pada data lama; layar tidak mengarang pengganti. */
  method?: Method
  /** Penjelasan AI atas opsi ini, ditulis atas permintaan setelah opsi dihitung. */
  report?: Report
}

/**
 * Seberapa keras engine mencari opsi; dipilih pengirim di langkah 3. Tiap tingkat memuat yang sebelumnya, jadi hasilnya
 * tidak pernah lebih buruk daripada tingkat di bawahnya: `fast` (tiap preset sekali, detik), `thorough` (beberapa cara
 * mengelompokkan stop lalu dipoles), `reviewed` (ditambah ronde tinjauan oleh peninjau AI atau aturan bawaan).
 */
export type Effort = 'fast' | 'thorough' | 'reviewed'

/** Dari yang tercepat; urutan ini juga urutan pilihan di layar. */
export const EFFORTS: readonly Effort[] = ['fast', 'thorough', 'reviewed']

export const DEFAULT_EFFORT: Effort = 'fast'

export const isEffort = (v: unknown): v is Effort => typeof v === 'string' && (EFFORTS as readonly string[]).includes(v)

/** Cara sebuah OptionSet ditemukan. Semua angka dihitung engine; bagian yang tidak terjadi dihilangkan, bukan diisi nol. */
export interface Method {
  effort: Effort
  /** Jumlah percobaan (penyelesaian) yang dijalankan, untuk semua preset. */
  trials: number
  /** Ronde tinjauan setelah putaran pertama; hanya pada `reviewed`. */
  rounds?: number
  /** Jawaban terbaik diperhalus dari posisinya. */
  polished?: boolean
  /** Siapa yang memilih percobaan lanjutan: model AI atau aturan bawaan (tanpa AI, atau model tak tersedia). */
  reviewer?: 'ai' | 'rules'
  /** Pencarian menyeluruh membuktikan setiap opsi optimal. Satu-satunya dasar untuk menyebut "optimal". */
  proven?: boolean
  /** Asal jarak: "mapbox", "mapbox-cache", atau "estimate". */
  roadSource?: string
}

/** Penjelasan opsi yang ditulis AI dalam bahasa permintaan (`locale`: kode bahasa). */
export interface Report {
  text: string
  locale: string
  at: ISODate
}

/* ---------- plan ---------- */

export type PlanStatus =
  | 'draft'
  | 'opsi_siap'
  | 'menunggu_driver'
  | 'ditugaskan'
  | 'berjalan'
  | 'selesai'
  | 'dibatalkan'

export interface PlanEvent {
  id: string
  at: ISODate
  text: Msg
}

export type PaymentMethod = 'saldo' | 'transfer' | 'tunai'

export interface Plan {
  id: string
  /** Kode tampilan, mis. PL-2041. */
  code: string
  userId: string
  status: PlanStatus
  schedule: Schedule
  priority: Priority
  shipments: Shipment[]
  optionSet?: OptionSet
  selectedOptionId?: string
  /** Rute hasil opsi terpilih, terisi saat dikonfirmasi. */
  routes: Route[]
  paymentMethod?: PaymentMethod
  totalCostRp?: number
  createdAt: ISODate
  updatedAt: ISODate
  confirmedAt?: ISODate
  finishedAt?: ISODate
  cancelReason?: string
  /** Terisi selama tindakan dua tahap berjalan ("cancel": menunggu jawaban driver); layar menampilkan "membatalkan…" dan tidak menawarkan tindakan lain. Hanya backend asli yang mengisinya. */
  pendingAction?: 'cancel'
  events: PlanEvent[]
  /** Langkah wizard terakhir yang dicapai (1 rute, 2 barang, 3 pilih opsi, 4 tinjau); untuk melanjutkan draft. */
  draftStep: number
}

export type NotificationAudience = `customer:${string}` | 'admin' | `driver:${string}`

export interface AppNotification {
  id: string
  audience: NotificationAudience
  at: ISODate
  title: Msg
  body: Msg
  planId?: string
  read: boolean
}

/*
 * Peta label memakai getter agar setiap pembacaan mengikuti bahasa aktif (`PRESET_LABEL[p]` tetap sama di pemanggil).
 */
export const PRESET_LABEL: Record<Preset, string> = {
  get termurah() {
    return t('dom.preset.termurah')
  },
  get terpendek() {
    return t('dom.preset.terpendek')
  },
  get tercepat() {
    return t('dom.preset.tercepat')
  },
  get seimbang() {
    return t('dom.preset.seimbang')
  },
}

export const PRESET_HINT: Record<Preset, string> = {
  get termurah() {
    return t('dom.presetHint.termurah')
  },
  get terpendek() {
    return t('dom.presetHint.terpendek')
  },
  get tercepat() {
    return t('dom.presetHint.tercepat')
  },
  get seimbang() {
    return t('dom.presetHint.seimbang')
  },
}

export const PLAN_STATUS_LABEL: Record<PlanStatus, string> = {
  get draft() {
    return t('dom.planStatus.draft')
  },
  get opsi_siap() {
    return t('dom.planStatus.opsi_siap')
  },
  get menunggu_driver() {
    return t('dom.planStatus.menunggu_driver')
  },
  get ditugaskan() {
    return t('dom.planStatus.ditugaskan')
  },
  get berjalan() {
    return t('dom.planStatus.berjalan')
  },
  get selesai() {
    return t('dom.planStatus.selesai')
  },
  get dibatalkan() {
    return t('dom.planStatus.dibatalkan')
  },
}

/** Label tindakan tertunda sebuah plan (`Plan.pendingAction`): menggantikan label status selama tindakan itu berjalan. */
export const PLAN_PENDING_LABEL: Record<NonNullable<Plan['pendingAction']>, string> = {
  get cancel() {
    return t('dom.planPending.cancel')
  },
}

/** Teks status yang ditampilkan: tindakan tertunda (mis. "Membatalkan…") menang atas status plan. */
export const planStatusText = (p: Pick<Plan, 'status' | 'pendingAction'>): string =>
  p.pendingAction ? PLAN_PENDING_LABEL[p.pendingAction] : PLAN_STATUS_LABEL[p.status]

export const ROUTE_STATUS_LABEL: Record<RouteStatus, string> = {
  get menunggu_driver() {
    return t('dom.routeStatus.menunggu_driver')
  },
  get ditugaskan() {
    return t('dom.routeStatus.ditugaskan')
  },
  get berjalan() {
    return t('dom.routeStatus.berjalan')
  },
  get selesai() {
    return t('dom.routeStatus.selesai')
  },
}

export const STOP_STATUS_LABEL: Record<StopStatus, string> = {
  get menunggu() {
    return t('dom.stopStatus.menunggu')
  },
  get menuju() {
    return t('dom.stopStatus.menuju')
  },
  get tiba() {
    return t('dom.stopStatus.tiba')
  },
  get selesai() {
    return t('dom.stopStatus.selesai')
  },
  get bermasalah() {
    return t('dom.stopStatus.bermasalah')
  },
}

export const VEHICLE_TYPE_LABEL: Record<VehicleType, string> = {
  get motor() {
    return t('dom.vehicleType.motor')
  },
  get blindvan() {
    return t('dom.vehicleType.blindvan')
  },
}

export const DRIVER_STATUS_LABEL: Record<DriverStatus, string> = {
  get online() {
    return t('dom.driverStatus.online')
  },
  get bertugas() {
    return t('dom.driverStatus.bertugas')
  },
  get offline() {
    return t('dom.driverStatus.offline')
  },
}

export const PROOF_CHECK_LABEL: Record<ProofCheckKey, string> = {
  get recipient() {
    return t('dom.proofCheck.recipient')
  },
  get parcel() {
    return t('dom.proofCheck.parcel')
  },
  get quality() {
    return t('dom.proofCheck.quality')
  },
}

export const PROOF_RESULT_LABEL: Record<ProofCheckResult, string> = {
  get ok() {
    return t('dom.proofResult.ok')
  },
  get unclear() {
    return t('dom.proofResult.unclear')
  },
  get missing() {
    return t('dom.proofResult.missing')
  },
}
