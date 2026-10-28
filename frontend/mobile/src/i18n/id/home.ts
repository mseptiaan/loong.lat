import type { Translation } from '@loong/i18n'
import type { enDrvHome } from '../en/home'

export const idDrvHome: Translation<typeof enDrvHome> = {
  // Beranda
  'drv.home.greeting': 'Halo, {name}',
  'drv.home.jobs': 'Tugas',
  'drv.home.statusError': 'Status belum bisa dimuat',
  'drv.home.statusWorking': 'Sedang bertugas',
  'drv.home.statusOnline': 'Online',
  'drv.home.statusOffline': 'Offline',
  'drv.home.statusWorkingHint': 'Selesaikan tugas Anda untuk mengubah status.',
  'drv.home.statusOnlineHint': 'Terlihat oleh dispatcher dan siap menerima tugas.',
  'drv.home.statusOfflineHint': 'Aktifkan agar bisa menerima tugas baru.',
  'drv.home.toastOnline': 'Anda online. Tugas baru akan masuk ke sini.',
  'drv.home.toastOffline': 'Anda offline.',
  'drv.home.toastFinishFirst': 'Selesaikan tugas aktif dulu sebelum mengubah status.',
  'drv.home.waitingTitle': 'Menunggu tugas',
  'drv.home.waitingHint': 'Anda online. Tugas baru dari dispatcher akan muncul di sini dan Anda akan mendapat notifikasi.',
  'drv.home.offlineTitle': 'Anda sedang offline',
  'drv.home.offlineHint': 'Aktifkan status online di atas untuk mulai menerima tugas.',
  'drv.home.today': 'Hari ini',
  'drv.home.todaySummary_other': '{amount|rupiah} · {count} rute selesai',
  'drv.home.todayError': 'Belum bisa dimuat',

  // kartu rute berjalan
  'drv.home.activeRoute': 'Rute berjalan · {code}',
  'drv.home.progressLabel': 'Kemajuan rute',
  'drv.home.continueRoute': 'Lanjutkan rute',

  // kartu tugas baru
  'drv.home.newJob': 'Tugas baru · {code} · {vehicle}',
  'drv.home.jobSummary_other': '{count} stop · {km|km}',
  'drv.home.estimate': 'estimasi',
  'drv.home.pickupAt': 'Ambil di <strong>{name}</strong>',
  'drv.home.scheduled': 'Dijadwalkan {when}',
  'drv.home.leavingNow': 'Berangkat sekarang, tiba sekitar {time|time}',
  'drv.home.viewJob': 'Lihat tugas',

  // Pendapatan
  'drv.home.earningsLast7': 'Pendapatan 7 hari terakhir',
  'drv.home.dayEarnings_other': '{amount|rupiah}, {count} rute',
  'drv.home.routesDone_other': '{count} rute selesai',
  'drv.home.last7': '7 hari terakhir',
  'drv.home.routeCount_other': '{count} rute',
  'drv.home.perDay': 'Per hari',
  'drv.home.barsNote': 'Angka di atas batang dalam ribuan rupiah.',
  'drv.home.earningsNote': 'Pendapatan adalah 70% dari tarif tiap rute; sisanya biaya platform.',

  // Riwayat
  'drv.home.historyIntro': 'Rute yang sudah Anda selesaikan.',
  'drv.home.historyEmptyTitle': 'Belum ada riwayat',
  'drv.home.historyEmptyHint': 'Rute yang selesai akan tercatat di sini beserta pendapatannya.',
  'drv.home.historyLine_other': 'Selesai {time|time} · {count} stop · {km|km} · {duration|duration}',
}
