import type { Translation } from '@loong/i18n'
import type { enDashHome } from '../en/home'

export const idDashHome: Translation<typeof enDashHome> = {
  'dash.home.greeting': 'Halo, {name}',
  'dash.home.greetingAnon': 'Halo, Anda',
  'dash.home.lead': 'Mau kirim apa hari ini? Tambahkan beberapa paket sekaligus agar rutenya bisa digabung dan lebih hemat.',
  'dash.home.welcomeTitle': 'Akun Anda siap',
  'dash.home.welcomeText': 'Selamat datang! Coba buat pengiriman pertama, lengkap dengan perbandingan rute.',

  'dash.home.activeTitle': 'Pengiriman aktif',
  'dash.home.activeEmptyTitle': 'Belum ada pengiriman aktif',
  'dash.home.activeEmptyHint': 'Pengiriman yang sedang berjalan muncul di sini lengkap dengan peta dan perkiraan tiba.',
  'dash.home.draftsTitle': 'Lanjutkan draft',
  'dash.home.recentTitle': 'Pengiriman terakhir',
  'dash.home.viewAll': 'Lihat semua',
  'dash.home.recentEmptyTitle': 'Belum ada riwayat',
  'dash.home.recentEmptyHint': 'Pengiriman Anda yang sudah dikonfirmasi akan tampil di sini.',

  'dash.home.summaryLabel': 'Ringkasan',
  'dash.home.savingsTitle': 'Penghematan',
  'dash.home.savingsEmptyTitle': 'Belum ada data',
  'dash.home.savingsEmptyHint': 'Hemat dari penggabungan rute dihitung setelah pengiriman pertama Anda selesai.',
  'dash.home.savedMonth': 'Hemat bulan ini',
  'dash.home.savedMonthHint_other': 'dari {count} pengiriman selesai, dibanding kirim terpisah',
  'dash.home.savedMonthNone': 'Belum ada pengiriman selesai bulan ini',
  'dash.home.completedCount': 'Pengiriman selesai',
  'dash.home.avgSavings': 'Rata-rata hemat',
  'dash.home.avgSavingsHint': 'dari semua pengiriman selesai',

  'dash.home.onboardTitle': 'Mulai dalam 3 langkah',
  'dash.home.onboardProgress': '{done} dari {total} selesai',
  'dash.home.onboardDismiss': 'Tutup panduan',
  'dash.home.stepFirst': 'Buat pengiriman pertama',
  'dash.home.stepAddress': 'Simpan alamat favorit (rumah, toko)',
  'dash.home.stepTrack': 'Lacak pengiriman sampai selesai',
}
