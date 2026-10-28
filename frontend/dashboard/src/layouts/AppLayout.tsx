import { useApi, useProfile, useSession } from '@loong/data'
import { Avatar, BRAND, Button, Logo, Menu, MenuItem, MenuLabel, MenuSeparator, cn } from '@loong/ui'
import { LOCALE_NAME, getLocale, setLocale } from '@loong/i18n'
import { NotificationBell, SkipLink, useDocumentTitle } from '@loong/shell'
import { House, Languages, LogOut, MapPin, Package, Plus, UserRound, Wallet } from 'lucide-react'
import { NavLink, Outlet, Link, useLocation, useNavigate } from 'react-router'
import { BalanceChip } from '../components/BalanceChip'
import { t } from '../i18n'

// teks dipanggil saat render (bukan di tingkat modul) agar mengikuti bahasa
const NAV = [
  { to: '/', label: () => t('dash.app.navHome'), icon: House, end: true },
  { to: '/plan', label: () => t('dash.app.navDeliveries'), icon: Package, end: false },
  { to: '/addresses', label: () => t('dash.app.navAddresses'), icon: MapPin, end: false },
] as const

/** Kerangka pengirim: navigasi atas (desktop) + tab bawah (HP). Satu aksi utama "Buat pengiriman" (New delivery). */
export function AppLayout() {
  const api = useApi()
  const session = useSession()
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const { data: profile } = useProfile()
  const name = profile?.name ?? session?.name ?? t('dash.app.navAccount')
  const onHome = pathname === '/'
  const otherLocale = getLocale() === 'en' ? 'id' : 'en'
  useDocumentTitle(
    pathname === '/'
      ? t('dash.app.navHome')
      : pathname.startsWith('/plan/')
        ? t('dash.app.titleDeliveryDetail')
        : pathname.startsWith('/plan')
          ? t('dash.app.navDeliveries')
          : pathname.startsWith('/addresses')
            ? t('dash.app.navAddresses')
            : pathname.startsWith('/wallet')
              ? t('dash.app.titleBalance')
              : pathname.startsWith('/account')
                ? t('dash.app.navAccount')
                : BRAND.name,
    t('dash.app.titleApp'),
  )

  return (
    <div className="min-h-screen pb-24 md:pb-0">
      <SkipLink />
      <header className="sticky top-0 z-40 border-b border-line bg-surface/90 backdrop-blur-md">
        <div className="mx-auto flex h-16 max-w-6xl items-center gap-4 px-4 sm:px-6">
          <Link to="/" aria-label={t('dash.app.homeLink')} className="rounded-input">
            <Logo />
          </Link>
          <nav aria-label={t('dash.app.mainNav')} className="ml-4 hidden gap-1 md:flex">
            {NAV.map((n) => (
              <NavLink
                key={n.to}
                to={n.to}
                end={n.end}
                className={({ isActive }) =>
                  cn(
                    'relative inline-flex h-10 items-center rounded-input px-3.5 text-body font-medium transition-colors',
                    'after:absolute after:inset-x-3.5 after:-bottom-[13px] after:h-0.5 after:rounded-full after:bg-accent after:opacity-0 after:transition-opacity',
                    isActive ? 'text-fg after:opacity-100' : 'text-muted hover:text-fg',
                  )
                }
              >
                {n.label()}
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-2">
            {/* di Beranda, aksi utama sudah ada di judul halaman; di sini hanya muncul di halaman lain */}
            {!onHome && (
              <Button variant="primary" asChild className="hidden md:inline-flex">
                <Link to="/plan/new">
                  <Plus className="size-4" aria-hidden />
                  {t('dash.app.newDelivery')}
                </Link>
              </Button>
            )}
            <BalanceChip />
            <NotificationBell onOpenPlan={(id) => navigate(`/plan/${id}`)} />
            <Menu
              trigger={
                <button type="button" aria-label={t('dash.app.accountMenu')} className="rounded-full p-0.5 transition-colors hover:bg-sunken">
                  <Avatar name={name} />
                </button>
              }
            >
              <MenuLabel>
                <span className="block font-semibold text-fg">{name}</span>
                <span className="block truncate">{profile?.email}</span>
              </MenuLabel>
              <MenuSeparator />
              <MenuItem icon={<UserRound />} onSelect={() => navigate('/account')}>
                {t('dash.app.menuMyAccount')}
              </MenuItem>
              <MenuItem icon={<Wallet />} onSelect={() => navigate('/wallet')}>
                {t('dash.app.menuBalance')}
              </MenuItem>
              <MenuItem icon={<MapPin />} onSelect={() => navigate('/addresses')}>
                {t('dash.app.menuAddresses')}
              </MenuItem>
              <MenuSeparator />
              {/* bahasa: butir menu yang menawarkan bahasa lain (ditulis dalam bahasanya sendiri); pengganti EN | ID penuh ada di halaman Akun */}
              <MenuItem icon={<Languages />} lang={otherLocale} onSelect={() => setLocale(otherLocale)}>
                {LOCALE_NAME[otherLocale]}
              </MenuItem>
              <MenuSeparator />
              <MenuItem
                icon={<LogOut />}
                onSelect={async () => {
                  await api.auth.logout()
                  navigate('/login', { replace: true })
                }}
              >
                {t('dash.app.signOut')}
              </MenuItem>
            </Menu>
          </div>
        </div>
      </header>

      <main id="utama" tabIndex={-1} className="mx-auto max-w-6xl px-4 py-6 outline-none sm:px-6 sm:py-8">
        <Outlet />
      </main>

      {/* HP: tab bawah dengan tombol tengah "Buat" (satu-satunya aksi utama di layar kecil) */}
      <nav aria-label={t('dash.app.mainNav')} className="fixed inset-x-0 bottom-0 z-40 border-t border-line bg-surface/95 pb-[env(safe-area-inset-bottom)] backdrop-blur-md md:hidden">
        <ul className="mx-auto grid max-w-md grid-cols-4 items-end px-2">
          <TabLink to="/" end icon={House} label={t('dash.app.navHome')} />
          <TabLink to="/plan" icon={Package} label={t('dash.app.navDeliveries')} />
          <TabLink to="/addresses" icon={MapPin} label={t('dash.app.navAddresses')} />
          <TabLink to="/account" icon={UserRound} label={t('dash.app.navAccount')} />
        </ul>
        {!onHome && (
          <Link
            to="/plan/new"
            aria-label={t('dash.app.newDelivery')}
            className="absolute -top-6 left-1/2 grid size-12 -translate-x-1/2 place-items-center rounded-full bg-accent text-on-accent shadow-overlay transition-transform active:scale-95"
          >
            <Plus className="size-6" aria-hidden />
          </Link>
        )}
      </nav>
    </div>
  )
}

function TabLink({ to, end, icon: Icon, label }: { to: string; end?: boolean; icon: typeof House; label: string }) {
  return (
    <li>
      <NavLink
        to={to}
        end={end}
        className={({ isActive }) =>
          cn('flex min-h-14 flex-col items-center justify-center gap-0.5 text-caption font-medium', isActive ? 'text-accent' : 'text-muted')
        }
      >
        <Icon className="size-5" aria-hidden />
        {label}
      </NavLink>
    </li>
  )
}
