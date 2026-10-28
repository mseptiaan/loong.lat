import type { Translation } from '@loong/i18n'
import type { enBo } from '../en'
import { idBoApp } from './app'
import { idBoCommand } from './command'
import { idBoPlans } from './plans'
import { idBoLive } from './live'
import { idBoUsers } from './users'
import { idBoBalances } from './balances'
import { idBoFleet } from './fleet'
import { idBoAnalytics } from './analytics'
import { idBoSettings } from './settings'

export const idBo: Translation<typeof enBo> = { ...idBoApp, ...idBoCommand, ...idBoPlans, ...idBoLive, ...idBoUsers, ...idBoBalances, ...idBoFleet, ...idBoAnalytics, ...idBoSettings }
