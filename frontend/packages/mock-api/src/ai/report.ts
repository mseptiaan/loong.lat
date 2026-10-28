import type { OptionSet, PlanOption } from '@loong/domain'
import { formatList, renderMsg, type Locale } from '@loong/i18n'
import { tAi } from '../i18n'

/** Kunci alasan opsi yang menandai batas waktu terlewat (srv.engine.reason.deadlineLate_*). */
const DEADLINE_LATE = 'srv.engine.reason.deadlineLate'

/**
 * Ringkasan opsi untuk pengirim, dirakit deterministik dari OptionSet (tiruan dari `ai.explainOptions`). Setiap angka di
 * dalamnya diambil dari angka opsi atau `method`; pencarian disebut "terbukti optimal" hanya bila `method.proven`.
 * Teks biasa (bukan Markdown) dalam bahasa permintaan; tiga paragraf pendek.
 */
export function buildOptionReport(set: OptionSet, locale: Locale): string {
  const t = tAi(locale)
  const label = (o: PlanOption) => renderMsg(o.label, locale)
  const main = set.options.find((o) => o.recommended) ?? set.options[0]!

  const intro = [
    t('ai.report.intro', { count: set.options.length, label: label(main), cost: main.totals.costRp, stops: main.totals.stops, distance: main.totals.distanceKm, duration: main.totals.durationMin }),
    main.savings.costRp > 0
      ? t('ai.report.saves', { baseCost: set.baseline.costRp, saving: main.savings.costRp, pct: main.savings.pct })
      : main.savings.costRp < 0
        ? t('ai.report.costsMore', { baseCost: set.baseline.costRp, extra: -main.savings.costRp })
        : t('ai.report.sameCost', { baseCost: set.baseline.costRp }),
  ]

  const compare: string[] = []
  if (set.options.length > 1) {
    const cheapest = set.options.reduce((a, b) => (b.totals.costRp < a.totals.costRp ? b : a))
    const fastest = set.options.reduce((a, b) => (b.totals.durationMin < a.totals.durationMin ? b : a))
    compare.push(
      cheapest.id === fastest.id
        ? t('ai.report.bestEverywhere')
        : t('ai.report.compare', { cheapLabel: label(cheapest), cheapCost: cheapest.totals.costRp, fastLabel: label(fastest), fastDuration: fastest.totals.durationMin }),
    )
  }
  const late = set.options.filter((o) => o.reasons.some((r) => r.key === DEADLINE_LATE))
  if (late.length) compare.push(t('ai.report.deadlineLate', { labels: formatList(late.map(label), locale) }))

  const how: string[] = []
  const m = set.method
  if (m) {
    how.push(t('ai.report.method', { count: m.trials }))
    if (m.polished) how.push(t('ai.report.methodPolished'))
    if (m.rounds) how.push(t('ai.report.methodRounds', { count: m.rounds }))
    how.push(m.proven ? t('ai.report.proven') : t('ai.report.notProven'))
  }

  return [intro, compare, how]
    .filter((p) => p.length > 0)
    .map((p) => p.join(' '))
    .join('\n\n')
}
