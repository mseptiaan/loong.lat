/** ui.*: kit UI (@loong/ui). */
export const enUi = {
  // dialog, sheet, confirmation
  'ui.dialog.close': 'Close',
  'ui.dialog.cancel': 'Cancel',
  'ui.dialog.defaultTitle': 'Dialog',
  'ui.sheet.defaultTitle': 'Panel',
  'ui.dialog.typeToConfirm': 'Type “{value}” to continue',

  // form controls
  'ui.select.placeholder': 'Select…',
  'ui.field.optional': '(optional)',
  'ui.password.noun': 'password',
  'ui.password.show': 'Show {noun}',
  'ui.password.hide': 'Hide {noun}',
  'ui.stepper.decrease': 'Decrease {label}',
  'ui.stepper.increase': 'Increase {label}',

  // loading, error, empty
  'ui.spinner.loading': 'Loading',
  'ui.error.title': 'Couldn’t load the data',
  'ui.error.message': 'Please try again in a moment.',
  'ui.error.offlineTitle': 'No connection',
  'ui.error.offlineMessage': 'Check your network. Anything you’ve entered is still safe.',
  'ui.error.retry': 'Try again',

  // command palette
  'ui.command.placeholder': 'Search plans, users, drivers, or actions…',
  'ui.command.label': 'Command palette',
  'ui.command.empty': 'No results. Try a plan code, a customer name, or a driver name.',
  'ui.command.select': 'select',
  'ui.command.open': 'open',

  // map
  'ui.map.helpWindows': 'Hold Ctrl and scroll to zoom the map',
  'ui.map.helpMac': 'Hold ⌘ and scroll to zoom the map',
  'ui.map.helpMobile': 'Use two fingers to move the map',
  'ui.map.routeLabel': 'Delivery route map',
  'ui.map.routesAttribution': 'Routes © Mapbox',
  'ui.map.attributionToggle': 'Map attribution',
  'ui.stopPin.pickup': 'Pickup stop {index}',
  'ui.stopPin.dropoff': 'Drop-off stop {index}',
  'ui.stopPin.pickupRange': 'Pickup stops {from} to {to}',
  'ui.stopPin.dropoffRange': 'Drop-off stops {from} to {to}',
  'ui.driverDot.available': '{label}, available',

  // AI tool trace
  'ui.toolTrace.seconds': '{sec|decimal} s',
  'ui.toolTrace.running': 'running',
  'ui.toolTrace.failed': 'failed',

  // stop list
  'ui.stopList.label': 'Stop order',
  'ui.stopList.pickup': 'Pick up · {place}',
  'ui.stopList.dropoff': 'Drop off · {place}',
  'ui.stopList.pickupMany_other': 'Pick up {count} parcels · {place}',
  'ui.stopList.dropoffMany_other': 'Drop off {count} parcels · {place}',

  // route option card
  'ui.route.recommended': 'Recommended',
  'ui.route.optionLabel': '{label}, {cost|rupiah}',
  'ui.route.saving': 'Save {cost|rupiah} ({pct|pct}) vs. sending separately',
  'ui.route.pricier': '{extra|rupiah} more than sending separately',
  'ui.route.pricierFaster': '{extra|rupiah} more than sending separately, but parcels arrive {faster|duration} sooner on average',
  'ui.route.sameCost': 'Same cost as sending separately',
  'ui.route.distance': 'Distance',
  'ui.route.finishesIn': 'Finishes in',
  'ui.route.avgArrival': 'Avg. parcel arrival',
  'ui.route.compare': 'Compare: sending separately {cost|rupiah}, parcels arrive in {avg|duration} on average.',
  'ui.route.motorbikes_one': '{count} motorbike',
  'ui.route.motorbikes_other': '{count} motorbikes',
  'ui.route.blindVans_one': '{count} blind van',
  'ui.route.blindVans_other': '{count} blind vans',
  'ui.route.stops_one': '{count} stop',
  'ui.route.stops_other': '{count} stops',

  // fare breakdown
  'ui.fare.routeTitle': 'Route {n} · {vehicle}',
  'ui.fare.total': 'Total',

  // token sheet (design QA page)
  'ui.tokens.eyebrow': 'loong.lat · token sheet · {density} density',
  'ui.tokens.intro': 'Shared visual foundation: warm neutrals, a single ocean-blue accent, large radii, soft shadows.',
  'ui.tokens.colorTitle': 'Color',
  'ui.tokens.neutrals': 'Neutrals',
  'ui.tokens.accent': 'Accent (ocean blue)',
  'ui.tokens.status': 'Status (dots and indicators only)',
  'ui.tokens.typographyTitle': 'Typography',
  'ui.tokens.sampleDisplay': 'Ship for less, arrive sooner',
  'ui.tokens.sampleHeading': 'Choose a delivery option',
  'ui.tokens.sampleTitle': 'Pick up in Kemang, drop off in Senayan',
  'ui.tokens.sampleBody': 'Body text. Saves 22% compared with sending separately. The driver arrives around {time|time} WIB.',
  'ui.tokens.sampleSmall': 'Secondary text: 3 items · {weight|kg} · fits on a motorbike',
  'ui.tokens.sampleCaption': 'Small caption · Updated {ago}',
  'ui.tokens.sampleNumbers': '{a|number} · {b|decimal}% · {c|time} · {d|km} · {e|number}',
  'ui.tokens.shapeTitle': 'Shape & depth',
  'ui.tokens.shapeInput': 'input 10px',
  'ui.tokens.shapeCard': 'card 14px',
  'ui.tokens.shapeModal': 'modal 20px',
  'ui.tokens.mapTitle': 'Map (MapLibre + OpenFreeMap)',
} as const
