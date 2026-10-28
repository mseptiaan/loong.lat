import { registerMessages, translator, type SharedMessages } from '@loong/i18n'
import { richTranslator } from '@loong/i18n/react'
import { enDash } from './en'
import { idDash } from './id'

registerMessages('en', enDash)
registerMessages('id', idDash)

type Messages = typeof enDash & SharedMessages

/** Terjemahan aplikasi ini (dash.*) ditambah katalog bersama. Lihat packages/i18n/README.md. */
export const t = translator<Messages>()
/** Teks dengan tag, mis. tr('dash.x.help', { link: (c) => <Link to="/">{c}</Link> }). */
export const tr = richTranslator<Messages>()
