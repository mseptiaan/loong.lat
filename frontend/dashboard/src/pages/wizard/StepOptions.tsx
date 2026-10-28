import { errorMessage, useApi, useSettings } from '@loong/data'
import { DEFAULT_EFFORT, VEHICLE_TYPE_LABEL, type Effort, type OptionSet, type PlanOption } from '@loong/domain'
import { renderMsg } from '@loong/i18n'
import { Copilot } from '@loong/shell'
import { Card, CardHeader, ErrorState, Notice, RouteMap, RouteOptionCard, Skeleton, StopList, cn, toast } from '@loong/ui'
import { Check, Lightbulb, Loader2 } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { t, tr } from '../../i18n'
import { AiSummary } from './AiSummary'
import { SearchDepth } from './SearchDepth'
import { STAGES, calcCopy, choose, initialPick, initialProgress, methodLine, searchOptions, settled, type ProgressState, type SearchPick } from './search'

type Phase =
  | { kind: 'running'; effort: Effort; progress: ProgressState }
  /** `failure`: pencarian ulang gagal, tetapi opsi sebelumnya masih utuh dan tetap ditampilkan. */
  | { kind: 'ready'; failure?: string }
  | { kind: 'error'; message: string; effort: Effort }

/**
 * Langkah 3: menghitung dan membandingkan opsi. Perhitungan ditampilkan sebagai tahapan nyata dari engine
 * (bukan spinner kosong); setelah selesai, kartu opsi + peta (rute terpilih tergambar) + alasan.
 * Pengirim memilih seberapa keras mesin mencari (kedalaman pencarian) dan boleh meminta AI menjelaskan opsinya.
 */
