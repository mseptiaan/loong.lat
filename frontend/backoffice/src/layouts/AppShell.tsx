import { useApi, usePlans, useSession, useVirtualNow } from '@loong/data'
import { LocaleSwitcher, NotificationBell, SkipLink, useDocumentTitle } from '@loong/shell'
import { Avatar, Kbd, Logo, Menu, MenuItem, MenuLabel, MenuSeparator, cn } from '@loong/ui'
import { LogOut, Search } from 'lucide-react'
import { useEffect, useState } from 'react'
import { NavLink, Outlet, useLocation, useNavigate } from 'react-router'
import { AppCommand } from '../components/AppCommand'
import { t } from '../i18n'
import { needsAction } from '../lib/plans'
import { NAV_GROUPS, sectionLabel } from './nav'

/**
 * Kerangka Command: sidebar 240px bertint (bukan hitam) yang menciut jadi rail 56px di bawah 1280px,
 * topbar 56px dengan pencarian ⌘K. Konten mengisi sisanya; tiap halaman mengatur kepadatannya sendiri.
 */
export function AppShell() {
  const api = useApi()
  const navigate = useNavigate()
  const { pathname } = useLocation()
  const session = useSession()
  const plans = usePlans()
  const nowMs = useVirtualNow(30_000)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const actionCount = plans.data?.filter(needsAction).length ?? 0
  useDocumentTitle(sectionLabel(pathname), 'Backoffice')

  // ⌘K / Ctrl+K membuka palet dari mana saja
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
        e.preventDefault()
        setPaletteOpen((open) => !open)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  const name = session?.name ?? t('bo.app.shellRoleAdmin')

  return (
    <div className="grid min-h-screen grid-cols-[3.5rem_minmax(0,1fr)] xl:grid-cols-[15rem_minmax(0,1fr)]">
      <SkipLink />
      <aside className="sticky top-0 flex h-screen flex-col border-r border-line bg-sunken">
        <div className="flex h-14 items-center justify-center border-b border-line px-3 xl:justify-start xl:px-4">
          <NavLink to="/" aria-label={t('bo.app.shellHome')} className="rounded-input">
            <span className="hidden xl:block">
              <Logo />
            </span>
            <span className="xl:hidden">
              <Logo wordmark={false} />
            </span>
          </NavLink>
        </div>

        <nav aria-label={t('bo.app.shellNav')} className="min-h-0 flex-1 space-y-5 overflow-y-auto px-2 py-4 xl:px-3">
          {NAV_GROUPS.map((group) => (
            <div key={group.labelKey}>
              <p className="mb-1.5 hidden px-2.5 text-caption font-medium text-subtle xl:block">{t(group.labelKey)}</p>
              <ul className="space-y-0.5">
                {group.items.map((item) => (
                  <li key={item.to}>
                    <NavLink
                      to={item.to}
                      end={item.end}
                      title={t(item.labelKey)}
                      className={({ isActive }) =>
                        cn(
                          'flex h-9 items-center justify-center gap-3 rounded-input text-body font-medium transition-colors xl:justify-start xl:px-2.5',
                          isActive ? 'bg-surface text-fg shadow-card' : 'text-muted hover:bg-surface/60 hover:text-fg',
                        )
                      }
                    >
                      {({ isActive }) => (
                        <>
                          <item.icon className={cn('size-[18px] shrink-0', isActive && 'text-accent')} aria-hidden />
                          <span className="sr-only xl:not-sr-only xl:flex-1">{t(item.labelKey)}</span>
                          {item.to === '/' && actionCount > 0 && (
                            <span className="hidden text-small font-semibold tabular-nums text-warning-fg xl:inline" aria-label={t('bo.app.navNeedsAttention', { count: actionCount })}>
                              {actionCount}
                            </span>
                          )}
                        </>
                      )}
                    </NavLink>
                  </li>
                ))}
              </ul>
            </div>
          ))}
        </nav>

        <div className="border-t border-line p-2 xl:p-3">
          <Menu
            align="start"
            trigger={
              <button
                type="button"
                aria-label={t('bo.app.shellAccountMenu')}
                className="flex h-11 w-full items-center justify-center gap-2.5 rounded-input text-left transition-colors hover:bg-surface/60 xl:justify-start xl:px-2"
              >
                <Avatar name={name} />
                <span className="hidden min-w-0 xl:block">
                  <span className="block truncate text-small font-semibold">{name}</span>
                  <span className="block truncate text-caption text-muted">{t('bo.app.shellRoleAdmin')}</span>
                </span>
              </button>
            }
          >
            <MenuLabel>
              <span className="block font-semibold text-fg">{name}</span>
              <span className="block text-muted">admin@loong.lat</span>
            </MenuLabel>
            <MenuSeparator />
            <MenuItem
              icon={<LogOut />}
              onSelect={async () => {
                await api.auth.logout()
                navigate('/login', { replace: true })
              }}
            >
              {t('bo.app.shellSignOut')}
            </MenuItem>
          </Menu>
        </div>
      </aside>

      <div className="flex min-w-0 flex-col">
        <header className="sticky top-0 z-30 flex h-14 items-center gap-3 border-b border-line bg-bg/90 px-4 backdrop-blur-md lg:px-6">
          <p className="text-title">{sectionLabel(pathname)}</p>
          <div className="ml-auto flex items-center gap-2">
            <button
              type="button"
              onClick={() => setPaletteOpen(true)}
              className="inline-flex h-9 items-center gap-2.5 rounded-input border border-line-input bg-surface px-3 text-small text-muted transition-colors hover:border-subtle hover:text-fg lg:w-72"
              aria-label={t('bo.app.shellSearchAria')}
            >
              <Search className="size-4 shrink-0" aria-hidden />
              <span className="hidden flex-1 text-left lg:inline">{t('bo.app.shellSearchPlaceholder')}</span>
              <span className="hidden gap-1 lg:inline-flex">
                <Kbd>⌘</Kbd>
                <Kbd>K</Kbd>
              </span>
            </button>
            <LocaleSwitcher className="hidden sm:inline-flex" />
            <NotificationBell onOpenPlan={(id) => navigate(`/plan/${id}`)} />
          </div>
        </header>

        <main id="utama" tabIndex={-1} className="min-w-0 flex-1 p-4 outline-none lg:p-6">
          <Outlet />
        </main>
      </div>

      <AppCommand open={paletteOpen} onOpenChange={setPaletteOpen} nowMs={nowMs} />
    </div>
  )
}
