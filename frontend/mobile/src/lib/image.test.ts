import { describe, expect, it } from 'vitest'
import { fitWithin } from './image'

describe('fitWithin', () => {
  it('mengecilkan sisi terpanjang dengan rasio tetap', () => {
    expect(fitWithin(4000, 3000, 720)).toEqual({ width: 720, height: 540 })
    expect(fitWithin(3000, 4000, 720)).toEqual({ width: 540, height: 720 })
  })

  it('tidak memperbesar gambar yang sudah kecil', () => {
    expect(fitWithin(315, 560, 720)).toEqual({ width: 315, height: 560 })
  })

  it('selalu menghasilkan sisi minimal 1 piksel', () => {
    expect(fitWithin(10000, 2, 720)).toEqual({ width: 720, height: 1 })
  })
})
