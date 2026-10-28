import { registerMessages, translator, type SharedMessages } from '@loong/i18n'
import { richTranslator } from '@loong/i18n/react'
import { enDrv } from './en'
import { idDrv } from './id'

registerMessages('en', enDrv)
registerMessages('id', idDrv)

type Messages = typeof enDrv & SharedMessages

/** Terjemahan aplikasi ini (drv.*) ditambah katalog bersama. Lihat packages/i18n/README.md. */
export const t = translator<Messages>()
/** Teks dengan tag, mis. tr('drv.x.help', { link: (c) => <Link to="/">{c}</Link> }). */
export const tr = richTranslator<Messages>()
