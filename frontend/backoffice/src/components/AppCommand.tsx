import { useDrivers, usePlans, useUsers } from '@loong/data'
import { DRIVER_STATUS_LABEL, PLAN_STATUS_LABEL, formatRupiah, planStatusText } from '@loong/domain'
import { CommandPalette, type CommandSection } from '@loong/ui'
import { IdCard, Map, PackageSearch, Plus, TriangleAlert, UserRound, Wallet } from 'lucide-react'
import { useMemo } from 'react'
import { useNavigate } from 'react-router'
import { t } from '../i18n'
import { NAV_ITEMS } from '../layouts/nav'
import { isOperational, routeText, sortQueue } from '../lib/plans'

/** Kata kunci pencarian dari katalog: daftar dipisah koma, memuat istilah kedua bahasa. */
const keywords = (list: string) => list.split(',').map((k) => k.trim()).filter(Boolean)

/** Palet ⌘K: halaman, aksi cepat, dan pencarian plan/user/driver dari data yang sudah dimuat di cache. */
export function AppCommand({ open, onOpenChange, nowMs }: { open: boolean; onOpenChange: (open: boolean) => void; nowMs: number }) {
  const navigate = useNavigate()
  const plans = usePlans()
  const users = useUsers()
  const drivers = useDrivers()

  const sections = useMemo<CommandSection[]>(() => {
    const operational = sortQueue((plans.data ?? []).filter(isOperational), nowMs)
    return [
      {
        heading: t('bo.app.cmdQuickActions'),
        items: [
          { id: 'aksi:antrean', label: t('bo.app.cmdOpenQueue'), icon: <TriangleAlert />, keywords: keywords(t('bo.app.cmdOpenQueueKeywords')), onSelect: () => navigate('/?tab=tindakan') },
          { id: 'aksi:topup', label: t('bo.app.cmdPendingTopUps'), icon: <Wallet />, keywords: keywords(t('bo.app.cmdPendingTopUpsKeywords')), onSelect: () => navigate('/balances?status=menunggu') }, // i18n-ignore: nilai status dari backend
          { id: 'aksi:peta', label: t('bo.app.cmdOpenMap'), icon: <Map />, keywords: keywords(t('bo.app.cmdOpenMapKeywords')), onSelect: () => navigate('/map') },
          { id: 'aksi:kendaraan', label: t('bo.app.cmdAddVehicle'), icon: <Plus />, keywords: keywords(t('bo.app.cmdAddVehicleKeywords')), onSelect: () => navigate('/vehicles?new=1') },
          { id: 'aksi:driver', label: t('bo.app.cmdAddDriver'), icon: <Plus />, keywords: keywords(t('bo.app.cmdAddDriverKeywords')), onSelect: () => navigate('/driver?new=1') },
        ],
      },
      {
        heading: t('bo.app.cmdPages'),
        items: NAV_ITEMS.map((n) => ({ id: `nav:${n.to}`, label: t(n.labelKey), icon: <n.icon />, onSelect: () => navigate(n.to) })),
      },
      {
        heading: t('bo.app.cmdPlans'),
        limitWhenEmpty: 5,
        items: operational.map((p) => ({
          id: `plan:${p.id}`,
          label: `${p.code} · ${routeText(p)}`,
          hint: `${planStatusText(p)}${p.totalCostRp ? ` · ${formatRupiah(p.totalCostRp)}` : ''}`,
          icon: <PackageSearch />,
          keywords: [p.customerName, ...p.driverNames, p.status, PLAN_STATUS_LABEL[p.status]],
          onSelect: () => navigate(`/plan/${p.id}`),
        })),
      },
      {
        heading: t('bo.app.cmdUsers'),
        limitWhenEmpty: 3,
        items: (users.data ?? []).map((u) => ({
          id: `user:${u.id}`,
          label: u.name,
          hint: u.email,
          icon: <UserRound />,
          keywords: [u.phone],
          onSelect: () => navigate(`/user/${u.id}`),
        })),
      },
      {
        heading: t('bo.app.cmdDrivers'),
        limitWhenEmpty: 3,
        items: (drivers.data ?? []).map((d) => ({
          id: `driver:${d.id}`,
          label: d.name,
          hint: DRIVER_STATUS_LABEL[d.status],
          icon: <IdCard />,
          keywords: [d.phone],
          onSelect: () => navigate(`/driver?search=${encodeURIComponent(d.name)}`),
        })),
      },
    ]
  }, [plans.data, users.data, drivers.data, navigate, nowMs])

  return <CommandPalette open={open} onOpenChange={onOpenChange} sections={sections} />
}

