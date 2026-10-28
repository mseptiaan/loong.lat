import { describe, expect, it, vi } from 'vitest'
import { MAX_WAV_SECONDS, WAV_HEADER_BYTES, WAV_SAMPLE_RATE, downmixToMono, encodeWav, recordingToWav, resample, toPcm16, type AudioDecoder } from './wav'

const tag = (view: DataView, offset: number) => String.fromCharCode(...[0, 1, 2, 3].map((i) => view.getUint8(offset + i)))
const fill = (n: number, v: number) => new Float32Array(n).fill(v)
/** Pendekode palsu yang mengembalikan kanal yang diberikan. */
const decoding = (sampleRate: number, ...channels: Float32Array[]): AudioDecoder => async () => ({ sampleRate, channels })

describe('toPcm16', () => {
  it('memetakan -1..1 ke bilangan bulat 16-bit bertanda', () => {
    expect(toPcm16(0)).toBe(0)
    expect(toPcm16(1)).toBe(32767)
    expect(toPcm16(-1)).toBe(-32768)
    expect(toPcm16(0.5)).toBe(16384)
    expect(toPcm16(-0.5)).toBe(-16384)
  })

  it('menjepit nilai di luar jangkauan dan mengubah NaN menjadi 0', () => {
    expect(toPcm16(1.7)).toBe(32767)
    expect(toPcm16(-3)).toBe(-32768)
    expect(toPcm16(Infinity)).toBe(32767)
    expect(toPcm16(-Infinity)).toBe(-32768)
    expect(toPcm16(NaN)).toBe(0)
  })
})

describe('encodeWav', () => {
  const view = (samples: number[], rate = WAV_SAMPLE_RATE) => new DataView(encodeWav(Float32Array.from(samples), rate))

  it('header RIFF 44 byte: ukuran, PCM, mono, 16 kHz, 16-bit', () => {
    const v = view([0, 0.5, -0.5, 1])
    expect(v.byteLength).toBe(WAV_HEADER_BYTES + 8)
    expect(tag(v, 0)).toBe('RIFF')
    expect(v.getUint32(4, true)).toBe(36 + 8) // ukuran berkas - 8
    expect(tag(v, 8)).toBe('WAVE')
    expect(tag(v, 12)).toBe('fmt ')
    expect(v.getUint32(16, true)).toBe(16)
    expect(v.getUint16(20, true)).toBe(1) // PCM
    expect(v.getUint16(22, true)).toBe(1) // mono
    expect(v.getUint32(24, true)).toBe(16_000)
    expect(v.getUint32(28, true)).toBe(32_000) // byte per detik
    expect(v.getUint16(32, true)).toBe(2) // blockAlign
    expect(v.getUint16(34, true)).toBe(16)
    expect(tag(v, 36)).toBe('data')
    expect(v.getUint32(40, true)).toBe(8)
  })

  it('laju lain ikut ke header (byteRate = laju x 2)', () => {
    const v = view([0], 24_000)
    expect(v.getUint32(24, true)).toBe(24_000)
    expect(v.getUint32(28, true)).toBe(48_000)
  })

  it('sampel little-endian, bertanda, dijepit', () => {
    const v = view([0.5, -1, 2, -2, NaN])
    expect([0, 1, 2, 3, 4].map((i) => v.getInt16(WAV_HEADER_BYTES + i * 2, true))).toEqual([16384, -32768, 32767, -32768, 0])
    // 16384 = 0x4000: byte rendah dulu
    expect(v.getUint8(WAV_HEADER_BYTES)).toBe(0x00)
    expect(v.getUint8(WAV_HEADER_BYTES + 1)).toBe(0x40)
    // -32768 = 0x8000
    expect(v.getUint8(WAV_HEADER_BYTES + 2)).toBe(0x00)
    expect(v.getUint8(WAV_HEADER_BYTES + 3)).toBe(0x80)
  })

  it('tanpa sampel hanya header dengan data 0 byte', () => {
    const v = view([])
    expect(v.byteLength).toBe(44)
    expect(v.getUint32(4, true)).toBe(36)
    expect(v.getUint32(40, true)).toBe(0)
  })
})

describe('downmixToMono', () => {
  it('merata-ratakan kanal per sampel', () => {
    const out = downmixToMono([Float32Array.from([1, 0, -1]), Float32Array.from([0, 0.5, -1])])
    expect(Array.from(out)).toEqual([0.5, 0.25, -1])
  })

  it('satu kanal dikembalikan apa adanya, tanpa kanal menjadi kosong, kanal beda panjang dipotong', () => {
    const one = Float32Array.from([0.1, 0.2])
    expect(downmixToMono([one])).toBe(one)
    expect(downmixToMono([])).toHaveLength(0)
    expect(downmixToMono([fill(5, 1), fill(3, 0)])).toHaveLength(3)
  })

  it('tiga kanal dibagi tiga', () => {
    expect(Array.from(downmixToMono([fill(2, 0.3), fill(2, 0.6), fill(2, 0.9)])).map((x) => Number(x.toFixed(4)))).toEqual([0.6, 0.6])
  })
})

