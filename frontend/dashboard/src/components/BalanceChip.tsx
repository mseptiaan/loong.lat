import { useWallet } from '@loong/data'
import { formatRupiah } from '@loong/domain'
import { Skeleton } from '@loong/ui'
import { Wallet } from 'lucide-react'
import { Link } from 'react-router'
import { t } from '../i18n'

/** Saldo tersedia di bilah atas; menuju halaman saldo. Disembunyikan bila saldo gagal dimuat (angka salah lebih buruk dari tanpa angka). */
export function BalanceChip() {
  const { data, isLoading, isError } = useWallet()
  if (isError) return null
  return (
    <Link
      to="/wallet"
      aria-label={data ? t('dash.wallet.chipLabel', { amount: data.balanceRp }) : t('dash.wallet.chipLabelNoAmount')}
      className="inline-flex h-10 items-center gap-2 rounded-input border border-line bg-surface px-3 text-body font-medium tabular-nums transition-colors hover:bg-sunken"
    >
      <Wallet className="size-4 text-subtle" aria-hidden />
      {isLoading || !data ? <Skeleton className="h-4 w-16" /> : formatRupiah(data.balanceRp)}
    </Link>
  )
}
