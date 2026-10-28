import { describe, expect, it, vi } from 'vitest'
import { MAX_BODY_BYTES, SCALE_STEPS, SEND_AS_IS_BYTES, prepareAudio, prepareImage, toBase64, type ImageScaler, type RawMedia } from './media'

const bytes = (n: number, fill = 65): ArrayBuffer => new Uint8Array(n).fill(fill).buffer
const photo = (n: number, type = 'image/jpeg') => ({ name: 'foto.jpg', type, size: n, data: bytes(n) })
/** Pengecil palsu: selalu menghasilkan JPEG sebesar `size` byte, dan mencatat langkah yang diminta. */
const scaler = (size: number | ((call: number) => number)) => {
  let calls = 0
  return vi.fn<ImageScaler>(async () => ({ type: 'image/jpeg', data: bytes(typeof size === 'number' ? size : size(++calls)) }))
}

describe('toBase64', () => {
  it('sama dengan base64 standar, juga untuk data yang lebih besar dari satu potongan', () => {
    expect(toBase64(new TextEncoder().encode('loong').buffer)).toBe('bG9vbmc=')
    expect(toBase64(new Uint8Array([0, 255, 128]))).toBe('AP+A')
    const big = new Uint8Array(0x8000 * 3 + 7).map((_, i) => i % 251)
    expect(toBase64(big.buffer)).toBe(btoa(Array.from(big, (b) => String.fromCharCode(b)).join('')))
  })
})

describe('prepareImage', () => {
  it('foto kecil dikirim apa adanya tanpa dibuka: nama, tipe, ukuran sebenarnya, dan base64', async () => {
    const scale = scaler(10)
    const wire = await prepareImage(photo(1000), { scale })
    expect(wire).toEqual({ name: 'foto.jpg', type: 'image/jpeg', size: 1000, data: btoa('A'.repeat(1000)) })
    expect(scale).not.toHaveBeenCalled()
  })

  it('foto besar diperkecil ke JPEG sisi 1280 px mutu 0,85; size dan type mengikuti hasilnya', async () => {
    const scale = scaler(200_000)
    const wire = await prepareImage(photo(2_000_000, 'image/png'), { scale })
    expect(scale).toHaveBeenCalledTimes(1)
    const [image, step] = scale.mock.calls[0]!
    expect(image.type).toBe('image/png')
    expect(image.data.byteLength).toBe(2_000_000)
    expect(step).toEqual({ maxSide: 1280, quality: 0.85 })
    expect(wire).toMatchObject({ name: 'foto.jpg', type: 'image/jpeg', size: 200_000 })
    expect(atob(wire!.data)).toHaveLength(200_000)
  })

  it('turun ke langkah yang lebih kecil bila badan masih di atas batas, dan berhenti begitu muat', async () => {
    // langkah 1 masih 2,5 MB (base64 > 3 MB), langkah 2 muat
    const scale = scaler((call) => (call === 1 ? 2_500_000 : 400_000))
    const wire = await prepareImage(photo(6_000_000), { scale })
    expect(scale.mock.calls.map(([, step]) => step)).toEqual([SCALE_STEPS[0], SCALE_STEPS[1]])
    expect(wire?.size).toBe(400_000)
  })

  it('tidak muat di semua langkah: tidak dikirim (pemanggil memakai mesin lokal)', async () => {
    const scale = scaler(2_900_000)
    expect(await prepareImage(photo(6_000_000), { scale })).toBeUndefined()
    expect(scale).toHaveBeenCalledTimes(SCALE_STEPS.length)
  })

  it('badan dibatasi ~3 MB termasuk pembengkakan base64: 2,4 MB biner sudah terlalu besar, 2,2 MB muat', async () => {
    expect(await prepareImage(photo(2_400_000))).toBeUndefined()
    const ok = await prepareImage(photo(2_200_000))
    expect(ok).toBeDefined()
    expect(JSON.stringify({ input: ok }).length).toBeLessThanOrEqual(MAX_BODY_BYTES)
  })

  it('tanpa pengecil: foto yang muat dikirim apa adanya, yang tidak muat ditolak', async () => {
    expect(await prepareImage(photo(SEND_AS_IS_BYTES + 1))).toMatchObject({ size: SEND_AS_IS_BYTES + 1 })
    expect(await prepareImage(photo(5_000_000))).toBeUndefined()
  })

  it('pengecil yang gagal atau tidak bisa membuka berkas diperlakukan sebagai tidak ada', async () => {
    const broken = vi.fn<ImageScaler>(async () => {
      throw new Error('decode gagal')
    })
    expect(await prepareImage(photo(1_000_000), { scale: broken })).toMatchObject({ size: 1_000_000 }) // muat: dikirim apa adanya
    expect(await prepareImage(photo(5_000_000), { scale: broken })).toBeUndefined() // tidak muat
    expect(broken).toHaveBeenCalledTimes(2) // berhenti di langkah pertama, tidak mencoba semua langkah
    const none = vi.fn<ImageScaler>(async () => undefined)
    expect(await prepareImage(photo(1_000_000), { scale: none })).toMatchObject({ size: 1_000_000 })
  })

  it('hasil pengecilan yang malah lebih besar dari aslinya dibuang', async () => {
    const wire = await prepareImage(photo(400_000), { scale: scaler(900_000) })
    expect(wire?.size).toBe(400_000)
    expect(wire?.type).toBe('image/jpeg') // tipe asli
  })

  it('tanpa data biner (atau kosong): tidak ada yang bisa dikirim', async () => {
    expect(await prepareImage({ name: 'a.jpg', type: 'image/jpeg', size: 1000 })).toBeUndefined()
    expect(await prepareImage({ name: 'a.jpg', type: 'image/jpeg', size: 0, data: new ArrayBuffer(0) })).toBeUndefined()
  })

  it('batas badan bisa diganti', async () => {
    const small = await prepareImage(photo(5000), { maxBodyBytes: 4000 })
    expect(small).toBeUndefined()
    const fits: RawMedia = { type: 'image/jpeg', data: bytes(2000) }
    expect(await prepareImage({ name: 'a.jpg', type: fits.type, size: 2000, data: fits.data }, { maxBodyBytes: 4000 })).toMatchObject({ size: 2000 })
  })
})

