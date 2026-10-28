// Mendaftarkan katalog bersama dan pemformat bernama sebagai efek samping. Diimpor eksplisit (bukan hanya di-re-export)
// supaya bundler tidak membuangnya saat aplikasi tidak memakai enShared/idShared secara langsung; lihat "sideEffects".
import './messages'
import './format'

export * from './locale'
export * from './translate'
export * from './types'
export * from './format'
export { catalogProblems } from './check'
export { enShared, idShared, type SharedMessages } from './messages'

import type { SharedMessages } from './messages'
import { msgFactory, translator, translatorFor } from './translate'
import type { Locale } from './locale'
import type { Translate } from './types'

/** `t` untuk katalog bersama (ui.*, shell.*, srv.*, dom.*, fmt.*). Aplikasi membuat `t` sendiri dengan katalognya. */
export const t = translator<SharedMessages>()
export const tFor = (locale: Locale): Translate<SharedMessages> => translatorFor<SharedMessages>(locale)
/** Membuat `Msg` bertipe untuk kunci bersama (tanpa mengubahnya jadi teks). */
export const msg = msgFactory<SharedMessages>()
