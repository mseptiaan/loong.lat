import { registerMessages, translatorFor, type Locale, type SharedMessages, type Translate } from '@loong/i18n'
import { enAi } from './en'
import { idAi } from './id'

registerMessages('en', enAi)
registerMessages('id', idAi)

export type AiMessages = typeof enAi

/**
 * Teks sesaat dari AI tiruan dalam bahasa permintaan. Server melayani banyak bahasa bergantian, jadi jangan memakai
 * `t` global di sini: selalu `tAi(locale)`.
 */
export const tAi = (locale: Locale): Translate<AiMessages & SharedMessages> => translatorFor<AiMessages & SharedMessages>(locale)

export { enAi, idAi }
