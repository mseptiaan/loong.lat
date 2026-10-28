import { getLocale, translate, type Locale } from '@loong/i18n'
import type { Item, ItemSource } from './types'

export interface CatalogItem {
  key: string
  weightKg: number
  lengthCm: number
  widthCm: number
  heightCm: number
  fragile: boolean
}

/**
 * Katalog barang umum: preset cepat di wizard ("Tambah cepat") dan bahan hasil tiruan AI baca-gambar/suara,
 * sehingga hasil "AI" selalu masuk akal dan bisa dihitung engine.
 */
export const ITEM_CATALOG: readonly CatalogItem[] = [
  { key: 'dokumen', weightKg: 0.3, lengthCm: 32, widthCm: 24, heightCm: 2, fragile: false },
  { key: 'paket-kecil', weightKg: 0.8, lengthCm: 30, widthCm: 20, heightCm: 10, fragile: false },
  { key: 'sepatu', weightKg: 1.2, lengthCm: 34, widthCm: 22, heightCm: 13, fragile: false },
  { key: 'kue', weightKg: 1.5, lengthCm: 30, widthCm: 30, heightCm: 15, fragile: true },
  { key: 'laptop', weightKg: 2.8, lengthCm: 42, widthCm: 30, heightCm: 10, fragile: true },
  { key: 'makanan', weightKg: 5, lengthCm: 40, widthCm: 30, heightCm: 25, fragile: false },
  { key: 'kardus-sedang', weightKg: 4, lengthCm: 40, widthCm: 30, heightCm: 30, fragile: false },
  { key: 'kardus-besar', weightKg: 12, lengthCm: 60, widthCm: 40, heightCm: 40, fragile: false },
  { key: 'galon', weightKg: 19, lengthCm: 30, widthCm: 30, heightCm: 48, fragile: false },
  { key: 'dispenser', weightKg: 7, lengthCm: 35, widthCm: 35, heightCm: 100, fragile: true },
  { key: 'kursi', weightKg: 9, lengthCm: 60, widthCm: 60, heightCm: 100, fragile: false },
  { key: 'rak', weightKg: 25, lengthCm: 120, widthCm: 30, heightCm: 20, fragile: false },
  { key: 'kulkas-mini', weightKg: 22, lengthCm: 50, widthCm: 50, heightCm: 85, fragile: true },
]

/** Nama barang katalog dalam bahasa tertentu (bawaan: bahasa aktif). Teksnya ada di katalog bersama (`cat.<kunci>`). */
export function catalogName(key: string, locale: Locale = getLocale()): string {
  return translate(locale, `cat.${catalogItem(key).key}`)
}

/** Nama yang ditampilkan untuk sebuah barang: barang katalog mengikuti bahasa tampilan, barang yang diketik sendiri apa adanya. */
export function itemLabel(item: { name: string; catalogKey?: string }, locale: Locale = getLocale()): string {
  return item.catalogKey && ITEM_CATALOG.some((c) => c.key === item.catalogKey) ? catalogName(item.catalogKey, locale) : item.name
}

export function catalogItem(key: string): CatalogItem {
  const found = ITEM_CATALOG.find((c) => c.key === key)
  if (!found) throw new Error(`Unknown catalog item: ${key}`)
  return found
}

/** Membuat Item dari katalog. `id` wajib (pembuat id berbeda di UI dan di mock). */
export function itemFromCatalog(
  key: string,
  opts: { id: string; qty?: number; source?: ItemSource; confidence?: number; locale?: Locale },
): Item {
  const c = catalogItem(key)
  return {
    id: opts.id,
    name: catalogName(key, opts.locale),
    catalogKey: key,
    qty: opts.qty ?? 1,
    weightKg: c.weightKg,
    lengthCm: c.lengthCm,
    widthCm: c.widthCm,
    heightCm: c.heightCm,
    fragile: c.fragile,
    source: opts.source ?? 'manual',
    ...(opts.confidence !== undefined ? { confidence: opts.confidence } : {}),
  }
}
