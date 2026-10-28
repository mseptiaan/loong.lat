import { ITEM_CATALOG, LOW_CONFIDENCE, catalogName, type DetectedItem, type Item, type ItemSource } from '@loong/domain'
import { t } from '../../i18n'
import { uid } from '../../pages/wizard/model'

export interface ReviewRow extends DetectedItem {
  /** Ikut ditambahkan ke pengiriman? Barang berkeyakinan rendah mulai tidak dicentang sampai diperiksa. */
  include: boolean
}

export const toRows = (items: DetectedItem[]): ReviewRow[] => items.map((i) => ({ ...i, include: i.confidence >= LOW_CONFIDENCE }))

export const toItems = (rows: ReviewRow[], source: ItemSource): Item[] =>
  rows
    .filter((r) => r.include)
    .map((r) => ({
      id: uid('itm'),
      name: r.name.trim() || t('dash.ai.itemFallback'),
      // nama bawaan katalog yang tidak diubah pengguna tetap mengikuti bahasa tampilan
      ...(ITEM_CATALOG.some((c) => c.key === r.key) && r.name.trim() === catalogName(r.key) ? { catalogKey: r.key } : {}),
      qty: r.qty,
      weightKg: r.weightKg,
      lengthCm: r.lengthCm,
      widthCm: r.widthCm,
      heightCm: r.heightCm,
      fragile: r.fragile,
      source,
      confidence: r.confidence,
    }))
