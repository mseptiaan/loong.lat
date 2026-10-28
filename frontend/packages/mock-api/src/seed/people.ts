/** Admin tunggal untuk demo (kredensial di DEMO_CREDENTIALS). */
export const ADMIN = {
  id: 'admin-1',
  name: 'Rani Wulandari',
  email: 'admin@loong.lat',
  password: 'admin1234',
} as const

/** Semua driver seed memakai PIN yang sama. */
export const DRIVER_PIN = '123456'

export interface PersonSeed {
  name: string
  phone: string
}

/** Pengirim. Pengguna pertama adalah akun demo utama. */
export const CUSTOMERS: readonly (PersonSeed & { email: string })[] = [
  { name: 'Dina Pramesti', email: 'dina@contoh.id', phone: '081311220001' },
  { name: 'Fajar Nugroho', email: 'fajar.nugroho@contoh.id', phone: '081311220002' },
  { name: 'Sari Anggraini', email: 'sari.anggraini@contoh.id', phone: '081311220003' },
  { name: 'Bagas Pratama', email: 'bagas.pratama@contoh.id', phone: '081311220004' },
  { name: 'Maya Kusumawati', email: 'maya.kusumawati@contoh.id', phone: '081311220005' },
  { name: 'Rizky Maulana', email: 'rizky.maulana@contoh.id', phone: '081311220006' },
  { name: 'Putri Lestari', email: 'putri.lestari@contoh.id', phone: '081311220007' },
  { name: 'Andi Wijaya', email: 'andi.wijaya@contoh.id', phone: '081311220008' },
  { name: 'Citra Dewi', email: 'citra.dewi@contoh.id', phone: '081311220009' },
  { name: 'Hendra Saputra', email: 'hendra.saputra@contoh.id', phone: '081311220010' },
  { name: 'Nadia Rahma', email: 'nadia.rahma@contoh.id', phone: '081311220011' },
  { name: 'Yoga Permana', email: 'yoga.permana@contoh.id', phone: '081311220012' },
]

/** Penerima/kontak di lokasi, dipakai bergantian di seed. */
export const CONTACTS: readonly PersonSeed[] = [
  { name: 'Ibu Ratna', phone: '081290010001' },
  { name: 'Pak Joko', phone: '081290010002' },
  { name: 'Mbak Wulan', phone: '081290010003' },
  { name: 'Pak Dedi', phone: '081290010004' },
  { name: 'Kak Tasya', phone: '081290010005' },
  { name: 'Pak Surya', phone: '081290010006' },
  { name: 'Ibu Lilis', phone: '081290010007' },
  { name: 'Mas Arif', phone: '081290010008' },
  { name: 'Bu Marlina', phone: '081290010009' },
  { name: 'Pak Teguh', phone: '081290010010' },
]

/**
 * Driver. Indeks menentukan kendaraan di seed (lihat seed/index.ts):
 * 0-4 motor, 5-7 blindvan, 8 belum punya kendaraan. Driver pertama = akun demo (0812-3456-7801).
 */
export const DRIVERS: readonly PersonSeed[] = [
  { name: 'Budi Santoso', phone: '081234567801' },
  { name: 'Agus Setiawan', phone: '081234567802' },
  { name: 'Dewi Kusuma', phone: '081234567803' },
  { name: 'Eko Prasetyo', phone: '081234567804' },
  { name: 'Fitri Handayani', phone: '081234567805' },
  { name: 'Gilang Ramadhan', phone: '081234567806' },
  { name: 'Hasan Basri', phone: '081234567807' },
  { name: 'Indra Gunawan', phone: '081234567808' },
  { name: 'Joko Susilo', phone: '081234567809' },
]
