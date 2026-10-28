import { formatDateTime, formatRupiah, type WalletTx } from '@loong/domain'
import { renderMsg } from '@loong/i18n'
import { TBody, TD, TH, THead, TR, TableWrap } from '@loong/ui'
import { Link } from 'react-router'
import { t } from '../i18n'

const KIND_KEY = {
  topup: 'bo.balances.ledgerKindTopup',
  pembayaran: 'bo.balances.ledgerKindPayment',
  refund: 'bo.balances.ledgerKindRefund',
} as const satisfies Record<WalletTx['kind'], string>

/** Buku besar saldo satu pengguna: tiap baris menyimpan saldo sesudahnya, jadi angka akhir bisa diperiksa dari atas ke bawah. */
export function WalletTxTable({ rows }: { rows: WalletTx[] }) {
  return (
    <TableWrap>
      <THead>
        <tr>
          <TH>{t('bo.balances.ledgerColTime')}</TH>
          <TH>{t('bo.balances.ledgerColKind')}</TH>
          <TH>{t('bo.balances.ledgerColNote')}</TH>
          <TH numeric>{t('bo.balances.ledgerColAmount')}</TH>
          <TH numeric>{t('bo.balances.ledgerColBalanceAfter')}</TH>
        </tr>
      </THead>
      <TBody>
        {rows.map((tx) => (
          <TR key={tx.id}>
            <TD className="whitespace-nowrap tabular-nums text-muted">{formatDateTime(tx.at)}</TD>
            <TD className="whitespace-nowrap">{t(KIND_KEY[tx.kind])}</TD>
            <TD>
              {tx.planId ? (
                <Link to={`/plan/${tx.planId}`} className="underline-offset-4 hover:underline">
                  {renderMsg(tx.title)}
                </Link>
              ) : (
                renderMsg(tx.title)
              )}
            </TD>
            <TD numeric className={`whitespace-nowrap font-semibold ${tx.amountRp > 0 ? 'text-success-fg' : ''}`}>
              {tx.amountRp > 0 ? '+' : '−'}
              {formatRupiah(Math.abs(tx.amountRp))}
            </TD>
            <TD numeric className="whitespace-nowrap text-muted">
              {formatRupiah(tx.balanceAfterRp)}
            </TD>
          </TR>
        ))}
      </TBody>
    </TableWrap>
  )
}
