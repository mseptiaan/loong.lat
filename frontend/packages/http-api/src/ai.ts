import { ApiError, type AudioInput, type ImageInput } from '@loong/domain'
import type { LocalMethod } from './local'
import { prepareAudio, prepareImage, type ImageScaler } from './media'
import { LOCAL_FALLBACK, toBody } from './wire'

/**
 * Galat "layanan mati": jaringan putus, atau backend/gateway AI jatuh atau belum dikonfigurasi. Hanya ini yang membuat
 * klien beralih ke mesin lokal. Penolakan (`ditolak`: dimatikan admin, belum masuk), galat validasi, dan konflik adalah
 * jawaban yang sah dari backend: diteruskan apa adanya, tidak diulang di klien.
 */
export const isOutage = (err: unknown): boolean => err instanceof ApiError && (err.code === 'jaringan' || err.code === 'layanan')

export interface AiDeps {
  /** Panggilan biasa ke backend (menunggu API siap). */
  call(name: string, args: unknown[]): Promise<unknown>
  stream(name: string, body: Record<string, unknown>): AsyncIterable<unknown>
  /** Mesin deterministik di klien (local.ts). */
  local: Readonly<Record<string, LocalMethod>>
  scale?: ImageScaler
  maxBodyBytes?: number
}

type Route = (...args: never[]) => unknown

/**
 * Jalur khusus metode AI yang tidak cukup dengan "teruskan ke backend": biner dikodekan base64 (foto diperkecil lebih dulu),
 * dan metode yang punya cadangan jatuh ke mesin lokal saat backend mati. Kuncinya nama metode ApiClient.
 */
export function createAiRoutes(deps: AiDeps): Record<string, Route> {
  const { call, stream, local } = deps
  const localFn = (name: string) => local[name] as unknown as (...args: unknown[]) => unknown

  /** Backend dulu; jatuh ke mesin lokal hanya saat layanan mati. */
  async function withFallback(name: string, args: unknown[]): Promise<unknown> {
    try {
      return await call(name, args)
    } catch (err) {
      if (!LOCAL_FALLBACK.has(name) || !isOutage(err)) throw err
      return localFn(name)(...args)
    }
  }

  /**
   * Foto ke backend (diperkecil, base64). Tanpa data yang bisa dikirim, atau saat layanan mati (juga di tengah aliran),
   * wizard dilanjutkan oleh mesin lokal, yang mengulang tahapannya dari awal.
   */
  async function* readImage(input: ImageInput): AsyncGenerator<unknown> {
    const wire = await prepareImage(input, { scale: deps.scale, maxBodyBytes: deps.maxBodyBytes })
    if (wire) {
      try {
        yield* stream('ai.readImage', toBody('ai.readImage', [wire]))
        return
      } catch (err) {
        if (!LOCAL_FALLBACK.has('ai.readImage') || !isOutage(err)) throw err
      }
    }
    yield* localFn('ai.readImage')(input) as AsyncIterable<unknown>
  }

  return {
    'ai.readImage': readImage,
    'ai.parseSpeech': ((transcript: string) => withFallback('ai.parseSpeech', [transcript])) as Route,
    'ai.parseIntent': ((text: string) => withFallback('ai.parseIntent', [text])) as Route,
    // tanpa cadangan: tiruan tidak bisa mentranskripsi; layar jatuh ke Web Speech API peramban atau ketik
    // async: rekaman kosong atau terlalu besar menolak (reject) alih-alih melempar secara sinkron
    'ai.transcribe': (async (input: AudioInput) => call('ai.transcribe', [prepareAudio(input, deps.maxBodyBytes)])) as Route,
  }
}