export function StepOptions({
  ensureSaved,
  optionSet,
  onOptionSet,
  selectedId,
  onSelect,
  planId,
}: {
  /** Menyimpan draft terbaru dan mengembalikan id plan. */
  ensureSaved: () => Promise<string>
  optionSet: OptionSet | null
  /** `reset`: opsi baru dari perhitungan; pilihan lama dibuang (backend juga menghapusnya) dan yang direkomendasikan dipilih. */
  onOptionSet: (set: OptionSet, opts?: { reset?: boolean }) => void
  selectedId: string | undefined
  onSelect: (id: string) => void
  /** Id draft; diperlukan Copilot dan ringkasan AI untuk menjawab dari data plan ini. */
  planId?: string
}) {
  const api = useApi()
  const { data: settings } = useSettings()
  const aiOn = !!settings?.ai.copilot
  // pilihan awal mengikuti opsi yang sudah ada; yang pertama kali masuk langkah ini dihitung otomatis dengan tingkat bawaan
  const [pick, setPick] = useState<SearchPick>(() => initialPick(optionSet))
  const [phase, setPhase] = useState<Phase>(optionSet ? { kind: 'ready' } : { kind: 'running', effort: DEFAULT_EFFORT, progress: initialProgress() })
  const runId = useRef(0)
  const latest = useRef({ ensureSaved, onOptionSet, optionSet })
  useEffect(() => {
    latest.current = { ensureSaved, onOptionSet, optionSet }
  })

  /**
   * Menghitung opsi dengan `effort`. `force`: hitung ulang walau plan sudah punya opsi ("Cari lagi"; backend menggantikan opsi dan
   * menghapus pilihan). Tanpa `force`, opsi yang sudah tersimpan dipakai apa adanya (masuk lagi ke langkah ini).
   */
  const run = useCallback(
    async (effort: Effort, force: boolean) => {
      const mine = ++runId.current
      const hadOptions = force && !!latest.current.optionSet
      setPhase({ kind: 'running', effort, progress: initialProgress() })
      try {
        const outcome = await searchOptions(
          { ensureSaved: () => latest.current.ensureSaved(), getPlan: (id) => api.plans.get(id), compute: (id, opts) => api.plans.computeOptions(id, opts) },
          effort,
          force,
          { current: () => mine === runId.current, onProgress: (progress) => setPhase({ kind: 'running', effort, progress }) },
        )
        if (outcome.kind === 'cancelled') return
        if (outcome.kind === 'reused') {
          latest.current.onOptionSet(outcome.set)
          setPick(initialPick(outcome.set))
        } else {
          latest.current.onOptionSet(outcome.set, { reset: true })
          setPick(settled(effort))
        }
        setPhase({ kind: 'ready' })
      } catch (err) {
        if (mine !== runId.current) return
        const message = errorMessage(err)
        // pencarian ulang yang gagal tidak membuang opsi sebelumnya: tetap tampil, dengan penjelasan
        setPhase(hadOptions ? { kind: 'ready', failure: message } : { kind: 'error', message, effort })
      }
    },
    [api],
  )

  useEffect(() => {
    // dijadwalkan (bukan dipanggil langsung) agar efek tidak memicu setState sinkron
    const start = setTimeout(() => {
      if (!optionSet) void run(DEFAULT_EFFORT, false)
    }, 0)
    const current = runId
    return () => {
      clearTimeout(start)
      current.current++
    }
    // hanya saat dimuat: perubahan masukan membuang optionSet di induk dan komponen ini dibuat ulang
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (phase.kind === 'error') {
    return (
      <Card className="mx-auto max-w-2xl">
        <ErrorState title={t('dash.wizard.calcErrorTitle')} message={phase.message} onRetry={() => void run(phase.effort, false)} />
      </Card>
    )
  }

  if (phase.kind === 'running' || !optionSet) {
    const progress = phase.kind === 'running' ? phase.progress : initialProgress()
    const copy = calcCopy(phase.kind === 'running' ? phase.effort : DEFAULT_EFFORT)
    const stepLabels = [t('dash.wizard.calcStep1'), t('dash.wizard.calcStep2'), t('dash.wizard.calcStep3'), t('dash.wizard.calcStep4')]
    // tahap ke-5 (menggambar rute di jalan) hanya ada bila penyedia jalan aktif
    const rows = Math.max(STAGES, progress.stages.length)
    const total = Math.max(STAGES, progress.total)
    const step = Math.min(progress.step, total)
    return (
      <div className="grid gap-6 lg:grid-cols-[minmax(0,26rem)_minmax(0,1fr)]">
        <Card className="space-y-4 p-card lg:col-span-2" aria-live="polite">
          <CardHeader title={copy.title} description={copy.hint} />
          <ol className="space-y-2.5">
            {Array.from({ length: rows }, (_, i) => {
              const lines = progress.stages[i] ?? []
              const label = lines[0]
              // baris sesudah judul adalah sub-langkah pencarian di dalam tahap ini; yang terakhir yang sedang berjalan
              const subs = lines.slice(1)
              const done = i + 1 < step
              const current = i + 1 === step
              return (
                <li key={i} className={cn('text-body', !label && 'text-subtle')}>
                  <div className="flex items-center gap-3">
                    <span className="grid size-5 shrink-0 place-items-center">
                      {done ? (
                        <Check className="size-4 text-accent" aria-hidden />
                      ) : current ? (
                        <Loader2 className="size-4 animate-spin text-accent" aria-hidden />
                      ) : (
                        <span className="size-1.5 rounded-full bg-line-strong" aria-hidden />
                      )}
                    </span>
                    <span className={cn(current && 'font-semibold')}>{label ?? stepLabels[i]}</span>
                  </div>
                  {subs.length > 0 && (
                    <ul className="mt-1.5 space-y-1 pl-8">
                      {subs.map((line, j) => (
                        <li key={j} className={cn('text-small', current && j === subs.length - 1 ? 'text-fg' : 'text-muted')}>
                          {line}
                        </li>
                      ))}
                    </ul>
                  )}
                </li>
              )
            })}
          </ol>
          <div className="h-1.5 overflow-hidden rounded-full bg-sunken" role="progressbar" aria-valuemin={0} aria-valuemax={total} aria-valuenow={step} aria-label={t('dash.wizard.calcProgress')}>
            <div className="h-full rounded-full bg-accent transition-[width] duration-[360ms] ease-out" style={{ width: `${(step / total) * 100}%` }} />
          </div>
        </Card>
        <div className="space-y-3">
          <Skeleton className="h-52 rounded-card" />
          <Skeleton className="h-52 rounded-card" />
        </div>
        <Skeleton className="h-96 rounded-card" />
      </div>
    )
  }

  const selected: PlanOption = optionSet.options.find((o) => o.id === selectedId) ?? optionSet.options.find((o) => o.recommended) ?? optionSet.options[0]!
  const baseline = optionSet.baseline
  const shipmentCount = new Set(selected.routes.flatMap((r) => r.stops.map((st) => st.shipmentId))).size

  const how = methodLine(optionSet.method)

  return (
    <div className="space-y-5">
      {phase.kind === 'ready' && phase.failure && (
        <Notice tone="danger" title={t('dash.wizard.depth.failedTitle')}>
          {phase.failure} {t('dash.wizard.depth.failedKept')}
        </Notice>
      )}

      <SearchDepth pick={pick} aiOn={aiOn} onChoose={(effort) => setPick((p) => choose(p, effort))} onSearch={() => void run(pick.chosen, true)} />

      <div className="space-y-1">
        <p className="text-body text-muted">
          {tr('dash.wizard.baseline', {
            cost: baseline.costRp,
            distance: baseline.distanceKm,
            b: (c) => <span className="font-semibold tabular-nums text-fg">{c}</span>,
          })}
        </p>
        {how && <p className="text-small text-muted">{how}</p>}
      </div>
      {optionSet.options.length === 1 && (
        <Notice tone="info" title={t('dash.wizard.oneRouteTitle')}>
          {/* satu opsi bisa berarti satu pengiriman, atau beberapa pengiriman yang semua presetnya bertemu di rute yang sama */}
          {shipmentCount > 1 ? t('dash.wizard.oneRouteBodyMany', { count: shipmentCount }) : t('dash.wizard.oneRouteBody')}
        </Notice>
      )}

      <div className="grid items-start gap-6 lg:grid-cols-[minmax(0,26rem)_minmax(0,1fr)]">
        <div className="space-y-4">
          <div className="space-y-3" role="radiogroup" aria-label={t('dash.wizard.optionsLabel')}>
            {optionSet.options.map((o) => (
              <RouteOptionCard key={o.id} option={o} baseline={baseline} selected={o.id === selected.id} name="opsi-rute" onSelect={() => onSelect(o.id)} />
            ))}
          </div>

          {planId && settings?.ai.copilot && (
            <AiSummary
              key={`${optionSet.computedAt}|${optionSet.method?.effort ?? ''}|${optionSet.method?.trials ?? ''}`}
              planId={planId}
              report={optionSet.report}
              onReport={(report) => onOptionSet({ ...optionSet, report })}
            />
          )}
        </div>

        <div className="space-y-4 lg:sticky lg:top-6">
          <div className="h-72 overflow-hidden rounded-card border border-line sm:h-96">
            <RouteMap
              fitKey={optionSet.inputHash}
              traceKey={selected.id}
              routes={optionSet.options.flatMap((o) =>
                o.routes.map((r) => ({ id: r.id, route: r, emphasis: o.id === selected.id ? ('primary' as const) : ('muted' as const), showStops: o.id === selected.id })),
              )}
              onSelectRoute={(routeId) => {
                const hit = optionSet.options.find((o) => o.routes.some((r) => r.id === routeId))
                if (hit) onSelect(hit.id)
              }}
              ariaLabel={t('dash.wizard.compareMap')}
            />
          </div>

          <Card className="p-card">
            <CardHeader title={t('dash.wizard.whyTitle', { label: renderMsg(selected.label).toLowerCase() })} description={t('dash.wizard.whyHint')} />
            <ul className="mt-3 space-y-2">
              {selected.reasons.map((r, i) => (
                <li key={i} className="flex gap-2.5 text-body">
                  <Lightbulb className="mt-1 size-3.5 shrink-0 text-subtle" aria-hidden />
                  <span>{renderMsg(r)}</span>
                </li>
              ))}
            </ul>
          </Card>

          {planId && settings?.ai.copilot && (
            <Copilot
              scope={{ kind: 'plan', planId }}
              starters={
                optionSet.options.length > 1
                  ? [t('dash.wizard.starterWhy'), t('dash.wizard.starterCompare'), t('dash.wizard.starterFaster')]
                  : [t('dash.wizard.starterOnlyOne'), t('dash.wizard.starterCost')]
              }
              listClassName="max-h-[26rem]"
              onAction={async (a) => {
                if (a.kind === 'pilih_opsi' && a.optionId) {
                  onSelect(a.optionId)
                  toast.success(t('dash.wizard.optionChosen', { label: optionSet.options.find((o) => o.id === a.optionId)?.label ?? t('dash.wizard.optionFallback') }))
                }
              }}
            />
          )}

          <Card className="p-card">
            <CardHeader title={t('dash.wizard.stopOrder')} />
            <div className="mt-4 space-y-5">
              {selected.routes.map((r, i) => (
                <div key={r.id}>
                  {selected.routes.length > 1 && <p className="mb-2 text-small font-semibold text-muted">{t('dash.wizard.routeVehicle', { n: i + 1, vehicle: VEHICLE_TYPE_LABEL[r.vehicleType] })}</p>}
                  <StopList stops={r.stops} group />
                </div>
              ))}
            </div>
          </Card>
        </div>
      </div>
    </div>
  )
}
