/** bo.settings.* */
export const enBoSettings = {
  'bo.settings.title': 'Settings',
  'bo.settings.description': 'Driver assignment and AI features apply to all users; display and notifications are saved on this device.',

  'bo.settings.displayTitle': 'Display',
  'bo.settings.displayDescription': 'Density sets the row height of tables and controls.',
  'bo.settings.densityLabel': 'Display density',
  'bo.settings.densityCompact': 'Compact',
  'bo.settings.densityComfortable': 'Comfortable',
  'bo.settings.densityHint': 'Compact fits more rows on large screens; Comfortable is roomier for small or touch screens.',

  'bo.settings.languageTitle': 'Language',
  'bo.settings.languageDescription': 'The language of this app. It is saved on this device.',

  'bo.settings.dispatchTitle': 'Driver assignment',
  'bo.settings.dispatchDescription': 'Applies to new and waiting orders. Assignment follows rules from our engine, not AI.',
  'bo.settings.dispatchAutoLabel': 'Assign drivers automatically',
  'bo.settings.dispatchAutoDescription':
    'Orders for now: the nearest driver who is online, free, and has capacity (15 km radius). Scheduled orders: a driver who is free at that time, preferring a high rating and a light workload that day. If no one fits yet, the plan waits and the system tries again.',
  'bo.settings.dispatchManualNote': 'You can still assign or change drivers manually from the queue and plan details, whether automatic assignment is on or off.',
  'bo.settings.toastAutoOn': 'Automatic assignment turned on. Waiting orders are matched with drivers right away.',
  'bo.settings.toastAutoOff': 'Automatic assignment turned off. Assign drivers manually.',

  'bo.settings.aiTitle': 'AI features for customers',
  'bo.settings.aiDescription': 'AI only suggests; nothing changes without user action. When turned off, customers use manual entry.',
  'bo.settings.aiVisionLabel': 'Read item photos',
  'bo.settings.aiVisionDescription': 'Customers can photograph items to fill in the item list. Customers always review the result before it is used.',
  'bo.settings.aiVoiceLabel': 'Voice input',
  'bo.settings.aiVoiceDescription': 'Customers can say their items out loud; speech is parsed into an item list.',
  'bo.settings.aiIntentLabel': 'Understand needs',
  'bo.settings.aiIntentDescription': 'Free-form sentences like “cheapest but before 5 pm” are parsed into priorities and deadlines.',
  'bo.settings.aiCopilotLabel': 'Copilot and summaries',
  'bo.settings.aiCopilotDescription': 'Route option explanations, plan status Q&A, driver suggestions, and analytics summaries.',
  'bo.settings.toastAiSaved': 'AI settings saved. They apply to all users.',

  'bo.settings.notifyTitle': 'Notifications',
  'bo.settings.notifyDescription': 'What appears in the bell and needs your attention.',
  'bo.settings.notifyUnassignedLabel': 'Plans waiting for a driver',
  'bo.settings.notifyUnassignedDescription': 'Notify me when a plan has had no driver for more than 5 minutes.',
  'bo.settings.notifyAnomalyLabel': 'Anomalies in the field',
  'bo.settings.notifyAnomalyDescription': 'A driver is 10 minutes or more late, or reports a problem on site.',
  'bo.settings.notifyDailyLabel': 'Daily summary',
  'bo.settings.notifyDailyDescription': 'Yesterday’s plan count, revenue, and savings.',
} as const
