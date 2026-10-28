import type { Translation } from '@loong/i18n'
import type { enDashApp } from '../en/app'

export const idDashApp: Translation<typeof enDashApp> = {
  'dash.app.titleApp': 'Pengirim',
  'dash.app.titleDeliveryDetail': 'Detail pengiriman',
  'dash.app.titleBalance': 'Saldo',

  'dash.app.navHome': 'Beranda',
  'dash.app.navDeliveries': 'Pengiriman',
  'dash.app.navAddresses': 'Alamat',
  'dash.app.navAccount': 'Akun',
  'dash.app.mainNav': 'Utama',
  'dash.app.homeLink': 'loong.lat, ke beranda',
  'dash.app.newDelivery': 'Buat pengiriman',

  'dash.app.accountMenu': 'Menu akun',
  'dash.app.menuMyAccount': 'Akun saya',
  'dash.app.menuBalance': 'Saldo dan top up',
  'dash.app.menuAddresses': 'Alamat tersimpan',
  'dash.app.signOut': 'Keluar',

  'dash.app.authLoginLink': 'loong.lat, ke halaman masuk',
  'dash.app.authTitleSignUp': 'Daftar',
  'dash.app.authTitleForgot': 'Lupa kata sandi',
  'dash.app.authTitleSignIn': 'Masuk',
  'dash.app.authProofLabel': 'Rata-rata penghematan per pengiriman gabungan',
  'dash.app.authProofText': 'dibanding mengirim tiap paket sendiri-sendiri, dari {count|number} pengiriman bulan lalu di Jabodetabek.',
  'dash.app.authProofLabelLive': 'Diukur pada optimizer kami sendiri',
  'dash.app.authProofTextLive': 'pengiriman per kuotasi diselesaikan dengan pencarian lengkap dan ditandai terbukti optimal.',
  'dash.app.authTrust': 'Alamat dan lokasi Anda hanya dipakai untuk menghitung rute.',

  'dash.app.notFoundTitle': 'Halaman tidak ditemukan',
  'dash.app.notFoundHint': 'Alamat yang Anda buka tidak ada atau sudah dipindahkan.',
  'dash.app.notFoundBack': 'Kembali ke beranda',

  'dash.app.errEmail': 'Isi email yang valid, mis. nama@contoh.id.',
  'dash.app.errPhone': 'Nomor HP belum lengkap. Isi minimal 9 digit.',
  'dash.app.emailPlaceholder': 'nama@contoh.id',
}
