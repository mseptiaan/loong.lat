/** drv.home.*: Beranda, Pendapatan, Riwayat. */
export const enDrvHome = {
  // Beranda
  'drv.home.greeting': 'Hi, {name}',
  'drv.home.jobs': 'Jobs',
  'drv.home.statusError': "Couldn't load your status",
  'drv.home.statusWorking': 'On duty',
  'drv.home.statusOnline': 'Online',
  'drv.home.statusOffline': 'Offline',
  'drv.home.statusWorkingHint': 'Finish your job to change your status.',
  'drv.home.statusOnlineHint': 'Visible to the dispatcher and ready to take jobs.',
  'drv.home.statusOfflineHint': 'Turn on to receive new jobs.',
  'drv.home.toastOnline': "You're online. New jobs will show up here.",
  'drv.home.toastOffline': "You're offline.",
  'drv.home.toastFinishFirst': 'Finish your active job before changing your status.',
  'drv.home.waitingTitle': 'Waiting for jobs',
  'drv.home.waitingHint': "You're online. New jobs from the dispatcher will appear here and you'll get a notification.",
  'drv.home.offlineTitle': "You're offline",
  'drv.home.offlineHint': 'Turn on online status above to start receiving jobs.',
  'drv.home.today': 'Today',
  'drv.home.todaySummary_one': '{amount|rupiah} · {count} route completed',
  'drv.home.todaySummary_other': '{amount|rupiah} · {count} routes completed',
  'drv.home.todayError': "Couldn't load",

  // kartu rute berjalan
  'drv.home.activeRoute': 'Route in progress · {code}',
  'drv.home.progressLabel': 'Route progress',
  'drv.home.continueRoute': 'Continue route',

  // kartu tugas baru
  'drv.home.newJob': 'New job · {code} · {vehicle}',
  'drv.home.jobSummary_one': '{count} stop · {km|km}',
  'drv.home.jobSummary_other': '{count} stops · {km|km}',
  'drv.home.estimate': 'estimate',
  'drv.home.pickupAt': 'Pick up at <strong>{name}</strong>',
  'drv.home.scheduled': 'Scheduled: {when}',
  'drv.home.leavingNow': 'Leaving now, arriving around {time|time}',
  'drv.home.viewJob': 'View job',

  // Pendapatan
  'drv.home.earningsLast7': 'Earnings for the last 7 days',
  'drv.home.dayEarnings_one': '{amount|rupiah}, {count} route',
  'drv.home.dayEarnings_other': '{amount|rupiah}, {count} routes',
  'drv.home.routesDone_one': '{count} route completed',
  'drv.home.routesDone_other': '{count} routes completed',
  'drv.home.last7': 'Last 7 days',
  'drv.home.routeCount_one': '{count} route',
  'drv.home.routeCount_other': '{count} routes',
  'drv.home.perDay': 'Per day',
  'drv.home.barsNote': 'Figures above the bars are in thousands of rupiah.',
  'drv.home.earningsNote': "Your earnings are 70% of each route's fare; the rest is the platform fee.",

  // Riwayat
  'drv.home.historyIntro': 'Routes you have completed.',
  'drv.home.historyEmptyTitle': 'No history yet',
  'drv.home.historyEmptyHint': 'Completed routes are recorded here along with their earnings.',
  'drv.home.historyLine_one': 'Finished {time|time} · {count} stop · {km|km} · {duration|duration}',
  'drv.home.historyLine_other': 'Finished {time|time} · {count} stops · {km|km} · {duration|duration}',
} as const
