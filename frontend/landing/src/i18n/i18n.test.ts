import { catalogProblems } from '@loong/i18n'
import { describe, expect, it } from 'vitest'
import { enLand } from './en'
import { idLand } from './id'

describe('katalog landing', () => {
  it('Inggris dan Indonesia punya kunci dan tempat penampung yang sama', () => {
    expect(catalogProblems(enLand, idLand)).toEqual([])
  })
})
