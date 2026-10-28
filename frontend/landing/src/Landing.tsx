import type { AppId } from '@loong/domain'
import { Logo, buttonStyles, cn } from '@loong/ui'
import { ArrowRight, ArrowUpRight, Repeat, Timer, WifiOff, type LucideIcon } from 'lucide-react'
import { useEffect, type ReactNode } from 'react'
import { LocaleSwitch } from './components/LocaleSwitch'
import { RouteSteps } from './components/RouteSteps'
import { Shot, type ShotName } from './components/Shot'
import { t } from './i18n'
import { LIVE, appHref, appHost } from './links'


const WRAP = 'mx-auto w-full max-w-[1200px] px-5 md:px-8'
const H2 = 'text-[clamp(1.75rem,3vw,2.375rem)] font-bold leading-[1.12] tracking-[-0.022em] text-fg'
const FRAME = 'overflow-hidden rounded-card border border-line-strong bg-surface shadow-overlay'

export function Landing() {
  // judul dan deskripsi dokumen ikut bahasa (pohon dipasang ulang tiap bahasa berganti, lihat I18nBoundary)
  useEffect(() => {
    document.title = t('land.meta.title')
    document.querySelector('meta[name="description"]')?.setAttribute('content', t('land.meta.description'))
  }, [])

  return (
    <>
      <a
        href="#main"
        className={cn(buttonStyles({ variant: 'primary', size: 'sm' }), 'fixed left-3 top-3 z-50 -translate-y-16 focus-visible:translate-y-0')}
      >
        {t('land.nav.skip')}
      </a>
      <Nav />
      <main id="main" tabIndex={-1} className="outline-none">
        <Hero />
        <Proof />
        <Feature
          id="ai-explain"
          title={t('land.explain.title')}
          body={t('land.explain.body')}
          shot="ai"
          alt={t('land.explain.shotAlt')}
          className="pt-24 md:pt-32"
        />
        <Feature
          title={t('land.track.title')}
          body={t('land.track.body')}
          shot="tracking"
          alt={t('land.track.shotAlt')}
          flip
          className="pt-20 md:pt-28"
        />
        <How />
        <Sandbox />
        <Apps />
        <MetaModels />
        <TryIt />
      </main>
      <Footer />
    </>
  )
}

/* ------------------------------------------------------------------ nav ------------------------------------------------------------------ */

function Nav() {
  const links: [string, string][] = [
    ['#how', t('land.nav.how')],
    ['#sandbox', t('land.nav.sandbox')],
    ['#apps', t('land.nav.apps')],
    ['#meta-ai', t('land.nav.ai')],
  ]
  return (
    <header className="sticky top-0 z-40 border-b border-line bg-bg">
      <div className={cn(WRAP, 'flex h-16 items-center gap-6')}>
        <a href="#main" className="rounded-chip text-fg" aria-label="loong.lat">
          <Logo className="h-7" />
        </a>
        <nav aria-label={t('land.nav.label')} className="hidden items-center gap-1 md:flex">
          {links.map(([href, label]) => (
            <a key={href} href={href} className="rounded-chip px-3 py-2 text-small font-semibold text-muted transition-colors duration-[140ms] hover:text-fg">
              {label}
            </a>
          ))}
        </nav>
        <div className="ml-auto flex items-center gap-3">
          <LocaleSwitch />
          <a href={appHref('console')} className={cn(buttonStyles({ variant: 'secondary', size: 'sm' }), 'hidden sm:inline-flex')}>
            {t('land.cta.demo')}
          </a>
        </div>
      </div>
    </header>
  )
}

/* ------------------------------------------------------------------ hero ----------------------------------------------------------------- */

