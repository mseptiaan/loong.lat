import type { ProofAnalysis, ProofCheck } from '@loong/domain'
import { msg } from '@loong/i18n'
import { hash32 } from './vision'

/** Nama model yang dicatat di ringkasan; backend asli mengisi model yang benar-benar dipakai. */
export const PROOF_MODEL = 'muse-spark-1.3 (mock)'

/** Batas ukuran data URL foto bukti (karakter), jauh di atas hasil kompresi Driver (±60-120 KB). */
export const MAX_PROOF_PHOTO_CHARS = 900_000

export const isProofPhoto = (photo: unknown): photo is string =>
  typeof photo === 'string' && photo.length <= MAX_PROOF_PHOTO_CHARS && /^data:image\/(?:jpeg|png|webp);base64,[A-Za-z0-9+/]+=*$/.test(photo)

type Scenario = 'seated' | 'holding' | 'atDoor'

/** Kata di nama berkas yang menentukan isi foto (sama seperti `detectItems` untuk baca barang). */
const NAME_HINTS: [RegExp, Scenario][] = [
  [/contoh|sample|penerima|recipient|orang|person|selfie/i, 'seated'],
  [/pintu|door|depan|front|titip|lobby|satpam|guard|teras/i, 'atDoor'],
  [/paket|parcel|package|kardus|box|serah|handover/i, 'holding'],
]

const check = (key: ProofCheck['key'], result: ProofCheck['result'], note: ProofCheck['note']): ProofCheck => ({ key, result, note })

function scenarioFor(name: string, size: number): Scenario {
  const hinted = NAME_HINTS.find(([re]) => re.test(name))?.[1]
  if (hinted) return hinted
  return hash32(`${name}|${size}`) % 2 === 0 ? 'holding' : 'atDoor'
}

/**
 * "AI merangkum foto bukti" yang deterministik per berkas. Tidak membaca piksel: pilihan skenario mengikuti kata di nama
 * berkas, atau hash bila tidak ada petunjuk. Hasilnya berbentuk keluaran terstruktur yang akan dihasilkan model sungguhan
 * (Muse Spark, input gambar + skema JSON). Teks disimpan sebagai `Msg` supaya mengikuti bahasa layar saat dibaca.
 */
export function analyzeProofPhoto(input: { name: string; size: number }, nowMs: number): ProofAnalysis {
  const scenario = scenarioFor(input.name, input.size)
  const quality = check('quality', 'ok', msg('srv.proof.quality.good'))
  const base = { model: PROOF_MODEL, analyzedAt: new Date(nowMs).toISOString() }
  switch (scenario) {
    case 'seated':
      return {
        ...base,
        summary: msg('srv.proof.summary.seated'),
        checks: [check('recipient', 'ok', msg('srv.proof.recipient.visible')), check('parcel', 'unclear', msg('srv.proof.parcel.unclearItem')), quality],
        confidence: 0.78,
      }
    case 'holding':
      return {
        ...base,
        summary: msg('srv.proof.summary.holding'),
        checks: [check('recipient', 'ok', msg('srv.proof.recipient.visible')), check('parcel', 'ok', msg('srv.proof.parcel.held')), quality],
        confidence: 0.93,
      }
    case 'atDoor':
      return {
        ...base,
        summary: msg('srv.proof.summary.atDoor'),
        checks: [check('recipient', 'missing', msg('srv.proof.recipient.none')), check('parcel', 'ok', msg('srv.proof.parcel.atDoor')), quality],
        confidence: 0.88,
      }
  }
}
