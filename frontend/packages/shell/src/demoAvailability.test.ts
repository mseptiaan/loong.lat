import { describe, expect, it } from 'vitest'
import { hasDemoPanel } from './demoAvailability'

describe('Panel Demo', () => {
  it('ada untuk backend tiruan dan tidak untuk backend asli (http)', () => {
    expect(hasDemoPanel({ kind: 'mock' })).toBe(true)
    expect(hasDemoPanel({ kind: 'http' })).toBe(false)
  })
})
