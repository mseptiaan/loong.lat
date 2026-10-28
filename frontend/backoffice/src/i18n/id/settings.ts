import type { Translation } from '@loong/i18n'
import type { enBoSettings } from '../en/settings'

export const idBoSettings: Translation<typeof enBoSettings> = {
  'bo.settings.title': 'Pengaturan',
  'bo.settings.description': 'Penugasan driver dan fitur AI berlaku untuk semua pengguna; tampilan dan notifikasi tersimpan di perangkat ini.',

  'bo.settings.displayTitle': 'Tampilan',
  'bo.settings.displayDescription': 'Kepadatan menentukan tinggi baris tabel dan kontrol.',
  'bo.settings.densityLabel': 'Kepadatan tampilan',
  'bo.settings.densityCompact': 'Rapat',
  'bo.settings.densityComfortable': 'Nyaman',
  'bo.settings.densityHint': 'Rapat memuat lebih banyak baris untuk layar besar; Nyaman lebih lega untuk layar kecil atau sentuh.',

  'bo.settings.languageTitle': 'Bahasa',
  'bo.settings.languageDescription': 'Bahasa aplikasi ini. Pilihan tersimpan di perangkat ini.',

  'bo.settings.dispatchTitle': 'Penugasan driver',
  'bo.settings.dispatchDescription': 'Berlaku untuk pesanan baru dan yang sedang menunggu. Penugasan memakai aturan dari engine kita, bukan AI.',
  'bo.settings.dispatchAutoLabel': 'Tugaskan driver otomatis',
  'bo.settings.dispatchAutoDescription':
    'Pesanan sekarang: driver terdekat yang online, kosong, dan muat (radius 15 km). Pesanan terjadwal: driver yang kosong pada jamnya, diutamakan rating tinggi dan beban hari itu yang ringan. Bila belum ada yang cocok, plan menunggu dan sistem mencoba lagi.',
  'bo.settings.dispatchManualNote': 'Anda tetap bisa menugaskan atau mengganti driver secara manual dari antrean dan detail plan, baik penugasan otomatis menyala maupun mati.',
  'bo.settings.toastAutoOn': 'Penugasan otomatis dinyalakan. Pesanan yang menunggu langsung dicarikan driver.',
  'bo.settings.toastAutoOff': 'Penugasan otomatis dimatikan. Tugaskan driver secara manual.',

  'bo.settings.aiTitle': 'Fitur AI untuk pelanggan',
  'bo.settings.aiDescription': 'AI hanya mengusulkan; tidak ada yang berubah tanpa tindakan pengguna. Bila dimatikan, pelanggan memakai isian manual.',
  'bo.settings.aiVisionLabel': 'Baca foto barang',
  'bo.settings.aiVisionDescription': 'Pelanggan bisa memotret barang untuk mengisi daftar barang. Hasilnya selalu diperiksa pelanggan sebelum dipakai.',
  'bo.settings.aiVoiceLabel': 'Input suara',
  'bo.settings.aiVoiceDescription': 'Pelanggan bisa menyebutkan barang dengan suara; ucapan diurai menjadi daftar barang.',
  'bo.settings.aiIntentLabel': 'Pahami kebutuhan',
  'bo.settings.aiIntentDescription': 'Kalimat bebas seperti “paling hemat tapi sebelum jam 5” diurai menjadi prioritas dan batas waktu.',
  'bo.settings.aiCopilotLabel': 'Copilot dan ringkasan',
  'bo.settings.aiCopilotDescription': 'Penjelasan opsi rute, tanya jawab status plan, saran driver, dan ringkasan analitik.',
  'bo.settings.toastAiSaved': 'Pengaturan AI disimpan. Berlaku untuk semua pengguna.',

  'bo.settings.notifyTitle': 'Notifikasi',
  'bo.settings.notifyDescription': 'Apa yang muncul di lonceng dan perlu Anda perhatikan.',
  'bo.settings.notifyUnassignedLabel': 'Plan menunggu driver',
  'bo.settings.notifyUnassignedDescription': 'Beri tahu bila ada plan yang belum punya driver lebih dari 5 menit.',
  'bo.settings.notifyAnomalyLabel': 'Anomali di lapangan',
  'bo.settings.notifyAnomalyDescription': 'Driver terlambat 10 menit atau lebih, atau melaporkan masalah di lokasi.',
  'bo.settings.notifyDailyLabel': 'Ringkasan harian',
  'bo.settings.notifyDailyDescription': 'Jumlah plan, omzet, dan penghematan kemarin.',
}