describe('prepareAudio', () => {
  it('rekaman WAV dikirim sebagai base64 dengan tipe audio/wav', () => {
    const data = new TextEncoder().encode('suara').buffer
    expect(prepareAudio({ type: 'audio/wav', size: 5, data })).toEqual({ type: 'audio/wav', size: 5, data: 'c3VhcmE=' })
  })

  it('parameter tipe dibuang agar backend menerima tipe polos', () => {
    expect(prepareAudio({ type: 'audio/wav;rate=16000', size: 10, data: bytes(10) }).type).toBe('audio/wav')
  })

  it('size mengikuti byte yang dikirim, bukan klaim pemanggil; tipe kosong menjadi audio/wav', () => {
    expect(prepareAudio({ type: '', size: 999, data: bytes(10) })).toMatchObject({ type: 'audio/wav', size: 10 })
  })

  it('rekaman kosong atau tanpa data ditolak sebagai galat validasi', () => {
    for (const input of [{ type: 'audio/wav', size: 0 }, { type: 'audio/wav', size: 0, data: new ArrayBuffer(0) }]) {
      expect(() => prepareAudio(input)).toThrow(expect.objectContaining({ code: 'tidak_valid', msg: { key: 'srv.api.noSpeech' } }))
    }
  })

  it('rekaman terlalu besar ditolak dengan pesan yang menyuruh merekam lebih singkat', () => {
    expect(() => prepareAudio({ type: 'audio/wav', size: 3_000_000, data: bytes(3_000_000) })).toThrow(
      expect.objectContaining({ code: 'tidak_valid', field: 'file', msg: { key: 'srv.api.audioTooLarge' } }),
    )
    // satu menit WAV 16 kHz mono (1.920.044 byte) muat di bawah batas badan ~3 MB
    expect(prepareAudio({ type: 'audio/wav', size: 1_920_044, data: bytes(1_920_044) }).size).toBe(1_920_044)
  })
})
