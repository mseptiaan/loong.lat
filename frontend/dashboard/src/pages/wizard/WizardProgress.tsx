import { cn } from '@loong/ui'
import { Check } from 'lucide-react'
import { t, tr } from '../../i18n'
import { STEPS } from './model'

/**
 * "Langkah 3 dari 5: Prioritas" + bilah progres. Langkah yang sudah dicapai bisa diklik untuk kembali
 * (state tidak hilang); langkah di depan belum bisa dilompati.
 */
export function WizardProgress({ step, maxReached, onGo }: { step: number; maxReached: number; onGo: (n: number) => void }) {
  const current = STEPS[step - 1]!
  return (
    <nav aria-label={t('dash.wizard.progressLabel')} className="space-y-3">
      <p className="text-small text-muted" aria-live="polite">
        {tr('dash.wizard.progressCurrent', {
          step,
          total: STEPS.length,
          label: current.label,
          b: (c) => <span className="font-semibold text-fg">{c}</span>,
        })}
      </p>
      <ol className="flex items-center gap-2">
        {STEPS.map((s) => {
          const done = s.n < step
          const reachable = s.n <= maxReached && s.n !== step
          return (
            <li key={s.n} className="flex flex-1 flex-col gap-1.5 sm:flex-none sm:flex-row sm:items-center sm:gap-2">
              <button
                type="button"
                disabled={!reachable}
                aria-current={s.n === step ? 'step' : undefined}
                aria-label={done ? t('dash.wizard.stepButtonDone', { n: s.n, label: s.label }) : t('dash.wizard.stepButton', { n: s.n, label: s.label })}
                onClick={() => onGo(s.n)}
                className={cn('group flex items-center gap-2 rounded-chip text-left', reachable && 'cursor-pointer')}
              >
                <span
                  className={cn(
                    'hidden size-6 shrink-0 place-items-center rounded-full border text-caption font-bold tabular-nums transition-colors sm:grid',
                    s.n === step && 'border-accent bg-accent text-on-accent',
                    done && 'border-accent bg-accent-tint text-accent',
                    s.n > step && 'border-line-strong bg-surface text-subtle',
                  )}
                >
                  {done ? <Check className="size-3.5" aria-hidden /> : s.n}
                </span>
                <span className={cn('hidden text-small font-medium lg:inline', s.n === step ? 'text-fg' : done ? 'text-muted group-hover:text-fg' : 'text-subtle')}>{s.label}</span>
              </button>
              <span aria-hidden className={cn('h-1 rounded-full transition-colors sm:hidden', s.n <= step ? 'bg-accent' : 'bg-line-strong')} />
              {s.n < STEPS.length && <span aria-hidden className={cn('mx-1 hidden h-px w-4 sm:block lg:w-8', s.n < step ? 'bg-accent' : 'bg-line-strong')} />}
            </li>
          )
        })}
      </ol>
    </nav>
  )
}
