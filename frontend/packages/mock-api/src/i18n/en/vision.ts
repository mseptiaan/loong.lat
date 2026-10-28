/** ai.vision.* dan ai.compute.*: label progres (baca foto dan hitung opsi) serta ringkasan hasil baca foto. */
export const enVision = {
  'ai.vision.step1': 'Reading the photo',
  'ai.vision.step2': 'Recognizing items',
  'ai.vision.step3': 'Estimating weight and size',
  'ai.vision.step4': 'Matching against the item list',
  'ai.vision.types_one': '{count} type of item',
  'ai.vision.types_other': '{count} types of items',
  'ai.vision.pieces_one': '{count} piece',
  'ai.vision.pieces_other': '{count} pieces',
  'ai.vision.summary': 'Detected {types} ({pieces}).',
  'ai.vision.lowConfidence_one': '{count} of them is low-confidence; check it before adding.',
  'ai.vision.lowConfidence_other': '{count} of them are low-confidence; check them before adding.',

  'ai.compute.readPoints_one': 'Reading {count} location',
  'ai.compute.readPoints_other': 'Reading {count} locations',
  'ai.compute.measureRoads': 'Measuring distances along real roads',
  'ai.compute.estimateDistances': 'Calculating distances and travel times between points',
  'ai.compute.combine_one': 'Building vehicle combinations for {count} shipment',
  'ai.compute.combine_other': 'Building vehicle combinations for {count} shipments',
  'ai.compute.compare': 'Comparing cost, distance, and time',
  'ai.compute.drawRoutes': 'Drawing routes on the roads',
} as const
