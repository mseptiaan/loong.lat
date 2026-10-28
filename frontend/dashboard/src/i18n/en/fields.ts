/** dash.fields.* */
export const enDashFields = {
  // lib: forms, schedule, money
  'dash.fields.scheduleMissing': 'Choose a pickup date and time.',
  'dash.fields.scheduleSoon': 'Choose a time at least 20 minutes from now.',
  'dash.fields.scheduleFar': 'You can schedule up to 14 days ahead.',
  'dash.fields.remainExpired': 'expired',
  'dash.fields.remainLessThanMin': 'less than 1 min',

  // DeadlineField
  'dash.fields.deadlineSwitch': 'Must arrive before a set time',
  'dash.fields.deadlineHint': 'Applies to this shipment only. Options that can’t make it in time are dropped when others can.',
  'dash.fields.deadlineToday': 'Latest arrival (WIB, today)',
  'dash.fields.deadlineOn': 'Latest arrival (WIB, {date|date})',

  // ScheduleFields
  'dash.fields.whenPickup': 'When should the parcel be picked up?',
  'dash.fields.pickupTime': 'Pickup time',
  'dash.fields.modeNow': 'Now',
  'dash.fields.modeLater': 'Schedule',
  'dash.fields.nowNote': 'We’ll find the nearest driver as soon as the order is confirmed; they usually set off within 5-10 minutes.',
  'dash.fields.date': 'Date',
  'dash.fields.startTime': 'Pickup start time (WIB)',
  'dash.fields.arrivalWindow': '{day} · the driver arrives between {from} and {to}.',

  // PlaceField
  'dash.fields.placeholder': 'Search for a mall, station, campus, or address',
  'dash.fields.placeResults': 'Search results: {label}',
  'dash.fields.savedAddresses': 'Saved addresses',
  'dash.fields.searching': 'Searching…',
  'dash.fields.noResults': 'No results for “{query}”. Try the name of a nearby mall or station, or pick on the map.',
  'dash.fields.pickOnMap': 'Pick on map',
  'dash.fields.mapTitle': 'Choose {label} on the map',

  // PlaceMapPicker
  'dash.fields.mapPoint': 'Point on the map',
  'dash.fields.mapHint': 'Tap the map or drag the pin to the exact spot.',
  'dash.fields.cancel': 'Cancel',
  'dash.fields.useLocation': 'Use this location',
  'dash.fields.noLocation': 'No location selected yet.',

  // ItemEditor
  'dash.fields.quickAdd': 'Quick add',
  'dash.fields.manual': 'Enter manually',
  'dash.fields.itemName': 'Item name',
  'dash.fields.itemExample': 'e.g. Box of clothes',
  'dash.fields.quantity': 'Quantity',
  'dash.fields.qtyOf': 'quantity of {name}',
  'dash.fields.removeItem': 'Remove {name}',
  'dash.fields.itemFallback': 'item',
  'dash.fields.unitWeight': 'Unit weight',
  'dash.fields.length': 'Length',
  'dash.fields.width': 'Width',
  'dash.fields.height': 'Height',
  'dash.fields.fragile': 'Fragile',
  'dash.fields.fromPhoto': 'From photo · {pct|pct} confidence',
  'dash.fields.fromPhotoCheck': 'From photo · check it, {pct|pct} confidence',
  'dash.fields.fromVoice': 'From voice · {pct|pct} confidence',
  'dash.fields.fromVoiceCheck': 'From voice · check it, {pct|pct} confidence',
  'dash.fields.itemsKg_one': '{count} item · {kg|kg}',
  'dash.fields.itemsKg_other': '{count} items · {kg|kg}',
  'dash.fields.fitsMotor': 'Fits on a motorbike',
  'dash.fields.needsVan': 'Needs a blind van',
  'dash.fields.needsVanWhy': 'Needs a blind van: {why}',
  'dash.fields.overCapacity': 'Exceeds the capacity of all vehicles',
} as const
