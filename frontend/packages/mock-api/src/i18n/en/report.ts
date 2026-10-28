/** ai.report.*: ringkasan opsi (`ai.explainOptions`) yang dirakit tiruan dari angka OptionSet. Teks biasa, tanpa Markdown. */
export const enReport = {
  'ai.report.intro_one': 'There is one option, {label}: {cost|rupiah} for {stops} stops, {distance|km}, finishing in about {duration|duration}.',
  'ai.report.intro_other': 'There are {count} options. The recommended one is {label}: {cost|rupiah} for {stops} stops, {distance|km}, finishing in about {duration|duration}.',
  'ai.report.saves': 'Compared with sending each parcel separately ({baseCost|rupiah}), it saves {saving|rupiah} ({pct}%).',
  'ai.report.costsMore': 'It costs {extra|rupiah} more than sending each parcel separately ({baseCost|rupiah}).',
  'ai.report.sameCost': 'It costs the same as sending each parcel separately ({baseCost|rupiah}).',
  'ai.report.compare': 'The cheapest is {cheapLabel} at {cheapCost|rupiah}, and the fastest to finish is {fastLabel} at {fastDuration|duration}.',
  'ai.report.bestEverywhere': 'One option is both the cheapest and the fastest to finish.',
  'ai.report.deadlineLate': 'Heads up: {labels} would miss a delivery deadline.',
  'ai.report.method_one': 'These options were found in {count} attempt.',
  'ai.report.method_other': 'These options were found in {count} attempts.',
  'ai.report.methodPolished': 'The best routes were then polished.',
  'ai.report.methodRounds_one': '{count} review round checked them.',
  'ai.report.methodRounds_other': '{count} review rounds checked them.',
  'ai.report.proven': 'An exhaustive search confirmed they are optimal.',
  'ai.report.notProven': 'They are the best found, not proven optimal.',
} as const
