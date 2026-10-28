import type { Translation } from '../../types'
import type { enUi } from '../en/ui'

export const idUi: Translation<typeof enUi> = {
  // dialog, sheet, confirmation
  'ui.dialog.close': 'Tutup',
  'ui.dialog.cancel': 'Batal',
  'ui.dialog.defaultTitle': 'Dialog',
  'ui.sheet.defaultTitle': 'Panel',
  'ui.dialog.typeToConfirm': 'Ketik “{value}” untuk melanjutkan',

  // form controls
  'ui.select.placeholder': 'Pilih…',
  'ui.field.optional': '(opsional)',
  'ui.password.noun': 'kata sandi',
  'ui.password.show': 'Tampilkan {noun}',
  'ui.password.hide': 'Sembunyikan {noun}',
  'ui.stepper.decrease': 'Kurangi {label}',
  'ui.stepper.increase': 'Tambah {label}',

  // loading, error, empty
  'ui.spinner.loading': 'Memuat',
  'ui.error.title': 'Data belum bisa dimuat',
  'ui.error.message': 'Coba lagi sebentar lagi.',
  'ui.error.offlineTitle': 'Tidak ada koneksi',
  'ui.error.offlineMessage': 'Periksa jaringan Anda. Data yang sudah Anda isi tetap aman.',
  'ui.error.retry': 'Coba lagi',

  // command palette
  'ui.command.placeholder': 'Cari plan, user, driver, atau aksi…',
  'ui.command.label': 'Palet perintah',
  'ui.command.empty': 'Tidak ada hasil. Coba kode plan, nama pelanggan, atau nama driver.',
  'ui.command.select': 'pilih',
  'ui.command.open': 'buka',

  // map
  'ui.map.helpWindows': 'Tahan Ctrl dan gulir untuk memperbesar peta',
  'ui.map.helpMac': 'Tahan ⌘ dan gulir untuk memperbesar peta',
  'ui.map.helpMobile': 'Geser dengan dua jari untuk menggerakkan peta',
  'ui.map.routeLabel': 'Peta rute pengiriman',
  'ui.map.routesAttribution': 'Rute © Mapbox',
  'ui.map.attributionToggle': 'Atribusi peta',
  'ui.stopPin.pickup': 'Ambil stop {index}',
  'ui.stopPin.dropoff': 'Antar stop {index}',
  'ui.stopPin.pickupRange': 'Ambil stop {from} sampai {to}',
  'ui.stopPin.dropoffRange': 'Antar stop {from} sampai {to}',
  'ui.driverDot.available': '{label}, tersedia',

  // AI tool trace
  'ui.toolTrace.seconds': '{sec|decimal} dtk',
  'ui.toolTrace.running': 'sedang berjalan',
  'ui.toolTrace.failed': 'gagal',

  // stop list
  'ui.stopList.label': 'Urutan stop',
  'ui.stopList.pickup': 'Ambil · {place}',
  'ui.stopList.dropoff': 'Antar · {place}',
  'ui.stopList.pickupMany_other': 'Ambil {count} paket · {place}',
  'ui.stopList.dropoffMany_other': 'Antar {count} paket · {place}',

  // route option card
  'ui.route.recommended': 'Disarankan',
  'ui.route.optionLabel': '{label}, {cost|rupiah}',
  'ui.route.saving': 'Hemat {cost|rupiah} ({pct|pct}) dibanding kirim terpisah',
  'ui.route.pricier': '{extra|rupiah} lebih mahal dari kirim terpisah',
  'ui.route.pricierFaster': '{extra|rupiah} lebih mahal dari kirim terpisah, tapi paket tiba rata-rata {faster|duration} lebih cepat',
  'ui.route.sameCost': 'Sama dengan biaya kirim terpisah',
  'ui.route.distance': 'Jarak',
  'ui.route.finishesIn': 'Selesai dalam',
  'ui.route.avgArrival': 'Paket tiba rata-rata',
  'ui.route.compare': 'Bandingkan: kirim terpisah {cost|rupiah}, paket tiba rata-rata {avg|duration}.',
  'ui.route.motorbikes_other': '{count} motor',
  'ui.route.blindVans_other': '{count} blindvan',
  'ui.route.stops_other': '{count} stop',

  // fare breakdown
  'ui.fare.routeTitle': 'Rute {n} · {vehicle}',
  'ui.fare.total': 'Total',

  // token sheet (design QA page)
  'ui.tokens.eyebrow': 'loong.lat · lembar token · kepadatan {density}',
  'ui.tokens.intro': 'Fondasi visual bersama: netral hangat, satu aksen biru laut, radius besar, bayangan lembut.',
  'ui.tokens.colorTitle': 'Warna',
  'ui.tokens.neutrals': 'Netral',
  'ui.tokens.accent': 'Aksen (biru laut)',
  'ui.tokens.status': 'Status (hanya untuk titik/indikator)',
  'ui.tokens.typographyTitle': 'Tipografi',
  'ui.tokens.sampleDisplay': 'Kirim lebih hemat, sampai lebih cepat',
  'ui.tokens.sampleHeading': 'Pilih opsi pengiriman',
  'ui.tokens.sampleTitle': 'Ambil di Kemang, antar ke Senayan',
  'ui.tokens.sampleBody': 'Teks isi. Hemat 22% dibanding kirim terpisah. Driver tiba sekitar {time|time} WIB.',
  'ui.tokens.sampleSmall': 'Teks sekunder: 3 barang · {weight|kg} · muat di motor',
  'ui.tokens.sampleCaption': 'Keterangan kecil · Diperbarui {ago}',
  'ui.tokens.sampleNumbers': '{a|number} · {b|decimal}% · {c|time} · {d|km} · {e|number}',
  'ui.tokens.shapeTitle': 'Bentuk & kedalaman',
  'ui.tokens.shapeInput': 'input 10px',
  'ui.tokens.shapeCard': 'kartu 14px',
  'ui.tokens.shapeModal': 'modal 20px',
  'ui.tokens.mapTitle': 'Peta (MapLibre + OpenFreeMap)',
}
