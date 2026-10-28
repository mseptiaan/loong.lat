import type { Translation } from '@loong/i18n'
import type { enCopilot } from '../en/copilot'

export const idCopilot: Translation<typeof enCopilot> = {
  /* ---------- pembantu bersama ---------- */
  'ai.driverFallback': 'Driver',
  'ai.plans_other': '{count} plan',
  'ai.stops_other': '{count} stop',
  'ai.optionsTarget_other': '{count} opsi',

  /* ---------- tindakan yang diusulkan ---------- */
  'ai.action.chooseOption': 'Pilih {label}',
  'ai.action.applyBestDriver': 'Terapkan saran driver terbaik',
  'ai.action.open': 'Buka {code}',

  /* ---------- pertanyaan awal ---------- */
  'ai.starter.whyRecommended': 'Kenapa opsi ini yang disarankan?',
  'ai.starter.compareAll': 'Bandingkan semua opsi',
  'ai.starter.faster': 'Bagaimana kalau saya ingin lebih cepat?',
  'ai.starter.whyOnlyOne': 'Kenapa hanya ada satu opsi?',
  'ai.starter.costBreakdown': 'Rincian biaya',
  'ai.starter.whatToDo': 'Apa yang perlu saya lakukan?',
  'ai.starter.bestDriver': 'Siapa driver terbaik?',
  'ai.starter.whyLate': 'Kenapa terlambat?',
  'ai.starter.whenArrive': 'Kapan paket sampai?',

  /* ---------- jejak alat umum ---------- */
  'ai.tool.readPlan': 'Membaca plan',
  'ai.tool.readOptions': 'Membaca opsi rute',
  'ai.tool.optionCount': 'opsi: {count}',
  'ai.tool.statusOnly': 'status: {status}',
  'ai.tool.planRoutes': 'plan: {code}\nrute: {count}',
  'ai.route.head': '**Rute {n}**',
  'ai.route.headVehicle': '**Rute {n}** ({vehicle})',

  /* ---------- menjelaskan opsi ---------- */
  'ai.copilot.explain.recommended': '**{label}** adalah opsi yang saya sarankan untuk prioritas **{priority}**.',
  'ai.copilot.explain.chosen': '**{label}** adalah opsi yang Anda pilih untuk prioritas **{priority}**.',
  'ai.copilot.explain.summary': 'Ringkasnya {distance|km} dengan {vehicles}; paket tiba rata-rata {avg|duration} setelah berangkat.',
  'ai.copilot.explain.faster': 'Bila waktu lebih penting: **{label}** tiba rata-rata {minutes|duration} lebih cepat, dengan tambahan biaya {extra|rupiah}.',
  'ai.copilot.explain.cheaper': 'Bila biaya lebih penting: **{label}** {saving|rupiah} lebih murah, tetapi paket tiba rata-rata {minutes|duration} lebih lama.',
  'ai.copilot.explain.readPlanDetail': 'plan: {code}\nprioritas: {priority}\npengiriman: {count}',
  'ai.copilot.explain.readOptionsDetail': 'opsi: {options}\nbaseline: {cost|rupiah} · {distance|km}',
  'ai.copilot.explain.traceReasons': 'Menelusuri alasan',

  /* ---------- membandingkan opsi ---------- */
  'ai.copilot.compare.single': 'Hanya ada **satu** opsi yang masuk akal: **{label}**, {cost|rupiah}, {distance|km}, selesai dalam {duration|duration}.',
  'ai.copilot.compare.singleHint': 'Dengan satu pengiriman tidak ada yang bisa digabung atau dibagi. Tambahkan pengiriman lain untuk melihat perbandingan.',
  'ai.copilot.compare.singleDetail': 'opsi: 1\n{label}',
  'ai.copilot.compare.header': '| Opsi | Biaya | Jarak | Selesai | Tiba rata-rata | Kendaraan |\n| --- | --- | --- | --- | --- | --- |',
  'ai.copilot.compare.chosen': '(dipilih)',
  'ai.copilot.compare.summary': 'Paling murah: **{cheapest}**. Paling pendek: **{shortest}**. Paket tiba paling cepat: **{fastest}**.',
  'ai.copilot.compare.compareTitle': 'Membandingkan biaya, jarak, dan waktu',
  'ai.copilot.compare.compareDetail': 'termurah: {cheapest}\nterpendek: {shortest}\ntercepat: {fastest}',

  /* ---------- paling cepat, murah, pendek ---------- */
  'ai.copilot.extreme.sortBy.cepat': 'Mengurutkan menurut tiba rata-rata',
  'ai.copilot.extreme.sortBy.murah': 'Mengurutkan menurut biaya',
  'ai.copilot.extreme.sortBy.pendek': 'Mengurutkan menurut jarak',
  'ai.copilot.extreme.alreadyBest.cepat': 'Opsi yang dipilih, **{label}**, sudah yang paling cepat: tiba rata-rata **{value|duration}**.',
  'ai.copilot.extreme.alreadyBest.murah': 'Opsi yang dipilih, **{label}**, sudah yang paling murah: biaya **{value|rupiah}**.',
  'ai.copilot.extreme.alreadyBest.pendek': 'Opsi yang dipilih, **{label}**, sudah yang paling pendek: jarak **{value|km}**.',
  'ai.copilot.extreme.best.cepat': 'Yang paling cepat adalah **{best}**: {gain}, dengan {price} dibanding **{current}**.',
  'ai.copilot.extreme.best.murah': 'Yang paling murah adalah **{best}**: {gain}, dengan {price} dibanding **{current}**.',
  'ai.copilot.extreme.best.pendek': 'Yang paling pendek adalah **{best}**: {gain}, dengan {price} dibanding **{current}**.',
  'ai.copilot.extreme.numbers': 'Angka lengkapnya: {cost|rupiah}, {distance|km}, selesai dalam {duration|duration}.',
  'ai.copilot.extreme.gain.cepat': 'paket tiba rata-rata **{value|duration}** lebih cepat',
  'ai.copilot.extreme.gain.pendek': 'jarak **{value|km}** lebih pendek',
  'ai.copilot.extreme.gain.murah': '**{value|rupiah}** lebih murah',
  'ai.copilot.extreme.price.later': 'waktu tiba rata-rata {value|duration} lebih lama',
  'ai.copilot.extreme.price.sooner': 'waktu tiba rata-rata {value|duration} lebih singkat',
  'ai.copilot.extreme.price.more': 'biaya {value|rupiah} lebih mahal',
  'ai.copilot.extreme.price.less': 'biaya {value|rupiah} lebih murah',
  'ai.copilot.extreme.price.same': 'biaya sama',

  /* ---------- status dan perkiraan tiba ---------- */
  'ai.copilot.status.cancelled': '{code} dibatalkan.',
  'ai.copilot.status.cancelledReason': '{code} dibatalkan: {reason}',
  'ai.copilot.status.readPlanDetail': 'plan: {code}\nstatus: {status}\nrute: {count}',
  'ai.copilot.status.doneAt': 'selesai pukul {time|time}, {stops}.',
  'ai.copilot.status.done': 'selesai, {stops}.',
  'ai.copilot.status.recalcEta': 'Menghitung ulang perkiraan tiba',
  'ai.copilot.status.recalcDetail': 'rute: {route}\nstop selesai: {done}/{total}\nETA terakhir: {eta}',
  'ai.copilot.status.progress_other': '{done} dari {count} stop selesai',
  'ai.copilot.status.runningNamed': '**{driver}** sedang di jalan, {progress}.',
  'ai.copilot.status.runningUnnamed': 'Driver sedang di jalan, {progress}.',
  'ai.copilot.status.nextPickup': 'Berikutnya mengambil di **{place}**, tiba sekitar **{time|time}**.',
  'ai.copilot.status.nextDropoff': 'Berikutnya mengantar ke **{place}**, tiba sekitar **{time|time}**.',
  'ai.copilot.status.allDone': 'Semua stop selesai sekitar {time|time}.',
  'ai.copilot.status.assigned': '**{driver}** sudah ditugaskan dan akan berangkat sekitar {time|time}.',
  'ai.copilot.status.searching': 'masih dicarikan driver.',
  'ai.copilot.status.bestCandidate': 'Kandidat terbaik: **{driver}** ({distance|km} dari titik ambil).',

  /* ---------- keterlambatan dan masalah ---------- */
  'ai.copilot.late.checkSchedule': 'Memeriksa jadwal vs posisi driver',
  'ai.copilot.late.checkDetail': 'rute: {route}\nketerlambatan: {delay|duration}\nstop berikutnya: {stop}',
  'ai.copilot.late.routeN': 'Rute {n}',
  'ai.copilot.late.thisRoute': 'Rute ini',
  'ai.copilot.late.delayed_other': '{label} terlambat **{count} menit**.',
  'ai.copilot.late.delayedEta_other': '{label} terlambat **{count} menit**; tiba di {stop} sekitar {eta|time} (semula {planned|time}).',
  'ai.copilot.late.problemStop': 'Stop {seq} di **{place}** ditandai bermasalah.',
  'ai.copilot.late.problemStopIssue': 'Stop {seq} di **{place}** ditandai bermasalah: {issue}.',
  'ai.copilot.late.adviceDriver': 'Hubungi **{driver}** ({phone|phone}) untuk memastikan kondisinya. Bila terlambat lebih dari 20 menit, hubungi penerima untuk menggeser jadwal.',
  'ai.copilot.late.adviceStop': 'Tindak lanjuti stop {seq}: hubungi {contact} ({phone|phone}) dan atur pengantaran ulang.',
  'ai.copilot.late.none': 'Tidak ada keterlambatan atau masalah pada plan ini. Semua stop sesuai jadwal.',
  'ai.copilot.late.adviceCustomer': 'Anda tidak perlu melakukan apa pun. Perkiraan tiba sudah dihitung ulang dan Anda akan diberi tahu bila ada perubahan lagi.',

  /* ---------- driver ---------- */
  'ai.copilot.driver.assigned_other': '**{driver}**, {vehicle}, rating {rating|decimal2} dari {count|number} rute.',
  'ai.copilot.driver.rateCandidates': 'Menilai kandidat driver',
  'ai.copilot.driver.candidateLine': '{driver}: skor {score}, {distance|km}',
  'ai.copilot.driver.noCandidates': 'tidak ada kandidat',
  'ai.copilot.driver.candidates_other': '{count} kandidat',
  'ai.copilot.driver.suggest': 'belum ada driver. Saran saya **{driver}** ({plate}): {reasons}.',
  'ai.copilot.driver.weak': 'belum ada driver, dan kandidat yang tersedia kurang cocok. Yang terbaik saat ini **{driver}** ({plate}): {reason}. Pertimbangkan menunggu driver lain online.',
  'ai.copilot.driver.noneAdmin': 'belum ada driver; tidak ada kandidat yang tersedia sekarang.',
  'ai.copilot.driver.noneCustomer': 'belum ada driver; kami sedang mencarikan yang terdekat.',

  /* ---------- biaya ---------- */
  'ai.copilot.cost.readFare': 'Membaca rincian tarif',
  'ai.copilot.cost.readFareDetail': 'rute: {count}\ntotal: {total|rupiah}',
  'ai.copilot.cost.subtotal': 'Subtotal: **{amount|rupiah}**',
  'ai.copilot.cost.total': 'Total **{amount|rupiah}**.',

  /* ---------- di luar kemampuan dan keadaan khusus ---------- */
  'ai.copilot.help.intro': 'Saya bisa membantu menjelaskan pilihan rute, membandingkan opsi, memeriksa status dan keterlambatan, serta menjelaskan biaya. Pertanyaan itu belum bisa saya jawab dari data plan ini.',
  'ai.copilot.help.tryAsking': 'Coba tanyakan:',
  'ai.copilot.noOptions.detail': 'plan: {code}\nopsi: belum dihitung',
  'ai.copilot.noOptions.text': 'Opsi rute untuk plan ini belum dihitung. Selesaikan langkah sebelumnya, lalu tanyakan lagi.',
  'ai.copilot.notConfirmed.text': 'Plan ini belum dikonfirmasi, jadi belum ada driver atau rute yang berjalan. Konfirmasi dulu untuk melihat status pengirimannya.',
}
