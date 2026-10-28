import { ApiError, type AudioInput, type ImageInput } from '@loong/domain'
import { msg } from '@loong/i18n'

/*
 * Biner (foto, suara) di kawat: `ImageInput.data` dan `AudioInput.data` adalah ArrayBuffer di layar, tetapi badan RPC
 * adalah JSON, jadi dikirim sebagai base64 di dalam `input`:
 *   ai.readImage  -> {"input":{"name","type","size","data":"<base64>"}}
 *   ai.transcribe -> {"input":{"type":"audio/wav","size","data":"<base64>"}}
 * `size` adalah jumlah byte yang SEBENARNYA dikirim (setelah foto diperkecil), dan `type` tipe MIME-nya.
 */

/** Batas badan permintaan yang membawa biner (~3 MB, sudah termasuk pembengkakan base64 sebesar 4/3). */
export const MAX_BODY_BYTES = 3 * 1024 * 1024

/** Foto sekecil ini dikirim apa adanya, tanpa dibuka dan dikodekan ulang. */
export const SEND_AS_IS_BYTES = 300 * 1024

export interface ScaleStep {
  /** Sisi terpanjang hasil (px). */
  maxSide: number
  /** Mutu JPEG 0..1. */
  quality: number
}

/** Tangga pengecilan: coba yang terbaik dulu, turun hanya bila badan permintaan masih terlalu besar. */
export const SCALE_STEPS: readonly ScaleStep[] = [
  { maxSide: 1280, quality: 0.85 },
  { maxSide: 1024, quality: 0.7 },
  { maxSide: 720, quality: 0.6 },
]

export interface RawMedia {
  type: string
  data: ArrayBuffer
}

/** Memperkecil gambar menjadi JPEG; `undefined` bila tidak bisa (tanpa canvas, atau berkas bukan gambar yang bisa dibuka). */
export type ImageScaler = (image: RawMedia, step: ScaleStep) => Promise<RawMedia | undefined>

export interface WireImage {
  name: string
  type: string
  size: number
  data: string
}

export interface WireAudio {
  type: string
  size: number
  data: string
}

export function toBase64(data: ArrayBuffer | Uint8Array): string {
  const bytes = data instanceof Uint8Array ? data : new Uint8Array(data)
  let binary = ''
  // per potongan: argumen fungsi punya batas, jadi foto besar tidak boleh di-spread sekaligus
  for (let i = 0; i < bytes.length; i += 0x8000) binary += String.fromCharCode.apply(null, bytes.subarray(i, i + 0x8000) as unknown as number[])
  return btoa(binary)
}

const base64Length = (bytes: number) => Math.ceil(bytes / 3) * 4

/** Apakah badan `{input: {...meta, data}}` untuk `bytes` byte biner muat dalam `max`; dihitung tanpa mengodekan datanya. */
function fitsBody(meta: object, bytes: number, max: number): boolean {
  const overhead = new TextEncoder().encode(JSON.stringify({ input: { ...meta, data: '' } })).length
  return overhead + base64Length(bytes) <= max
}

export interface PrepareImageOptions {
  scale?: ImageScaler
  maxBodyBytes?: number
}

/**
 * Menyiapkan foto untuk kawat. Foto besar diperkecil (sisi terpanjang 1280 px, JPEG 0,85; turun bertahap bila badan masih
 * di atas batas). Mengembalikan `undefined` bila foto tidak bisa dikirim: tak ada datanya, atau terlalu besar dan tak bisa
 * diperkecil. Pemanggil lalu memakai mesin lokal.
 */
