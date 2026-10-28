import type { Translation } from '../../../types'
import type { enSrvRpc } from '../../en/srv/rpc'

export const idSrvRpc: Translation<typeof enSrvRpc> = {
  'srv.rpc.unreachable': 'Backend tiruan ({origin}) tidak dapat dimuat. Pastikan alamat itu bisa dibuka dan dev server berjalan.',
  'srv.rpc.serverUnreachable': 'Server ({origin}) belum bisa dihubungi. Periksa koneksi internet Anda lalu coba lagi.',
  'srv.rpc.timeout': 'Backend tiruan tidak merespons. Coba lagi.',
  'srv.rpc.closed': 'Koneksi ke backend tiruan ditutup.',
  'srv.rpc.internal': 'Terjadi kesalahan di backend tiruan: {detail}',
  'srv.rpc.unknownMethod': 'Metode tidak dikenal: {method}',
  'srv.rpc.notMethod': 'Bukan metode: {method}',
  'srv.rpc.badRequest': 'Permintaan tidak bisa dibaca. Periksa isinya lalu coba lagi.',
  'srv.rpc.tooManyRequests': 'Terlalu banyak percobaan. Coba lagi dalam {seconds} detik.',
  'srv.rpc.idempotencyKeyRequired': 'Tindakan ini memerlukan header Idempotency-Key.',
  'srv.rpc.idempotencyKeyReused': 'Idempotency-Key ini sudah dipakai untuk permintaan lain.',
  'srv.rpc.idempotencyInFlight': 'Permintaan yang sama masih diproses. Tunggu sebentar.',
}
