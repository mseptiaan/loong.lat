import { ApiError, type ApiClient } from '@loong/domain'
import { msg } from '@loong/i18n'
import { canDecodeAudio, recordingToWav, type AudioDecoder } from './wav'

/*
 * Input suara di mode http: rekam dengan MediaRecorder, kodekan sebagai WAV 16 kHz mono (satu-satunya format yang diterima
 * Muse Voice), kirim ke `ai.transcribe`, lalu transkripnya diperiksa pengguna dan diurai `ai.parseSpeech`. Di mode tiruan
 * tetap Web Speech API peramban. Semua yang bisa diuji tanpa peramban ada di sini (dan di wav.ts) dengan dependensi yang bisa
 * disuntik; VoiceSheet.tsx hanya merangkainya ke layar.
 */

/** Rekaman dihentikan otomatis di sini; WAV 16 kHz mono satu menit = 1,9 MB (2,6 MB base64), di bawah batas badan permintaan. */
export const MAX_RECORD_MS = 60_000

/** Format perekam yang dicoba berurutan (Chrome/Firefox: webm atau ogg, Safari: mp4). */
const MIME_CANDIDATES = ['audio/webm;codecs=opus', 'audio/webm', 'audio/ogg;codecs=opus', 'audio/mp4'] as const

export function pickRecorderMime(isSupported: (mime: string) => boolean): string | undefined {
  return MIME_CANDIDATES.find((m) => isSupported(m))
}

export type CaptureMode = 'record' | 'recognize' | 'type'

/**
 * Cara menangkap suara: merekam untuk backend (hanya mode http, saat saklar suara menyala dan peramban bisa merekam), pengenalan
 * suara peramban, atau mengetik saja. `recordFailed` (perekaman atau transkripsi gagal) menurunkan "record" ke pilihan berikutnya.
 */
export function pickCaptureMode(o: { http: boolean; voiceOn: boolean; canRecord: boolean; canRecognize: boolean; recordFailed?: boolean }): CaptureMode {
  if (o.http && o.voiceOn && o.canRecord && !o.recordFailed) return 'record'
  return o.canRecognize ? 'recognize' : 'type'
}

/* ---------- perekam ---------- */

interface TrackLike {
  stop(): void
}
export interface StreamLike {
  getTracks(): TrackLike[]
}

export interface RecorderLike {
  state: string
  mimeType: string
  ondataavailable: ((e: { data: Blob }) => void) | null
  onstop: (() => void) | null
  onerror: ((e: unknown) => void) | null
  start(): void
  stop(): void
}

export interface RecordingDeps {
  getUserMedia(constraints: { audio: true }): Promise<StreamLike>
  createRecorder(stream: StreamLike, options: { mimeType?: string }): RecorderLike
  isTypeSupported(mime: string): boolean
  maxMs?: number
  /** Dipanggil saat batas durasi menghentikan perekam; pemanggil lalu menyelesaikan rekaman seperti biasa. */
  onLimit?: () => void
}

export interface Recording {
  /** Menghentikan perekam (bila belum) dan mengembalikan rekaman; menolak bila perekam galat. */
  stop(): Promise<Blob>
  /** Membuang rekaman dan melepas mikrofon. */
  cancel(): void
}

/** Meminta mikrofon dan mulai merekam. Menolak bila izin ditolak atau perekam tidak bisa dibuat (mikrofon dilepas lagi). */
export async function startRecording(deps: RecordingDeps): Promise<Recording> {
  const stream = await deps.getUserMedia({ audio: true })
  const release = () => stream.getTracks().forEach((t) => t.stop())
  const mimeType = pickRecorderMime((m) => deps.isTypeSupported(m))
  let recorder: RecorderLike
  try {
    recorder = deps.createRecorder(stream, { mimeType })
  } catch (err) {
    release()
    throw err
  }

  const chunks: Blob[] = []
  const timer = setTimeout(() => {
    if (recorder.state === 'inactive') return
    deps.onLimit?.()
    recorder.stop()
  }, deps.maxMs ?? MAX_RECORD_MS)
  const done = new Promise<Blob>((resolve, reject) => {
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) chunks.push(e.data)
    }
    recorder.onstop = () => {
      clearTimeout(timer)
      release()
      resolve(new Blob(chunks, { type: recorder.mimeType || mimeType || 'audio/webm' }))
    }
    recorder.onerror = (e) => {
      clearTimeout(timer)
      release()
      reject(e instanceof Error ? e : new Error('recorder'))
    }
  })
  done.catch(() => undefined) // galat yang tak ditunggu siapa pun (mis. sudah dibatalkan) bukan "unhandled rejection"

  recorder.start()

  return {
    stop() {
      if (recorder.state !== 'inactive') recorder.stop()
      return done
    },
    cancel() {
      clearTimeout(timer)
      recorder.ondataavailable = null
      recorder.onstop = null
      recorder.onerror = null
      if (recorder.state !== 'inactive') recorder.stop()
      release()
    },
  }
}

/**
 * Mengodekan rekaman sebagai WAV 16 kHz mono, mengirimnya ke `ai.transcribe`, dan mengembalikan teksnya; transkrip kosong
 * ditolak seperti "belum ada ucapan". Rekaman yang tidak bisa didekode menolak dengan galat pendekode (layar jatuh ke Web Speech API).
 */
export async function transcribeRecording(ai: Pick<ApiClient['ai'], 'transcribe'>, blob: Blob, decode: AudioDecoder): Promise<string> {
  const data = await recordingToWav(await blob.arrayBuffer(), decode)
  const { transcript } = await ai.transcribe({ type: 'audio/wav', size: data.byteLength, data })
  const text = transcript.trim()
  if (!text) throw new ApiError('tidak_valid', msg('srv.api.noSpeech'), { field: 'file' })
  return text
}

/** Penggolongan galat perekaman atau transkripsi untuk memilih pesan: izin mikrofon, tak ada mikrofon, jawaban backend, atau lainnya. */
export function captureErrorKind(err: unknown): 'denied' | 'noMic' | 'api' | 'other' {
  if (err instanceof ApiError) return 'api'
  const name = typeof err === 'object' && err !== null && 'name' in err ? String((err as { name: unknown }).name) : ''
  if (name === 'NotAllowedError' || name === 'SecurityError' || name === 'PermissionDeniedError') return 'denied'
  if (name === 'NotFoundError' || name === 'OverconstrainedError' || name === 'DevicesNotFoundError') return 'noMic'
  return 'other'
}

/* ---------- peramban ---------- */

/** Apakah peramban ini bisa merekam suara dan mendekodenya untuk dikirim (MediaRecorder, mikrofon, AudioContext). */
export const canRecordAudio = (): boolean =>
  typeof MediaRecorder !== 'undefined' && typeof navigator !== 'undefined' && !!navigator.mediaDevices?.getUserMedia && canDecodeAudio()

/** Dependensi perekam sungguhan: mikrofon peramban dan MediaRecorder. */
export const browserRecordingDeps = (onLimit?: () => void): RecordingDeps => ({
  getUserMedia: (constraints) => navigator.mediaDevices.getUserMedia(constraints),
  // tipe peristiwa MediaRecorder lebih sempit daripada RecorderLike; yang dipakai di sini hanya `data`
  createRecorder: (stream, options) => new MediaRecorder(stream as MediaStream, options) as unknown as RecorderLike,
  isTypeSupported: (mime) => MediaRecorder.isTypeSupported(mime),
  onLimit,
})
