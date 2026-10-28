import type { Translation } from '@loong/i18n'
import type { enAi } from '../en'
import { idCopilot } from './copilot'
import { idInsights } from './insights'
import { idIntent } from './intent'
import { idReport } from './report'
import { idVision } from './vision'

export const idAi: Translation<typeof enAi> = { ...idCopilot, ...idInsights, ...idVision, ...idIntent, ...idReport }
