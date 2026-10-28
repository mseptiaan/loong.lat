import { ChartColumn, IdCard, LayoutDashboard, ListChecks, Map, Settings, Truck, Users, Wallet, type LucideIcon } from 'lucide-react'
import type { enBoApp } from '../i18n/en/app'
import { t } from '../i18n'

/** Kunci label nav (bo.app.nav*). Konstanta modul menyimpan kunci; t() dipanggil saat render agar mengikuti bahasa. */
export type NavKey = Exclude<Extract<keyof typeof enBoApp, `bo.app.nav${string}`>, `${string}_${'one' | 'other'}`>

export interface NavItem {
  to: string
  labelKey: NavKey
  icon: LucideIcon
  end?: boolean
}

export interface NavGroup {
  labelKey: NavKey
  items: NavItem[]
}

export const NAV_GROUPS: NavGroup[] = [
  {
    labelKey: 'bo.app.navGroupOperations',
    items: [
      { to: '/', labelKey: 'bo.app.navCommand', icon: LayoutDashboard, end: true },
      { to: '/plan', labelKey: 'bo.app.navPlans', icon: ListChecks },
      { to: '/map', labelKey: 'bo.app.navLive', icon: Map },
    ],
  },
  {
    labelKey: 'bo.app.navGroupData',
    items: [
      { to: '/user', labelKey: 'bo.app.navUsers', icon: Users },
      { to: '/balances', labelKey: 'bo.app.navBalances', icon: Wallet },
      { to: '/driver', labelKey: 'bo.app.navDrivers', icon: IdCard },
      { to: '/vehicles', labelKey: 'bo.app.navVehicles', icon: Truck },
    ],
  },
  {
    labelKey: 'bo.app.navGroupInsights',
    items: [{ to: '/analytics', labelKey: 'bo.app.navAnalytics', icon: ChartColumn }],
  },
  {
    labelKey: 'bo.app.navGroupSystem',
    items: [{ to: '/settings', labelKey: 'bo.app.navSettings', icon: Settings }],
  },
]

export const NAV_ITEMS: NavItem[] = NAV_GROUPS.flatMap((g) => g.items)

/** Label bagian aktif untuk topbar; jalur detail (/plan/xyz) mengikuti induknya. */
export function sectionLabel(pathname: string): string {
  const hit = NAV_ITEMS.filter((n) => (n.end ? pathname === n.to : pathname === n.to || pathname.startsWith(`${n.to}/`))).sort((a, b) => b.to.length - a.to.length)[0]
  return hit ? t(hit.labelKey) : 'Backoffice'
}
