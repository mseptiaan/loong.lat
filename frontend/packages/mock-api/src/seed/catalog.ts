import { itemFromCatalog, type Item, type ItemSource } from '@loong/domain'

// Katalog barang ada di domain (dipakai juga oleh wizard); di sini hanya pembuat Item dengan id acak.
export { ITEM_CATALOG, catalogItem, type CatalogItem } from '@loong/domain'

let counter = 0
export const nextId = (prefix: string) =>
  `${prefix}-${Date.now().toString(36)}${(counter++).toString(36)}${Math.floor(Math.random() * 1296).toString(36)}`

export function makeItem(key: string, opts: { id?: string; qty?: number; source?: ItemSource; confidence?: number } = {}): Item {
  return itemFromCatalog(key, { ...opts, id: opts.id ?? nextId('itm') })
}
