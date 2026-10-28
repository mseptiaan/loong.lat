import { errorMessage, qk, useApi, useMutation, useQueryClient, useTopUps, useUser, useUserWalletTx, useVirtualNow } from '@loong/data'
import { formatAgo, formatPhone, formatRupiah } from '@loong/domain'
import { Avatar, Button, Card, CardHeader, ConfirmDialog, EmptyState, ErrorState, Notice, PageHeader, Skeleton, Stat, StatusLabel, toast } from '@loong/ui'
import { Ban, PackageSearch, Receipt, ShieldCheck, Wallet } from 'lucide-react'
import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { PlanTable } from '../components/PlanTable'
import { TopUpTable } from '../components/TopUpTable'
import { WalletTxTable } from '../components/WalletTxTable'
import { t, tr } from '../i18n'
import { isOperational } from '../lib/plans'

export function UserDetail() {
  const { id } = useParams()
  const api = useApi()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const detail = useUser(id)
  const topUps = useTopUps({ userId: id })
  const ledger = useUserWalletTx(id)
  const nowMs = useVirtualNow(60_000)
  const [confirm, setConfirm] = useState(false)

  const toggle = useMutation({
    mutationFn: (blocked: boolean) => api.users.setBlocked(id!, blocked),
    onSuccess: (u) => {
      toast.success(u.blocked ? t('bo.users.toastBlocked', { name: u.name }) : t('bo.users.toastUnblocked', { name: u.name }))
      setConfirm(false)
      void qc.invalidateQueries({ queryKey: qk.user(id!) })
      void qc.invalidateQueries({ queryKey: ['users'] })
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  if (detail.isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-16 w-80" />
        <Skeleton className="h-28 rounded-card" />
        <Skeleton className="h-64 rounded-card" />
      </div>
    )
  }
  if (detail.isError || !detail.data) {
    return (
      <Card>
        <ErrorState title={t('bo.users.notFoundTitle')} message={detail.error ? errorMessage(detail.error) : t('bo.users.notFoundHint')} onRetry={() => void detail.refetch()} />
      </Card>
    )
  }

  const { user, summary, plans } = detail.data
  const operational = plans.filter(isOperational)

  return (
    <div className="space-y-4">
      <PageHeader
        back={{ label: t('bo.users.detailBack'), onClick: () => navigate('/user') }}
        title={
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <Avatar name={user.name} size="lg" />
            {user.name}
            <StatusLabel tone={summary.blocked ? 'danger' : 'success'} className="text-body font-normal">
              {summary.blocked ? t('bo.users.statusBlocked') : t('bo.users.statusActive')}
            </StatusLabel>
          </span>
        }
        description={t('bo.users.detailSubtitle', { email: user.email, phone: formatPhone(user.phone), joined: user.createdAt })}
        actions={
          summary.blocked ? (
            <Button variant="secondary" iconLeft={<ShieldCheck className="size-4" aria-hidden />} loading={toggle.isPending} onClick={() => toggle.mutate(false)}>
              {t('bo.users.unblock')}
            </Button>
          ) : (
            <Button variant="secondary" iconLeft={<Ban className="size-4" aria-hidden />} onClick={() => setConfirm(true)}>
              {t('bo.users.blockAccount')}
            </Button>
          )
        }
      />

      {summary.blocked && (
        <Notice tone="danger" title={t('bo.users.blockedTitle')}>
          {t('bo.users.blockedHint')}
        </Notice>
      )}

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <Stat label={t('bo.users.colSpend')} value={formatRupiah(summary.totalSpendRp)} emphasis="primary" />
        <Stat label={t('bo.users.colBalance')} value={formatRupiah(summary.balanceRp)} />
        <Stat label={t('bo.users.colPlans')} value={summary.plansCount} />
        <Stat label={t('bo.users.colCompleted')} value={summary.completedCount} />
        <Stat label={t('bo.users.colLastPlan')} value={<span className="text-title">{summary.lastPlanAt ? formatAgo(summary.lastPlanAt, new Date(nowMs)) : '—'}</span>} />
      </div>

      <Card className="overflow-hidden">
        <CardHeader title={t('bo.users.plansTitle')} description={t('bo.users.plansCount', { count: operational.length })} className="border-b border-line px-card py-3" />
        {operational.length === 0 ? (
          <EmptyState icon={<PackageSearch />} title={t('bo.users.plansEmptyTitle')} description={t('bo.users.plansEmptyHint')} compact />
        ) : (
          <PlanTable plans={operational} nowMs={nowMs} variant="list" showCustomer={false} />
        )}
      </Card>

      <Card className="overflow-hidden">
        <CardHeader
          title={t('bo.users.topUpsTitle')}
          description={topUps.data ? t('bo.users.topUpsSummary', { count: topUps.data.length, pending: topUps.data.filter((x) => x.status === 'menunggu').length }) : undefined}
          className="border-b border-line px-card py-3"
        />
        {topUps.isError ? (
          <ErrorState compact message={errorMessage(topUps.error)} onRetry={() => void topUps.refetch()} />
        ) : topUps.isLoading ? (
          <div className="space-y-px p-3">
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
          </div>
        ) : (topUps.data ?? []).length === 0 ? (
          <EmptyState icon={<Wallet />} title={t('bo.users.topUpsEmptyTitle')} description={t('bo.users.topUpsEmptyHint')} compact />
        ) : (
          <TopUpTable rows={topUps.data!} showUser={false} />
        )}
      </Card>

      <Card className="overflow-hidden">
        <CardHeader
          title={t('bo.users.ledgerTitle')}
          description={t('bo.users.ledgerNow', { balance: summary.balanceRp })}
          action={
            <Link to="/balances" className="text-small font-semibold text-accent underline underline-offset-4">
              {t('bo.users.ledgerAllTopUps')}
            </Link>
          }
          className="border-b border-line px-card py-3"
        />
        {ledger.isError ? (
          <ErrorState compact message={errorMessage(ledger.error)} onRetry={() => void ledger.refetch()} />
        ) : ledger.isLoading ? (
          <div className="space-y-px p-3">
            <Skeleton className="h-10" />
            <Skeleton className="h-10" />
          </div>
        ) : (ledger.data ?? []).length === 0 ? (
          <EmptyState icon={<Receipt />} title={t('bo.users.ledgerEmptyTitle')} description={t('bo.users.ledgerEmptyHint')} compact />
        ) : (
          <WalletTxTable rows={ledger.data!} />
        )}
      </Card>

      <p className="text-small text-muted">
        {tr('bo.users.moreContext', {
          link: (c) => (
            <Link to={`/plan?user=${user.id}`} className="font-semibold text-accent underline underline-offset-4">
              {c}
            </Link>
          ),
        })}
      </p>

      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={t('bo.users.confirmTitle', { name: user.name })}
        description={t('bo.users.confirmHint')}
        confirmLabel={t('bo.users.blockAccount')}
        tone="danger"
        loading={toggle.isPending}
        onConfirm={() => toggle.mutate(true)}
      />
    </div>
  )
}
