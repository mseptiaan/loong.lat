import { Fragment, useSyncExternalStore, type ReactNode } from 'react'
import { DEFAULT_LOCALE, getLocale, subscribeLocale, type Locale } from './locale'
import { interpolate, resolveTemplate, type Params } from './translate'
import type { KeyOf, Messages, ParamNames, TagNames, Vars } from './types'

/** Bahasa aktif sebagai nilai reaktif (untuk pengganti bahasa). Teks lain cukup memakai `t()`. */
export function useLocale(): Locale {
  return useSyncExternalStore(subscribeLocale, getLocale, () => DEFAULT_LOCALE)
}

/**
 * Memasang ulang seluruh pohon di bawahnya saat bahasa berganti, sehingga setiap `t()` (termasuk di luar hook)
 * dihitung ulang. Router, klien API, dan cache data berada di atasnya dan tetap hidup.
 */
export function I18nBoundary({ children }: { children: ReactNode }) {
  const locale = useLocale()
  return <Fragment key={locale}>{children}</Fragment>
}

/* ---------- teks kaya: "Butuh bantuan? <link>Hubungi kami</link>" ---------- */

type Tree = string | { tag: string; children: Tree[] }

const TAG = /<(\/?)([A-Za-z][\w-]*)>/g

function parse(template: string): Tree[] {
  const root: { tag: string; children: Tree[] } = { tag: '', children: [] }
  const stack = [root]
  let last = 0
  for (const m of template.matchAll(TAG)) {
    const top = stack[stack.length - 1]!
    if (m.index > last) top.children.push(template.slice(last, m.index))
    last = m.index + m[0].length
    if (m[1]) {
      if (stack.length > 1 && top.tag === m[2]) stack.pop()
    } else {
      const node = { tag: m[2]!, children: [] as Tree[] }
      top.children.push(node)
      stack.push(node)
    }
  }
  if (last < template.length) stack[0]!.children.push(template.slice(last))
  return root.children
}

type Renderers = Record<string, (children: ReactNode) => ReactNode>

function render(nodes: Tree[], locale: Locale, params: Params, renderers: Renderers): ReactNode[] {
  return nodes.map((node, i) => {
    if (typeof node === 'string') return <Fragment key={i}>{interpolate(locale, node, params)}</Fragment>
    const fn = renderers[node.tag]
    const children = render(node.children, locale, params, renderers)
    return <Fragment key={i}>{fn ? fn(children) : children}</Fragment>
  })
}

export type RichTranslate<M extends Messages> = <K extends KeyOf<M>>(
  key: K,
  params: Vars<ParamNames<M, K>> & { [T in TagNames<M, K>]: (children: ReactNode) => ReactNode },
) => ReactNode

/**
 * Seperti `t`, tetapi `<tag>teks</tag>` di dalam teks dirender oleh fungsi dengan nama tag itu, mis.
 * `tr('x.help', { link: (c) => <Link to="/bantuan">{c}</Link> })`. Nilai parameter tidak pernah dibaca sebagai tag.
 */
export function richTranslator<M extends Messages>(): RichTranslate<M> {
  return ((key: string, params: Record<string, unknown>) => {
    const locale = getLocale()
    const count = typeof params.count === 'number' ? params.count : undefined
    const template = resolveTemplate(locale, key, count)
    if (template === undefined) return key
    const values: Params = {}
    const renderers: Renderers = {}
    for (const [name, value] of Object.entries(params)) {
      if (typeof value === 'function') renderers[name] = value as Renderers[string]
      else values[name] = value as Params[string]
    }
    return <>{render(parse(template), locale, values, renderers)}</>
  }) as RichTranslate<M>
}
