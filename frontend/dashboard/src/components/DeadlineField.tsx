import type { ISODate } from '@loong/domain'
import { Field, Input, Switch } from '@loong/ui'
import { t } from '../i18n'
import { dateWib, parseWib, timeWib } from '../lib/schedule'

/**
 * Batas waktu tiba satu pengiriman: saklar + jam. Tiap titik antar bisa punya batas sendiri, jadi ini milik kartu
 * pengiriman (langkah 1), bukan satu angka untuk seluruh plan. Batas baru jatuh di hari keberangkatan; batas yang sudah
 * ada (mis. dari AI, yang menggulirkan jam yang sudah lewat ke besok) memakai harinya sendiri, dan labelnya menyebut
 * tanggal itu bila bukan hari ini, supaya "17.00" tidak terbaca hari ini padahal besok.
 */
export function DeadlineField({
  value,
  onChange,
  departureMs,
  nowMs,
  error,
}: {
  value: ISODate | undefined
  onChange: (value: ISODate | undefined) => void
  departureMs: number
  nowMs: number
  error?: string
}) {
  const valueMs = value ? Date.parse(value) : undefined
  const baseDate = valueMs !== undefined && Number.isFinite(valueMs) ? dateWib(valueMs) : dateWib(departureMs)
  const labelMs = valueMs !== undefined && Number.isFinite(valueMs) ? valueMs : departureMs
  return (
    <div className="space-y-3">
      <Switch
        checked={!!value}
        onCheckedChange={(on) => {
          if (!on) return onChange(undefined)
          const t = departureMs + 2 * 3_600_000
          onChange(new Date(Math.ceil(t / 900_000) * 900_000).toISOString())
        }}
        label={t('dash.fields.deadlineSwitch')}
        description={t('dash.fields.deadlineHint')}
      />
      {value && (
        <Field label={baseDate === dateWib(nowMs) ? t('dash.fields.deadlineToday') : t('dash.fields.deadlineOn', { date: labelMs })} error={error}>
          <Input
            type="time"
            step={900}
            className="max-w-40"
            value={timeWib(Date.parse(value))}
            onChange={(e) => {
              const ms = parseWib(baseDate, e.target.value)
              if (Number.isFinite(ms)) onChange(new Date(ms).toISOString())
            }}
          />
        </Field>
      )}
    </div>
  )
}
