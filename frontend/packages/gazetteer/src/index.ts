import { haversineKm, type LatLng, type Place, type PlaceKind } from '@loong/domain'
import { DEFAULT_LOCALE, formatDecimal, t, translate, type Locale } from '@loong/i18n'
import { PLACES } from './places'

export { PLACES }

const normalize = (s: string) =>
  s
    .toLowerCase()
    .normalize('NFD')
    .replace(/[̀-ͯ]/g, '')
    .replace(/[^a-z0-9 ]+/g, ' ')
    .replace(/\s+/g, ' ')
    .trim()

/** Sinonim jenis tempat yang sering diketik orang (Indonesia dan Inggris, apa pun bahasa layarnya). */
const KIND_WORDS: Record<PlaceKind, string[]> = {
  mall: ['mall', 'mal', 'plaza', 'pusat belanja', 'shopping'],
  stasiun: ['stasiun', 'krl', 'mrt', 'kereta', 'station', 'train'],
  kampus: ['kampus', 'universitas', 'univ', 'kuliah', 'campus', 'university', 'college'],
  kantor: ['kantor', 'gedung', 'menara', 'office', 'building', 'tower'],
  pasar: ['pasar', 'market'],
  'rumah-sakit': ['rumah sakit', 'rs', 'klinik', 'hospital', 'clinic'],
  landmark: ['monas', 'taman', 'stadion', 'wisata', 'park', 'stadium', 'tourist'],
  hunian: ['apartemen', 'apartment', 'perumahan', 'cluster', 'rumah', 'residential', 'house'],
  gudang: ['gudang', 'pergudangan', 'kawasan industri', 'warehouse', 'industrial'],
}

interface Indexed {
  place: Place
  name: string
  haystack: string
  kindWords: string
}

const INDEX: Indexed[] = PLACES.map((place) => ({
  place,
  name: normalize(place.name),
  haystack: normalize(`${place.name} ${place.address} ${place.area}`),
  kindWords: KIND_WORDS[place.kind].join(' '),
}))

/**
 * Cari lokasi tanpa API key. Semua kata kunci harus cocok di nama/alamat/kawasan/jenis.
 * Urutan: nama diawali kata kunci > nama memuat > alamat/kawasan memuat > hanya jenis.
 */
export function searchPlaces(query: string, limit = 8): Place[] {
  const q = normalize(query)
  if (!q) return []
  const tokens = q.split(' ')
  const scored: { place: Place; score: number }[] = []
  for (const item of INDEX) {
    let score = 0
    let ok = true
    for (const t of tokens) {
      if (item.name.startsWith(t) || item.name.includes(` ${t}`)) score += 4
      else if (item.name.includes(t)) score += 3
      else if (item.haystack.includes(t)) score += 2
      else if (item.kindWords.includes(t)) score += 1
      else {
        ok = false
        break
      }
    }
    if (ok) {
      if (item.name === q) score += 6
      else if (item.name.startsWith(q)) score += 3
      scored.push({ place: item.place, score })
    }
  }
  return scored
    .sort((a, b) => b.score - a.score || a.place.name.localeCompare(b.place.name, 'id'))
    .slice(0, limit)
    .map((s) => s.place)
}

export function getPlace(id: string): Place | undefined {
  return PLACES.find((p) => p.id === id)
}

/** Titik terdekat dari sebuah koordinat (dipakai sebagai "reverse geocode" tiruan saat pin digeser di peta). */
export function nearestPlace(position: LatLng): { place: Place; distanceKm: number } {
  let best = PLACES[0]!
  let bestKm = Infinity
  for (const place of PLACES) {
    const km = haversineKm(position, place.position)
    if (km < bestKm) {
      bestKm = km
      best = place
    }
  }
  return { place: best, distanceKm: bestKm }
}

/** Nama yang ramah untuk titik bebas di peta, mis. "Dekat Plaza Senayan (±600 m)". */
/**
 * Nama dan alamat untuk titik yang dipilih di peta. Teksnya tersimpan sebagai data rute, jadi dibuat dalam bahasa
 * permintaan (`locale`); backend tiruan mengoper bahasa panggilan.
 */
export function describePoint(position: LatLng, locale: Locale = DEFAULT_LOCALE): { name: string; address: string } {
  const { place, distanceKm } = nearestPlace(position)
  const dist = distanceKm < 1 ? `±${Math.max(50, Math.round(distanceKm * 10) * 100)} m` : `±${formatDecimal(distanceKm, 1, locale)} km`
  return {
    name: translate(locale, 'dom.pin.name'),
    address: translate(locale, 'dom.pin.near', { place: place.name, dist, area: place.area }),
  }
}

export const PLACE_KIND_LABEL: Record<PlaceKind, string> = {
  get mall() {
    return t('dom.placeKind.mall')
  },
  get stasiun() {
    return t('dom.placeKind.stasiun')
  },
  get kampus() {
    return t('dom.placeKind.kampus')
  },
  get kantor() {
    return t('dom.placeKind.kantor')
  },
  get pasar() {
    return t('dom.placeKind.pasar')
  },
  get 'rumah-sakit'() {
    return t('dom.placeKind.rumah-sakit')
  },
  get landmark() {
    return t('dom.placeKind.landmark')
  },
  get hunian() {
    return t('dom.placeKind.hunian')
  },
  get gudang() {
    return t('dom.placeKind.gudang')
  },
}
