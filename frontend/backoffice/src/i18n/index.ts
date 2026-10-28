import { registerMessages, translator, type SharedMessages } from '@loong/i18n'
import { richTranslator } from '@loong/i18n/react'
import { enBo } from './en'
import { idBo } from './id'

registerMessages('en', enBo)
registerMessages('id', idBo)

type Messages = typeof enBo & SharedMessages

/** Terjemahan aplikasi ini (bo.*) ditambah katalog bersama. Lihat packages/i18n/README.md. */
export const t = translator<Messages>()
/** Teks dengan tag, mis. tr('bo.x.help', { link: (c) => <Link to="/">{c}</Link> }). */
export const tr = richTranslator<Messages>()
