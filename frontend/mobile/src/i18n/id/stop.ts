import type { Translation } from '@loong/i18n'
import type { enDrvStop } from '../en/stop'

export const idDrvStop: Translation<typeof enDrvStop> = {
  // SwipeToConfirm
  'drv.stop.swipeBusy': 'Memproses…',
  'drv.stop.swipeConfirmHint': 'Ketuk sekali lagi untuk konfirmasi',
  'drv.stop.swipeAria': '{label}. Geser ke kanan, ketuk dua kali, atau tekan Enter.',

  // kepala dan peringatan
  'drv.stop.back': 'Kembali ke rute',
  'drv.stop.heading': 'Stop {seq} dari {total}',
  'drv.stop.notStartedNotice': 'Mulai rute dulu sebelum mengerjakan stop.',
  'drv.stop.notActiveNotice': 'Stop ini belum aktif. Selesaikan stop {seq} dulu.',
  'drv.stop.notFoundTitle': 'Stop tidak ditemukan',
  'drv.stop.notFoundHint': 'Tugas ini mungkin sudah selesai atau dialihkan.',

  // lokasi dan kontak
  'drv.stop.scheduledPickup': 'Ambil · jadwal {time|time}',
  'drv.stop.scheduledDropoff': 'Antar · jadwal {time|time}',
  'drv.stop.openNavigation': 'Buka navigasi',
  'drv.stop.senderContact': 'Kontak pengirim',
  'drv.stop.recipientContact': 'Kontak penerima',
  'drv.stop.call': 'Telepon',
  'drv.stop.whatsappMessage': 'Halo {contact}, saya driver loong.lat untuk {code}. Saya sudah dekat ke {place}.',

  // barang
  'drv.stop.itemsPickup': 'Barang yang diambil',
  'drv.stop.itemsDropoff': 'Barang yang diantar',
  'drv.stop.itemsCheck': 'Periksa jumlah dan kondisi sebelum dibawa.',
  'drv.stop.fragile': 'Mudah pecah, tangani dengan hati-hati',

  // bukti
  'drv.stop.photoPreview': 'Pratinjau foto bukti',
  'drv.stop.photoRemove': 'Hapus foto',
  'drv.stop.pickupPhotoTitle': 'Foto barang',
  'drv.stop.pickupPhotoHint': 'Opsional. Foto barang saat diambil membantu bila ada klaim kerusakan.',
  'drv.stop.pickupPhotoButton': 'Ambil foto barang',
  'drv.stop.proofTitle': 'Bukti antar',
  'drv.stop.proofPhotoLabel': 'Foto bukti antar',
  'drv.stop.proofPhotoHint': 'Foto barang di lokasi tujuan, atau bersama penerima.',
  'drv.stop.proofPhotoButton': 'Ambil foto bukti',
  'drv.stop.otpLabel': 'Kode OTP penerima',
  'drv.stop.otpHint': 'Minta kode 4 angka dari penerima.',
  'drv.stop.demoFill': '<strong>Mode demo:</strong> kode penerima {code}. Ketuk untuk mengisi.',
  'drv.stop.useOtp': 'Pakai kode OTP saja',
  'drv.stop.usePhoto': 'Penerima tidak punya kode? Pakai foto bukti',
  'drv.stop.photoReadError': 'Foto ini tidak bisa dibaca. Ambil ulang.',
  'drv.stop.demoSamplePhoto': '<strong>Mode demo:</strong> pakai foto contoh penerima.',
  'drv.stop.proofNeedPhoto': 'Ambil foto bukti antar dulu.',
  'drv.stop.proofNeedOtp': 'Masukkan kode OTP 4 angka dari penerima.',

  // stop sudah ditutup
  'drv.stop.closedDoneTitle': 'Stop selesai',
  'drv.stop.closedIssueTitle': 'Stop ditandai bermasalah',
  'drv.stop.closedDoneAt': 'Diselesaikan pukul {time}.',
  'drv.stop.closedIssueFallback': 'Ada kendala di lokasi.',

  // laporan masalah
  'drv.stop.reportIssue': 'Lapor masalah di stop ini',
  'drv.stop.issueTitle': 'Lapor masalah',
  'drv.stop.issueHint': 'Stop ini ditandai bermasalah dan rute lanjut ke stop berikutnya. Admin dan pelanggan diberi tahu.',
  'drv.stop.issueSend': 'Kirim laporan',
  'drv.stop.issueKind': 'Jenis masalah',
  'drv.stop.issueKindError': 'Pilih jenis masalahnya.',
  'drv.stop.issueNote': 'Catatan',
  'drv.stop.issueNoteError': 'Jelaskan masalahnya singkat.',
  'drv.stop.issueNotePlaceholder': 'mis. Sudah ditelepon 3 kali, tidak diangkat.',

  // setelah stop ditutup (toast)
  'drv.stop.donePickup': 'Pengambilan selesai.',
  'drv.stop.doneDropoff': 'Pengantaran selesai.',
  'drv.stop.issueSent': 'Laporan terkirim. Admin dan pelanggan diberi tahu.',
  'drv.stop.doneNext': '{message} Lanjut ke {name}.',

  // aksi bawah
  'drv.stop.swipeArrive': 'Geser untuk tiba di lokasi',
  'drv.stop.swipePickedUp': 'Geser jika barang sudah diambil',
  'drv.stop.swipeReceived': 'Geser jika barang sudah diterima',
}
