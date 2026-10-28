import { enBoApp } from './app'
import { enBoCommand } from './command'
import { enBoPlans } from './plans'
import { enBoLive } from './live'
import { enBoUsers } from './users'
import { enBoBalances } from './balances'
import { enBoFleet } from './fleet'
import { enBoAnalytics } from './analytics'
import { enBoSettings } from './settings'

export const enBo = { ...enBoApp, ...enBoCommand, ...enBoPlans, ...enBoLive, ...enBoUsers, ...enBoBalances, ...enBoFleet, ...enBoAnalytics, ...enBoSettings } as const
