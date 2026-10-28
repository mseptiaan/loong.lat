import type { Translation } from '../../../types'
import type { enSrvProof } from '../../en/srv/proof'

export const idSrvProof: Translation<typeof enSrvProof> = {
  'srv.proof.summary.seated':
    'Seorang pria lanjut usia berkaus putih tanpa lengan duduk di bangku rendah di depan dinding batako, memegang benda kecil berwarna gelap dengan kedua tangan sambil melihatnya. Hanya dia yang terlihat di foto.',
  'srv.proof.summary.holding': 'Seseorang mengangkat paket berkardus di depannya sambil menghadap kamera. Seluruh kardus terlihat di dalam foto.',
  'srv.proof.summary.atDoor': 'Sebuah paket berkardus tergeletak di lantai di samping pintu tertutup. Tidak ada orang di dalam foto.',
  'srv.proof.recipient.visible': 'Satu orang terlihat jelas dan menghadap kamera.',
  'srv.proof.recipient.none': 'Tidak ada orang yang terlihat, jadi serah-terima ke penerima tidak dapat dipastikan.',
  'srv.proof.parcel.held': 'Paket berkardus diangkat dan terlihat utuh di dalam foto.',
  'srv.proof.parcel.atDoor': 'Paket berkardus terlihat jelas di samping pintu.',
  'srv.proof.parcel.unclearItem': 'Ia memegang benda kecil berwarna gelap; bisa jadi paket atau ponsel, dan tidak ada label yang terbaca.',
  'srv.proof.quality.good': 'Tajam dan cukup terang; tidak ada bagian yang terpotong.',
}
