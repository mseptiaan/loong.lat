import { registerMessages } from '../translate'
import type { Translation } from '../types'
import { enDomain } from './en/domain'
import { enFormat } from './en/format'
import { enShell } from './en/shell'
import { enSrv } from './en/srv'
import { enUi } from './en/ui'
import { idDomain } from './id/domain'
import { idFormat } from './id/format'
import { idShell } from './id/shell'
import { idSrv } from './id/srv'
import { idUi } from './id/ui'

/**
 * Katalog bersama tiga aplikasi, dibagi per pemilik supaya bisa diedit paralel:
 *   fmt.*  pemformat (format.ts)            dom.* / cat.*  label domain dan katalog barang
 *   ui.*   kit UI (@loong/ui)               shell.*        kerangka (@loong/shell, @loong/data)
 *   srv.*  teks netral-bahasa dari backend tiruan: peristiwa, notifikasi, alasan opsi, galat (dirender di layar)
 * Teks khusus aplikasi ada di katalog aplikasi itu sendiri (dash.*, bo.*, drv.*).
 */
export const enShared = { ...enFormat, ...enDomain, ...enUi, ...enShell, ...enSrv } as const
export const idShared: Translation<typeof enShared> = { ...idFormat, ...idDomain, ...idUi, ...idShell, ...idSrv }

export type SharedMessages = typeof enShared

registerMessages('en', enShared)
registerMessages('id', idShared)
