import { TOPUP_METHOD_LABEL, TOPUP_STATUS_LABEL, formatDateTime, formatRupiah, type AdminTopUp, type TopUp, type TopUpStatus } from '@loong/domain'
import { Avatar, StatusLabel, TBody, TD, TH, THead, TR, TableWrap, type Tone } from '@loong/ui'
import { useNavigate } from 'react-router'
import { t } from '../i18n'

const TONE: Record<TopUpStatus, Tone> = { menunggu: 'warning', berhasil: 'success', kedaluwarsa: 'neutral', dibatalkan: 'neutral' }

/** Keterangan waktu yang relevan dengan status: kapan dibayar, atau sampai kapan berlaku. */
function when(topUp: TopUp): string {
  if (topUp.status === 'berhasil' && topUp.paidAt) return t('bo.balances.topUpPaidAt', { at: topUp.paidAt })
  if (topUp.status === 'menunggu') return t('bo.balances.topUpValidUntil', { at: topUp.expiresAt })
  if (topUp.status === 'kedaluwarsa') return t('bo.balances.topUpEndedAt', { at: topUp.expiresAt })
  return t('bo.balances.topUpCancelledByUser')
}

/**
 * Tabel top up. `showUser` menambah kolom pengguna (baris menuju detail user); tanpa itu dipakai di halaman satu user.
 */
export function TopUpTable({ rows, showUser = true }: { rows: (TopUp | AdminTopUp)[]; showUser?: boolean }) {
  const navigate = useNavigate()
  return (
    <TableWrap>
      <THead>
        <tr>
          <TH>{t('bo.balances.topUpColCreated')}</TH>
          {showUser && <TH>{t('bo.balances.topUpColUser')}</TH>}
          <TH>{t('bo.balances.topUpColMethod')}</TH>
          <TH>{t('bo.balances.topUpColReference')}</TH>
          <TH numeric>{t('bo.balances.topUpColAmount')}</TH>
          <TH>{t('bo.balances.topUpColStatus')}</TH>
        </tr>
      </THead>
      <TBody>
        {rows.map((row) => {
          const user = 'userName' in row ? row : undefined
          const go = showUser && user ? () => navigate(`/user/${row.userId}`) : undefined
          return (
            <TR
              key={row.id}
              interactive={!!go}
              tabIndex={go ? 0 : undefined}
              onClick={go}
              onKeyDown={go ? (e) => e.key === 'Enter' && go() : undefined}
              aria-label={go ? `${row.id}, ${user!.userName}, ${formatRupiah(row.amountRp)}` : undefined}
              className={go ? 'outline-none focus-visible:shadow-[inset_0_0_0_2px_var(--accent)]' : undefined}
            >
              <TD className="whitespace-nowrap tabular-nums text-muted">{formatDateTime(row.createdAt)}</TD>
              {showUser && (
                <TD className="whitespace-nowrap py-2">
                  {user ? (
                    <span className="flex items-center gap-3">
                      <Avatar name={user.userName} />
                      <span>
                        <span className="block font-semibold">{user.userName}</span>
                        <span className="block text-small text-muted">{user.userEmail}</span>
                      </span>
                    </span>
                  ) : (
                    '—'
                  )}
                </TD>
              )}
              <TD className="whitespace-nowrap">{TOPUP_METHOD_LABEL[row.method]}</TD>
              <TD className="whitespace-nowrap font-mono text-small tabular-nums text-muted">{row.reference}</TD>
              <TD numeric className="whitespace-nowrap font-semibold">
                {formatRupiah(row.amountRp)}
              </TD>
              <TD className="whitespace-nowrap py-2">
                <StatusLabel tone={TONE[row.status]}>{TOPUP_STATUS_LABEL[row.status]}</StatusLabel>
                <span className="block text-small text-muted">{when(row)}</span>
              </TD>
            </TR>
          )
        })}
      </TBody>
    </TableWrap>
  )
}
