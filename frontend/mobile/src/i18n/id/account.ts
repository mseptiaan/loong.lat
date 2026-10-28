import type { Translation } from '@loong/i18n'
import type { enDrvAccount } from '../en/account'

export const idDrvAccount: Translation<typeof enDrvAccount> = {
  'drv.account.title': 'Profil',
  'drv.account.stats_other': '{count} rute · sejak {joined|date}',

  'drv.account.vehicle': 'Kendaraan',
  'drv.account.vehicleType': 'Jenis',
  'drv.account.vehicleModel': 'Model',
  'drv.account.vehiclePlate': 'Plat',
  'drv.account.vehicleLoad': 'Muatan maksimal',
  'drv.account.vehicleLoadValue': '{kg|kg} · {volume|float} m³',
  'drv.account.vehicleLargest': 'Barang terbesar',
  'drv.account.vehicleNone': 'Belum ada kendaraan yang dipasangkan. Hubungi admin agar bisa menerima tugas.',

  'drv.account.documents': 'Dokumen',
  'drv.account.documentsHint': 'Diverifikasi oleh admin saat pendaftaran.',
  'drv.account.docLicense': 'SIM C',
  'drv.account.docRegistration': 'STNK kendaraan',
  'drv.account.verified': 'Terverifikasi',

  'drv.account.keepAwake': 'Layar tetap menyala',
  'drv.account.keepAwakeHint': 'Selama rute berjalan, agar navigasi dan konfirmasi tidak terputus. Memakai baterai lebih banyak.',
  'drv.account.language': 'Bahasa',
  'drv.account.signOut': 'Keluar',
}
