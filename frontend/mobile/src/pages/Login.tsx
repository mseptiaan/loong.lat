import { REAL_BACKEND, errorMessage, useApi, useMutation } from '@loong/data'
import { DEMO_CREDENTIALS } from '@loong/domain'
import { Button, Field, Input, Logo, Notice, PasswordInput } from '@loong/ui'
import { useRef, useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router'
import { LocaleSwitcher, useDocumentTitle } from '@loong/shell'
import { t, tr } from '../i18n'
import { safeNext } from '../lib/auth'

type Errors = { phone?: string; pin?: string; form?: string }

/** Masuk driver: nomor HP + PIN 6 digit. Kolom besar, satu tangan, tanpa pilihan lain. */
export function Login() {
  const api = useApi()
  useDocumentTitle(t('drv.app.loginDocTitle'), t('drv.app.titleApp'))
  const navigate = useNavigate()
  const { search } = useLocation()
  const form = useRef<HTMLFormElement>(null)
  const [phone, setPhone] = useState('')
  const [pin, setPin] = useState('')
  const [errors, setErrors] = useState<Errors>({})

  const login = useMutation({
    mutationFn: () => api.auth.loginDriver(phone, pin),
    onSuccess: () => navigate(safeNext(search), { replace: true }),
    onError: (err) => setErrors({ form: errorMessage(err) }),
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    const next: Errors = {}
    const digits = phone.replace(/\D/g, '')
    if (digits.length < 9) next.phone = t('drv.app.loginPhoneError')
    if (!/^\d{6}$/.test(pin)) next.pin = t('drv.app.loginPinError')
    setErrors(next)
    if (next.phone || next.pin) {
      // setelah render: atribut aria-invalid baru terpasang di frame berikutnya
      requestAnimationFrame(() => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus())
      return
    }
    login.mutate()
  }

  const demo = DEMO_CREDENTIALS.driver

  return (
    <main className="mx-auto flex min-h-dvh max-w-[28rem] flex-col justify-between px-6 pb-[calc(1.5rem+env(safe-area-inset-bottom))] pt-10">
      <div>
        <div className="flex items-center justify-between gap-3">
          <Logo />
          <LocaleSwitcher className="[&>[role=radio]]:h-12 [&>[role=radio]]:min-w-12" />
        </div>
        <h1 className="mt-10 text-heading">{t('drv.app.loginTitle')}</h1>
        <p className="mt-1 text-body text-muted">{t('drv.app.loginIntro')}</p>

        <form ref={form} onSubmit={submit} noValidate className="mt-6 space-y-4">
          {errors.form && <Notice tone="danger">{errors.form}</Notice>}
          <Field label={t('drv.app.loginPhone')} error={errors.phone}>
            <Input type="tel" inputMode="tel" autoComplete="tel" value={phone} onChange={(e) => setPhone(e.target.value)} placeholder="0812-3456-7890" autoFocus />
          </Field>
          <Field label={t('drv.app.loginPin')} hint={t('drv.app.loginPinHint')} error={errors.pin}>
            <PasswordInput noun="PIN" inputMode="numeric" maxLength={6} autoComplete="current-password" value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ''))} />
          </Field>
          <Button type="submit" variant="primary" size="lg" className="w-full" loading={login.isPending}>
            {t('drv.app.loginSubmit')}
          </Button>
        </form>

        <p className="mt-6 text-small text-muted">{t('drv.app.loginHelp')}</p>
      </div>

      {/* akun demo hanya ada di backend tiruan */}
      {!REAL_BACKEND && (
        <button
          type="button"
          onClick={() => {
            setPhone(demo.id)
            setPin(demo.secret)
            setErrors({})
          }}
          className="mt-10 w-full rounded-card border border-dashed border-line-strong p-3.5 text-left text-small text-muted transition-colors hover:border-subtle hover:bg-sunken"
        >
          {tr('drv.app.loginDemo', { strong: (c) => <span className="font-semibold text-fg">{c}</span>, label: demo.label })}
        </button>
      )}
    </main>
  )
}
