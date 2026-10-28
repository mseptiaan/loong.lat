import { errorMessage, useProfile, usePlans, useSession, useVirtualNow } from '@loong/data'
import { ApiError, formatPercent, formatRupiah, type PlanSummary, type User } from '@loong/domain'
import { Button, Card, CardHeader, EmptyState, ErrorState, Notice, PageHeader, Skeleton, Stat } from '@loong/ui'
import { Check, Package, Plus, X } from 'lucide-react'
import { useMemo, useState } from 'react'
import { Link, useSearchParams } from 'react-router'
import { ActiveShipmentCard, PlanRow } from '../components/plans'
import { WalletCard } from '../components/WalletCard'
import { t } from '../i18n'

const DISMISS_KEY = 'loong:onboarding-dismissed'
const WEEK = 7 * 86_400_000
const ACTIVE = ['menunggu_driver', 'ditugaskan', 'berjalan'] as const
const ymWib = (ms: number) => new Date(ms + 7 * 3_600_000).toISOString().slice(0, 7)

function useStats(plans: PlanSummary[] | undefined, now: number) {
  return useMemo(() => {
    const done = (plans ?? []).filter((p) => p.status === 'selesai' && p.savingsRp !== undefined)
    const month = ymWib(now)
    const thisMonth = done.filter((p) => ymWib(new Date(p.createdAt).getTime()) === month)
    const savedMonth = thisMonth.reduce((s, p) => s + Math.max(0, p.savingsRp ?? 0), 0)
    const weekly = Array.from({ length: 8 }, () => 0)
    for (const p of done) {
      const idx = 7 - Math.floor((now - new Date(p.createdAt).getTime()) / WEEK)
      if (idx >= 0 && idx < 8) weekly[idx]! += Math.max(0, p.savingsRp ?? 0)
    }
    // satu dasar hitung untuk semua angka: hemat dibanding kirim terpisah, hanya yang positif, dibobot nilai pengiriman
    const savedAll = done.reduce((s, p) => s + Math.max(0, p.savingsRp ?? 0), 0)
    const paidAll = done.reduce((s, p) => s + (p.totalCostRp ?? 0), 0)
    return {
      savedMonth,
      countMonth: thisMonth.length,
      spark: weekly.some((v) => v > 0) ? weekly : undefined,
      completed: (plans ?? []).filter((p) => p.status === 'selesai').length,
      avgPct: savedAll + paidAll > 0 ? Math.round((savedAll / (savedAll + paidAll)) * 100) : undefined,
    }
  }, [plans, now])
}

function Onboarding({ plans, profile, onDismiss }: { plans: PlanSummary[]; profile?: User; onDismiss: () => void }) {
  const steps = [
    { done: plans.some((p) => p.status !== 'draft' && p.status !== 'opsi_siap'), label: t('dash.home.stepFirst'), to: '/plan/new' },
    { done: (profile?.savedAddresses.length ?? 0) > 0, label: t('dash.home.stepAddress'), to: '/addresses' },
    { done: plans.some((p) => p.status === 'selesai'), label: t('dash.home.stepTrack'), to: '/plan' },
  ]
  const left = steps.filter((s) => !s.done).length
  if (left === 0) return null
  return (
    <Card className="p-card">
      <CardHeader
        title={t('dash.home.onboardTitle')}
        description={t('dash.home.onboardProgress', { done: steps.length - left, total: steps.length })}
        action={
          <button type="button" onClick={onDismiss} aria-label={t('dash.home.onboardDismiss')} className="-m-1 grid size-8 place-items-center rounded-input text-subtle hover:bg-sunken hover:text-fg">
            <X className="size-4" aria-hidden />
          </button>
        }
      />
      <ol className="mt-3 space-y-1">
        {steps.map((s) => (
          <li key={s.to}>
            <Link to={s.to} className="flex min-h-hit items-center gap-3 rounded-input px-1 text-body transition-colors hover:bg-sunken">
              <span className={`grid size-5 shrink-0 place-items-center rounded-full border ${s.done ? 'border-accent bg-accent text-on-accent' : 'border-line-input bg-surface'}`} aria-hidden>
                {s.done && <Check className="size-3" />}
              </span>
              <span className={s.done ? 'text-muted line-through' : ''}>{s.label}</span>
            </Link>
          </li>
        ))}
      </ol>
    </Card>
  )
}

