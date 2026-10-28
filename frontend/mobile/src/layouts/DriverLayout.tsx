import { BRAND, cn } from '@loong/ui'
import { History, House, UserRound, Wallet, WifiOff } from 'lucide-react'
import { SkipLink, useDocumentTitle } from '@loong/shell'
import { NavLink, Outlet, useLocation, useMatch } from 'react-router'
import { t } from '../i18n'
import { useOnlineStatus } from '../lib/device'

// kunci (bukan teks) agar label mengikuti bahasa saat render
const TABS = [
  { to: '/', labelKey: 'drv.app.navHome', icon: House, end: true },
  { to: '/history', labelKey: 'drv.app.navHistory', icon: History, end: false },
  { to: '/earnings', labelKey: 'drv.app.navEarnings', icon: Wallet, end: false },
  { to: '/profile', labelKey: 'drv.app.navProfile', icon: UserRound, end: false },
] as const

/**
 * Kolom selebar ponsel (di desktop dipusatkan dengan bingkai tipis). Layar rute adalah mode fokus: tanpa tab bawah,
 * karena bagian bawah dipakai aksi utama (geser untuk tiba/selesai).
 */
export function DriverLayout() {
  const online = useOnlineStatus()
  const focus = useMatch('/route/*') !== null
  const { pathname } = useLocation()
  useDocumentTitle(
    pathname === '/'
      ? t('drv.app.navHome')
      : pathname.startsWith('/route')
        ? pathname.includes('/stop/')
          ? t('drv.app.titleStop')
          : t('drv.app.titleRoute')
        : pathname.startsWith('/history')
          ? t('drv.app.navHistory')
          : pathname.startsWith('/earnings')
            ? t('drv.app.navEarnings')
            : pathname.startsWith('/profile')
              ? t('drv.app.navProfile')
              : BRAND.name,
    t('drv.app.titleApp'),
  )

  return (
    <div className="min-h-dvh bg-sunken">
      <SkipLink />
      <div className="relative mx-auto min-h-dvh max-w-[28rem] bg-bg sm:border-x sm:border-line">
        {!online && (
          <div role="status" className="sticky top-0 z-50 flex items-center gap-2 bg-warning-tint px-4 py-2.5 text-small font-medium text-warning-fg">
            <WifiOff className="size-4 shrink-0" aria-hidden />
            {t('drv.app.offline')}
          </div>
        )}
        <div id="utama" tabIndex={-1} className={cn('outline-none', !focus && 'pb-[calc(5rem+env(safe-area-inset-bottom))]')}>
          <Outlet />
        </div>

        {!focus && (
          <nav
            aria-label={t('drv.app.navLabel')}
            className="fixed inset-x-0 bottom-0 z-40 mx-auto grid max-w-[28rem] grid-cols-4 border-t border-line bg-surface/95 pb-[env(safe-area-inset-bottom)] backdrop-blur-md"
          >
            {TABS.map(({ to, labelKey, icon: Icon, end }) => (
              <NavLink
                key={to}
                to={to}
                end={end}
                className={({ isActive }) =>
                  cn(
                    'relative flex min-h-16 flex-col items-center justify-center gap-1 text-caption font-medium transition-colors',
                    // penanda aktif bukan hanya warna: garis di atas + huruf tebal (terbaca di bawah matahari atau bagi buta warna)
                    isActive ? 'font-bold text-accent before:absolute before:inset-x-4 before:top-0 before:h-[3px] before:rounded-b-full before:bg-accent' : 'text-muted active:text-fg',
                  )
                }
              >
                <Icon className="size-6" aria-hidden />
                {t(labelKey)}
              </NavLink>
            ))}
          </nav>
        )}
      </div>
    </div>
  )
}
