import type { Translation } from '@loong/i18n'
import type { enDrvApp } from '../en/app'

export const idDrvApp: Translation<typeof enDrvApp> = {
  // kerangka (DriverLayout)
  'drv.app.titleApp': 'Driver',
  'drv.app.navLabel': 'Utama',
  'drv.app.navHome': 'Beranda',
  'drv.app.navHistory': 'Riwayat',
  'drv.app.navEarnings': 'Pendapatan',
  'drv.app.navProfile': 'Profil',
  'drv.app.titleRoute': 'Rute',
  'drv.app.titleStop': 'Stop',
  'drv.app.offline': 'Anda offline. Aksi baru dikirim setelah tersambung lagi.',

  // umum (dipakai lebih dari satu layar)
  'drv.app.backHome': 'Kembali ke beranda',
  'drv.app.arrived': 'Sudah tiba',
  'drv.app.minutesLeft': '{minutes|duration} lagi',
  'drv.app.nextPickup': 'Berikutnya · Ambil',
  'drv.app.nextDropoff': 'Berikutnya · Antar',
  'drv.app.landmark': 'Patokan: {note}',
  'drv.app.toastArrived': 'Kedatangan dicatat. Pelanggan sudah diberi tahu.',
  'drv.app.stopsDone_other': '{done} dari {count} stop selesai',

  // masuk
  'drv.app.loginDocTitle': 'Masuk',
  'drv.app.loginTitle': 'Masuk sebagai driver',
  'drv.app.loginIntro': 'Gunakan nomor HP dan PIN dari admin Anda.',
  'drv.app.loginPhone': 'Nomor HP',
  'drv.app.loginPhoneError': 'Isi nomor HP yang valid, mis. 0812-3456-7801.',
  'drv.app.loginPin': 'PIN',
  'drv.app.loginPinHint': '6 angka',
  'drv.app.loginPinError': 'PIN terdiri dari 6 angka.',
  'drv.app.loginSubmit': 'Masuk',
  'drv.app.loginHelp': 'Akun driver dibuat oleh admin. Lupa PIN? Hubungi dispatcher Anda.',
  'drv.app.loginDemo': '<strong>Mencoba demo?</strong> Isi otomatis dengan akun {label}.',

  // halaman tidak ditemukan
  'drv.app.notFoundTitle': 'Halaman tidak ditemukan',
  'drv.app.notFoundHint': 'Alamat yang Anda buka tidak ada atau sudah dipindahkan.',
}
