import { formatRelativeDay, formatTime, type Schedule } from '@loong/domain'
import { Field, Input, Segmented } from '@loong/ui'
import { Clock, Zap } from 'lucide-react'
import { t } from '../i18n'
import { dateWib, defaultLater, parseWib, scheduleFrom, timeWib } from '../lib/schedule'

/** Sekarang / Jadwalkan. "Jadwalkan" memunculkan tanggal + jam (WIB); driver tiba dalam jendela satu jam. */
export function ScheduleFields({
  value,
  onChange,
  nowMs,
  error,
}: {
  value: Schedule
  onChange: (s: Schedule) => void
  nowMs: number
  error?: string
}) {
  const later = value.mode === 'terjadwal' && value.window
  const from = later ? Date.parse(value.window!.from) : NaN
  const date = Number.isFinite(from) ? dateWib(from) : ''
  const time = Number.isFinite(from) ? timeWib(from) : ''

  const setPart = (d: string, t: string) => {
    if (parseWib(d, t) >= 0) onChange(scheduleFrom(d, t))
    else onChange({ mode: 'terjadwal', window: undefined })
  }

  return (
    <div className="space-y-3">
      <Field label={t('dash.fields.whenPickup')} error={value.mode === 'sekarang' ? undefined : error}>
        <Segmented
          aria-label={t('dash.fields.pickupTime')}
          fill
          value={value.mode}
          onValueChange={(m) => {
            if (m === 'sekarang') onChange({ mode: 'sekarang' })
            else {
              const d = defaultLater(nowMs)
              onChange(scheduleFrom(d.date, d.time))
            }
          }}
          options={[
            { value: 'sekarang', label: t('dash.fields.modeNow'), icon: <Zap className="size-3.5" aria-hidden /> },
            { value: 'terjadwal', label: t('dash.fields.modeLater'), icon: <Clock className="size-3.5" aria-hidden /> },
          ]}
        />
      </Field>
      {value.mode === 'sekarang' ? (
        <p className="text-small text-muted">{t('dash.fields.nowNote')}</p>
      ) : (
        <div className="grid gap-3 sm:grid-cols-2">
          <Field label={t('dash.fields.date')} error={error}>
            <Input type="date" value={date} min={dateWib(nowMs)} onChange={(e) => setPart(e.target.value, time || '09:00')} />
          </Field>
          <Field label={t('dash.fields.startTime')}>
            <Input type="time" step={900} value={time} onChange={(e) => setPart(date || dateWib(nowMs), e.target.value)} />
          </Field>
          {Number.isFinite(from) && (
            <p className="text-small text-muted sm:col-span-2">
              {t('dash.fields.arrivalWindow', { day: formatRelativeDay(from, new Date(nowMs)), from: formatTime(from), to: formatTime(from + 3_600_000) })}
            </p>
          )}
        </div>
      )}
    </div>
  )
}
