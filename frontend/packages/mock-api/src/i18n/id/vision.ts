import type { Translation } from '@loong/i18n'
import type { enVision } from '../en/vision'

export const idVision: Translation<typeof enVision> = {
  'ai.vision.step1': 'Membaca foto',
  'ai.vision.step2': 'Mengenali barang',
  'ai.vision.step3': 'Memperkirakan berat dan ukuran',
  'ai.vision.step4': 'Mencocokkan dengan daftar barang',
  'ai.vision.types_other': '{count} jenis barang',
  'ai.vision.pieces_other': '{count} buah',
  'ai.vision.summary': 'Terdeteksi {types} ({pieces}).',
  'ai.vision.lowConfidence_other': '{count} di antaranya kurang yakin; periksa dulu sebelum menambahkan.',

  'ai.compute.readPoints_other': 'Membaca {count} titik lokasi',
  'ai.compute.measureRoads': 'Mengukur jarak lewat jalan sungguhan',
  'ai.compute.estimateDistances': 'Menghitung jarak dan waktu tempuh antar titik',
  'ai.compute.combine_other': 'Menyusun kombinasi kendaraan untuk {count} pengiriman',
  'ai.compute.compare': 'Membandingkan biaya, jarak, dan waktu',
  'ai.compute.drawRoutes': 'Menggambar rute di jalan',
}
