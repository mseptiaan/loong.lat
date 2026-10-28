import { useApi, useDemoState, useMutation, useVirtualNow, errorMessage } from '@loong/data'
import { appOrigin, currentApp, formatTime, type AppId, type FaultMode } from '@loong/domain'
import { t } from '@loong/i18n'
import { Button, ConfirmDialog, Segmented, Select, Switch, toast, cn } from '@loong/ui'
import { ExternalLink, FastForward, Play, RotateCcw, SlidersHorizontal, X } from 'lucide-react'
import { useState } from 'react'
import { FAULT_MODES, faultHint, faultLabel } from './demoFaults'

const FLAG = 'loong:demo'

/** Panel Demo muncul bila alamat memuat `?demo` (lalu diingat) dan bisa disembunyikan. Tak ada di produksi tanpa flag. */
function initialVisible(): boolean {
  try {
    if (new URLSearchParams(location.search).has('demo')) {
      localStorage.setItem(FLAG, '1')
      return true
    }
    return localStorage.getItem(FLAG) === '1'
  } catch {
    return false
  }
}

const SPEEDS = [
  { value: '1', label: '1x' },
  { value: '10', label: '10x' },
  { value: '60', label: '60x' },
]

/** Menit yang dimajukan tombol cepat dan keterlambatan driver tiruan. */
const ADVANCE_MINUTES = [5, 30] as const
const DELAY_MINUTES = 15

const APP_LABEL: Partial<Record<AppId, string>> = { console: 'Dashboard', backoffice: 'Backoffice', driver: 'Driver' }

