import { catalogProblems } from '@loong/i18n'
import { describe, expect, it } from 'vitest'
import { enBo } from './en'
import { idBo } from './id'

describe('katalog backoffice', () => {
  it('Inggris dan Indonesia punya kunci dan tempat penampung yang sama', () => {
    expect(catalogProblems(enBo, idBo)).toEqual([])
  })
})