export function Home() {
  const { data: plans, isLoading, isError, error, refetch, isRefetching } = usePlans()
  const { data: profile } = useProfile()
  const session = useSession()
  const now = useVirtualNow(1000)
  const [params] = useSearchParams()
  const [dismissed, setDismissed] = useState(() => localStorage.getItem(DISMISS_KEY) === '1')
  const stats = useStats(plans, now)

  const active = (plans ?? []).filter((p) => (ACTIVE as readonly string[]).includes(p.status)).sort((a, b) => b.updatedAt.localeCompare(a.updatedAt))
  const drafts = (plans ?? []).filter((p) => p.status === 'draft' || p.status === 'opsi_siap')
  const recent = (plans ?? []).filter((p) => p.status !== 'draft' && p.status !== 'opsi_siap').slice(0, 5)
  // nama dari sesi sudah ada di perangkat, jadi sapaan tetap benar walau profil gagal dimuat
  const firstName = (profile?.name ?? session?.name ?? '').split(' ')[0]

  return (
    <div className="space-y-8">
      <PageHeader
        title={firstName ? t('dash.home.greeting', { name: firstName }) : t('dash.home.greetingAnon')}
        description={t('dash.home.lead')}
        actions={
          <Button variant="primary" size="lg" asChild>
            <Link to="/plan/new">
              <Plus className="size-4" aria-hidden />
              {t('dash.app.newDelivery')}
            </Link>
          </Button>
        }
      />

      {params.has('welcome') && <Notice tone="success" title={t('dash.home.welcomeTitle')}>{t('dash.home.welcomeText')}</Notice>}

      {isError ? (
        <Card>
          <ErrorState message={errorMessage(error)} onRetry={() => void refetch()} retrying={isRefetching} offline={error instanceof ApiError && error.code === 'jaringan'} />
        </Card>
      ) : (
        <div className="grid gap-8 lg:grid-cols-[minmax(0,1fr)_21rem]">
          <div className="min-w-0 space-y-8">
            <section aria-labelledby="aktif" className="space-y-3">
              <h2 id="aktif" className="text-title">
                {t('dash.home.activeTitle')}
              </h2>
              {isLoading ? (
                <Card className="overflow-hidden">
                  <Skeleton className="h-44 w-full rounded-none" />
                  <div className="space-y-3 p-card">
                    <Skeleton className="h-5 w-40" />
                    <Skeleton className="h-4 w-3/4" />
                    <Skeleton className="h-12 w-full" />
                  </div>
                </Card>
              ) : active.length === 0 ? (
                <Card>
                  <EmptyState
                    compact
                    icon={<Package />}
                    title={t('dash.home.activeEmptyTitle')}
                    description={t('dash.home.activeEmptyHint')}
                  />
                </Card>
              ) : (
                <div className="grid gap-4 xl:grid-cols-2">
                  {active.map((p, i) => (
                    <ActiveShipmentCard key={p.id} plan={p} withMap={i < 2} />
                  ))}
                </div>
              )}
            </section>

            {drafts.length > 0 && (
              <section aria-labelledby="draft" className="space-y-3">
                <h2 id="draft" className="text-title">
                  {t('dash.home.draftsTitle')}
                </h2>
                <div className="space-y-3">
                  {drafts.map((p) => (
                    <PlanRow key={p.id} plan={p} now={now} />
                  ))}
                </div>
              </section>
            )}

            <section aria-labelledby="terakhir" className="space-y-3">
              <div className="flex items-end justify-between">
                <h2 id="terakhir" className="text-title">
                  {t('dash.home.recentTitle')}
                </h2>
                <Link to="/plan" className="text-small font-semibold text-accent underline underline-offset-4 hover:text-accent-hover">
                  {t('dash.home.viewAll')}
                </Link>
              </div>
              {isLoading ? (
                <div className="space-y-3">
                  {[0, 1, 2].map((i) => (
                    <Skeleton key={i} className="h-[4.75rem] w-full rounded-card" />
                  ))}
                </div>
              ) : recent.length === 0 ? (
                <Card>
                  <EmptyState compact icon={<Package />} title={t('dash.home.recentEmptyTitle')} description={t('dash.home.recentEmptyHint')} />
                </Card>
              ) : (
                <div className="space-y-3">
                  {recent.map((p) => (
                    <PlanRow key={p.id} plan={p} now={now} />
                  ))}
                </div>
              )}
            </section>
          </div>

          <aside className="space-y-4" aria-label={t('dash.home.summaryLabel')}>
            <WalletCard />
            {isLoading ? (
              <>
                <Skeleton className="h-36 w-full rounded-card" />
                <div className="grid grid-cols-2 gap-4">
                  <Skeleton className="h-24 rounded-card" />
                  <Skeleton className="h-24 rounded-card" />
                </div>
              </>
            ) : (
              <>
                {stats.completed === 0 ? (
                  // akun baru: "Rp 0" dan "0%" terbaca seperti hasil buruk padahal belum ada data
                  <Card className="p-card">
                    <p className="text-small font-medium text-muted">{t('dash.home.savingsTitle')}</p>
                    <p className="mt-1 text-title">{t('dash.home.savingsEmptyTitle')}</p>
                    <p className="mt-1 text-small text-muted">{t('dash.home.savingsEmptyHint')}</p>
                  </Card>
                ) : (
                  <>
                    <Stat
                      emphasis="primary"
                      label={t('dash.home.savedMonth')}
                      value={formatRupiah(stats.savedMonth)}
                      hint={stats.countMonth > 0 ? t('dash.home.savedMonthHint', { count: stats.countMonth }) : t('dash.home.savedMonthNone')}
                      spark={stats.spark}
                    />
                    <div className="grid grid-cols-2 gap-4">
                      <Stat label={t('dash.home.completedCount')} value={String(stats.completed)} />
                      <Stat label={t('dash.home.avgSavings')} value={stats.avgPct === undefined ? '—' : formatPercent(stats.avgPct)} hint={t('dash.home.avgSavingsHint')} />
                    </div>
                  </>
                )}
                {!dismissed && plans && (
                  <Onboarding
                    plans={plans}
                    profile={profile}
                    onDismiss={() => {
                      localStorage.setItem(DISMISS_KEY, '1')
                      setDismissed(true)
                    }}
                  />
                )}
              </>
            )}
          </aside>
        </div>
      )}
    </div>
  )
}
