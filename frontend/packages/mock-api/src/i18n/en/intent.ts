/** ai.intent.*: label chip hasil urai maksud ("paling hemat tapi sampai sebelum jam 5"). */
export const enIntent = {
  'ai.intent.deadline': 'Arrive before {time|time}',
  'ai.intent.deadlineOn': 'Arrive before {time|time} on {time|date}',
  'ai.intent.deadlinePlace': 'Arrive at {place} before {time|time}',
  'ai.intent.deadlinePlaceOn': 'Arrive at {place} before {time|time} on {time|date}',
  'ai.intent.priority': 'Priority: {preset}',
  'ai.intent.priorityMixed': 'Priority: {preset} (some words contradict each other)',
} as const
