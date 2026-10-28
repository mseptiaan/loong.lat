import type { Translation } from '@loong/i18n'
import type { enDrv } from '../en'
import { idDrvApp } from './app'
import { idDrvHome } from './home'
import { idDrvRoute } from './route'
import { idDrvStop } from './stop'
import { idDrvAccount } from './account'

export const idDrv: Translation<typeof enDrv> = { ...idDrvApp, ...idDrvHome, ...idDrvRoute, ...idDrvStop, ...idDrvAccount }
