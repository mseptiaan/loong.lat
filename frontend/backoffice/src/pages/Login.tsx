import { REAL_BACKEND, errorMessage, useApi, useMutation } from '@loong/data'
import { DEMO_CREDENTIALS } from '@loong/domain'
import { Button, Card, Field, Input, Logo, Notice, PasswordInput } from '@loong/ui'
import { useRef, useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { LocaleSwitcher, useDocumentTitle } from '@loong/shell'
import { t, tr } from '../i18n'
import { safeNext } from '../lib/auth'

type Errors = { email?: string; password?: string; form?: string }

/** Kartu di tengah: layar utilitas, tanpa panel pemasaran (operator hanya ingin masuk). */
export function Login() {
  const api = useApi()
  useDocumentTitle(t('bo.app.loginDocTitle'), 'Backoffice')
  const navigate = useNavigate()
  const { search } = useLocation()
  const form = useRef<HTMLFormElement>(null)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState<Errors>({})

  const login = useMutation({
    mutationFn: () => api.auth.loginAdmin(email, password),
    onSuccess: () => navigate(safeNext(search), { replace: true }),
    onError: (err) => setErrors({ form: errorMessage(err) }),
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    const next: Errors = {}
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email.trim())) next.email = t('bo.app.loginEmailError')
    if (!password) next.password = t('bo.app.loginPasswordError')
    setErrors(next)
    if (next.email || next.password) {
      requestAnimationFrame(() => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus())
      return
    }
    login.mutate()
  }

  const demo = DEMO_CREDENTIALS.admin

  return (
    <main className="relative grid min-h-screen place-items-center bg-sunken p-6">
      <LocaleSwitcher className="absolute right-4 top-4" />
      <Card className="w-full max-w-sm p-7">
        <Logo />
        <h1 className="mt-6 text-heading">{t('bo.app.loginTitle')}</h1>
        <p className="mt-1 text-body text-muted">{t('bo.app.loginSubtitle')}</p>

        <form ref={form} onSubmit={submit} noValidate className="mt-5 space-y-4">
          {errors.form && <Notice tone="danger">{errors.form}</Notice>}
          <Field label={t('bo.app.loginEmail')} error={errors.email}>
            <Input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} placeholder={t('bo.app.loginEmailPlaceholder')} autoFocus />
          </Field>
          <Field label={t('bo.app.loginPassword')} error={errors.password}>
            <PasswordInput autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
          </Field>
          <Button type="submit" variant="primary" size="lg" className="w-full" loading={login.isPending}>
            {t('bo.app.loginSubmit')}
          </Button>
        </form>

        {/* akun demo hanya ada di backend tiruan */}
        {!REAL_BACKEND && (
          <button
            type="button"
            onClick={() => {
              setEmail(demo.id)
              setPassword(demo.secret)
              setErrors({})
            }}
            className="mt-6 w-full rounded-card border border-dashed border-line-strong p-3 text-left text-small text-muted transition-colors hover:border-subtle hover:bg-sunken"
          >
            {tr('bo.app.loginDemo', { account: demo.label, strong: (c) => <span className="font-semibold text-fg">{c}</span> })}
          </button>
        )}
      </Card>
    </main>
  )
}
