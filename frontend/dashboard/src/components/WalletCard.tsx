import { errorMessage, useWallet } from '@loong/data'
import { formatRupiah } from '@loong/domain'
import { Button, Card, Skeleton } from '@loong/ui'
import { Plus } from 'lucide-react'
import { Link } from 'react-router'
import { t } from '../i18n'

/** Kartu saldo di Beranda: angka saldo, jalan pintas top up, dan penanda bila ada top up yang menunggu pembayaran. */
export function WalletCard() {
  const { data, isLoading, isError, error, refetch } = useWallet()
  const pendingHref = `/wallet/top-up/${data?.pendingTopUps[0]?.id ?? ''}`
  return (
    <Card className="p-card">
      <p className="text-small font-medium text-muted">{t('dash.wallet.cardTitle')}</p>
      {isLoading ? (
        <Skeleton className="mt-2 h-8 w-36" />
      ) : isError || !data ? (
        <p className="mt-1 text-small text-muted">
          {errorMessage(error)}{' '}
          <button type="button" onClick={() => void refetch()} className="font-semibold text-accent underline underline-offset-4">
            {t('dash.wallet.retry')}
          </button>
        </p>
      ) : (
        <>
          <p className="mt-1 text-metric-sm tabular-nums">{formatRupiah(data.balanceRp)}</p>
          {data.pendingTopUps.length > 0 && (
            <p className="mt-1 text-small text-muted">
              <Link to={pendingHref} className="font-semibold text-accent underline underline-offset-4 hover:text-accent-hover">
                {t('dash.wallet.pendingCount', { count: data.pendingTopUps.length })}
              </Link>
            </p>
          )}
        </>
      )}
      <div className="mt-4 flex gap-2">
        <Button variant="secondary" asChild>
          <Link to="/wallet/top-up">
            <Plus className="size-4" aria-hidden />
            {t('dash.wallet.topUp')}
          </Link>
        </Button>
        <Button variant="ghost" asChild>
          <Link to="/wallet">{t('dash.wallet.history')}</Link>
        </Button>
      </div>
    </Card>
  )
}