/** Komposisi fold: product-dominant. Satu baris tipe yang ringkas, lalu tangkapan layar asli di tengah yang terpotong di bawah fold. */
function Hero() {
  return (
    <section aria-labelledby="hero-title" className="overflow-hidden">
      <div className={cn(WRAP, 'pt-[clamp(56px,11vh,120px)]')}>
        <div className="grid items-start gap-x-12 gap-y-6 lg:grid-cols-12">
          <h1
            id="hero-title"
            className="enter text-[clamp(2.25rem,4.2vw,3.25rem)] font-bold leading-[1.05] tracking-[-0.032em] text-fg lg:col-span-7"
          >
            {t('land.hero.title')}
          </h1>
          <div className="enter [animation-delay:70ms] lg:col-span-5 lg:pt-2">
            <p className="max-w-[42ch] text-[1.0625rem] leading-relaxed text-muted">{t('land.hero.lead')}</p>
            <div className="mt-6 flex flex-wrap items-center gap-3">
              <a href={appHref('console')} className={buttonStyles({ variant: 'primary', size: 'lg' })}>
                {t('land.cta.demo')}
                <ArrowRight className="size-4" aria-hidden />
              </a>
              <a href="#how" className={buttonStyles({ variant: 'ghost', size: 'lg' })}>
                {t('land.cta.how')}
              </a>
            </div>
            <p className="mt-4 text-small text-subtle">{t(LIVE ? 'land.hero.trustLive' : 'land.hero.trust')}</p>
          </div>
        </div>
      </div>
      <div className={cn(WRAP, 'enter mt-12 [animation-delay:140ms] md:mt-16')}>
        <div className={cn(FRAME, 'hero-shot mx-auto h-[clamp(300px,52vh,600px)] rounded-b-none border-b-0')}>
          <Shot name="options" alt={t('land.hero.shotAlt')} priority className="h-auto w-full min-w-[432px] max-w-none" />
        </div>
      </div>
    </section>
  )
}

/* ------------------------------------------------------------- bukti terukur ------------------------------------------------------------- */

function Proof() {
  const items: [string, string][] = [
    [t('land.proof.optimalValue', { n: 5 }), t('land.proof.optimalLabel')],
    [t('land.proof.speedValue', { s: 1.2 }), t('land.proof.speedLabel')],
    [t('land.proof.tollValue', { n: 0 }), t('land.proof.tollLabel')],
  ]
  return (
    <section aria-labelledby="proof-title" className="border-y border-line bg-surface">
      <div className={cn(WRAP, 'py-14 md:py-16')}>
        <h2 id="proof-title" className="text-small font-semibold text-muted">
          {t('land.proof.title')}
        </h2>
        <dl className="mt-6 grid gap-8 md:grid-cols-3 md:gap-0 md:divide-x md:divide-line">
          {items.map(([value, label], i) => (
            <div key={i} className="md:px-8 md:first:pl-0 md:last:pr-0">
              <dt className="text-[clamp(2.5rem,4vw,3.25rem)] font-bold leading-none tracking-[-0.03em] tabular-nums text-fg">{value}</dt>
              <dd className="mt-3 max-w-[32ch] text-muted">{label}</dd>
            </div>
          ))}
        </dl>
        <p className="mt-8 text-caption text-subtle">{t('land.proof.source')}</p>
      </div>
    </section>
  )
}

/* ---------------------------------------------------------------- fitur ------------------------------------------------------------------ */

function Feature({
  id,
  title,
  body,
  shot,
  alt,
  flip = false,
  className,
}: {
  id?: string
  title: string
  body: string
  shot: ShotName
  alt: string
  flip?: boolean
  className?: string
}) {
  return (
    <section id={id} aria-label={title} className={className}>
      <div className={cn(WRAP, 'grid items-center gap-x-14 gap-y-8 lg:grid-cols-12')}>
        <div className={cn('lg:col-span-4', flip && 'lg:order-2')}>
          <h2 className={H2}>{title}</h2>
          <p className="mt-4 max-w-[46ch] text-[1.0625rem] leading-relaxed text-muted">{body}</p>
        </div>
        <div className={cn(FRAME, 'lg:col-span-8', flip && 'lg:order-1')}>
          <Shot name={shot} alt={alt} className="aspect-[16/10] h-auto w-full object-cover object-left-top" />
        </div>
      </div>
    </section>
  )
}

/* ------------------------------------------------------------- cara kerja ---------------------------------------------------------------- */

function How() {
  const steps = [1, 2, 3, 4].map((n) => ({
    title: t(`land.how.s${n as 1 | 2 | 3 | 4}Title`),
    body: t(`land.how.s${n as 1 | 2 | 3 | 4}Body`),
  }))
  return (
    <section id="how" aria-labelledby="how-title" className="scroll-mt-16 pt-28 md:pt-40">
      <div className={WRAP}>
        <h2 id="how-title" className={cn(H2, 'max-w-[20ch]')}>
          {t('land.how.title')}
        </h2>
        <p className="mt-3 max-w-[52ch] text-muted">{t('land.how.lead')}</p>
        <div className="mt-12">
          <RouteSteps steps={steps} />
        </div>
      </div>
    </section>
  )
}

/* --------------------------------------------------------------- sandbox ----------------------------------------------------------------- */