describe('resample', () => {
  it('panjang keluaran = panjang x tujuan / asal, dibulatkan', () => {
    expect(resample(fill(48_000, 0.1), 48_000, 16_000)).toHaveLength(16_000)
    expect(resample(fill(44_100, 0.1), 44_100, 16_000)).toHaveLength(16_000)
    expect(resample(fill(8_000, 0.1), 8_000, 16_000)).toHaveLength(16_000)
    expect(resample(fill(100, 0.1), 48_000, 16_000)).toHaveLength(33) // 33,33 dibulatkan
    expect(resample(fill(10, 0.1), 44_100, 16_000)).toHaveLength(4) // 3,63 dibulatkan
  })

  it('laju sama mengembalikan masukan yang sama; masukan kosong tetap kosong', () => {
    const x = fill(10, 0.2)
    expect(resample(x, 16_000, 16_000)).toBe(x)
    expect(resample(new Float32Array(0), 48_000, 16_000)).toHaveLength(0)
  })

  it('menurunkan merata-ratakan jendela sumber (sinyal tetap tetap, selang-seling menjadi 0)', () => {
    expect(Array.from(resample(fill(9, 0.5), 48_000, 16_000))).toEqual([0.5, 0.5, 0.5])
    const alternating = Float32Array.from({ length: 6 }, (_, i) => (i % 2 === 0 ? 1 : -1))
    expect(Array.from(resample(alternating, 48_000, 16_000))).toEqual([expect.closeTo(1 / 3, 5), expect.closeTo(-1 / 3, 5)])
    // jendela 3 sampel: (1 + -1 + 1) / 3 dan (-1 + 1 + -1) / 3
  })

  it('menaikkan memakai interpolasi linear', () => {
    expect(Array.from(resample(Float32Array.from([0, 1]), 8_000, 16_000))).toEqual([0, 0.5, 1, 1]) // sampel terakhir diulang
  })

  it('laju tidak valid ditolak', () => {
    expect(() => resample(fill(4, 0), 0, 16_000)).toThrow(RangeError)
    expect(() => resample(fill(4, 0), 48_000, -1)).toThrow(RangeError)
  })
})

describe('recordingToWav', () => {
  const sampleRateOf = (wav: ArrayBuffer) => new DataView(wav).getUint32(24, true)

  it('stereo 48 kHz selama 1 detik menjadi WAV mono 16 kHz dengan header yang konsisten', async () => {
    const wav = await recordingToWav(new ArrayBuffer(8), decoding(48_000, fill(48_000, 0.5), fill(48_000, -0.5)))
    const v = new DataView(wav)
    expect(wav.byteLength).toBe(WAV_HEADER_BYTES + 16_000 * 2)
    expect(tag(v, 0)).toBe('RIFF')
    expect(sampleRateOf(wav)).toBe(16_000)
    expect(v.getUint16(22, true)).toBe(1)
    expect(v.getUint32(40, true)).toBe(32_000)
    expect(v.getUint32(4, true)).toBe(wav.byteLength - 8)
    // dua kanal berlawanan saling meniadakan setelah downmix
    expect(v.getInt16(WAV_HEADER_BYTES + 100, true)).toBe(0)
  })

  it('byte rekaman diteruskan ke pendekode', async () => {
    const decode = vi.fn(decoding(16_000, fill(160, 0.1)))
    const input = new TextEncoder().encode('rekaman').buffer
    await recordingToWav(input, decode)
    expect(decode).toHaveBeenCalledWith(input)
  })

  it('16 kHz mono tidak diubah sampelnya (hanya dibungkus)', async () => {
    const wav = await recordingToWav(new ArrayBuffer(1), decoding(16_000, Float32Array.from([0.5, -0.5])))
    const v = new DataView(wav)
    expect(wav.byteLength).toBe(44 + 4)
    expect([v.getInt16(44, true), v.getInt16(46, true)]).toEqual([16384, -16384])
  })

  it('klip lebih panjang dari batas dipotong: 60 dtk = 1.920.044 byte, base64 di bawah batas badan 3 MB', async () => {
    const wav = await recordingToWav(new ArrayBuffer(1), decoding(16_000, fill(16_000 * (MAX_WAV_SECONDS + 10), 0.1)))
    expect(wav.byteLength).toBe(WAV_HEADER_BYTES + 16_000 * 2 * 60)
    expect(Math.ceil(wav.byteLength / 3) * 4).toBeLessThan(3 * 1024 * 1024)
    const short = await recordingToWav(new ArrayBuffer(1), decoding(16_000, fill(16_000 * 5, 0.1)), 2)
    expect(short.byteLength).toBe(WAV_HEADER_BYTES + 16_000 * 2 * 2) // batas bisa diganti
  })

  it('rekaman tanpa sampel atau tanpa kanal ditolak sebagai "belum ada ucapan"', async () => {
    await expect(recordingToWav(new ArrayBuffer(1), decoding(48_000, new Float32Array(0)))).rejects.toMatchObject({ code: 'tidak_valid', msg: { key: 'srv.api.noSpeech' } })
    await expect(recordingToWav(new ArrayBuffer(1), decoding(48_000))).rejects.toMatchObject({ code: 'tidak_valid' })
  })

  it('galat pendekode diteruskan (peramban tidak bisa mendekode rekaman ini)', async () => {
    const failing: AudioDecoder = () => Promise.reject(new Error('EncodingError'))
    await expect(recordingToWav(new ArrayBuffer(1), failing)).rejects.toThrow('EncodingError')
  })
})
