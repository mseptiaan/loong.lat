import { enDrvApp } from './app'
import { enDrvHome } from './home'
import { enDrvRoute } from './route'
import { enDrvStop } from './stop'
import { enDrvAccount } from './account'

export const enDrv = { ...enDrvApp, ...enDrvHome, ...enDrvRoute, ...enDrvStop, ...enDrvAccount } as const
