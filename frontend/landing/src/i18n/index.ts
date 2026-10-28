import { registerMessages, translator, type SharedMessages } from '@loong/i18n'
import { enLand } from './en'
import { idLand } from './id'

registerMessages('en', enLand)
registerMessages('id', idLand)

type Messages = typeof enLand & SharedMessages

/** Terjemahan halaman depan (land.*) ditambah katalog bersama. Lihat packages/i18n/README.md. */
export const t = translator<Messages>()
