import { afterEach, describe, expect, it, vi } from 'vitest'
import { ApiError, type ApiClient } from '@loong/domain'
import type { AudioDecoder } from './wav'
import { MAX_RECORD_MS, captureErrorKind, pickCaptureMode, pickRecorderMime, startRecording, transcribeRecording, type RecorderLike, type RecordingDeps } from './voice'

afterEach(() => vi.useRealTimers())

/** Perekam palsu: `stop()` mengeluarkan satu potongan data lalu `onstop`, seperti MediaRecorder sungguhan. */
class FakeRecorder implements RecorderLike {
  state = 'inactive'
  mimeType: string
  ondataavailable: ((e: { data: Blob }) => void) | null = null
  onstop: (() => void) | null = null
  onerror: ((e: unknown) => void) | null = null
  constructor(readonly options: { mimeType?: string }) {
    this.mimeType = options.mimeType ?? ''
  }
  start() {
    this.state = 'recording'
  }
  stop() {
    if (this.state === 'inactive') return
    this.state = 'inactive'
    this.ondataavailable?.({ data: new Blob(['suara']) })
    this.onstop?.()
  }
}

function fakeDeps(over: Partial<RecordingDeps> = {}) {
  const track = { stop: vi.fn() }
  const recorders: FakeRecorder[] = []
  const deps: RecordingDeps = {
    getUserMedia: vi.fn(async () => ({ getTracks: () => [track] })),
    createRecorder: (_s, o) => {
      const r = new FakeRecorder(o)
      recorders.push(r)
      return r
    },
    isTypeSupported: (m) => m === 'audio/webm',
    ...over,
  }
  return { deps, track, recorders }
}

describe('pickRecorderMime', () => {
  it('memilih format pertama yang didukung, atau undefined', () => {
    expect(pickRecorderMime(() => true)).toBe('audio/webm;codecs=opus')
    expect(pickRecorderMime((m) => m === 'audio/mp4')).toBe('audio/mp4')
    expect(pickRecorderMime(() => false)).toBeUndefined()
  })
})

describe('pickCaptureMode', () => {
  const base = { http: true, voiceOn: true, canRecord: true, canRecognize: true }
  it('merekam hanya di mode http dengan saklar suara menyala dan perekam tersedia', () => {
    expect(pickCaptureMode(base)).toBe('record')
    expect(pickCaptureMode({ ...base, http: false })).toBe('recognize') // tiruan: Web Speech API
    expect(pickCaptureMode({ ...base, voiceOn: false })).toBe('recognize')
    expect(pickCaptureMode({ ...base, canRecord: false })).toBe('recognize')
  })

  it('perekaman atau transkripsi gagal: turun ke Web Speech API, lalu ke mengetik', () => {
    expect(pickCaptureMode({ ...base, recordFailed: true })).toBe('recognize')
    expect(pickCaptureMode({ ...base, recordFailed: true, canRecognize: false })).toBe('type')
    expect(pickCaptureMode({ http: false, voiceOn: true, canRecord: true, canRecognize: false })).toBe('type')
  })
})

describe('startRecording', () => {
  it('merekam dengan format yang didukung; stop mengembalikan rekaman dan melepas mikrofon', async () => {
    const { deps, track, recorders } = fakeDeps()
    const rec = await startRecording(deps)
    expect(recorders[0]?.options).toEqual({ mimeType: 'audio/webm' })
    expect(recorders[0]?.state).toBe('recording')
    expect(track.stop).not.toHaveBeenCalled()

    const blob = await rec.stop()
    expect(blob.type).toBe('audio/webm')
    expect(await blob.text()).toBe('suara')
    expect(track.stop).toHaveBeenCalledOnce()
  })

  it('izin mikrofon ditolak: menolak dan tidak ada perekam yang dibuat', async () => {
    const denied = Object.assign(new Error('x'), { name: 'NotAllowedError' })
    const { deps, recorders } = fakeDeps({ getUserMedia: vi.fn(async () => Promise.reject(denied)) })
    await expect(startRecording(deps)).rejects.toBe(denied)
    expect(recorders).toHaveLength(0)
  })

  it('perekam tidak bisa dibuat: mikrofon dilepas lagi', async () => {
    const { deps, track } = fakeDeps({
      createRecorder: () => {
        throw new Error('format tidak didukung')
      },
    })
    await expect(startRecording(deps)).rejects.toThrow('format tidak didukung')
    expect(track.stop).toHaveBeenCalledOnce()
  })

  it('batas durasi menghentikan perekam, memberi tahu pemanggil, dan rekaman tetap bisa diambil', async () => {
    vi.useFakeTimers()
    const onLimit = vi.fn()
    const { deps, recorders } = fakeDeps({ onLimit })
    const rec = await startRecording(deps)
    await vi.advanceTimersByTimeAsync(MAX_RECORD_MS - 1)
    expect(onLimit).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(1)
    expect(onLimit).toHaveBeenCalledOnce()
    expect(recorders[0]?.state).toBe('inactive')
    expect(await (await rec.stop()).text()).toBe('suara')
  })

  it('stop sebelum batas membatalkan penghitung waktu', async () => {
    vi.useFakeTimers()
    const onLimit = vi.fn()
    const { deps } = fakeDeps({ onLimit, maxMs: 1000 })
    const rec = await startRecording(deps)
    await rec.stop()
    await vi.advanceTimersByTimeAsync(5000)
    expect(onLimit).not.toHaveBeenCalled()
  })

  it('cancel membuang rekaman dan melepas mikrofon tanpa mengeluarkan data', async () => {
    const { deps, track, recorders } = fakeDeps()
    const rec = await startRecording(deps)
    rec.cancel()
    expect(recorders[0]?.state).toBe('inactive')
    expect(track.stop).toHaveBeenCalledOnce()
  })

  it('galat perekam membuat stop menolak dan melepas mikrofon', async () => {
    const { deps, track, recorders } = fakeDeps()
    const rec = await startRecording(deps)
    recorders[0]!.onerror?.(new Error('encoder'))
    await expect(rec.stop()).rejects.toThrow('encoder')
    expect(track.stop).toHaveBeenCalled()
  })
})

