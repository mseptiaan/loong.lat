import { ApiError } from '@loong/domain'
import { msg } from '@loong/i18n'

/*
 * Backend suara (Muse Voice) hanya menerima WAV PCM mono 16 atau 24 kHz, sedangkan MediaRecorder menghasilkan webm/opus
 * atau mp4. Jadi setelah merekam: dekode, jadikan mono, turunkan ke 16 kHz, lalu kodekan PCM 16-bit little-endian dengan
 * header RIFF 44 byte. Semuanya fungsi murni di sini; pendekode (AudioContext) disuntikkan supaya bisa diuji tanpa peramban.
 */

export const WAV_SAMPLE_RATE = 16_000
/** Batas klip yang dikirim (detik): 16 kHz x 2 byte x 60 dtk = 1,9 MB, 2,6 MB sebagai base64, di bawah batas badan ~3 MB. */
export const MAX_WAV_SECONDS = 60
export const WAV_HEADER_BYTES = 44

/** Hasil dekode: satu larik sampel (-1..1) per kanal. */
export interface DecodedAudio {
  sampleRate: number
  channels: Float32Array[]
}

export type AudioDecoder = (data: ArrayBuffer) => Promise<DecodedAudio>

/** Rata-rata semua kanal per sampel; satu kanal dikembalikan apa adanya. Kanal dipotong ke yang terpendek. */
export function downmixToMono(channels: readonly Float32Array[]): Float32Array {
  if (channels.length === 0) return new Float32Array(0)
  if (channels.length === 1) return channels[0]!
  const length = Math.min(...channels.map((c) => c.length))
  const out = new Float32Array(length)
  for (const channel of channels) for (let i = 0; i < length; i++) out[i]! += channel[i]!
  for (let i = 0; i < length; i++) out[i]! /= channels.length
  return out
}

/**
 * Mengubah laju sampel. Menurunkan (48 kHz -> 16 kHz) merata-ratakan sampel sumber di tiap jendela keluaran, cukup sebagai
 * penyaring sederhana agar tidak terlalu berdesis; menaikkan (8 kHz -> 16 kHz) memakai interpolasi linear.
 * Panjang keluaran = panjang masukan x tujuan / asal, dibulatkan.
 */
export function resample(input: Float32Array, from: number, to: number): Float32Array {
  if (!(from > 0) || !(to > 0)) throw new RangeError(`invalid sample rate: ${from} -> ${to}`)
  if (from === to || input.length === 0) return input
  const length = Math.round((input.length * to) / from)
  const out = new Float32Array(length)
  const ratio = from / to
  const last = input.length - 1
  for (let i = 0; i < length; i++) {
    if (ratio > 1) {
      const start = Math.min(Math.floor(i * ratio), last)
      const end = Math.min(Math.max(Math.floor((i + 1) * ratio), start + 1), input.length)
      let sum = 0
      for (let j = start; j < end; j++) sum += input[j]!
      out[i] = sum / (end - start)
    } else {
      const pos = i * ratio
      const i0 = Math.min(Math.floor(pos), last)
      const frac = pos - i0
      out[i] = input[i0]! * (1 - frac) + input[Math.min(i0 + 1, last)]! * frac
    }
  }
  return out
}

/** Sampel float (-1..1) ke bilangan bulat 16-bit bertanda: dijepit, lalu -1 -> -32768 dan +1 -> 32767. NaN menjadi 0. */
export function toPcm16(sample: number): number {
  if (Number.isNaN(sample)) return 0
  const s = Math.max(-1, Math.min(1, sample))
  return Math.round(s < 0 ? s * 0x8000 : s * 0x7fff)
}

/** WAV PCM 16-bit little-endian mono: header RIFF 44 byte lalu sampel. */
export function encodeWav(samples: Float32Array, sampleRate: number): ArrayBuffer {
  const dataBytes = samples.length * 2
  const buffer = new ArrayBuffer(WAV_HEADER_BYTES + dataBytes)
  const view = new DataView(buffer)
  const ascii = (offset: number, text: string) => {
    for (let i = 0; i < text.length; i++) view.setUint8(offset + i, text.charCodeAt(i))
  }
  ascii(0, 'RIFF')
  view.setUint32(4, 36 + dataBytes, true) // ukuran sisa berkas
  ascii(8, 'WAVE')
  ascii(12, 'fmt ')
  view.setUint32(16, 16, true) // ukuran blok fmt untuk PCM
  view.setUint16(20, 1, true) // 1 = PCM
  view.setUint16(22, 1, true) // mono
  view.setUint32(24, sampleRate, true)
  view.setUint32(28, sampleRate * 2, true) // byte per detik
  view.setUint16(32, 2, true) // byte per sampel (blockAlign)
  view.setUint16(34, 16, true) // bit per sampel
  ascii(36, 'data')
  view.setUint32(40, dataBytes, true)
  for (let i = 0; i < samples.length; i++) view.setInt16(WAV_HEADER_BYTES + i * 2, toPcm16(samples[i]!), true)
  return buffer
}

/**
 * Rekaman peramban (webm/opus, mp4, ...) menjadi WAV 16 kHz mono. Klip lebih panjang dari `maxSeconds` dipotong. Menolak bila
 * rekaman tidak bisa didekode (galat pendekode diteruskan) atau tidak berisi sampel ("belum ada ucapan").
 */
export async function recordingToWav(data: ArrayBuffer, decode: AudioDecoder, maxSeconds: number = MAX_WAV_SECONDS): Promise<ArrayBuffer> {
  const audio = await decode(data)
  let pcm = resample(downmixToMono(audio.channels), audio.sampleRate, WAV_SAMPLE_RATE)
  const max = Math.floor(maxSeconds * WAV_SAMPLE_RATE)
  if (pcm.length > max) pcm = pcm.subarray(0, max)
  if (pcm.length === 0) throw new ApiError('tidak_valid', msg('srv.api.noSpeech'), { field: 'file' })
  return encodeWav(pcm, WAV_SAMPLE_RATE)
}

/* ---------- peramban ---------- */

type AudioContextCtor = new () => AudioContext

const audioContextCtor = (): AudioContextCtor | undefined =>
  typeof window === 'undefined' ? undefined : ((window as unknown as { AudioContext?: AudioContextCtor; webkitAudioContext?: AudioContextCtor }).AudioContext ?? (window as unknown as { webkitAudioContext?: AudioContextCtor }).webkitAudioContext)

/** Apakah peramban ini bisa mendekode rekaman (AudioContext). */
export const canDecodeAudio = (): boolean => audioContextCtor() !== undefined

/** Pendekode sungguhan: `AudioContext.decodeAudioData` (yang mengenali webm/opus, ogg, dan mp4 sesuai peramban). */
export const browserAudioDecoder: AudioDecoder = async (data) => {
  const Ctor = audioContextCtor()
  if (!Ctor) throw new Error('AudioContext tidak tersedia') // i18n-ignore: galat pemrogram (tidak tampil ke pengguna)
  const ctx = new Ctor()
  try {
    const buffer = await ctx.decodeAudioData(data)
    return { sampleRate: buffer.sampleRate, channels: Array.from({ length: buffer.numberOfChannels }, (_, i) => buffer.getChannelData(i)) }
  } finally {
    void ctx.close()
  }
}
