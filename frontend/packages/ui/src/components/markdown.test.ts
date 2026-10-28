import { describe, expect, it } from 'vitest'
import { parseBlocks } from './markdown'

describe('parseBlocks', () => {
  it('paragraf, daftar, dan tabel', () => {
    const blocks = parseBlocks('Halo **dunia**\n\n- satu\n- dua\n\n| A | B |\n| --- | --- |\n| 1 | 2 |', false)
    expect(blocks.map((b) => b.type)).toEqual(['p', 'ul', 'table'])
    const table = blocks[2]!
    expect(table.type === 'table' && table.head).toEqual(['A', 'B'])
    expect(table.type === 'table' && table.rows).toEqual([['1', '2']])
  })

  it('daftar bernomor dan baris berurutan digabung jadi satu paragraf', () => {
    const blocks = parseBlocks('baris satu\nbaris dua\n\n1. a\n2. b', false)
    expect(blocks[0]).toEqual({ type: 'p', text: 'baris satu baris dua' })
    expect(blocks[1]).toEqual({ type: 'ol', items: ['a', 'b'] })
  })

  it('selama mengalir, baris tabel yang belum lengkap disembunyikan; setelah selesai tampil', () => {
    const partial = '| A | B |\n| --- | --- |\n| 1 | 2 |\n| 3 |'
    expect(parseBlocks(partial, true)[0]).toMatchObject({ rows: [['1', '2']] })
    expect(parseBlocks(partial, false)[0]).toMatchObject({ rows: [['1', '2'], ['3']] })
    // setelah baris diakhiri newline, ia dianggap lengkap
    expect(parseBlocks(`${partial}\n`, true)[0]).toMatchObject({ rows: [['1', '2'], ['3']] })
  })

  it('teks kosong tidak menghasilkan blok', () => {
    expect(parseBlocks('', true)).toEqual([])
  })
})