/** Bentuk job v1 yang dikirim optimizer ke sandbox Modal (backend/services/optimizer/internal/job). Kunci adalah kode, tidak diterjemahkan. */
const JOB_EXCERPT = `{
  "points": [
    { "lat": -6.22664, "lng": 106.63553 },
    { "lat": -6.24067, "lng": 106.62857 }, …
  ],
  "distM": [0, 14230, 9870, …],
  "shipments": [
    { "pickup": 0, "dropoff": 3, "weightKg": 4.6 }, …
  ],
  "presets": ["termurah", "terpendek", "tercepat", "seimbang"]
}`

function Sandbox() {
  const facts: [LucideIcon, string, string][] = [
    [WifiOff, t('land.sandbox.netTitle'), t('land.sandbox.netBody')],
    [Repeat, t('land.sandbox.sameTitle'), t('land.sandbox.sameBody')],
    [Timer, t('land.sandbox.fastTitle'), t('land.sandbox.fastBody')],
  ]
  return (
    <section id="sandbox" aria-labelledby="sandbox-title" className="scroll-mt-16 pt-28 md:pt-36">
      <div className={cn(WRAP, 'grid gap-10 lg:grid-cols-12 lg:gap-14')}>
        <div className="lg:col-span-5">
          {/* logo Modal: /brands/modal.png (Modal-IconMark), lebar dibuat setara logo Meta di section model; teks sudah menyebut Modal */}
          <p className="mb-5 flex items-center gap-2.5 text-small font-semibold text-muted">
            <img src="/brands/modal.png" alt="" width={36} height={19} className="h-[19px] w-auto" />
            {t('land.sandbox.poweredBy')}
          </p>
          <h2 id="sandbox-title" className={H2}>
            {t('land.sandbox.title')}
          </h2>
          <p className="mt-4 max-w-[46ch] text-[1.0625rem] leading-relaxed text-muted">{t('land.sandbox.lead')}</p>
          <dl className="mt-8 grid gap-5">
            {facts.map(([Icon, title, body]) => (
              <div key={title} className="grid grid-cols-[20px_1fr] gap-x-3">
                <Icon className="mt-0.5 size-5 text-muted" aria-hidden />
                <dt className="font-semibold text-fg">{title}</dt>
                <dd className="col-start-2 mt-0.5 max-w-[44ch] text-muted">{body}</dd>
              </div>
            ))}
          </dl>
        </div>
        <figure className="min-w-0 lg:col-span-7 lg:self-center">
          <div className="overflow-hidden rounded-card border border-line-strong bg-surface shadow-card">
            <div className="flex items-center justify-between gap-4 border-b border-line px-5 py-3">
              <span className="text-small font-semibold text-fg">{t('land.sandbox.jobTitle')}</span>
              <span className="rounded-chip border border-line px-2 py-0.5 text-caption text-muted">{t('land.sandbox.badge')}</span>
            </div>
            <pre className="overflow-x-auto bg-sunken px-5 py-4 font-mono text-small leading-relaxed text-fg">
              <code>{JOB_EXCERPT}</code>
            </pre>
            <div className="border-t border-line px-5 py-4">
              <p className="text-small font-semibold text-fg">{t('land.sandbox.neverTitle')}</p>
              <p className="mt-0.5 text-small text-muted">{t('land.sandbox.neverBody')}</p>
            </div>
          </div>
          <figcaption className="mt-3 text-caption text-subtle">{t('land.sandbox.jobCaption')}</figcaption>
          <p className="mt-4 text-small text-subtle">{t(LIVE ? 'land.sandbox.noteLive' : 'land.sandbox.note')}</p>
        </figure>
      </div>
    </section>
  )
}

/* -------------------------------------------------------------- aplikasi ----------------------------------------------------------------- */

function AppTile({
  app,
  name,
  body,
  children,
  className,
}: {
  app: Exclude<AppId, 'api'>
  name: string
  body: string
  children: ReactNode
  className?: string
}) {
  return (
    <article className={cn('flex flex-col overflow-hidden rounded-modal border border-line bg-surface shadow-card', className)}>
      <div className="flex flex-wrap items-start justify-between gap-x-6 gap-y-3 p-6 md:p-7">
        <div className="max-w-[44ch]">
          <h3 className="text-title text-fg">{name}</h3>
          <p className="mt-1 text-muted">{body}</p>
        </div>
        <a
          href={appHref(app)}
          aria-label={t('land.apps.open', { name })}
          className="group inline-flex min-h-hit items-center gap-1.5 rounded-chip font-mono text-small text-accent underline-offset-4 hover:underline"
        >
          {appHost(app)}
          <ArrowUpRight className="size-4 transition-transform duration-[140ms] group-hover:-translate-y-0.5 group-hover:translate-x-0.5" aria-hidden />
        </a>
      </div>
      <div className="mt-auto">{children}</div>
    </article>
  )
}

