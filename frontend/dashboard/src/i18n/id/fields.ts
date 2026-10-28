import type { Translation } from '@loong/i18n'
import type { enDashFields } from '../en/fields'

export const idDashFields: Translation<typeof enDashFields> = {
  // lib: forms, schedule, money
  'dash.fields.scheduleMissing': 'Pilih tanggal dan jam pengambilan.',
  'dash.fields.scheduleSoon': 'Pilih jadwal minimal 20 menit dari sekarang.',
  'dash.fields.scheduleFar': 'Jadwal maksimal 14 hari ke depan.',
  'dash.fields.remainExpired': 'habis',
  'dash.fields.remainLessThanMin': 'kurang dari 1 mnt',

  // DeadlineField
  'dash.fields.deadlineSwitch': 'Harus tiba sebelum jam tertentu',
  'dash.fields.deadlineHint': 'Hanya untuk pengiriman ini. Opsi yang tidak sempat tiba akan disingkirkan bila ada yang memenuhi.',
  'dash.fields.deadlineToday': 'Paling lambat tiba (WIB, hari ini)',
  'dash.fields.deadlineOn': 'Paling lambat tiba (WIB, {date|date})',

  // ScheduleFields
  'dash.fields.whenPickup': 'Kapan paket diambil?',
  'dash.fields.pickupTime': 'Waktu pengambilan',
  'dash.fields.modeNow': 'Sekarang',
  'dash.fields.modeLater': 'Jadwalkan',
  'dash.fields.nowNote': 'Driver terdekat akan dicarikan begitu pesanan dikonfirmasi, biasanya berangkat dalam 5-10 menit.',
  'dash.fields.date': 'Tanggal',
  'dash.fields.startTime': 'Jam mulai ambil (WIB)',
  'dash.fields.arrivalWindow': '{day} · driver tiba antara {from} dan {to}.',

  // PlaceField
  'dash.fields.placeholder': 'Cari mal, stasiun, kampus, atau alamat',
  'dash.fields.placeResults': 'Hasil pencarian {label}',
  'dash.fields.savedAddresses': 'Alamat tersimpan',
  'dash.fields.searching': 'Mencari…',
  'dash.fields.noResults': 'Tidak ada hasil untuk “{query}”. Coba nama mal atau stasiun terdekat, atau tunjuk di peta.',
  'dash.fields.pickOnMap': 'Pilih di peta',
  'dash.fields.mapTitle': 'Pilih {label} di peta',

  // PlaceMapPicker
  'dash.fields.mapPoint': 'Titik di peta',
  'dash.fields.mapHint': 'Ketuk peta atau geser pin ke lokasi yang tepat.',
  'dash.fields.cancel': 'Batal',
  'dash.fields.useLocation': 'Gunakan lokasi ini',
  'dash.fields.noLocation': 'Belum ada lokasi dipilih.',

  // ItemEditor
  'dash.fields.quickAdd': 'Tambah cepat',
  'dash.fields.manual': 'Isi manual',
  'dash.fields.itemName': 'Nama barang',
  'dash.fields.itemExample': 'mis. Kardus berisi baju',
  'dash.fields.quantity': 'Jumlah',
  'dash.fields.qtyOf': 'jumlah {name}',
  'dash.fields.removeItem': 'Hapus {name}',
  'dash.fields.itemFallback': 'barang',
  'dash.fields.unitWeight': 'Berat satuan',
  'dash.fields.length': 'Panjang',
  'dash.fields.width': 'Lebar',
  'dash.fields.height': 'Tinggi',
  'dash.fields.fragile': 'Mudah pecah',
  'dash.fields.fromPhoto': 'Dari foto · yakin {pct|pct}',
  'dash.fields.fromPhotoCheck': 'Dari foto · periksa, yakin {pct|pct}',
  'dash.fields.fromVoice': 'Dari suara · yakin {pct|pct}',
  'dash.fields.fromVoiceCheck': 'Dari suara · periksa, yakin {pct|pct}',
  'dash.fields.itemsKg_other': '{count} barang · {kg|kg}',
  'dash.fields.fitsMotor': 'Muat di motor',
  'dash.fields.needsVan': 'Perlu blindvan',
  'dash.fields.needsVanWhy': 'Perlu blindvan: {why}',
  'dash.fields.overCapacity': 'Melebihi kapasitas semua kendaraan',
}
