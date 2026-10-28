import type { FaultMode } from '@loong/domain'
import { DEFAULT_LOCALE, LOCALES, registeredMessages, setLocale } from '@loong/i18n'
import { afterEach, describe, expect, it } from 'vitest'
import { FAULT_MODES, faultHint, faultLabel } from './demoFaults'

afterEach(() => setLocale(DEFAULT_LOCALE))

describe('mode kegagalan tiruan Panel Demo', () => {
  it('mencakup semua nilai FaultMode', () => {
    const all: Record<FaultMode, true> = { none: true, slow: true, error: true, offline: true, empty: true }
    expect([...FAULT_MODES].sort()).toEqual(Object.keys(all).sort())
  })

  it.each(LOCALES)('setiap mode punya nama dan keterangan (%s)', (locale) => {
    setLocale(locale)
    for (const mode of FAULT_MODES) {
      for (const key of [`shell.demo.fault.${mode}`, `shell.demo.faultHint.${mode}`]) expect(registeredMessages(locale)[key], key).toBeTruthy()
      expect(faultLabel(mode)).not.toMatch(/^shell\./)
      expect(faultHint(mode)).not.toMatch(/^shell\./)
    }
  })

  it('mengikuti bahasa saat dibaca', () => {
    expect(faultLabel('error')).toBe('Server error')
    setLocale('id')
    expect(faultLabel('error')).toBe('Galat server')
    expect(faultHint('slow')).toBe('Tiap panggilan tertahan ±2,6 dtk')
  })
})
