import type { Translation } from '@loong/i18n'
import type { enIntent } from '../en/intent'

export const idIntent: Translation<typeof enIntent> = {
  'ai.intent.deadline': 'Tiba sebelum {time|time}',
  'ai.intent.deadlineOn': 'Tiba sebelum {time|time} pada {time|date}',
  'ai.intent.deadlinePlace': 'Tiba di {place} sebelum {time|time}',
  'ai.intent.deadlinePlaceOn': 'Tiba di {place} sebelum {time|time} pada {time|date}',
  'ai.intent.priority': 'Prioritas: {preset}',
  'ai.intent.priorityMixed': 'Prioritas: {preset} (ada kata yang saling bertentangan)',
}
