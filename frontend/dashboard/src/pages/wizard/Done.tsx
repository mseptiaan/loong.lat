import { formatRupiah, type Plan } from '@loong/domain'
import { Button, Card } from '@loong/ui'
import { Check } from 'lucide-react'
import { Link } from 'react-router'
import { t, tr } from '../../i18n'

/** Layar sukses setelah konfirmasi: kode plan, apa yang terjadi berikutnya, dan dua langkah lanjut yang jelas. */
export function Done({ plan, onRestart }: { plan: Plan; onRestart: () => void }) {
  const saved = plan.optionSet?.options.find((o) => o.id === plan.selectedOptionId)?.savings
  const scheduled = plan.schedule.mode === 'terjadwal'
  const message =
    plan.status === 'ditugaskan'
      ? scheduled
        ? 'dash.wizard.doneAssignedScheduled'
        : 'dash.wizard.doneAssignedNow'
      : scheduled
        ? 'dash.wizard.doneWaitingScheduled'
        : 'dash.wizard.doneWaitingNow'
  return (
    <div className="mx-auto flex max-w-lg flex-col items-center py-10 text-center [animation:fade-up_360ms_var(--ease-out)]">
      <div className="grid size-16 place-items-center rounded-full bg-accent text-on-accent shadow-card" aria-hidden>
        <Check className="size-8" strokeWidth={2.5} />
      </div>
      <h1 className="mt-5 text-heading">{t('dash.wizard.doneTitle')}</h1>
      <p className="mt-1 text-body text-muted">
        {tr(message, { code: plan.code, b: (c) => <span className="font-semibold tabular-nums text-fg">{c}</span> })}
      </p>

      <Card className="mt-6 w-full space-y-1 p-card text-left">
        <p className="flex justify-between text-body">
          <span className="text-muted">{t('dash.wizard.doneTotal')}</span>
          <span className="font-semibold tabular-nums">{formatRupiah(plan.totalCostRp ?? 0)}</span>
        </p>
        {saved && saved.costRp > 0 && (
          <p className="flex justify-between text-body">
            <span className="text-muted">{t('dash.wizard.doneSaved')}</span>
            <span className="tabular-nums">{t('dash.wizard.doneSavedValue', { cost: saved.costRp, pct: saved.pct })}</span>
          </p>
        )}
        <p className="flex justify-between text-body">
          <span className="text-muted">{t('dash.wizard.doneVehicles')}</span>
          <span className="tabular-nums">{t('dash.wizard.doneVehicleCount', { count: plan.routes.length })}</span>
        </p>
      </Card>

      <div className="mt-6 flex w-full flex-col gap-3 sm:flex-row sm:justify-center">
        <Button variant="primary" size="lg" asChild>
          <Link to={`/plan/${plan.id}`}>{t('dash.wizard.doneTrack')}</Link>
        </Button>
        <Button variant="secondary" size="lg" onClick={onRestart}>
          {t('dash.wizard.doneAgain')}
        </Button>
      </div>
    </div>
  )
}
