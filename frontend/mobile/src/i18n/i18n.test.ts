import { catalogProblems } from '@loong/i18n'
import { describe, expect, it } from 'vitest'
import { enDrv } from './en'
import { idDrv } from './id'

describe('katalog mobile', () => {
  it('Inggris dan Indonesia punya kunci dan tempat penampung yang sama', () => {
    expect(catalogProblems(enDrv, idDrv)).toEqual([])
  })
})
