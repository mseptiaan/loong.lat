import { useApi } from '@loong/data'
import type { Report } from '@loong/domain'
import { getLocale } from '@loong/i18n'
import { Button, Card, ErrorState, Notice, Skeleton } from '@loong/ui'
import { RefreshCw, Sparkles } from 'lucide-react'
import { useCallback, useEffect, useReducer, useRef } from 'react'
import { t } from '../../i18n'
import { initialReport, readReport, reportLanguage, reportParagraphs, reportReducer, type ReportProblem } from './search'

/** Teks biasa dalam paragraf: tidak pernah diurai sebagai HTML atau Markdown (React meloloskan semua karakter). */
function ReportText({ report }: { report: Report }) {
  const language = reportLanguage(report.locale)
  return (
    <div className="space-y-2.5">
      <div className="max-w-prose space-y-2.5">
        {reportParagraphs(report.text).map((p, i) => (
          <p key={i} className="whitespace-pre-line text-body">
            {p}
          </p>
        ))}
      </div>
      {language && <p className="text-small text-muted">{t('dash.wizard.report.language', { language })}</p>}
    </div>
  )
}

/** Kerangka teks: tinggi dan lebar baris mendekati paragraf sungguhan agar tata letak tidak melompat. */
function ReportSkeleton() {
  return (
    <div className="space-y-2.5" aria-hidden>
      <Skeleton className="h-4 w-full" />
      <Skeleton className="h-4 w-11/12" />
      <Skeleton className="h-4 w-4/5" />
      <Skeleton className="mt-4 h-4 w-full" />
      <Skeleton className="h-4 w-2/3" />
    </div>
  )
}

/**
 * Galat ringkasan. Dua kunci khusus dari backend punya bahasa sendiri: "belum ada opsi" (tidak ada yang bisa dicoba ulang) dan
 * "tidak lolos pemeriksaan angka" (teks sengaja tidak ditampilkan; boleh dicoba lagi). Selain itu galat AI umum dengan coba lagi.
 */
function ReportProblemView({ problem, onRetry }: { problem: ReportProblem; onRetry: () => void }) {
  if (problem.kind === 'noOptions') return <Notice tone="info" title={t('dash.wizard.report.noOptionsTitle')}>{problem.message}</Notice>
  if (problem.kind === 'unverified') {
    return (
      <Notice
        tone="warning"
        title={t('dash.wizard.report.unverifiedTitle')}
        action={
          <Button size="sm" variant="secondary" onClick={onRetry}>
            {t('dash.wizard.report.tryAgain')}
          </Button>
        }
      >
        {problem.message}
      </Notice>
    )
  }
  return <ErrorState compact title={t('dash.wizard.report.errorTitle')} message={problem.message} onRetry={onRetry} />
}

/**
 * Kartu "Ringkasan AI" di langkah 3: penjelasan opsi dalam kata sederhana, ditulis atas permintaan (bukan otomatis).
 * Ringkasan yang sudah tersimpan di OptionSet langsung tampil. Pasang dengan `key` yang berubah bersama opsi, supaya
 * ringkasan opsi lama tidak ikut ke opsi baru.
 */
export function AiSummary({ planId, report, onReport }: { planId: string; report: Report | undefined; onReport: (report: Report) => void }) {
  const api = useApi()
  const [state, dispatch] = useReducer(reportReducer, report, initialReport)
  const runId = useRef(0)
  const latest = useRef(onReport)
  useEffect(() => {
    latest.current = onReport
  })
  // ditutup selagi menulis: hasilnya dibuang dan alirannya ditutup
  useEffect(() => {
    const current = runId
    return () => {
      current.current++
    }
  }, [])

  const generate = useCallback(async () => {
    const mine = ++runId.current
    dispatch({ type: 'start' })
    try {
      const result = await readReport(api.ai.explainOptions(planId), getLocale(), { current: () => mine === runId.current })
      if (!result || mine !== runId.current) return
      dispatch({ type: 'done', report: result })
      latest.current(result)
    } catch (error) {
      if (mine === runId.current) dispatch({ type: 'fail', error })
    }
  }, [api, planId])

  const loading = state.kind === 'loading'
  const shown = state.kind === 'ready' ? state.report : state.kind === 'error' ? state.previous : undefined

  return (
    <Card className="space-y-4 p-card" aria-busy={loading || undefined}>
      <div className="flex items-start gap-2.5">
        <span className="mt-0.5 grid size-7 shrink-0 place-items-center rounded-input bg-accent-tint text-accent" aria-hidden>
          <Sparkles className="size-4" />
        </span>
        <div className="min-w-0">
          <h2 className="text-title leading-tight">{t('dash.wizard.report.title')}</h2>
          <p className="text-small text-muted">{t('dash.wizard.report.hint')}</p>
        </div>
      </div>

      <p role="status" className="sr-only">
        {loading ? t('dash.wizard.report.writing') : state.kind === 'ready' ? t('dash.wizard.report.ready') : ''}
      </p>

      {state.kind === 'idle' && (
        <Button variant="secondary" iconLeft={<Sparkles className="size-4" aria-hidden />} onClick={() => void generate()}>
          {t('dash.wizard.report.explain')}
        </Button>
      )}

      {loading && <ReportSkeleton />}

      {state.kind === 'error' && <ReportProblemView problem={state.problem} onRetry={() => void generate()} />}

      {shown && <ReportText report={shown} />}

      {state.kind === 'ready' && (
        <Button size="sm" variant="ghost" iconLeft={<RefreshCw className="size-3.5" aria-hidden />} onClick={() => void generate()}>
          {t('dash.wizard.report.regenerate')}
        </Button>
      )}
    </Card>
  )
}
