import { catalogProblems, registeredMessages } from '@loong/i18n'
import { describe, expect, it } from 'vitest'
import { enAi, idAi, tAi } from './index'

describe('katalog AI tiruan (mock-api)', () => {
  it('Inggris dan Indonesia punya kunci, tempat penampung, dan tag yang sama', () => {
    expect(catalogProblems(enAi, idAi)).toEqual([])
  })

  it('semua kunci berawalan ai. dan terdaftar di kedua bahasa', () => {
    for (const key of Object.keys(enAi)) {
      expect(key.startsWith('ai.')).toBe(true)
      expect(registeredMessages('en')[key]).toBe(enAi[key as keyof typeof enAi])
    }
    for (const key of Object.keys(idAi)) expect(registeredMessages('id')[key]).toBeDefined()
  })

  it('tAi mengikuti bahasa yang diminta tanpa memengaruhi bahasa global', () => {
    expect(tAi('en')('ai.action.open', { code: 'PL-1' })).toBe('Open PL-1')
    expect(tAi('id')('ai.action.open', { code: 'PL-1' })).toBe('Buka PL-1')
    expect(tAi('en')('ai.action.open', { code: 'PL-1' })).toBe('Open PL-1')
  })

  it('plural dan pemformat bernama mengikuti bahasa permintaan', () => {
    expect(tAi('en')('ai.stops', { count: 1 })).toBe('1 stop')
    expect(tAi('en')('ai.stops', { count: 3 })).toBe('3 stops')
    expect(tAi('id')('ai.stops', { count: 3 })).toBe('3 stop')
    expect(tAi('en')('ai.insights.createdUp', { plans: '5 plans', pct: 12, revenue: 125000 })).toContain('Rp 125,000')
    expect(tAi('id')('ai.insights.createdUp', { plans: '5 plan', pct: 12, revenue: 125000 })).toContain('Rp 125.000')
  })
})

describe('katalog srv.api (bersama)', () => {
  it('semua kunci srv.api.* ada di kedua bahasa dan tidak kosong', async () => {
    const { enShared, idShared } = await import('@loong/i18n')
    const keys = Object.keys(enShared).filter((k) => k.startsWith('srv.api.'))
    expect(keys.length).toBeGreaterThan(100)
    for (const key of keys) {
      const base = key.replace(/_(one|other)$/, '')
      const id = (idShared as Record<string, string | undefined>)[key] ?? (idShared as Record<string, string | undefined>)[`${base}_other`]
      expect(id, key).toBeTruthy()
    }
  })
})
