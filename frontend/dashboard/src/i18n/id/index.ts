import type { Translation } from '@loong/i18n'
import type { enDash } from '../en'
import { idDashApp } from './app'
import { idDashAuth } from './auth'
import { idDashHome } from './home'
import { idDashPlans } from './plans'
import { idDashWallet } from './wallet'
import { idDashAccount } from './account'
import { idDashWizard } from './wizard'
import { idDashAi } from './ai'
import { idDashFields } from './fields'

export const idDash: Translation<typeof enDash> = { ...idDashApp, ...idDashAuth, ...idDashHome, ...idDashPlans, ...idDashWallet, ...idDashAccount, ...idDashWizard, ...idDashAi, ...idDashFields }