export async function prepareImage(input: ImageInput, options: PrepareImageOptions = {}): Promise<WireImage | undefined> {
  const { data } = input
  if (!data || data.byteLength === 0) return undefined
  const max = options.maxBodyBytes ?? MAX_BODY_BYTES
  const fits = (type: string, bytes: number) => fitsBody({ name: input.name, type, size: bytes }, bytes, max)
  const wire = (m: RawMedia): WireImage => ({ name: input.name, type: m.type, size: m.data.byteLength, data: toBase64(m.data) })
  const original: RawMedia = { type: input.type, data }

  if (data.byteLength > SEND_AS_IS_BYTES && options.scale) {
    for (const step of SCALE_STEPS) {
      const scaled = await options.scale(original, step).catch(() => undefined)
      if (!scaled) break // tidak bisa memperkecil sama sekali: pakai aslinya bila muat
      // gambar yang sudah kecil bisa membengkak saat dikodekan ulang; pilih yang lebih kecil
      const best = scaled.data.byteLength < data.byteLength ? scaled : original
      if (fits(best.type, best.data.byteLength)) return wire(best)
    }
  }
  return fits(original.type, data.byteLength) ? wire(original) : undefined
}

/**
 * Menyiapkan rekaman untuk kawat. Layar sudah mengodekannya sebagai WAV PCM mono 16 kHz (satu-satunya format yang diterima
 * Muse Voice; maksimal 60 dtk, jadi ±2,6 MB base64) dan tidak diubah lagi di sini; rekaman kosong atau terlalu besar ditolak
 * sebagai galat validasi. Parameter tipe dibuang agar backend menerima tipe polos; tipe kosong dianggap `audio/wav`.
 */
export function prepareAudio(input: AudioInput, maxBodyBytes: number = MAX_BODY_BYTES): WireAudio {
  const { data } = input
  if (!data || data.byteLength === 0) throw new ApiError('tidak_valid', msg('srv.api.noSpeech'), { field: 'file' })
  const type = input.type.split(';')[0]?.trim() || 'audio/wav'
  if (!fitsBody({ type, size: data.byteLength }, data.byteLength, maxBodyBytes)) {
    throw new ApiError('tidak_valid', msg('srv.api.audioTooLarge'), { field: 'file' })
  }
  return { type, size: data.byteLength, data: toBase64(data) }
}

/**
 * Pengecil gambar bawaan peramban: `createImageBitmap` (mengikuti orientasi EXIF) lalu canvas -> JPEG. `undefined` bila
 * lingkungan tidak punya keduanya (mis. Node di tes), sehingga foto dikirim apa adanya bila muat.
 */
export function browserImageScaler(): ImageScaler | undefined {
  if (typeof createImageBitmap !== 'function') return undefined
  if (typeof OffscreenCanvas === 'undefined' && typeof document === 'undefined') return undefined

  return async (image, { maxSide, quality }) => {
    const bitmap = await createImageBitmap(new Blob([image.data], { type: image.type }), { imageOrientation: 'from-image' })
    try {
      const ratio = Math.min(1, maxSide / Math.max(bitmap.width, bitmap.height))
      const width = Math.max(1, Math.round(bitmap.width * ratio))
      const height = Math.max(1, Math.round(bitmap.height * ratio))
      const blob = await encodeJpeg(bitmap, width, height, quality)
      return blob ? { type: 'image/jpeg', data: await blob.arrayBuffer() } : undefined
    } finally {
      bitmap.close()
    }
  }
}

async function encodeJpeg(bitmap: ImageBitmap, width: number, height: number, quality: number): Promise<Blob | null> {
  if (typeof OffscreenCanvas !== 'undefined') {
    const canvas = new OffscreenCanvas(width, height)
    const ctx = canvas.getContext('2d')
    if (!ctx) return null
    ctx.fillStyle = '#fff' // PNG transparan menjadi putih, bukan hitam
    ctx.fillRect(0, 0, width, height)
    ctx.drawImage(bitmap, 0, 0, width, height)
    return canvas.convertToBlob({ type: 'image/jpeg', quality })
  }
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const ctx = canvas.getContext('2d')
  if (!ctx) return null
  ctx.fillStyle = '#fff'
  ctx.fillRect(0, 0, width, height)
  ctx.drawImage(bitmap, 0, 0, width, height)
  return new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality))
}
