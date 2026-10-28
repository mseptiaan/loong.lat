import { errorMessage, useUsers, useVirtualNow } from '@loong/data'
import { formatAgo, formatPhone, formatRupiah } from '@loong/domain'
import {
  Avatar,
  Button,
  Card,
  EmptyState,
  ErrorState,
  Input,
  PageHeader,
  Select,
  Skeleton,
  StatusLabel,
  TBody,
  TD,
  TH,
  THead,
  TR,
  TableWrap,
} from '@loong/ui'
import { Search, Users as UsersIcon } from 'lucide-react'
import { useState } from 'react'
import { useNavigate } from 'react-router'
import { t } from '../i18n'

export function Users() {
  const users = useUsers()
  const navigate = useNavigate()
  const nowMs = useVirtualNow(60_000)
  const [q, setQ] = useState('')
  const [status, setStatus] = useState<'semua' | 'aktif' | 'diblokir'>('semua')

  const needle = q.trim().toLowerCase()
  const rows = (users.data ?? []).filter(
    (u) => (status === 'semua' || (status === 'diblokir') === u.blocked) && (!needle || `${u.name} ${u.email} ${u.phone}`.toLowerCase().includes(needle)),
  )
  const filtered = needle !== '' || status !== 'semua'
  const totalBalance = rows.reduce((s, u) => s + u.balanceRp, 0)

  return (
    <div className="space-y-4">
      <PageHeader title={t('bo.users.title')} description={t('bo.users.description')} />

      <Card className="overflow-hidden">
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2.5">
          <Input leading={<Search className="size-4" aria-hidden />} value={q} onChange={(e) => setQ(e.target.value)} placeholder={t('bo.users.search')} aria-label={t('bo.users.searchLabel')} className="h-9 w-72" />
          <Select
            aria-label={t('bo.users.statusFilter')}
            value={status}
            onValueChange={(v) => setStatus(v as 'semua' | 'aktif' | 'diblokir')}
            className="h-9 w-44"
            options={[
              { value: 'semua', label: t('bo.users.statusAll') },
              { value: 'aktif', label: t('bo.users.statusActive') },
              { value: 'diblokir', label: t('bo.users.statusBlocked') },
            ]}
          />
          <span className="ml-auto text-small tabular-nums text-muted">
            {t('bo.users.summary', { count: rows.length, total: totalBalance })}
          </span>
        </div>

        {users.isError ? (
          <ErrorState message={errorMessage(users.error)} onRetry={() => void users.refetch()} />
        ) : users.isLoading ? (
          <div className="space-y-px p-3">
            {Array.from({ length: 7 }, (_, i) => (
              <Skeleton key={i} className="h-12" />
            ))}
          </div>
        ) : rows.length === 0 ? (
          <EmptyState
            icon={<UsersIcon />}
            title={filtered ? t('bo.users.emptyFilteredTitle') : t('bo.users.emptyTitle')}
            description={filtered ? t('bo.users.emptyFilteredHint') : t('bo.users.emptyHint')}
            action={
              filtered ? (
                <Button
                  onClick={() => {
                    setQ('')
                    setStatus('semua')
                  }}
                >
                  {t('bo.users.resetFilters')}
                </Button>
              ) : undefined
            }
          />
        ) : (
          <TableWrap>
            <THead>
              <tr>
                <TH>{t('bo.users.colUser')}</TH>
                <TH>{t('bo.users.colPhone')}</TH>
                <TH numeric>{t('bo.users.colPlans')}</TH>
                <TH numeric>{t('bo.users.colCompleted')}</TH>
                <TH numeric>{t('bo.users.colSpend')}</TH>
                <TH numeric>{t('bo.users.colBalance')}</TH>
                <TH>{t('bo.users.colLastPlan')}</TH>
                <TH>{t('bo.users.colStatus')}</TH>
              </tr>
            </THead>
            <TBody>
              {rows.map((u) => (
                <TR
                  key={u.id}
                  interactive
                  tabIndex={0}
                  onClick={() => navigate(`/user/${u.id}`)}
                  onKeyDown={(e) => e.key === 'Enter' && navigate(`/user/${u.id}`)}
                  aria-label={`${u.name}, ${u.email}`}
                  className="outline-none focus-visible:shadow-[inset_0_0_0_2px_var(--accent)]"
                >
                  <TD className="whitespace-nowrap py-2">
                    <span className="flex items-center gap-3">
                      <Avatar name={u.name} />
                      <span>
                        <span className="block font-semibold">{u.name}</span>
                        <span className="block text-small text-muted">{u.email}</span>
                      </span>
                    </span>
                  </TD>
                  <TD className="whitespace-nowrap tabular-nums text-muted">{formatPhone(u.phone)}</TD>
                  <TD numeric>{u.plansCount}</TD>
                  <TD numeric>{u.completedCount}</TD>
                  <TD numeric className="whitespace-nowrap">
                    {formatRupiah(u.totalSpendRp)}
                  </TD>
                  <TD numeric className="whitespace-nowrap">
                    {formatRupiah(u.balanceRp)}
                  </TD>
                  <TD className="whitespace-nowrap text-muted">{u.lastPlanAt ? formatAgo(u.lastPlanAt, new Date(nowMs)) : '—'}</TD>
                  <TD className="whitespace-nowrap">
                    <StatusLabel tone={u.blocked ? 'danger' : 'success'}>{u.blocked ? t('bo.users.statusBlocked') : t('bo.users.statusActive')}</StatusLabel>
                  </TD>
                </TR>
              ))}
            </TBody>
          </TableWrap>
        )}
      </Card>
    </div>
  )
}
