import type { Translation } from '@loong/i18n'
import type { enReport } from '../en/report'

export const idReport: Translation<typeof enReport> = {
  'ai.report.intro_other': 'Ada {count} opsi. Yang direkomendasikan adalah {label}: {cost|rupiah} untuk {stops} stop, {distance|km}, selesai dalam sekitar {duration|duration}.',
  'ai.report.intro_one': 'Ada satu opsi, {label}: {cost|rupiah} untuk {stops} stop, {distance|km}, selesai dalam sekitar {duration|duration}.',
  'ai.report.saves': 'Dibanding kirim terpisah ({baseCost|rupiah}), hemat {saving|rupiah} ({pct}%).',
  'ai.report.costsMore': 'Biayanya {extra|rupiah} lebih mahal daripada kirim terpisah ({baseCost|rupiah}).',
  'ai.report.sameCost': 'Biayanya sama dengan kirim terpisah ({baseCost|rupiah}).',
  'ai.report.compare': 'Yang paling murah adalah {cheapLabel} seharga {cheapCost|rupiah}, dan yang paling cepat selesai adalah {fastLabel} dalam {fastDuration|duration}.',
  'ai.report.bestEverywhere': 'Satu opsi sekaligus yang paling murah dan paling cepat selesai.',
  'ai.report.deadlineLate': 'Perhatian: {labels} akan melewati batas waktu pengiriman.',
  'ai.report.method_other': 'Opsi ini ditemukan dalam {count} percobaan.',
  'ai.report.methodPolished': 'Rute terbaik lalu dipoles.',
  'ai.report.methodRounds_other': '{count} ronde tinjauan memeriksanya.',
  'ai.report.proven': 'Pencarian menyeluruh memastikan hasilnya optimal.',
  'ai.report.notProven': 'Ini yang terbaik yang ditemukan, belum terbukti optimal.',
}
