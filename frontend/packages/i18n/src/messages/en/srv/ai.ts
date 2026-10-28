/**
 * srv.ai.*: galat dari laporan AI atas opsi (`ai.explainOptions`) dan teks pemeriksaan foto bukti antar. Backend asli
 * mengirimnya sebagai kunci pesan; tiruan memakai kunci yang sama supaya layar tidak membedakan keduanya.
 *
 * `srv.ai.text` membungkus teks buatan model: parameter `text` berisi JSON `{"en": "...", "id": "..."}` dan pemformat
 * `localized` memilih bahasa layar (lihat `formatLocalized`). Pemeriksaan foto berjalan di latar belakang, jadi backend
 * tidak tahu bahasa pembacanya dan menyimpan keduanya.
 */
export const enSrvAi = {
  'srv.ai.report.noOptions': 'There are no options to explain yet.',
  'srv.ai.report.unverified': 'The AI summary could not be checked against the numbers, so it is not shown.',
  'srv.ai.text': '{text|localized}',
  'srv.ai.proof.noNote': 'No further detail.',
} as const
