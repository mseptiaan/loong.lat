/**
 * srv.engine.*: teks dari mesin hitung tiruan (mock-api/engine): galat validasi masukan, rincian tarif, label dan
 * alasan opsi. Disimpan sebagai `Msg` dengan parameter mentah; angka dan jam diformat di sini lewat `{x|rupiah}` dst.
 */
export const enSrvEngine = {
  // validasi masukan dan galat hitung
  'srv.engine.needShipment': 'Add at least one shipment.',
  'srv.engine.pickupMissing': 'Shipment {n}: pickup location not selected.',
  'srv.engine.dropoffMissing': 'Shipment {n}: drop-off location not selected.',
  'srv.engine.itemsMissing': 'Shipment {n}: add at least one item.',
  'srv.engine.itemIncomplete': 'Shipment {n}: complete the name, quantity, weight, and size of "{item}".',
  'srv.engine.itemFallback': 'item',
  'srv.engine.noVehicles': 'No active vehicles. Contact an admin to add vehicles to the fleet.',
  'srv.engine.tooBig': 'An item exceeds the capacity of every vehicle (weight, volume, or size).',
  'srv.engine.unavailable': 'The route optimizer is busy right now. Please try again in a moment.',

  // ringkasan barang pada stop (`RouteStop.itemsSummary`)
  'srv.engine.itemsSummary_one': '{count} item · {kg|kg}',
  'srv.engine.itemsSummary_other': '{count} items · {kg|kg}',

  // rincian tarif
  'srv.engine.fare.base': 'Base fare',
  'srv.engine.fare.distance': 'Distance {km|km}',
  'srv.engine.fare.time': 'Time {min|duration}',
  'srv.engine.fare.extraStops_one': '{count} extra stop',
  'srv.engine.fare.extraStops_other': '{count} extra stops',
  'srv.engine.fare.weight': 'Load {kg|kg} (over {over|kg})',
  'srv.engine.fare.rounding': 'Rounding',

  // label dan ringkasan opsi
  'srv.engine.label.best': 'Best route',
  'srv.engine.label.two': '{a} · {b}',
  'srv.engine.label.three': '{a} · {b} · {c}',
  'srv.engine.fleet.motor_one': '{count} motorbike',
  'srv.engine.fleet.motor_other': '{count} motorbikes',
  'srv.engine.fleet.blindvan_one': '{count} blind van',
  'srv.engine.fleet.blindvan_other': '{count} blind vans',
  'srv.engine.fleet.both': '{motor} + {van}',
  'srv.engine.headline_one': '{fleet} · {count} stop · arrives {time|time}',
  'srv.engine.headline_other': '{fleet} · {count} stops · arrives {time|time}',

  // alasan opsi
  'srv.engine.reason.route': 'Route {n}: {text}',
  'srv.engine.reason.vanDim': 'Using a blind van because the largest item ({dim} cm) exceeds the motorbike limit ({max} cm).',
  'srv.engine.reason.vanWeight': 'Using a blind van because the load ({kg|kg}) exceeds the motorbike limit ({max|kg}).',
  'srv.engine.reason.vanVolume': 'Using a blind van because the volume ({m3|decimal2} m³) exceeds the motorbike limit.',
  'srv.engine.reason.fitsMotor_one': '{count} item ({kg|kg}) fits on a motorbike.',
  'srv.engine.reason.fitsMotor_other': '{count} items ({kg|kg}) fit on a motorbike.',
  'srv.engine.reason.fitsVan_one': '{count} item ({kg|kg}) fits in a blind van.',
  'srv.engine.reason.fitsVan_other': '{count} items ({kg|kg}) fit in a blind van.',
  'srv.engine.reason.merge_one': 'Combining {shipments} shipments into {count} route: {km|km} versus {base|km} if sent separately ({pct}% shorter).',
  'srv.engine.reason.merge_other': 'Combining {shipments} shipments into {count} routes: {km|km} versus {base|km} if sent separately ({pct}% shorter).',
  'srv.engine.reason.costSaves': 'Cost {cost|rupiah}: saves {saving|rupiah} ({pct}%) compared with sending separately.',
  'srv.engine.reason.costMore': 'Cost {cost|rupiah}: {extra|rupiah} more than sending separately because the load is split across several vehicles to be faster.',
  'srv.engine.reason.costSame': 'Cost {cost|rupiah}, the same as sending separately.',
  'srv.engine.reason.provenOptimal_one': 'Proven optimal: every way to serve this shipment was checked.',
  'srv.engine.reason.provenOptimal_other': 'Proven optimal: every way to serve these {count} shipments was checked.',
  'srv.engine.reason.split': 'Split across {count} vehicles: longest trip {duration|duration}.',
  'srv.engine.reason.splitVs': 'Split across {count} vehicles: longest trip {duration|duration} (compared with {single|duration} using 1 vehicle).',
  'srv.engine.reason.single': 'The trip takes {duration|duration} with 1 vehicle.',
  'srv.engine.reason.pickupsFirst': 'All items are picked up first, then dropped off in order (shortest distance); parcels arrive {avg|duration} after departure on average.',
  'srv.engine.reason.interleaved': 'Drop-offs are interleaved with pickups so parcels arrive sooner: {avg|duration} after departure on average.',
  'srv.engine.reason.rush': 'Departing at {time|time} (rush hour): average speed drops by about {pct}%.',
  'srv.engine.reason.order': 'Stop order: {steps}.',
  'srv.engine.reason.orderRoute1': 'Stop order (route 1): {steps}.',
  'srv.engine.reason.deadlineLate_one': 'Deadline missed: {rows}.',
  'srv.engine.reason.deadlineLate_other': 'Deadlines missed: {rows}.',
  'srv.engine.reason.deadlineMet_one': 'Deadline met: {rows}.',
  'srv.engine.reason.deadlineMet_other': 'Deadlines met: {rows}.',

  // potongan penyusun alasan (disambung sebagai Msg bersarang, bukan string)
  'srv.engine.seq.pickup': 'Pick up {place}',
  'srv.engine.seq.dropoff': 'Drop off {place}',
  'srv.engine.seq.then': '{a} → {b}',
  'srv.engine.seq.more': '{a} → …',
  'srv.engine.deadline.row': '{place} arrives {arrive|time} (deadline {due|time})',
  'srv.engine.deadline.rowDated': '{place} arrives {arrive|date} {arrive|time} (deadline {due|date} {due|time})',
  'srv.engine.deadline.join': '{a}; {b}',

  // catatan pencarian di dalam tahap 3 (`progress.msg`): tingkat usaha "thorough" dan "reviewed". `count` = `n`, diisi layar untuk jamak.
  'srv.engine.search.experiments_one': 'Trying {n} way of grouping the stops',
  'srv.engine.search.experiments_other': 'Trying {n} ways of grouping the stops',
  'srv.engine.search.polish': 'Polishing the best routes',
  'srv.engine.search.round': 'Review round {round}',
  'srv.engine.search.reviewAI': 'The AI reviewer is checking the routes',
  'srv.engine.search.reviewRules': 'Checking the routes against the review rules',
  'srv.engine.search.satisfied': 'The review is satisfied',
  'srv.engine.search.more_one': 'The review asks for {n} more attempt',
  'srv.engine.search.more_other': 'The review asks for {n} more attempts',
} as const
