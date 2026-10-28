import { LOW_CONFIDENCE } from '@loong/domain'
import { Checkbox, Chip, Input, NumberStepper, cn } from '@loong/ui'
import { TriangleAlert } from 'lucide-react'
import { t } from '../../i18n'
import type { ReviewRow } from './rows'

/**
 * Hasil tebakan AI yang harus diperiksa manusia sebelum masuk ke pengiriman: tiap baris bisa dicentang, dinamai ulang,
 * dan diubah jumlahnya. Keyakinan rendah ditandai "periksa" dan tidak dicentang otomatis. `matched` menyorot potongan
 * ucapan yang melahirkan barang itu (suara).
 */
export function DetectedReview({ rows, onChange, matched }: { rows: ReviewRow[]; onChange: (rows: ReviewRow[]) => void; matched?: Record<number, string[]> }) {
  const set = (i: number, patch: Partial<ReviewRow>) => onChange(rows.map((r, k) => (k === i ? { ...r, ...patch } : r)))
  return (
    <ul className="space-y-3">
      {rows.map((r, i) => {
        const low = r.confidence < LOW_CONFIDENCE
        return (
          <li key={i} className={cn('rounded-card border p-3.5 transition-colors', low ? 'border-warning/40' : 'border-line', r.include ? 'bg-surface' : 'bg-sunken/60')}>
            <div className="flex items-start gap-3">
              <Checkbox checked={r.include} onCheckedChange={(include) => set(i, { include })} label={<span className="sr-only">{t('dash.ai.addRow', { name: r.name })}</span>} className="mt-2.5" />
              <div className="min-w-0 flex-1 space-y-2.5">
                <div className="flex items-start gap-3">
                  <Input aria-label={t('dash.fields.itemName')} value={r.name} onChange={(e) => set(i, { name: e.target.value })} className="min-w-0 flex-1" />
                  <NumberStepper label={t('dash.fields.qtyOf', { name: r.name })} value={r.qty} onChange={(qty) => set(i, { qty })} max={50} />
                </div>
                <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-small tabular-nums text-muted">
                  {t(r.fragile ? 'dash.ai.rowSpecFragile' : 'dash.ai.rowSpec', { kg: r.weightKg, l: r.lengthCm, w: r.widthCm, h: r.heightCm })}
                  <Chip className={cn(low && 'bg-warning-tint text-warning-fg')} icon={low ? <TriangleAlert className="size-3" aria-hidden /> : undefined}>
                    {low ? t('dash.ai.confidenceCheck', { pct: Math.round(r.confidence * 100) }) : t('dash.ai.confidence', { pct: Math.round(r.confidence * 100) })}
                  </Chip>
                </p>
                {matched?.[i] && (
                  <p className="text-small text-muted">
                    {t('dash.ai.fromSpeech')}{' '}
                    {matched[i]!.map((piece, k) => (
                      <mark key={k} className="mr-1 rounded-[4px] bg-accent-tint px-1 text-fg">
                        “{piece}”
                      </mark>
                    ))}
                  </p>
                )}
              </div>
            </div>
          </li>
        )
      })}
    </ul>
  )
}
