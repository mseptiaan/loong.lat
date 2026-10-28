import { enCopilot } from './copilot'
import { enInsights } from './insights'
import { enIntent } from './intent'
import { enReport } from './report'
import { enVision } from './vision'

/** Katalog teks AI tiruan (ai.*), Inggris = sumber kebenaran. */
export const enAi = { ...enCopilot, ...enInsights, ...enVision, ...enIntent, ...enReport } as const
