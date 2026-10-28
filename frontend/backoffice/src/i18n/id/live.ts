import type { Translation } from '@loong/i18n'
import type { enBoLive } from '../en/live'

export const idBoLive: Translation<typeof enBoLive> = {
  'bo.live.mapLabel': 'Peta langsung: posisi driver dan rute aktif',
  'bo.live.title': 'Rute aktif',
  'bo.live.routeCount_other': '{count} rute',
  'bo.live.readyCount_other': '{count} driver siap',
  'bo.live.loading': 'Memuat…',
  'bo.live.emptyTitle': 'Tidak ada rute aktif',
  'bo.live.emptyHint': 'Rute muncul saat driver ditugaskan atau mulai berangkat.',
  'bo.live.readyTitle': 'Siap menerima tugas',
  'bo.live.noDriver': 'Belum ada driver',
  'bo.live.headingTo': 'Menuju {place} · {min|duration}',
  'bo.live.headingToArrived': 'Menuju {place} · sudah tiba',
  'bo.live.progress': '{done}/{total} stop',
  'bo.live.delayed': 'Terlambat {min|duration}',
  'bo.live.problemStop': 'Ada stop bermasalah',
  'bo.live.closeCard': 'Tutup kartu rute',
  'bo.live.kindPickup': 'Ambil',
  'bo.live.kindDropoff': 'Antar',
  'bo.live.nextEta': 'Berikutnya: <stop>{kind} · {place}</stop> · tiba sekitar {time|time}',
  'bo.live.nextScheduled': 'Berikutnya: <stop>{kind} · {place}</stop> · dijadwalkan {time|time}',
  'bo.live.openPlan': 'Buka plan',
  'bo.live.callDriver': 'Telepon driver',
}
