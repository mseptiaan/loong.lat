import { REAL_BACKEND } from '@loong/data'
import { BRAND, Logo, Sparkline } from '@loong/ui'
import { ShieldCheck } from 'lucide-react'
import { LocaleSwitcher, useDocumentTitle } from '@loong/shell'
import { Link, Outlet, useLocation } from 'react-router'
import { t } from '../i18n'
import { RedirectIfAuthed } from '../lib/auth'

/**
 * Panel terpisah: permukaan netral bertint (BUKAN banjir warna aksen) dengan SATU bukti tepercaya,
 * dan baris kepercayaan di dasar panel. Di layar sempit panel hilang dan form berdiri sendiri.
 * Pengganti bahasa di pojok kanan atas, di luar alur form.
 */
export function AuthLayout() {
  const { pathname } = useLocation()
  useDocumentTitle(
    pathname.startsWith('/register') ? t('dash.app.authTitleSignUp') : pathname.startsWith('/forgot-password') ? t('dash.app.authTitleForgot') : t('dash.app.authTitleSignIn'),
    t('dash.app.titleApp'),
  )
  return (
    <RedirectIfAuthed>
      <div className="relative grid min-h-screen lg:grid-cols-[minmax(0,1fr)_minmax(0,1.1fr)]">
        <aside className="hidden flex-col justify-between bg-sunken p-10 lg:flex">
          <Link to="/login" aria-label={t('dash.app.authLoginLink')}>
            <Logo />
          </Link>
          {REAL_BACKEND ? (
            // backend asli: hanya fakta terukur dari optimizer (bukan angka contoh backend tiruan)
            <figure className="max-w-sm">
              <p className="text-small font-medium text-muted">{t('dash.app.authProofLabelLive')}</p>
              <p className="mt-1 text-metric tabular-nums">≤5</p>
              <p className="mt-1 text-body text-muted">{t('dash.app.authProofTextLive')}</p>
            </figure>
          ) : (
            <figure className="max-w-sm">
              <p className="text-small font-medium text-muted">{t('dash.app.authProofLabel')}</p>
              <p className="mt-1 text-metric tabular-nums">21%</p>
              <p className="mt-1 text-body text-muted">{t('dash.app.authProofText', { count: 1280 })}</p>
              <Sparkline values={[12, 14, 13, 16, 18, 17, 20, 21]} height={56} className="mt-6" />
            </figure>
          )}
          <p className="flex items-center gap-2 text-small text-muted">
            <ShieldCheck className="size-4 shrink-0" aria-hidden />
            {t('dash.app.authTrust')}
          </p>
        </aside>
        <main className="flex items-center justify-center bg-surface p-6 sm:p-10">
          <div className="w-full max-w-[24rem]">
            <Link to="/login" aria-label={BRAND.name} className="mb-8 inline-block lg:hidden">
              <Logo />
            </Link>
            <Outlet />
          </div>
        </main>
        <LocaleSwitcher className="absolute right-4 top-4 z-10 sm:right-6 sm:top-6" />
      </div>
    </RedirectIfAuthed>
  )
}
