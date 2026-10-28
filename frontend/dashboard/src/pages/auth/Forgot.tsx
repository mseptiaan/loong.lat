import { Button, Field, Input, Notice } from '@loong/ui'
import { MailCheck } from 'lucide-react'
import { useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { t, tr } from '../../i18n'
import { focusFirstInvalid, isEmail, useCountdown } from '../../lib/forms'

/** Simulasi: tidak ada email sungguhan yang dikirim di fase mockup. */
export function Forgot() {
  const form = useRef<HTMLFormElement>(null)
  const [email, setEmail] = useState('')
  const [error, setError] = useState<string>()
  const [sent, setSent] = useState(false)
  const countdown = useCountdown(30)

  function submit(e: FormEvent) {
    e.preventDefault()
    if (!isEmail(email)) {
      setError(t('dash.app.errEmail'))
      return focusFirstInvalid(form.current)
    }
    setError(undefined)
    setSent(true)
    countdown.start()
  }

  if (sent) {
    return (
      <>
        <div className="grid size-12 place-items-center rounded-card bg-sunken text-muted" aria-hidden>
          <MailCheck className="size-6" />
        </div>
        <h1 className="mt-4 text-heading">{t('dash.auth.sentTitle')}</h1>
        <p className="mt-1 text-body text-muted">
          {tr('dash.auth.sentText', { email, b: (c) => <span className="font-semibold text-fg">{c}</span> })}
        </p>
        <Notice tone="info" className="mt-5">
          {t('dash.auth.sentDemo')}
        </Notice>
        <div className="mt-5 flex flex-wrap items-center gap-3">
          <Button variant="secondary" onClick={countdown.start} aria-disabled={countdown.left > 0 || undefined}>
            {countdown.left > 0 ? t('dash.auth.resendIn', { left: countdown.left }) : t('dash.auth.resend')}
          </Button>
          <Link to="/login" className="text-body font-semibold text-accent underline underline-offset-4 hover:text-accent-hover">
            {t('dash.auth.backToSignIn')}
          </Link>
        </div>
      </>
    )
  }

  return (
    <>
      <h1 className="text-heading">{t('dash.auth.forgotTitle')}</h1>
      <p className="mt-1 text-body text-muted">{t('dash.auth.forgotLead')}</p>
      <form ref={form} onSubmit={submit} noValidate className="mt-6 space-y-4">
        <Field label={t('dash.auth.email')} error={error}>
          <Input type="email" autoComplete="email" value={email} onChange={(e) => setEmail(e.target.value)} placeholder={t('dash.app.emailPlaceholder')} autoFocus />
        </Field>
        <Button type="submit" variant="primary" size="lg" className="w-full">
          {t('dash.auth.forgotSubmit')}
        </Button>
      </form>
      <p className="mt-6 text-body text-muted">
        <Link to="/login" className="font-semibold text-accent underline underline-offset-4 hover:text-accent-hover">
          {t('dash.auth.backToSignIn')}
        </Link>
      </p>
    </>
  )
}
