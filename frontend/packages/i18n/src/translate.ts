import { DEFAULT_LOCALE, getLocale, type Locale } from './locale'
import type { ArgsFor, KeyOf, Messages, MsgFn, Translate } from './types'

/** Nilai yang boleh diisikan ke `{nama}`; `Msg` bersarang dirender dalam bahasa yang sama. */
export type Primitive = string | number
export type Value = Primitive | Msg
export type Params = Record<string, Value>

/**
 * Pesan netral-bahasa: kunci + parameter mentah (angka dan ISO apa adanya). Backend menyimpan ini untuk teks
 * yang tersimpan (peristiwa, notifikasi, alasan opsi, galat) dan layar merendernya dengan bahasa saat ini.
 */
export interface Msg {
  key: string
  params?: Record<string, Value>
}

/* ---------- katalog ---------- */

const catalogs: Record<Locale, Record<string, string>> = { en: {}, id: {} }

export function registerMessages(locale: Locale, messages: Readonly<Record<string, string | undefined>>): void {
  const target = catalogs[locale]
  for (const [key, text] of Object.entries(messages)) if (text !== undefined) target[key] = text
}

/** Hanya untuk pengujian dan pemeriksaan: salinan katalog terdaftar. */
export function registeredMessages(locale: Locale): Readonly<Record<string, string>> {
  return catalogs[locale]
}

const pluralRules = new Map<Locale, Intl.PluralRules>()
const pluralRule = (locale: Locale) => {
  let rules = pluralRules.get(locale)
  if (!rules) pluralRules.set(locale, (rules = new Intl.PluralRules(locale)))
  return rules
}

/* ---------- pemformat bernama: `{harga|rupiah}` ---------- */

type Formatter = (value: number | string, locale: Locale) => string
const formatters = new Map<string, Formatter>()
export function registerFormatter(name: string, fn: Formatter): void {
  formatters.set(name, fn)
}

/* ---------- penerjemahan ---------- */

const warned = new Set<string>()
function warn(message: string) {
  if (warned.has(message)) return
  warned.add(message)
  // dev: kunci hilang harus terlihat; produksi: tetap tampil sesuatu (Inggris atau kuncinya)
  console.warn(`[i18n] ${message}`)
}

/** Templat mentah (belum diisi parameter) dengan cadangan ke Inggris. */
export function resolveTemplate(locale: Locale, key: string, count?: number): string | undefined {
  for (const l of locale === DEFAULT_LOCALE ? [locale] : [locale, DEFAULT_LOCALE]) {
    const c = catalogs[l]
    if (count !== undefined) {
      const hit = c[`${key}_${pluralRule(l).select(count)}`] ?? c[`${key}_other`]
      if (hit !== undefined) return hit
    }
    const hit = c[key]
    if (hit !== undefined) return hit
  }
  return undefined
}

const TOKEN = /\{(\w+)(?:\|(\w+))?\}/g

export function interpolate(locale: Locale, template: string, params?: Params): string {
  if (!params) return template
  return template.replace(TOKEN, (whole, name: string, format: string | undefined) => {
    const value = params[name]
    if (value === undefined) {
      warn(`parameter "${name}" tidak diberikan untuk: ${template}`)
      return whole
    }
    if (typeof value === 'object') return translate(locale, value.key, value.params)
    if (format) {
      const fn = formatters.get(format)
      if (!fn) {
        warn(`pemformat tidak dikenal: ${format}`)
        return String(value)
      }
      return fn(value, locale)
    }
    return String(value)
  })
}

export function translate(locale: Locale, key: string, params?: Params): string {
  const count = typeof params?.count === 'number' ? params.count : undefined
  const template = resolveTemplate(locale, key, count)
  if (template === undefined) {
    warn(`kunci hilang (${locale}): ${key}`)
    return key
  }
  return interpolate(locale, template, params)
}

/** Render pesan netral-bahasa dalam bahasa saat ini (atau yang diminta). */
export function renderMsg(msg: Msg, locale: Locale = getLocale()): string {
  return translate(locale, msg.key, msg.params)
}

/* ---------- pembuat fungsi bertipe ---------- */

/** `t` bertipe untuk himpunan katalog `M`, mengikuti bahasa aktif. */
export function translator<M extends Messages>(): Translate<M> {
  return ((key: string, params?: Params) => translate(getLocale(), key, params)) as Translate<M>
}

/** `t` terikat bahasa tertentu (backend tiruan, yang melayani banyak bahasa sekaligus). */
export function translatorFor<M extends Messages>(locale: Locale): Translate<M> {
  return ((key: string, params?: Params) => translate(locale, key, params)) as Translate<M>
}

/** Membuat `Msg` bertipe (bukan teks) untuk disimpan atau dikirim. */
export function msgFactory<M extends Messages>(): MsgFn<M> {
  return ((key: string, params?: Params) => (params ? { key, params } : { key })) as MsgFn<M>
}

export type { ArgsFor, KeyOf }
