import { enDashApp } from './app'
import { enDashAuth } from './auth'
import { enDashHome } from './home'
import { enDashPlans } from './plans'
import { enDashWallet } from './wallet'
import { enDashAccount } from './account'
import { enDashWizard } from './wizard'
import { enDashAi } from './ai'
import { enDashFields } from './fields'

export const enDash = { ...enDashApp, ...enDashAuth, ...enDashHome, ...enDashPlans, ...enDashWallet, ...enDashAccount, ...enDashWizard, ...enDashAi, ...enDashFields } as const
