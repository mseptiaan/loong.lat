import { errorMessage, useApi, useMutation } from '@loong/data'
import { PRESET_LABEL, formatDateTime, formatList, formatTime, type IntentChip, type IntentParse, type Priority, type Shipment } from '@loong/domain'
import { Button, Card, Field, Notice, Textarea, toast } from '@loong/ui'
import { Check, Sparkles, X } from 'lucide-react'
import { useState } from 'react'
import { t } from '../../i18n'
import { shipmentsForPlace } from '../../lib/intent'
import { dateWib } from '../../lib/schedule'
import { MIN_DEADLINE_GAP_MS } from '../../pages/wizard/model'

/**
 * "Ceritakan kebutuhan Anda": kalimat bebas diurai AI menjadi chip prioritas dan batas waktu yang bisa dibuang satu per
 * satu. Tidak ada yang berubah sampai pengguna menekan "Terapkan". Prioritas menentukan opsi mana yang ditandai
 * "Disarankan" di langkah pilih opsi (semua opsi tetap ditampilkan). Batas waktu dari kalimat dipasang ke pengiriman
 * yang tujuannya disebut ("yang ke Kota Kasablanka"), atau ke semua pengiriman bila tidak ada tempat yang disebut atau
 * tidak ada yang cocok; tiap pengiriman tetap bisa diubah sendiri.
 */
/** Jam saja bila hari ini (WIB), selain itu tanggal dan jam: batas yang sudah lewat hari ini dibaca untuk besok. */
const when = (iso: string) => (dateWib(Date.parse(iso)) === dateWib(Date.now()) ? formatTime(iso) : formatDateTime(iso))
export function IntentCard({
  priority,
  onPriority,
  shipments,
  onShipments,
  departureMs,
}: {
  priority: Priority
  onPriority: (p: Priority) => void
  shipments: Shipment[]
  onShipments: (s: Shipment[]) => void
  departureMs: number
}) {
  const api = useApi()
  const [text, setText] = useState(priority.text ?? '')
  const [parse, setParse] = useState<IntentParse>()
  const [chips, setChips] = useState<IntentChip[]>([])

  const understand = useMutation({
    mutationFn: (t: string) => api.ai.parseIntent(t),
    onSuccess: (p) => {
      setParse(p)
      setChips(p.chips)
    },
  })

  function apply() {
    const preset = chips.find((c) => c.kind === 'prioritas')?.preset
    const deadlineChip = chips.find((c) => c.kind === 'batas_waktu')
    const deadline = deadlineChip?.deadline
    // hanya yang tampil sebagai chip yang berubah
    onPriority({ ...priority, ...(preset ? { preset } : {}), text: text.trim() || undefined })
    const done: string[] = []
    if (preset) done.push(t('dash.ai.appliedPriority', { preset: PRESET_LABEL[preset].toLowerCase() }))
    if (deadline && Date.parse(deadline) < departureMs + MIN_DEADLINE_GAP_MS) {
      toast.error(t('dash.ai.deadlineTooClose', { time: when(deadline) }))
    } else if (deadline) {
      const matched = shipmentsForPlace(shipments, deadlineChip?.place)
      const targets = matched.length ? matched : shipments.map((_, i) => i)
      onShipments(shipments.map((s, i) => (targets.includes(i) ? { ...s, deadline } : s)))
      const time = when(deadline)
      if (matched.length === 1 && shipments.length > 1) {
        done.push(t('dash.ai.appliedDeadlinePlace', { time, place: shipments[matched[0]!]!.dropoff.name }))
      } else {
        done.push(targets.length > 1 ? t('dash.ai.appliedDeadlineMany', { time, count: targets.length }) : t('dash.ai.appliedDeadlineOne', { time }))
      }
    }
    if (done.length) toast.success(t('dash.ai.applied', { list: formatList(done) }))
    setParse(undefined)
  }

  return (
    <Card className="space-y-3 p-card">
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-input bg-accent-tint text-accent" aria-hidden>
          <Sparkles className="size-4" />
        </span>
        <div>
          <h2 className="text-title leading-tight">{t('dash.ai.intentTitle')}</h2>
          <p className="text-small text-muted">{t('dash.ai.intentHint')}</p>
        </div>
      </div>

      <Field label={t('dash.ai.intentField')}>
        <Textarea
          value={text}
          rows={2}
          onChange={(e) => setText(e.target.value)}
          placeholder={t('dash.ai.intentExample')}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && (e.metaKey || e.ctrlKey) && text.trim()) understand.mutate(text)
          }}
        />
      </Field>

      {understand.isError && <Notice tone="danger">{errorMessage(understand.error)}</Notice>}

      {parse && (
        <div className="space-y-3 rounded-card bg-sunken p-3.5" aria-live="polite">
          {chips.length > 0 ? (
            <>
              <p className="text-small font-semibold">{t('dash.ai.understood')}</p>
              <ul className="flex flex-wrap gap-2">
                {chips.map((c) => (
                  <li key={c.id} className="inline-flex items-center gap-1 rounded-chip border border-line bg-surface py-1 pl-3 pr-1 text-small font-medium">
                    {c.label}
                    <button type="button" aria-label={t('dash.ai.removeChip', { label: c.label })} onClick={() => setChips((all) => all.filter((x) => x.id !== c.id))} className="grid size-6 place-items-center rounded-full text-subtle hover:bg-sunken hover:text-fg">
                      <X className="size-3.5" aria-hidden />
                    </button>
                  </li>
                ))}
              </ul>
              {parse.leftover && <p className="text-small text-muted">{t('dash.ai.leftover', { text: parse.leftover })}</p>}
              <div className="flex gap-2">
                <Button variant="primary" size="sm" iconLeft={<Check className="size-3.5" aria-hidden />} onClick={apply}>
                  {t('dash.ai.apply')}
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setParse(undefined)}>
                  {t('dash.ai.dismiss')}
                </Button>
              </div>
            </>
          ) : (
            <p className="text-body text-muted">{t('dash.ai.nothingCaught')}</p>
          )}
        </div>
      )}

      <Button variant="secondary" loading={understand.isPending} aria-disabled={!text.trim() || undefined} onClick={() => text.trim() && understand.mutate(text)}>
        {t('dash.ai.understandBtn')}
      </Button>
    </Card>
  )
}
