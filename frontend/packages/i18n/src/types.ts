import type { Msg, Value } from './translate'

/**
 * Tipe katalog. Kunci memakai titik dan huruf/angka saja (garis bawah dicadangkan untuk akhiran jamak:
 * `plans_one`, `plans_other`). Teks bahasa Inggris adalah sumber kebenaran; kunci dan tempat penampung
 * `{nama}` diturunkan dari tipe literalnya sehingga pemanggilan yang salah gagal saat typecheck.
 */
export type Messages = Readonly<Record<string, string>>

type Variant = 'zero' | 'one' | 'two' | 'few' | 'many' | 'other'
type StripVariant<K extends string> = K extends `${infer B}_${Variant}` ? B : K

/** Kunci yang boleh dipanggil: akhiran jamak dilipat ke kunci dasar. */
export type KeyOf<M> = StripVariant<Extract<keyof M, string>>

type TextOf<M, K extends string> =
  | (K extends keyof M ? M[K] : never)
  | (`${K}_${Variant}` extends infer V ? (V extends keyof M ? M[V] : never) : never)

/** `{name}` atau `{name|rupiah}` -> "name". */
type NamesIn<S> = S extends `${string}{${infer P}}${infer R}` ? (P extends `${infer N}|${string}` ? N : P) | NamesIn<R> : never

type IsPlural<M, K extends string> = K extends keyof M ? false : true

export type ParamNames<M, K extends string> = NamesIn<TextOf<M, K>> | (IsPlural<M, K> extends true ? 'count' : never)

export type Vars<N extends string> = { [P in N]: Value }

export type ArgsFor<M, K extends string> = [ParamNames<M, K>] extends [never] ? [params?: undefined] : [params: Vars<ParamNames<M, K>>]

export interface Translate<M extends Messages> {
  <K extends KeyOf<M>>(key: K, ...args: ArgsFor<M, K>): string
}

export interface MsgFn<M extends Messages> {
  <K extends KeyOf<M>>(key: K, ...args: ArgsFor<M, K>): Msg
}

/** `<tag>...</tag>` -> "tag" (hanya pasangan tag; dipakai `tr` untuk teks kaya seperti tautan di tengah kalimat). */
export type TagsIn<S> = S extends `${string}<${infer T}>${infer R}` ? (T extends `/${string}` ? never : T) | TagsIn<R> : never

export type TagNames<M, K extends string> = TagsIn<TextOf<M, K>>

/**
 * Katalog terjemahan harus punya kunci yang sama dengan sumber (Inggris). Akhiran jamak `_zero/_one/_two`
 * boleh dihilangkan: bahasa Indonesia tidak membedakan tunggal dan jamak.
 */
export type Translation<E extends Messages> = {
  [K in keyof E as K extends `${string}_${'zero' | 'one' | 'two'}` ? never : K]: string
} & {
  [K in keyof E as K extends `${string}_${'zero' | 'one' | 'two'}` ? K : never]?: string
}
