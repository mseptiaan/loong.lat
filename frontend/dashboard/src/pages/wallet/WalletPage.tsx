import { errorMessage, useVirtualNow, useWallet, useWalletTransactions } from '@loong/data'
import { ApiError, TOPUP_METHOD_LABEL, formatRelativeDay, formatRupiah, type TopUp, type WalletTx } from '@loong/domain'
import { renderMsg } from '@loong/i18n'
import { Button, Card, CardHeader, EmptyState, ErrorState, PageHeader, Skeleton, Stat } from '@loong/ui'
import { ArrowDownLeft, ArrowUpRight, Plus, Receipt, RotateCcw } from 'lucide-react'
import { Link } from 'react-router'
import { t } from '../../i18n'
import { formatRemaining } from '../../lib/money'

const ICON = { topup: ArrowDownLeft, pembayaran: ArrowUpRight, refund: RotateCcw } as const

function TxRow({ tx, now }: { tx: WalletTx; now: number }) {
  const Icon = ICON[tx.kind]
  const credit = tx.amountRp > 0
  const title = tx.planId ? (
    <Link to={`/plan/${tx.planId}`} className="truncate font-semibold underline-offset-4 hover:underline">
      {renderMsg(tx.title)}
    </Link>
  ) : (
    <span className="truncate font-semibold">{renderMsg(tx.title)}</span>
  )
  return (
    <li className="flex items-center gap-3 px-card py-3">
      <span className="grid size-9 shrink-0 place-items-center rounded-full bg-sunken text-muted" aria-hidden>
        <Icon className="size-4" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="flex min-w-0 text-body">{title}</p>
        <p className="text-small text-muted">{formatRelativeDay(tx.at, new Date(now))}</p>
      </div>
      <div className="shrink-0 text-right">
        <p className={`text-body font-semibold tabular-nums ${credit ? 'text-success-fg' : ''}`}>
          <span className="sr-only">{credit ? t('dash.wallet.moneyIn') : t('dash.wallet.moneyOut')} </span>
          {credit ? '+' : '−'}
          {formatRupiah(Math.abs(tx.amountRp))}
        </p>
        <p className="text-caption tabular-nums text-subtle">{t('dash.wallet.balanceAfter', { amount: tx.balanceAfterRp })}</p>
      </div>
    </li>
  )
}

function PendingRow({ top, now }: { top: TopUp; now: number }) {
  const remaining = Date.parse(top.expiresAt) - now
  const payHref = `/wallet/top-up/${top.id}`
  return (
    <li className="flex flex-wrap items-center justify-between gap-3 px-card py-3">
      <div className="min-w-0">
        <p className="text-body font-semibold tabular-nums">{formatRupiah(top.amountRp)}</p>
        <p className="text-small text-muted">
          {remaining > 0
            ? t('dash.wallet.pendingMeta', { method: TOPUP_METHOD_LABEL[top.method], remaining: formatRemaining(remaining) })
            : t('dash.wallet.pendingMetaExpired', { method: TOPUP_METHOD_LABEL[top.method] })}
        </p>
      </div>
      <Button variant="secondary" size="sm" asChild>
        <Link to={payHref}>{t('dash.wallet.payNow')}</Link>
      </Button>
    </li>
  )
}

/** Saldo loong.lat: angka saldo, top up yang menunggu, dan riwayat transaksi. */
export function WalletPage() {
  const now = useVirtualNow(1000)
  const wallet = useWallet()
  const txs = useWalletTransactions()

  return (
    <div className="space-y-6">
      <PageHeader
        title={t('dash.wallet.title')}
        description={t('dash.wallet.lead')}
        actions={
          <Button variant="primary" size="lg" asChild>
            <Link to="/wallet/top-up">
              <Plus className="size-4" aria-hidden />
              {t('dash.wallet.topUpButton')}
            </Link>
          </Button>
        }
      />

      {wallet.isError ? (
        <Card>
          <ErrorState message={errorMessage(wallet.error)} onRetry={() => void wallet.refetch()} retrying={wallet.isRefetching} offline={wallet.error instanceof ApiError && wallet.error.code === 'jaringan'} />
        </Card>
      ) : (
        <div className="grid gap-6 lg:grid-cols-[20rem_minmax(0,1fr)]">
          <div className="space-y-4">
            {wallet.isLoading || !wallet.data ? (
              <Skeleton className="h-28 rounded-card" />
            ) : (
              <Stat emphasis="primary" label={t('dash.wallet.available')} value={formatRupiah(wallet.data.balanceRp)} hint={t('dash.wallet.availableHint')} />
            )}
            {wallet.data && wallet.data.pendingTopUps.length > 0 && (
              <Card className="overflow-hidden">
                <CardHeader title={t('dash.wallet.pendingTitle')} description={t('dash.wallet.pendingHint')} className="border-b border-line px-card py-3" />
                <ul className="divide-y divide-line">
                  {wallet.data.pendingTopUps.map((top) => (
                    <PendingRow key={top.id} top={top} now={now} />
                  ))}
                </ul>
              </Card>
            )}
          </div>

          <Card className="overflow-hidden">
            <CardHeader title={t('dash.wallet.historyTitle')} description={t('dash.wallet.historyHint')} className="border-b border-line px-card py-3" />
            {txs.isLoading ? (
              <div className="space-y-3 p-card">
                {[0, 1, 2, 3].map((i) => (
                  <Skeleton key={i} className="h-12 w-full" />
                ))}
              </div>
            ) : txs.isError ? (
              <ErrorState compact message={errorMessage(txs.error)} onRetry={() => void txs.refetch()} retrying={txs.isRefetching} />
            ) : (txs.data ?? []).length === 0 ? (
              <EmptyState
                compact
                icon={<Receipt />}
                title={t('dash.wallet.emptyTitle')}
                description={t('dash.wallet.emptyHint')}
                action={
                  <Button variant="secondary" asChild>
                    <Link to="/wallet/top-up">{t('dash.wallet.topUpButton')}</Link>
                  </Button>
                }
              />
            ) : (
              <ul className="divide-y divide-line" aria-label={t('dash.wallet.historyLabel')}>
                {txs.data!.map((tx) => (
                  <TxRow key={tx.id} tx={tx} now={now} />
                ))}
              </ul>
            )}
          </Card>
        </div>
      )}
    </div>
  )
}
