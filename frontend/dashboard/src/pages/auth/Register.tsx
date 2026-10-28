import { errorField, errorMessage, useApi, useMutation } from '@loong/data'
import { Button, Field, Input, Notice, PasswordInput, cn } from '@loong/ui'
import { useRef, useState, type FormEvent } from 'react'
import { Link, useLocation, useNavigate } from 'react-router'
import { focusFirstInvalid, isEmail, passwordStrength, useDebounced } from '../../lib/forms'
import { safeNext } from '../../lib/auth'
import { t, tr } from '../../i18n'

type Errors = Partial<Record<'name' | 'email' | 'phone' | 'password' | 'form', string>>

export function Register() {
  const api = useApi()
  const navigate = useNavigate()
  const { search } = useLocation()
  const form = useRef<HTMLFormElement>(null)
  const [v, setV] = useState({ name: '', email: '', phone: '', password: '' })
  const [errors, setErrors] = useState<Errors>({})
  const set = (k: keyof typeof v) => (e: React.ChangeEvent<HTMLInputElement>) => setV((s) => ({ ...s, [k]: e.target.value }))

  // indikator kekuatan diperbarui setelah berhenti mengetik, dan tidak muncul sebelum pengguna mengetik
  const strength = passwordStrength(useDebounced(v.password, 300))

  const register = useMutation({
    mutationFn: () => api.auth.register(v),
    onSuccess: () => {
      const next = safeNext(search)
      navigate(next === '/' ? '/?welcome=1' : next, { replace: true })
    },
    onError: (err) => {
      const field = errorField(err)
      setErrors(field && field in v ? { [field]: errorMessage(err) } : { form: errorMessage(err) })
      if (field) focusFirstInvalid(form.current)
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    const next: Errors = {}
    if (!v.name.trim()) next.name = t('dash.auth.errName')
    if (!isEmail(v.email)) next.email = t('dash.app.errEmail')
    if (v.phone.replace(/\D/g, '').length < 9) next.phone = t('dash.app.errPhone')
    if (v.password.length < 8) next.password = t('dash.auth.errPasswordShort')
    setErrors(next)
    if (Object.keys(next).length) return focusFirstInvalid(form.current)
    register.mutate()
  }

  const toLogin = `/login${search}`

  return (
    <>
      <h1 className="text-heading">{t('dash.auth.registerTitle')}</h1>
      <p className="mt-1 text-body text-muted">{t('dash.auth.registerLead')}</p>

      <form ref={form} onSubmit={submit} noValidate className="mt-6 space-y-4">
        {errors.form && <Notice tone="danger">{errors.form}</Notice>}
        <Field label={t('dash.auth.fullName')} error={errors.name}>
          <Input autoComplete="name" value={v.name} onChange={set('name')} autoFocus />
        </Field>
        <Field label={t('dash.auth.email')} error={errors.email}>
          <Input type="email" autoComplete="email" value={v.email} onChange={set('email')} placeholder={t('dash.app.emailPlaceholder')} />
        </Field>
        <Field label={t('dash.auth.phone')} hint={t('dash.auth.phoneHint')} error={errors.phone}>
          <Input type="tel" inputMode="tel" autoComplete="tel" value={v.phone} onChange={set('phone')} placeholder="0812 3456 7890" />
        </Field>
        <Field
          label={t('dash.auth.password')}
          hint={
            <span aria-live="polite">
              {t('dash.auth.passwordHint')}{' '}
              {strength.label && (
                <span className={cn('font-semibold', strength.level === 0 ? 'text-danger-fg' : 'text-fg')}>{strength.label}.</span>
              )}
            </span>
          }
          error={errors.password}
        >
          <PasswordInput autoComplete="new-password" value={v.password} onChange={set('password')} />
        </Field>
        <Button type="submit" variant="primary" size="lg" className="w-full" loading={register.isPending}>
          {t('dash.auth.registerSubmit')}
        </Button>
        <p className="text-small text-muted">
          {tr('dash.auth.terms', {
            terms: (c) => (
              <a href="#syarat" className="underline underline-offset-4 hover:text-fg">
                {c}
              </a>
            ),
            privacy: (c) => (
              <a href="#privasi" className="underline underline-offset-4 hover:text-fg">
                {c}
              </a>
            ),
          })}
        </p>
      </form>

      <p className="mt-6 text-body text-muted">
        {tr('dash.auth.haveAccount', {
          link: (c) => (
            <Link to={toLogin} className="font-semibold text-accent underline underline-offset-4 hover:text-accent-hover">
              {c}
            </Link>
          ),
        })}
      </p>
    </>
  )
}
