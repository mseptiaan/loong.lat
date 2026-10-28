/**
 * Format tampilan (angka, jam, tanggal, durasi, "x menit lalu") ada di `@loong/i18n` karena mengikuti bahasa aktif.
 * Diekspor ulang di sini agar impor lama dari `@loong/domain` tetap berlaku.
 */
export {
  formatAgo,
  formatDate,
  formatDateLong,
  formatDateTime,
  formatDecimal,
  formatDuration,
  formatFloat,
  formatKg,
  formatKm,
  formatList,
  formatNumber,
  formatPercent,
  formatPhone,
  formatRelativeDay,
  formatRupiah,
  formatTime,
} from '@loong/i18n'
