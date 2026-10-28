import type { Translation } from '../../types'
import type { enFormat } from '../en/format'

export const idFormat: Translation<typeof enFormat> = {
  'fmt.duration.min': '{n} mnt',
  'fmt.duration.hour': '{h} j',
  'fmt.duration.hourMin': '{h} j {m} mnt',
  'fmt.day.today': 'Hari ini, {time}',
  'fmt.day.tomorrow': 'Besok, {time}',
  'fmt.day.yesterday': 'Kemarin, {time}',
  'fmt.ago.now': 'baru saja',
  'fmt.ago.seconds': '{n} dtk lalu',
  'fmt.ago.minutes': '{n} mnt lalu',
  'fmt.ago.hours': '{n} j lalu',
  'fmt.ago.yesterday': 'kemarin',
  'fmt.ago.days_other': '{count} hari lalu',
}