describe('transcribeRecording', () => {
  const ai = (transcribe: ApiClient['ai']['transcribe']) => ({ transcribe })
  /** Pendekode palsu: 0,5 dtk nada 48 kHz stereo, tanpa peramban. */
  const decoder = (): AudioDecoder => async () => ({ sampleRate: 48_000, channels: [new Float32Array(24_000).fill(0.5), new Float32Array(24_000).fill(0.5)] })

  it('mengodekan rekaman sebagai WAV 16 kHz mono (audio/wav) sebelum dikirim; hasilnya dipangkas', async () => {
    const transcribe = vi.fn<ApiClient['ai']['transcribe']>(async () => ({ transcript: '  dua kardus sedang  ' }))
    const decode = vi.fn(decoder())
    const text = await transcribeRecording(ai(transcribe), new Blob(['webm-opus'], { type: 'audio/webm' }), decode)
    expect(text).toBe('dua kardus sedang')
    expect(decode).toHaveBeenCalledOnce()
    expect(new TextDecoder().decode(decode.mock.calls[0]![0])).toBe('webm-opus') // pendekode menerima byte rekaman asli
    const [input] = transcribe.mock.calls[0]!
    // 0,5 dtk x 16 kHz = 8000 sampel x 2 byte + header 44
    expect(input).toMatchObject({ type: 'audio/wav', size: 16_044 })
    const view = new DataView(input.data!)
    expect(input.data!.byteLength).toBe(16_044)
    expect(String.fromCharCode(view.getUint8(0), view.getUint8(1), view.getUint8(2), view.getUint8(3))).toBe('RIFF')
    expect(view.getUint32(24, true)).toBe(16_000)
    expect(view.getUint16(22, true)).toBe(1)
  })

  it('rekaman yang tidak bisa didekode menolak dan tidak pernah sampai ke backend', async () => {
    const transcribe = vi.fn<ApiClient['ai']['transcribe']>()
    const failing: AudioDecoder = () => Promise.reject(Object.assign(new Error('format tidak dikenal'), { name: 'EncodingError' }))
    const err = await transcribeRecording(ai(transcribe), new Blob(['x']), failing).catch((e: unknown) => e)
    expect(err).toMatchObject({ name: 'EncodingError' })
    expect(captureErrorKind(err)).toBe('other') // layar: pesan "tidak bisa diubah menjadi teks", lalu Web Speech API atau ketik
    expect(transcribe).not.toHaveBeenCalled()
  })

  it('transkrip kosong ditolak sebagai "belum ada ucapan"', async () => {
    await expect(transcribeRecording(ai(async () => ({ transcript: '   ' })), new Blob(['x']), decoder())).rejects.toMatchObject({ code: 'tidak_valid', msg: { key: 'srv.api.noSpeech' } })
  })

  it('galat backend diteruskan apa adanya', async () => {
    const down = new ApiError('layanan', 'mati')
    await expect(transcribeRecording(ai(() => Promise.reject(down)), new Blob(['x']), decoder())).rejects.toBe(down)
  })
})

describe('captureErrorKind', () => {
  it('menggolongkan galat perekaman untuk memilih pesan', () => {
    expect(captureErrorKind(Object.assign(new Error('x'), { name: 'NotAllowedError' }))).toBe('denied')
    expect(captureErrorKind(Object.assign(new Error('x'), { name: 'NotFoundError' }))).toBe('noMic')
    expect(captureErrorKind(new ApiError('ditolak', 'x'))).toBe('api')
    expect(captureErrorKind(new Error('encoder'))).toBe('other')
    expect(captureErrorKind('aneh')).toBe('other')
  })
})
