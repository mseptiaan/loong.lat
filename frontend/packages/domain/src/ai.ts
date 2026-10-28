import type { ISODate, Preset } from './types'

/* =====================================================================================
 * Kontrak fitur AI. Dijawab oleh tiruan deterministik di mock-api (angkanya selalu cocok dengan data), atau oleh backend
 * asli yang memanggil model Meta (Muse Spark untuk gambar/teks, Muse Voice untuk suara). Di mode http, baca foto, suara, dan
 * maksud jatuh ke mesin deterministik yang sama bila backend AI mati (packages/http-api).
 * Prinsip: AI hanya mengusulkan; tidak satu pun metode di sini mengubah data tanpa tindakan eksplisit pengguna.
 * ===================================================================================== */

/** Fitur AI yang bisa dinyalakan/dimatikan admin; dibaca oleh semua aplikasi. */
export interface AiFeatures {
  vision: boolean
  voice: boolean
  intent: boolean
  copilot: boolean
}

/** Penugasan driver. Otomatis: pesanan sekarang ke driver terdekat yang tersedia, pesanan terjadwal ke driver yang kosong pada jamnya. */
export interface DispatchSettings {
  auto: boolean
}

export interface AppSettings {
  ai: AiFeatures
  dispatch: DispatchSettings
}

export const DEFAULT_SETTINGS: AppSettings = {
  ai: { vision: true, voice: true, intent: true, copilot: true },
  dispatch: { auto: true },
}

/* ---------- baca gambar ---------- */

export interface ImageInput {
  name: string
  type: string
  size: number
  /** Data biner untuk model sungguhan; tiruan tidak membacanya. Di kawat dikirim sebagai base64 (foto besar diperkecil dulu). */
  data?: ArrayBuffer
}

/** Barang hasil tebakan AI. `confidence` 0..1; di bawah ambang diberi tanda "periksa" di UI. */
export interface DetectedItem {
  /** Kunci katalog tempat nilai bawaan berasal (untuk debugging dan penyuntingan). */
  key: string
  name: string
  qty: number
  weightKg: number
  lengthCm: number
  widthCm: number
  heightCm: number
  fragile: boolean
  confidence: number
}

/** Di bawah ini UI memberi tanda "periksa" pada barang hasil AI. */
export const LOW_CONFIDENCE = 0.7

/** Skor kecocokan driver (0-100) minimum agar kandidat terbaik boleh disebut "disarankan"; di bawahnya hanya "satu-satunya yang tersedia". */
export const RECOMMEND_MIN_SCORE = 40

/**
 * Rekaman suara untuk `ai.transcribe`. Tiruan tidak bisa mentranskripsi; backend asli mengirimnya ke Muse Voice, yang hanya
 * menerima WAV PCM mono 16 atau 24 kHz (maksimal 10 menit). Layar mengodekan rekaman peramban menjadi WAV 16 kHz sebelum memanggil.
 */
export interface AudioInput {
  /** Tipe MIME rekaman: `audio/wav` untuk backend asli. */
  type: string
  size: number
  /** Data biner rekaman; di kawat dikirim sebagai base64. */
  data?: ArrayBuffer
}

export interface SpeechTranscript {
  transcript: string
}

export type ReadImageEvent =
  | { type: 'progress'; step: number; total: number; label: string }
  | { type: 'done'; items: DetectedItem[]; summary: string }

/* ---------- suara dan maksud ---------- */

export interface SpeechParse {
  items: DetectedItem[]
  /** Potongan transkrip yang dipahami dan barang yang dihasilkannya (untuk menyorot). */
  matches: { text: string; itemIndex: number }[]
  /** Bagian transkrip yang tidak dipahami; UI menawarkan mengisinya manual. */
  leftover: string
}

export interface IntentChip {
  id: string
  kind: 'prioritas' | 'batas_waktu'
  label: string
  preset?: Preset
  deadline?: ISODate
  /** Tempat antar yang dituju batas waktu ini, dengan kata-kata pelanggan; kosong = untuk semua pengiriman. */
  place?: string
}

export interface IntentParse {
  chips: IntentChip[]
  /** Potongan kalimat yang dipahami. */
  understood: string[]
  leftover: string
}

/* ---------- Copilot ---------- */

export type CopilotScope = { kind: 'plan'; planId: string } | { kind: 'ops' }

/** Satu panggilan alat yang terlihat oleh pengguna: judul + sasaran, status, durasi, dan detail yang bisa dibuka. */
export interface CopilotToolTrace {
  id: string
  title: string
  target?: string
  status: 'running' | 'done' | 'failed'
  ms?: number
  detail?: string
}

/** Tindakan yang diusulkan Copilot; baru dijalankan bila pengguna menekannya. */
export interface CopilotAction {
  id: string
  label: string
  kind: 'pilih_opsi' | 'tugaskan_terbaik' | 'buka_plan'
  planId?: string
  optionId?: string
  primary?: boolean
}

export type CopilotEvent =
  | { type: 'tool'; trace: CopilotToolTrace }
  | { type: 'token'; text: string }
  | { type: 'done'; actions: CopilotAction[]; requestId: string }