function Apps() {
  return (
    <section id="apps" aria-labelledby="apps-title" className="scroll-mt-16 pt-28 md:pt-36">
      <div className={WRAP}>
        <div className="grid gap-4 lg:grid-cols-12 lg:items-end">
          <h2 id="apps-title" className={cn(H2, 'lg:col-span-6')}>
            {t('land.apps.title')}
          </h2>
          <p className="max-w-[52ch] text-muted lg:col-span-6">{t('land.apps.lead')}</p>
        </div>
        <div className="mt-10 grid gap-5 lg:grid-cols-12 lg:grid-rows-[auto_auto]">
          <AppTile app="console" name={t('land.apps.consoleName')} body={t('land.apps.consoleBody')} className="lg:col-span-8">
            <div className="h-[260px] overflow-hidden border-t border-line md:h-[300px]">
              {/* diperbesar dan digeser ke kartu "Pengiriman 1": isi wizard berada di tengah tangkapan 1440 px */}
              <Shot name="items" alt={t('land.apps.consoleAlt')} className="-ml-[40%] -mt-[30%] h-auto w-[180%] max-w-none" />
            </div>
          </AppTile>
          <AppTile
            app="driver"
            name={t('land.apps.driverName')}
            body={t('land.apps.driverBody')}
            className="bg-sunken lg:col-span-4 lg:row-span-2"
          >
            {/* layar ponsel terpotong di tepi bawah kartu */}
            <div className="mx-auto mt-4 h-[380px] w-[min(300px,80%)] overflow-hidden rounded-t-[30px] border-x-[6px] border-t-[6px] border-fg bg-fg lg:h-[600px]">
              <Shot name="driver" alt={t('land.apps.driverAlt')} className="h-auto w-full rounded-t-[24px]" />
            </div>
          </AppTile>
          <AppTile app="backoffice" name={t('land.apps.backofficeName')} body={t('land.apps.backofficeBody')} className="lg:col-span-8">
            <div className="h-[260px] overflow-hidden border-t border-line md:h-[300px]">
              <Shot name="backoffice" alt={t('land.apps.backofficeAlt')} className="h-auto w-full min-w-[640px]" />
            </div>
          </AppTile>
        </div>
      </div>
    </section>
  )
}

/* ------------------------------------------------------------- model Meta ---------------------------------------------------------------- */

const MODEL_ROWS = [
  ['Muse Spark', 'photo'],
  ['Muse Voice Transcribe', 'voice'],
  ['Muse Spark', 'intent'],
  ['Muse Spark', 'explain'],
  ['Muse Spark', 'review'],
  ['Muse Spark', 'proof'],
  ['Muse Spark', 'insights'],
] as const

