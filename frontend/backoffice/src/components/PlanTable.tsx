import { VEHICLE_TYPE_LABEL, formatPercent, formatRelativeDay, formatRupiah, type PlanSummary, type VehicleType } from '@loong/domain'
import { Button, PlanStatusLabel, TBody, TD, TH, THead, TR, TableWrap, cn } from '@loong/ui'
import { Motorbike, Van } from 'lucide-react'
import { useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router'
import { t } from '../i18n'
import { actionText, isActive, needsAction, routeText } from '../lib/plans'

function Vehicles({ types }: { types: VehicleType[] }) {
  if (types.length === 0) return <span className="text-subtle">—</span>
  const counts = types.reduce<Record<string, number>>((acc, type) => ({ ...acc, [type]: (acc[type] ?? 0) + 1 }), {})
  return (
    <span className="inline-flex flex-wrap items-center gap-x-2.5 gap-y-0.5">
      {(Object.keys(counts) as VehicleType[]).map((type) => {
        const Icon = type === 'motor' ? Motorbike : Van
        return (
          <span key={type} className="inline-flex items-center gap-1 text-muted" title={t('bo.plans.vehicleCount', { count: counts[type]!, vehicle: VEHICLE_TYPE_LABEL[type] })}>
            <Icon className="size-3.5" aria-hidden />
            <span className="tabular-nums">{counts[type]}</span>
            <span className="sr-only">{VEHICLE_TYPE_LABEL[type]}</span>
          </span>
        )
      })}
    </span>
  )
}

const departure = (p: PlanSummary, nowMs: number) => {
  if (!p.startAt) return '—'
  if (p.scheduleMode === 'terjadwal') return formatRelativeDay(p.startAt, new Date(nowMs))
  // "Segera" hanya masuk akal selama masih menunggu driver; setelah itu yang relevan adalah jam berangkat sebenarnya
  return p.status === 'menunggu_driver' ? t('bo.plans.asap') : t('bo.plans.departsAt', { time: p.startAt })
}

/**
 * Tabel plan untuk data padat: header sticky, angka rata-kanan bertabular, aksi selalu terlihat (bukan hanya saat hover),
 * navigasi keyboard (j/k atau panah, Enter membuka, a menugaskan). `variant="queue"` ringkas untuk antrean;
 * `variant="list"` menambah pelanggan, waktu dibuat, dan penghematan.
 */
export function PlanTable({
  plans,
  nowMs,
  variant = 'queue',
  showCustomer = true,
  onAssign,
  className,
}: {
  plans: PlanSummary[]
  nowMs: number
  variant?: 'queue' | 'list'
  /** Kolom pelanggan di varian daftar; matikan bila tabel sudah berada di halaman satu pelanggan. */
  showCustomer?: boolean
  onAssign?: (planId: string) => void
  className?: string
}) {
  const navigate = useNavigate()
  const rows = useRef<(HTMLTableRowElement | null)[]>([])
  const [active, setActive] = useState(0) // roving tabindex: satu baris saja yang masuk urutan Tab
  const list = variant === 'list'
  // anggaran aksen: hanya baris teratas yang butuh tindakan memakai tombol primer; sisanya sekunder
  const firstActionId = plans.find((p) => isActive(p) && p.routesWithoutDriver > 0 && needsAction(p))?.id

  const focusRow = (i: number) => {
    const next = Math.max(0, Math.min(plans.length - 1, i))
    setActive(next)
    rows.current[next]?.focus()
  }

  return (
    <TableWrap className={cn('max-h-full', className)}>
      <THead>
        <tr>
          <TH>{t('bo.plans.colPlan')}</TH>
          {list && showCustomer && <TH>{t('bo.plans.colCustomer')}</TH>}
          <TH>{t('bo.plans.colRoute')}</TH>
          {list && <TH>{t('bo.plans.colVehicle')}</TH>}
          <TH>{list ? t('bo.plans.colCreated') : t('bo.plans.colDeparts')}</TH>
          <TH numeric>{t('bo.plans.colCost')}</TH>
          {list && <TH numeric>{t('bo.plans.colSavings')}</TH>}
          <TH>{t('bo.plans.colStatus')}</TH>
          <TH className="sticky right-0 z-20 shadow-[-10px_0_8px_-8px_rgb(0_0_0/0.08)]">{t('bo.plans.colDriver')}</TH>
        </tr>
      </THead>
      <TBody>
        {plans.map((p, i) => {
          const action = actionText(p, nowMs)
          const open = () => navigate(`/plan/${p.id}`)
          const unassigned = isActive(p) && p.routesWithoutDriver > 0 && !p.pendingAction // sedang dibatalkan: tak ada yang ditugaskan
          return (
            <TR
              key={p.id}
              interactive
              tabIndex={i === Math.min(active, plans.length - 1) ? 0 : -1}
              ref={(el) => {
                rows.current[i] = el
              }}
              onFocus={() => setActive(i)}
              onClick={open}
              onKeyDown={(e) => {
                if (e.target !== e.currentTarget) return // biarkan tombol di dalam baris menangani tombolnya sendiri
                if (e.metaKey || e.ctrlKey || e.altKey) return // Cmd/Ctrl+K (palet), Ctrl+A, dst. bukan pintasan baris
                if (e.key === 'Enter') {
                  open()
                } else if (e.key === 'j' || e.key === 'ArrowDown') {
                  e.preventDefault()
                  focusRow(i + 1)
                } else if (e.key === 'k' || e.key === 'ArrowUp') {
                  e.preventDefault()
                  focusRow(i - 1)
                } else if (e.key === 'a' && unassigned && onAssign) {
                  onAssign(p.id)
                }
              }}
              className="outline-none focus-visible:shadow-[inset_0_0_0_2px_var(--accent)]"
            >
              <TD className="whitespace-nowrap py-2">
                {/* tautan nyata: pembaca layar, kontrol suara, dan switch bisa membuka plan tanpa mengandalkan klik baris */}
                <Link to={`/plan/${p.id}`} tabIndex={-1} onClick={(e) => e.stopPropagation()} className="block rounded-chip font-semibold tabular-nums hover:underline">
                  {p.code}
                  <span className="sr-only">, {p.customerName}, {routeText(p)}</span>
                </Link>
                {!list && <span className="block text-small text-muted">{p.customerName}</span>}
              </TD>
              {list && showCustomer && <TD className="whitespace-nowrap">{p.customerName}</TD>}
              <TD className="max-w-[16rem] py-2">
                <span className="block truncate" title={routeText(p)}>
                  {routeText(p)}
                </span>
                <span className="flex items-center gap-2 text-small text-muted">
                  {t('bo.plans.stops', { count: p.stops })}
                  {!list && <Vehicles types={p.vehicleTypes} />}
                </span>
              </TD>
              {list && (
                <TD className="whitespace-nowrap">
                  <Vehicles types={p.vehicleTypes} />
                </TD>
              )}
              <TD className="whitespace-nowrap tabular-nums text-muted">{list ? formatRelativeDay(p.createdAt, new Date(nowMs)) : departure(p, nowMs)}</TD>
              <TD numeric className="whitespace-nowrap">
                {p.totalCostRp ? formatRupiah(p.totalCostRp) : <span className="text-subtle">—</span>}
              </TD>
              {list && (
                <TD numeric className="whitespace-nowrap text-muted">
                  {p.savingsPct && p.savingsPct > 0 ? formatPercent(p.savingsPct) : '—'}
                </TD>
              )}
              <TD className="whitespace-nowrap py-2">
                <PlanStatusLabel status={p.status} pendingAction={p.pendingAction} />
                {p.status === 'berjalan' && !action && !p.pendingAction && (
                  <span className="block text-small tabular-nums text-muted">
                    {t('bo.plans.progress', { done: p.progress.done, total: p.progress.total })}
                  </span>
                )}
                {action && needsAction(p) && <span className="block text-small font-medium text-warning-fg">{action}</span>}
              </TD>
              <TD className="sticky right-0 z-[5] whitespace-nowrap bg-surface shadow-[-10px_0_8px_-8px_rgb(0_0_0/0.08)] group-hover:bg-sunken group-focus-visible:bg-accent-tint">
                {unassigned && onAssign ? (
                  <Button
                    size="sm"
                    variant={p.id === firstActionId ? 'primary' : 'secondary'}
                    onClick={(e) => {
                      e.stopPropagation()
                      onAssign(p.id)
                    }}
                    aria-label={t('bo.plans.assignFor', { code: p.code })}
                  >
                    {t('bo.plans.assign')}
                  </Button>
                ) : p.driverNames.length > 0 ? (
                  <span>{p.driverNames.map((n) => n.split(' ')[0]).join(', ')}</span>
                ) : (
                  <span className="text-subtle">—</span>
                )}
              </TD>
            </TR>
          )
        })}
      </TBody>
    </TableWrap>
  )
}
