import { REAL_BACKEND, errorMessage, useApi, useMutation } from '@loong/data'
import { DEMO_CREDENTIALS } from '@loong/domain'
import { Button, Field, Input, Notice, PasswordInput } from '@loong/ui'
import { useRef, useState, type FormEvent } from 'react'
import { Link, useLocation, useNavigate } from 'react-router'
import { focusFirstInvalid, isEmail } from '../../lib/forms'
import { safeNext } from '../../lib/auth'
import { t, tr } from '../../i18n'

type Errors = { email?: string; password?: string; form?: string }

export function Login() {
  const api = useApi()
  const navigate = useNavigate()
  const { search } = useLocation()
  const form = useRef<HTMLFormElement>(null)
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [errors, setErrors] = useState<Errors>({})

  const login = useMutation({
    mutationFn: () => api.auth.loginCustomer(email, password),
    onSuccess: () => navigate(safeNext(search), { replace: true }),
    onError: (err) => {
      setErrors({ form: errorMessage(err) })
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    const next: Errors = {}
    if (!isEmail(email)) next.email = t('dash.app.errEmail')
    if (!password) next.password = t('dash.auth.errPassword')
    setErrors(next)
    if (Object.keys(next).length) return focusFirstInvalid(form.current)
    login.mutate()
  }

  const demo = DEMO_CREDENTIALS.customer
  const toRegister = `/register${search}`

  return (
    <>
      <h1 className="text-heading">{t('dash.auth.loginTitle')}</h1>
      <p className="mt-1 text-body text-muted">{t('dash.auth.loginLead')}</p>

      <form ref={form} onSubmit={submit} noValidate className="mt-6 space-y-4">
        {errors.form && <Notice tone="danger">{errors.form}</Notice>}
        <Field label={t('dash.auth.email')} error={errors.email}>
          <Input type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder={t('dash.app.emailPlaceholder')} autoFocus />
        </Field>
        <Field
          label={t('dash.auth.password')}
          aside={
            <Link to="/forgot-password" className="text-accent underline underline-offset-4 hover:text-accent-hover">
              {t('dash.auth.forgotLink')}
            </Link>
          }
          error={errors.password}
        >
          <PasswordInput autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        <Button type="submit" variant="primary" size="lg" className="w-full" loading={login.isPending}>
          {t('dash.auth.loginSubmit')}
        </Button>
      </form>

      <p className="mt-6 text-body text-muted">
        {tr('dash.auth.noAccount', {
          link: (c) => (
            <Link to={toRegister} className="font-semibold text-accent underline underline-offset-4 hover:text-accent-hover">
              {c}
            </Link>
          ),
        })}
      </p>

      {/* akun demo hanya ada di backend tiruan */}
      {!REAL_BACKEND && (
        <button
          type="button"
          onClick={() => {
            setEmail(demo.id)
            setPassword(demo.secret)
            setErrors({})
          }}
          className="mt-8 w-full rounded-card border border-dashed border-line-strong p-3 text-left text-small text-muted transition-colors hover:border-subtle hover:bg-sunken"
        >
          {tr('dash.auth.demo', { label: demo.label, b: (c) => <span className="font-semibold text-fg">{c}</span> })}
        </button>
      )}
    </>
  )
}