function MetaModels() {
  return (
    <section id="meta-ai" aria-labelledby="ai-title" className="scroll-mt-16 pt-28 md:pt-40">
      <div className={WRAP}>
        {/* logo Meta: /brands/meta.svg (Wikimedia Commons); teks di sebelahnya sudah menyebut Meta, jadi gambarnya dekoratif */}
        <p className="mb-5 flex items-center gap-2.5 text-small font-semibold text-muted">
          <img src="/brands/meta.svg" alt="" width={36} height={24} className="h-6 w-auto" />
          {t('land.ai.poweredBy')}
        </p>
        <h2 id="ai-title" className={cn(H2, 'max-w-[24ch]')}>
          {t('land.ai.title')}
        </h2>
        <p className="mt-4 max-w-[60ch] text-[1.0625rem] leading-relaxed text-muted">{t('land.ai.lead')}</p>
        <div className="mt-10 overflow-x-auto">
          <table className="w-full min-w-[640px] border-collapse text-left">
            <thead>
              <tr className="border-b border-line-strong text-small text-subtle">
                <th scope="col" className="w-[26%] py-3 pr-6 font-semibold">
                  {t('land.ai.colModel')}
                </th>
                <th scope="col" className="py-3 pr-6 font-semibold">
                  {t('land.ai.colJob')}
                </th>
                <th scope="col" className="w-[20%] py-3 font-semibold">
                  {t('land.ai.colWhere')}
                </th>
              </tr>
            </thead>
            <tbody>
              {MODEL_ROWS.map(([model, key]) => (
                <tr key={key} className="border-b border-line align-baseline">
                  <th scope="row" className="py-4 pr-6 font-semibold text-fg">
                    {model}
                  </th>
                  <td className="py-4 pr-6 text-fg">{t(`land.ai.${key}`)}</td>
                  <td className="py-4 text-small text-muted">{t(`land.ai.${key}Where`)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <p className="mt-6 max-w-[72ch] text-small text-subtle">{t(LIVE ? 'land.ai.noteLive' : 'land.ai.note')}</p>
      </div>
    </section>
  )
}

/* ------------------------------------------------------------- coba demo ----------------------------------------------------------------- */

const DEMO_ACCOUNTS = [
  ['sender', 'dina@contoh.id', 'demo1234'],
  ['admin', 'admin@loong.lat', 'admin1234'],
  ['driver', '0812-3456-7801', '123456'],
] as const

function TryIt() {
  return (
    <section aria-labelledby="try-title" className="mt-28 border-t border-line bg-surface md:mt-36">
      <div className={cn(WRAP, 'grid gap-10 py-20 md:py-24 lg:grid-cols-12 lg:gap-14')}>
        <div className="lg:col-span-5">
          <h2 id="try-title" className={H2}>
            {t('land.try.title')}
          </h2>
          <p className="mt-4 max-w-[44ch] text-muted">{t(LIVE ? 'land.try.leadLive' : 'land.try.lead')}</p>
          <a href={appHref('console')} className={cn(buttonStyles({ variant: 'primary', size: 'lg' }), 'mt-7')}>
            {t('land.cta.demo')}
            <ArrowRight className="size-4" aria-hidden />
          </a>
          <p className="mt-4 text-small text-subtle">{t(LIVE ? 'land.hero.trustLive' : 'land.hero.trust')}</p>
        </div>
        {/* min-w-0: tabel boleh bergulir di dalam kotaknya, bukan melebarkan kolom grid (dan halaman) di ponsel */}
        <div className="min-w-0 lg:col-span-7">
          <div className="overflow-x-auto rounded-card border border-line bg-bg">
            <table className="w-full min-w-[460px] border-collapse text-left text-small">
              <thead>
                <tr className="border-b border-line text-subtle">
                  <th scope="col" className="px-5 py-3 font-semibold">
                    {t('land.try.colRole')}
                  </th>
                  <th scope="col" className="px-5 py-3 font-semibold">
                    {t('land.try.colLogin')}
                  </th>
                  <th scope="col" className="px-5 py-3 font-semibold">
                    {t('land.try.colSecret')}
                  </th>
                </tr>
              </thead>
              <tbody>
                {DEMO_ACCOUNTS.map(([role, login, secret]) => (
                  <tr key={role} className="border-b border-line last:border-0">
                    <th scope="row" className="px-5 py-3.5 font-semibold text-fg">
                      {t(`land.try.${role}`)}
                    </th>
                    <td className="px-5 py-3.5 font-mono text-fg">{login}</td>
                    <td className="px-5 py-3.5 font-mono text-fg">{secret}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {/* panel ?demo hanya ada di backend tiruan */}
          {!LIVE && <p className="mt-4 text-small text-muted">{t('land.try.panel')}</p>}
        </div>
      </div>
    </section>
  )
}

/* --------------------------------------------------------------- footer ------------------------------------------------------------------ */

function Footer() {
  const apps: [Exclude<AppId, 'api'>, string][] = [
    ['console', t('land.apps.consoleName')],
    ['backoffice', t('land.apps.backofficeName')],
    ['driver', t('land.apps.driverName')],
  ]
  return (
    <footer className="border-t border-line">
      <div className={cn(WRAP, 'grid gap-8 py-12 md:grid-cols-[1fr_auto] md:items-start')}>
        <div>
          <Logo className="h-6 text-fg" />
          <p className="mt-4 text-small text-muted">{t('land.footer.hackathon')}</p>
          <p className="mt-1 text-small text-subtle">{t('land.footer.credits')}</p>
        </div>
        <div className="flex flex-col gap-4 md:items-end">
          <nav aria-label={t('land.footer.apps')} className="flex flex-wrap gap-x-5 gap-y-1">
            {apps.map(([app, name]) => (
              <a key={app} href={appHref(app)} className="inline-flex min-h-hit items-center text-small text-muted underline-offset-4 hover:text-fg hover:underline">
                {name}
              </a>
            ))}
          </nav>
        </div>
      </div>
    </footer>
  )
}
