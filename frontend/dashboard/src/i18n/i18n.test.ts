import { catalogProblems } from '@loong/i18n'
import { describe, expect, it } from 'vitest'
import { enDash } from './en'
import { idDash } from './id'

describe('katalog dashboard', () => {
  it('Inggris dan Indonesia punya kunci dan tempat penampung yang sama', () => {
    expect(catalogProblems(enDash, idDash)).toEqual([])
  })
})
