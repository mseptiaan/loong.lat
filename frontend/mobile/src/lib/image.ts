/** Foto bukti diperkecil di HP sebelum dikirim: data URL JPEG yang muat di batas server (900 ribu karakter). */
const MAX_EDGE = 720
const QUALITY_STEPS = [0.72, 0.55, 0.4]
const MAX_CHARS = 800_000

/** Ukuran akhir: sisi terpanjang `max`, rasio tetap, tidak pernah diperbesar. */
export function fitWithin(width: number, height: number, max: number): { width: number; height: number } {
  const scale = Math.min(1, max / Math.max(width, height))
  return { width: Math.max(1, Math.round(width * scale)), height: Math.max(1, Math.round(height * scale)) }
}

/** Mengecilkan berkas gambar menjadi data URL JPEG. Melempar galat bila berkas bukan gambar yang bisa dibaca. */
export async function compressPhoto(file: Blob): Promise<string> {
  const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' })
  try {
    const { width, height } = fitWithin(bitmap.width, bitmap.height, MAX_EDGE)
    const canvas = document.createElement('canvas')
    canvas.width = width
    canvas.height = height
    const ctx = canvas.getContext('2d')
    if (!ctx) throw new Error('canvas unavailable')
    ctx.drawImage(bitmap, 0, 0, width, height)
    let out = ''
    for (const q of QUALITY_STEPS) {
      out = canvas.toDataURL('image/jpeg', q)
      if (out.length <= MAX_CHARS) break
    }
    if (!out.startsWith('data:image/jpeg')) throw new Error('JPEG encoding failed')
    return out
  } finally {
    bitmap.close()
  }
}
