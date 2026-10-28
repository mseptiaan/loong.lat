import { ApiError, type DemoState } from '@loong/domain'
import { getLocale, msg } from '@loong/i18n'
import { MockServer, createApi as createMockApi, memoryRawStorage } from '@loong/mock-api'
import { UNAVAILABLE_KEY } from './wire'

/** Jawaban dari klien sendiri untuk metode yang tidak dilayani backend. */
export type LocalMethod = (...args: never[]) => unknown

export const unavailable = () => new ApiError('tidak_ditemukan', msg(UNAVAILABLE_KEY))

/**
 * Yang dijawab klien sendiri, tanpa data backend:
 *  - `places.*` murni (gazetteer), dan `demo.state` (jam nyata: layar menurunkan ETA dan posisi dari jam ini);
 *  - `ai.readImage/parseSpeech/parseIntent` adalah mesin deterministik (aturan dan katalog barang), dipakai sebagai
 *    CADANGAN saat backend AI mati (lihat `LOCAL_FALLBACK` di wire.ts dan ai.ts). Semuanya memakai server tiruan di
 *    memori yang tidak menyentuh penyimpanan apa pun dan tidak dibagi dengan siapa pun.
 * Selebihnya (notifikasi, akun, pengguna, keuangan, pengaturan, Copilot, wawasan) dilayani backend; tidak ada data palsu di sini.
 */
export function createLocal(): Record<string, LocalMethod> {
  const server = new MockServer({ storage: memoryRawStorage(), latency: [0, 0] })
  const fakeCustomer = () => {
    const u = server.db().users[0]
    return { role: 'customer' as const, userId: u?.id ?? 'local', name: u?.name ?? 'Local' }
  }
  // dibuat per panggilan agar bahasa saat ini yang dipakai
  const mock = () => createMockApi(server, fakeCustomer(), getLocale())

  return {
    'places.search': ((query: string, limit?: number) => mock().places.search(query, limit)) as LocalMethod,
    'places.describe': ((position: Parameters<ReturnType<typeof mock>['places']['describe']>[0]) => mock().places.describe(position)) as LocalMethod,

    'ai.readImage': ((input: Parameters<ReturnType<typeof mock>['ai']['readImage']>[0]) => mock().ai.readImage(input)) as LocalMethod,
    'ai.parseSpeech': ((transcript: string) => mock().ai.parseSpeech(transcript)) as LocalMethod,
    'ai.parseIntent': ((text: string) => mock().ai.parseIntent(text)) as LocalMethod,

    // waktu nyata: layar menurunkan ETA dan posisi dari jam ini, dan backend memberi waktu nyata
    'demo.state': (() => {
      const now = Date.now()
      const state: DemoState = {
        clock: { speed: 1, baseReal: now, baseVirtual: now },
        now,
        fault: 'none',
        autopilot: false,
        seededAt: new Date(now).toISOString(),
        scenario: 'live',
      }
      return Promise.resolve(state)
    }) as LocalMethod,
  }
}
