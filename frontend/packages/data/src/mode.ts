/**
 * Mode klien yang dibangun: `VITE_API_MODE=http` berarti aplikasi bicara ke backend asli (`@loong/http-api`), selain itu ke
 * backend tiruan di browser. Dibaca saat build, jadi bagian layar yang hanya masuk akal untuk salah satunya bisa
 * dibuang dari bundel, bukan disembunyikan sesudah dimuat.
 */
export const REAL_BACKEND: boolean = import.meta.env.VITE_API_MODE === 'http'

/**
 * Tombol "simulasikan pembayaran" untuk top up. Selalu ada di backend tiruan. Di backend asli hanya bila backend dijalankan
 * dengan SIMULATE_PAYMENTS=true dan klien dibangun dengan `VITE_SIMULATE_PAYMENTS=1` (skrip `npm run dev:http` menyalakannya);
 * tanpa itu, top up hanya lunas lewat konfirmasi penyedia pembayaran.
 */
export const SIMULATED_PAYMENTS: boolean = !REAL_BACKEND || import.meta.env.VITE_SIMULATE_PAYMENTS === '1'
