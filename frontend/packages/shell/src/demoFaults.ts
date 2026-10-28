import type { FaultMode } from '@loong/domain'
import { t } from '@loong/i18n'

/** Mode kegagalan tiruan Panel Demo, berurutan seperti di daftar pilihan. */
export const FAULT_MODES = ['none', 'slow', 'error', 'offline', 'empty'] as const satisfies readonly FaultMode[]

/** Nama dan keterangan satu mode, mengikuti bahasa saat dipanggil (jangan disimpan di tingkat modul). */
export const faultLabel = (mode: FaultMode): string => t(`shell.demo.fault.${mode}`)
export const faultHint = (mode: FaultMode): string => t(`shell.demo.faultHint.${mode}`)