export function DemoPanel() {
  const api = useApi()
  const [visible, setVisible] = useState(initialVisible)
  const [open, setOpen] = useState(false)
  const [confirmReset, setConfirmReset] = useState(false)
  const demo = useDemoState().data
  const now = useVirtualNow(1000)

  const run = useMutation({
    mutationFn: async (fn: () => Promise<string | void>) => fn(),
    onSuccess: (msg) => {
      if (msg) toast.success(msg)
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  if (!visible) return null
  const self = currentApp()

  return (
    <>
      {!open && (
        <button
          type="button"
          onClick={() => setOpen(true)}
          aria-label={t('shell.demo.title')}
          className="fixed bottom-28 left-3 z-[70] inline-flex h-10 items-center justify-center gap-2 rounded-full border border-line bg-raised text-small font-semibold shadow-overlay transition-transform hover:-translate-y-px active:scale-[0.98] max-sm:w-10 sm:bottom-4 sm:left-auto sm:right-4 sm:px-3.5"
        >
          <SlidersHorizontal className="size-4" aria-hidden />
          <span className="max-sm:hidden">{t('shell.demo.short')}</span>
          {demo && demo.clock.speed > 1 && <span className="rounded-chip bg-accent-tint px-1.5 text-caption text-accent">{demo.clock.speed}x</span>}
          {demo && demo.fault !== 'none' && <span className="size-1.5 rounded-full bg-danger" aria-label={t('shell.demo.faultActive')} />}
        </button>
      )}

      {open && (
        <section
          aria-label={t('shell.demo.title')}
          className="fixed bottom-28 left-4 z-[70] max-h-[calc(100dvh-9rem)] w-[min(22rem,calc(100vw-2rem))] overflow-y-auto rounded-modal sm:bottom-4 sm:left-auto sm:right-4 sm:max-h-[calc(100dvh-2rem)] border border-line bg-raised p-4 text-body shadow-overlay [animation:fade-up_220ms_var(--ease-out)]"
        >
          <header className="flex items-center justify-between">
            <h2 className="text-title">{t('shell.demo.title')}</h2>
            <div className="flex items-center gap-1">
              <button
                type="button"
                onClick={() => {
                  localStorage.removeItem(FLAG)
                  setVisible(false)
                }}
                className="rounded-chip px-2 py-1 text-small text-muted hover:bg-sunken hover:text-fg"
              >
                {t('shell.demo.hide')}
              </button>
              <button type="button" aria-label={t('shell.demo.closePanel')} onClick={() => setOpen(false)} className="grid size-8 place-items-center rounded-input text-muted hover:bg-sunken hover:text-fg">
                <X className="size-4" aria-hidden />
              </button>
            </div>
          </header>

          <div className="mt-3 space-y-4">
            <div className="flex items-baseline justify-between rounded-card bg-sunken px-3 py-2">
              <span className="text-small text-muted">{t('shell.demo.virtualClock')}</span>
              <span className="text-metric-sm tabular-nums">{formatTime(now)}</span>
            </div>

            <div className="space-y-2">
              <p className="text-small font-semibold">{t('shell.demo.timeSpeed')}</p>
              <Segmented
                aria-label={t('shell.demo.timeSpeed')}
                fill
                value={String(demo?.clock.speed ?? 1)}
                onValueChange={(v) => run.mutate(() => api.demo.setSpeed(Number(v)))}
                options={SPEEDS}
              />
              <div className="flex gap-2">
                {ADVANCE_MINUTES.map((n) => (
                  <Button
                    key={n}
                    size="sm"
                    iconLeft={<FastForward className="size-3.5" aria-hidden />}
                    onClick={() => run.mutate(async () => (await api.demo.advance(n), t('shell.demo.advanced', { n })))}
                  >
                    {t('shell.demo.advanceShort', { n })}
                  </Button>
                ))}
              </div>
            </div>

            <Switch
              checked={demo?.autopilot ?? false}
              onCheckedChange={(on) => run.mutate(async () => (await api.demo.setAutopilot(on), on ? t('shell.demo.autopilotOn') : t('shell.demo.autopilotOff')))}
              label={t('shell.demo.autopilotLabel')}
              description={t('shell.demo.autopilotHint')}
            />

            <div className="space-y-2">
              <p className="text-small font-semibold">{t('shell.demo.faultTitle')}</p>
              <Select
                aria-label={t('shell.demo.faultTitle')}
                value={demo?.fault ?? 'none'}
                onValueChange={(v) => run.mutate(async () => (await api.demo.setFault(v as FaultMode), t('shell.demo.faultMode', { mode: faultLabel(v as FaultMode) })))}
                options={FAULT_MODES.map((f) => ({ value: f, label: faultLabel(f), description: faultHint(f) }))}
              />
            </div>

            <div className="space-y-2">
              <p className="text-small font-semibold">{t('shell.demo.quickActions')}</p>
              <div className="flex flex-wrap gap-2">
                <Button size="sm" iconLeft={<Play className="size-3.5" aria-hidden />} onClick={() => run.mutate(async () => t('shell.demo.spawned', { code: (await api.demo.spawnPlan()).code }))}>
                  {t('shell.demo.spawnPlan')}
                </Button>
                <Button size="sm" onClick={() => run.mutate(async () => t('shell.demo.delayed', { code: (await api.demo.triggerDelay(undefined, DELAY_MINUTES)).planCode, n: DELAY_MINUTES }))}>
                  {t('shell.demo.delayButton', { n: DELAY_MINUTES })}
                </Button>
                <Button size="sm" variant="secondary" iconLeft={<RotateCcw className="size-3.5" aria-hidden />} onClick={() => setConfirmReset(true)}>
                  {t('shell.demo.reset')}
                </Button>
              </div>
            </div>

            <div className="space-y-2 border-t border-line pt-3">
              <p className="text-small font-semibold">{t('shell.demo.openOther')}</p>
              <div className="flex flex-wrap gap-2">
                {(['console', 'backoffice', 'driver'] as const)
                  .filter((a) => a !== self)
                  .map((a) => (
                    <a
                      key={a}
                      href={`${appOrigin(a)}/?demo`}
                      target="_blank"
                      rel="noreferrer"
                      className={cn('inline-flex h-8 items-center gap-1.5 rounded-input border border-line-input px-3 text-small font-semibold hover:bg-sunken')}
                    >
                      {APP_LABEL[a]}
                      <ExternalLink className="size-3.5" aria-hidden />
                    </a>
                  ))}
              </div>
            </div>
          </div>
        </section>
      )}

      <ConfirmDialog
        open={confirmReset}
        onOpenChange={setConfirmReset}
        title={t('shell.demo.resetTitle')}
        description={t('shell.demo.resetBody')}
        confirmLabel={t('shell.demo.reset')}
        tone="danger"
        loading={run.isPending}
        onConfirm={() => {
          run.mutate(async () => (await api.demo.reset(), t('shell.demo.resetDone')))
          setConfirmReset(false)
        }}
      />
    </>
  )
}
