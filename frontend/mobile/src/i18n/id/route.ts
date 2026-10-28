import type { Translation } from '@loong/i18n'
import type { enDrvRoute } from '../en/route'

export const idDrvRoute: Translation<typeof enDrvRoute> = {
  // kartu stop berikutnya
  'drv.route.nextStopLabel': 'Stop berikutnya',
  'drv.route.navigate': 'Navigasi',
  'drv.route.stopDetails': 'Detail stop',

  // halaman rute
  'drv.route.mapLabel': 'Peta rute. Padanan teks ada di tampilan Daftar.',
  'drv.route.factDistance': 'Jarak',
  'drv.route.factDuration': 'Selesai dalam',
  'drv.route.factWeight': 'Berat',
  'drv.route.toastStarted': 'Rute dimulai. Hati-hati di jalan.',
  'drv.route.notFoundTitle': 'Tugas tidak ditemukan',
  'drv.route.notFoundHint': 'Rute ini mungkin sudah selesai, dialihkan ke driver lain, atau dibatalkan oleh admin.',
  'drv.route.headerRunning_other': '{done} dari {count} stop selesai · {vehicle} · {km|km}',
  'drv.route.headerIdle_other': '{count} stop · {vehicle} · {km|km}',
  'drv.route.view': 'Tampilan rute',
  'drv.route.viewList': 'Daftar',
  'drv.route.viewMap': 'Peta',
  'drv.route.estimatedEarnings': 'Estimasi pendapatan',
  'drv.route.departs': 'Berangkat {time|time}',
  'drv.route.stopOrder': 'Urutan stop',
  'drv.route.start': 'Mulai rute',
  'drv.route.startTo': 'Mulai rute · ke {name}',
  'drv.route.swipeArrive': 'Geser untuk tiba',
  'drv.route.completePickup': 'Selesaikan pengambilan',
  'drv.route.completeDropoff': 'Selesaikan pengantaran',

  // rute selesai
  'drv.route.doneMissingTitle': 'Ringkasan tidak ditemukan',
  'drv.route.doneMissingHint': 'Rute ini belum tercatat selesai.',
  'drv.route.doneTitle': 'Rute selesai',
  'drv.route.doneThanks': 'Semua stop di rute Anda untuk {code} sudah selesai. Terima kasih, kerja bagus.',
  'drv.route.doneEarnings': 'Pendapatan rute ini',
  'drv.route.doneStops': 'Stop',
  'drv.route.doneTime': 'Waktu',
  'drv.route.viewHistory': 'Lihat riwayat',
}
