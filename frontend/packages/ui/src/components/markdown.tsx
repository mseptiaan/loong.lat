import type { ReactNode } from 'react'
import { cn } from '../lib/cn'

type Block =
  | { type: 'p'; text: string }
  | { type: 'ul'; items: string[] }
  | { type: 'ol'; items: string[] }
  | { type: 'table'; head: string[]; rows: string[][] }

const isTableRow = (l: string) => l.trimStart().startsWith('|')
const isSeparator = (l: string) => /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/.test(l)
const cells = (l: string) =>
  l
    .trim()
    .replace(/^\|/, '')
    .replace(/\|$/, '')
    .split('|')
    .map((c) => c.trim())

/**
 * Markdown secukupnya untuk jawaban AI: paragraf, daftar, tabel, **tebal**, `kode`. Diurai ulang pada tiap potongan
 * yang datang (progresif, bukan menunggu selesai). Selama mengalir, baris tabel yang belum lengkap disembunyikan
 * agar tidak berkedip, dan `**` yang belum ditutup langsung dianggap tebal. Tidak ada HTML mentah.
 */
export function parseBlocks(src: string, streaming: boolean): Block[] {
  const lines = src.replace(/\r/g, '').split('\n')
  // baris terakhir yang belum diikuti newline masih bisa bertambah: jangan tampilkan tabel setengah jadi
  if (streaming && !src.endsWith('\n') && lines.length > 0 && isTableRow(lines[lines.length - 1]!)) lines.pop()
  const blocks: Block[] = []
  let i = 0
  while (i < lines.length) {
    const line = lines[i]!
    if (line.trim() === '') {
      i++
    } else if (isTableRow(line)) {
      const rows: string[][] = []
      let head: string[] | undefined
      while (i < lines.length && isTableRow(lines[i]!)) {
        const l = lines[i]!
        if (isSeparator(l)) head ??= rows.pop() ?? []
        else rows.push(cells(l))
        i++
      }
      blocks.push({ type: 'table', head: head ?? rows.shift() ?? [], rows })
    } else if (/^\s*-\s+/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^\s*-\s+/.test(lines[i]!)) items.push(lines[i++]!.replace(/^\s*-\s+/, ''))
      blocks.push({ type: 'ul', items })
    } else if (/^\s*\d+\.\s+/.test(line)) {
      const items: string[] = []
      while (i < lines.length && /^\s*\d+\.\s+/.test(lines[i]!)) items.push(lines[i++]!.replace(/^\s*\d+\.\s+/, ''))
      blocks.push({ type: 'ol', items })
    } else {
      const para: string[] = []
      while (i < lines.length && lines[i]!.trim() !== '' && !isTableRow(lines[i]!) && !/^\s*(-|\d+\.)\s+/.test(lines[i]!)) para.push(lines[i++]!.trim())
      blocks.push({ type: 'p', text: para.join(' ') })
    }
  }
  return blocks
}

function inline(text: string): ReactNode[] {
  // **tebal** (termasuk yang belum ditutup di ujung teks) dan `kode`
  return text.split(/(\*\*[^*]*(?:\*\*|$)|`[^`]+`)/).flatMap((part, i): ReactNode[] => {
    if (!part) return []
    if (part.startsWith('**')) return [<strong key={i}>{part.replace(/^\*\*/, '').replace(/\*\*$/, '')}</strong>]
    if (part.startsWith('`') && part.endsWith('`') && part.length > 1) return [<code key={i} className="rounded-[4px] bg-sunken px-1 font-mono text-[0.85em]">{part.slice(1, -1)}</code>]
    return [part]
  })
}

function Caret() {
  return <span aria-hidden className="ml-0.5 inline-block h-[1.05em] w-[2px] translate-y-[0.2em] bg-accent [animation:caret-blink_1s_steps(1)_infinite] motion-reduce:animate-none" />
}

/** Jawaban AI sebagai teks terstruktur. `streaming` menambahkan caret di ujung agar terlihat masih hidup. */
export function Markdown({ text, streaming = false, className }: { text: string; streaming?: boolean; className?: string }) {
  const blocks = parseBlocks(text, streaming)
  const lastIndex = blocks.length - 1
  return (
    <div className={cn('space-y-2.5 text-body', className)}>
      {blocks.map((b, bi) => {
        const caret = streaming && bi === lastIndex
        if (b.type === 'p')
          return (
            <p key={bi}>
              {inline(b.text)}
              {caret && <Caret />}
            </p>
          )
        if (b.type === 'ul' || b.type === 'ol') {
          const List = b.type === 'ul' ? 'ul' : 'ol'
          return (
            <List key={bi} className={cn('space-y-1 pl-5', b.type === 'ul' ? 'list-disc' : 'list-decimal')}>
              {b.items.map((it, ii) => (
                <li key={ii} className="pl-0.5">
                  {inline(it)}
                  {caret && ii === b.items.length - 1 && <Caret />}
                </li>
              ))}
            </List>
          )
        }
        return (
          <div key={bi} className="overflow-x-auto">
            <table className="w-full min-w-max border-collapse text-small">
              <thead>
                <tr>
                  {b.head.map((h, hi) => (
                    <th key={hi} scope="col" className="border-b border-line px-2 py-1.5 text-left font-semibold text-muted first:pl-0">
                      {inline(h)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {b.rows.map((r, ri) => (
                  <tr key={ri}>
                    {r.map((c, ci) => (
                      <td key={ci} className="border-b border-line px-2 py-1.5 tabular-nums first:pl-0">
                        {inline(c)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
            {caret && <Caret />}
          </div>
        )
      })}
    </div>
  )
}
