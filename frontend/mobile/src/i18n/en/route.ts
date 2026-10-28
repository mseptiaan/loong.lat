/** drv.route.*: halaman rute, ringkasan rute selesai, kartu stop berikutnya. */
export const enDrvRoute = {
  // kartu stop berikutnya
  'drv.route.nextStopLabel': 'Next stop',
  'drv.route.navigate': 'Navigate',
  'drv.route.stopDetails': 'Stop details',

  // halaman rute
  'drv.route.mapLabel': 'Route map. A text equivalent is in the List view.',
  'drv.route.factDistance': 'Distance',
  'drv.route.factDuration': 'Finishes in',
  'drv.route.factWeight': 'Weight',
  'drv.route.toastStarted': 'Route started. Drive safely.',
  'drv.route.notFoundTitle': 'Job not found',
  'drv.route.notFoundHint': 'This route may have been completed, reassigned to another driver, or canceled by an admin.',
  'drv.route.headerRunning_one': '{done} of {count} stop completed · {vehicle} · {km|km}',
  'drv.route.headerRunning_other': '{done} of {count} stops completed · {vehicle} · {km|km}',
  'drv.route.headerIdle_one': '{count} stop · {vehicle} · {km|km}',
  'drv.route.headerIdle_other': '{count} stops · {vehicle} · {km|km}',
  'drv.route.view': 'Route view',
  'drv.route.viewList': 'List',
  'drv.route.viewMap': 'Map',
  'drv.route.estimatedEarnings': 'Estimated earnings',
  'drv.route.departs': 'Departs {time|time}',
  'drv.route.stopOrder': 'Stop order',
  'drv.route.start': 'Start route',
  'drv.route.startTo': 'Start route · to {name}',
  'drv.route.swipeArrive': 'Swipe to arrive',
  'drv.route.completePickup': 'Complete pickup',
  'drv.route.completeDropoff': 'Complete drop-off',

  // rute selesai
  'drv.route.doneMissingTitle': 'Summary not found',
  'drv.route.doneMissingHint': "This route hasn't been recorded as completed yet.",
  'drv.route.doneTitle': 'Route completed',
  'drv.route.doneThanks': 'Every stop on your route for {code} is done. Thank you, great work.',
  'drv.route.doneEarnings': 'Earnings for this route',
  'drv.route.doneStops': 'Stops',
  'drv.route.doneTime': 'Time',
  'drv.route.viewHistory': 'View history',
} as const
