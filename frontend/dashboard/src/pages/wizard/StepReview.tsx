import { REAL_BACKEND } from '@loong/data'
import {
  VEHICLE_TYPE_LABEL,
  aggregateItems,
  formatRelativeDay,
  formatTime,
  itemLabel,
  roundUpTopUp,
  type PaymentMethod,
  type PlanOption,
  type Schedule,
  type Shipment,
} from '@loong/domain'
import { Button, Card, CardHeader, Checkbox, FareBreakdown, Field, Notice, Segmented, StopList } from '@loong/ui'
import { renderMsg } from '@loong/i18n'
import { Link, useLocation } from 'react-router'
import { t, tr } from '../../i18n'

/** `label` = teks aksesibel lengkap ("Ubah rute dan jadwal"), sudah diterjemahkan oleh pemanggil. */
function EditLink({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <Button variant="link" className="text-small" onClick={onClick} aria-label={label}>
      {t('dash.wizard.reviewEdit')}
    </Button>
  )
}

/** Langkah 4: ringkasan bisa diperiksa per bagian ("Ubah" melompat ke langkah itu), harga, pembayaran, persetujuan. */
export function StepReview({
  shipments,
  schedule,
  option,
  nowMs,
  payment,
  onPayment,
  terms,
  onTerms,
  termsError,
  goTo,
  balanceRp,
}: {
  shipments: Shipment[]
  schedule: Schedule
  option: PlanOption
  nowMs: number
  payment: PaymentMethod
  onPayment: (p: PaymentMethod) => void
  terms: boolean
  onTerms: (v: boolean) => void
  termsError?: string
  goTo: (step: number) => void
  /** Saldo tersedia; undefined selama belum dimuat. */
  balanceRp?: number
}) {
  const here = useLocation()
  const { totals, savings } = option
  // kekurangan saldo bila bayar dengan saldo (0 = cukup atau belum diketahui)
  const saldoShort = payment === 'saldo' && balanceRp !== undefined ? Math.max(0, totals.costRp - balanceRp) : 0
  const start = option.routes[0]?.startAt
  const topUpPath = saldoShort > 0 ? `/wallet/top-up?amount=${roundUpTopUp(saldoShort)}&next=${encodeURIComponent(here.pathname + here.search)}` : ''
  return (
    <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_minmax(0,24rem)]">
      <div className="min-w-0 space-y-5">
        <Card className="p-card">
          <CardHeader title={t('dash.wizard.reviewRoute')} action={<EditLink onClick={() => goTo(1)} label={t('dash.wizard.editRoute')} />} />
          <p className="mt-2 text-body text-muted">
            {schedule.mode === 'sekarang'
              ? t('dash.wizard.pickupNow', { time: start ? formatTime(start) : '—' })
              : schedule.window
                ? t('dash.wizard.pickupScheduled', { when: formatRelativeDay(schedule.window.from, new Date(nowMs)) })
                : t('dash.wizard.pickupUnset')}
          </p>
          <div className="mt-5 space-y-5">
            {option.routes.map((r, i) => (
              <div key={r.id}>
                {option.routes.length > 1 && (
                  <p className="mb-2 text-small font-semibold text-muted">
                    {t('dash.wizard.routeVehicleKm', { n: i + 1, vehicle: VEHICLE_TYPE_LABEL[r.vehicleType], distance: r.distanceKm })}
                  </p>
                )}
                <StopList stops={r.stops} group />
              </div>
            ))}
          </div>
        </Card>

        <Card className="p-card">
          <CardHeader title={t('dash.wizard.reviewItems')} action={<EditLink onClick={() => goTo(2)} label={t('dash.wizard.editItems')} />} />
          <ul className="mt-3 divide-y divide-line">
            {shipments.map((s, i) => {
              const a = aggregateItems(s.items)
              return (
                <li key={s.id} className="flex items-start justify-between gap-4 py-2.5 first:pt-0 last:pb-0">
                  <div className="min-w-0">
                    <p className="text-body font-semibold">
                      {t('dash.wizard.shipmentLabel', { n: i + 1, from: s.pickup.name, to: s.dropoff.name })}
                    </p>
                    <p className="truncate text-small text-muted">{s.items.map((it) => `${it.qty} × ${itemLabel(it)}`).join(', ')}</p>
                    {s.deadline && <p className="text-small text-muted">{t('dash.wizard.mustArrive', { time: formatTime(s.deadline) })}</p>}
                  </div>
                  <p className="shrink-0 text-small tabular-nums text-muted">{t('dash.fields.itemsKg', { count: a.itemCount, kg: a.weightKg })}</p>
                </li>
              )
            })}
          </ul>
        </Card>

        <Card className="p-card">
          <CardHeader title={t('dash.wizard.reviewOption')} action={<EditLink onClick={() => goTo(3)} label={t('dash.wizard.editOption')} />} />
          <p className="mt-2 text-body">
            <span className="font-semibold">{renderMsg(option.label)}</span> · {renderMsg(option.headline)}
          </p>
          <p className="mt-1 text-small text-muted">
            {t('dash.wizard.optionTotals', { distance: totals.distanceKm, duration: totals.durationMin, avg: totals.avgDeliveryMin })}
          </p>
        </Card>
      </div>

      <div className="space-y-5 lg:sticky lg:top-6">
        <Card className="p-card">
          <CardHeader title={t('dash.wizard.costSummary')} />
          <FareBreakdown routes={option.routes} className="mt-4" />
          {savings.costRp > 0 && (
            <p className="mt-3 text-small text-muted">{t('dash.wizard.youSave', { cost: savings.costRp, pct: savings.pct })}</p>
          )}
        </Card>

        <Card className="space-y-4 p-card">
          <Field label={t('dash.wizard.payMethod')}>
            <Segmented
              aria-label={t('dash.wizard.payMethod')}
              fill
              value={payment}
              onValueChange={onPayment}
              options={[
                { value: 'saldo', label: t('dash.wizard.payBalance') },
                // backend asli baru bisa menerima pembayaran dari saldo; transfer dan tunai hanya di backend tiruan
                ...(REAL_BACKEND
                  ? []
                  : [
                      { value: 'transfer' as const, label: t('dash.wizard.payTransfer') },
                      { value: 'tunai' as const, label: t('dash.wizard.payCash') },
                    ]),
              ]}
            />
          </Field>
          <p className="text-small text-muted">
            {payment === 'transfer'
              ? t('dash.wizard.noteTransfer')
              : payment === 'tunai'
                ? t('dash.wizard.noteCash')
                : balanceRp === undefined
                  ? t('dash.wizard.noteBalance')
                  : saldoShort > 0
                    ? t('dash.wizard.noteBalanceShort', { balance: balanceRp })
                    : t('dash.wizard.noteBalanceAfter', { balance: balanceRp, left: balanceRp - totals.costRp })}
          </p>
          {saldoShort > 0 && (
            <Notice
              tone="warning"
              title={t('dash.wizard.shortTitle', { short: saldoShort })}
              action={
                <Button variant="secondary" size="sm" asChild>
                  <Link to={topUpPath}>{t('dash.wizard.topUp')}</Link>
                </Button>
              }
            >
              {t('dash.wizard.shortBody')}
            </Notice>
          )}
          <div>
            <Checkbox
              checked={terms}
              onCheckedChange={onTerms}
              invalid={!!termsError}
              label={tr('dash.wizard.terms', {
                terms: (c) => (
                  <a href="#syarat" className="underline underline-offset-4">
                    {c}
                  </a>
                ),
              })}
            />
            {termsError && (
              <p role="alert" className="mt-2 text-small text-danger-fg">
                {termsError}
              </p>
            )}
          </div>
        </Card>
      </div>
    </div>
  )
}
