import type { Translation } from '@loong/i18n'
import type { enInsights } from '../en/insights'

export const idInsights: Translation<typeof enInsights> = {
  /* ---------- anomali ---------- */
  'ai.anomaly.late': '**{code}** terlambat {minutes|duration}',
  'ai.anomaly.problem': '**{code}** ada stop bermasalah',
  'ai.anomaly.both': '**{code}** terlambat {minutes|duration} dan ada stop bermasalah',

  /* ---------- apa yang perlu dikerjakan ---------- */
  'ai.ops.readQueue': 'Membaca antrean',
  'ai.ops.readQueueDetail': 'plan aktif: {active}\nmenunggu driver: {waiting}',
  'ai.ops.activePlans_other': '{count} plan aktif',
  'ai.ops.checkAnomalies': 'Memeriksa anomali',
  'ai.ops.none': 'tidak ada',
  'ai.ops.anomalies_other': '{count} anomali',
  'ai.ops.checkDrivers': 'Memeriksa ketersediaan driver',
  'ai.ops.checkDriversDetail': 'online tanpa tugas: {idle}',
  'ai.ops.driversReady_other': '{count} driver siap',
  'ai.ops.allClear': 'Tidak ada yang mendesak sekarang. Semua plan aktif punya driver dan berjalan sesuai jadwal.',
  'ai.ops.intro': 'Urutan yang saya sarankan:',
  'ai.ops.contactDriver': '{anomaly}. Hubungi driver dan pastikan kondisinya.',
  'ai.ops.waitingWithIdle': '**{code}** menunggu driver {waited|duration}. Buka dan terapkan saran driver terbaik.',
  'ai.ops.waitingNoIdle': '**{code}** menunggu driver {waited|duration}. Tidak ada driver siap; hubungi driver offline atau tambah driver.',

  /* ---------- ringkasan 7 hari ---------- */
  'ai.insights.title': '**Ringkasan 7 hari terakhir**',
  'ai.insights.created': '**{plans}** dibuat, nilai pesanan **{revenue|rupiah}**.',
  'ai.insights.createdSame': '**{plans}** dibuat, sama dengan 7 hari sebelumnya, nilai pesanan **{revenue|rupiah}**.',
  'ai.insights.createdUp': '**{plans}** dibuat, naik {pct|pct} dari 7 hari sebelumnya, nilai pesanan **{revenue|rupiah}**.',
  'ai.insights.createdDown': '**{plans}** dibuat, turun {pct|pct} dari 7 hari sebelumnya, nilai pesanan **{revenue|rupiah}**.',
  'ai.insights.saved': 'Penggabungan rute menghemat pelanggan **{saved|rupiah}** ({pct|pct} dari biaya kirim terpisah).',
  'ai.insights.clean': '**{clean} dari {done}** plan yang selesai berjalan tanpa kendala ({pct|pct}).',
  'ai.insights.peak': 'Pesanan paling ramai masuk sekitar **{from}-{to}** ({plans}).',
  'ai.insights.attention': '**Perlu perhatian**',
  'ai.insights.attentionNone': 'Tidak ada anomali atau antrean yang menumpuk saat ini.',
  'ai.insights.waitingNoIdle': '{plans} menunggu driver, dan tidak ada driver yang siap menerima tugas.',
  'ai.insights.waitingIdle': '{plans} menunggu driver; {drivers} siap menerima tugas.',
  'ai.insights.drivers_other': '{count} driver',
  'ai.insights.advice': '**Saran**',
  'ai.insights.adviceNoDrivers': 'Tidak ada driver siap saat ada plan menunggu: hubungi driver offline atau aktifkan driver tambahan.',
  'ai.insights.adviceLate': 'Hubungi driver yang terlambat lebih dulu; keterlambatan di atas 20 menit sebaiknya diberi tahu ke penerima.',
  'ai.insights.advicePeak': 'Pastikan driver online sebelum {hour}, jam pesanan paling ramai.',
  'ai.insights.footer': 'Ringkasan ini dibuat otomatis dari data plan dan tidak mengubah data apa pun.',
  'ai.insights.readPlans': 'Membaca plan',
  'ai.insights.readPlansDetail': 'jendela: 14 hari\ntotal: {total}\n7 hari terakhir: {current}\n7 hari sebelumnya: {previous}',
  'ai.insights.calcSavings': 'Menghitung penghematan penggabungan',
  'ai.insights.calcSavingsDetail': 'hemat: {saved|rupiah}\nomzet: {revenue|rupiah}',
  'ai.insights.checkQueue': 'Memeriksa anomali dan antrean',
  'ai.insights.checkQueueDetail': 'anomali: {anomalies}\nmenunggu driver: {waiting}\ndriver siap: {idle}',
  'ai.insights.findPeak': 'Mencari jam tersibuk',
  'ai.insights.noData': 'tidak ada data',
}
