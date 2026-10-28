import { errorField, errorMessage, useApi, useMutation, useProfile, useQueryClient, qk } from '@loong/data'
import { LocaleSwitcher } from '@loong/shell'
import { Button, Card, CardHeader, ErrorState, Field, Input, Notice, PageHeader, Segmented, Skeleton, Switch, toast } from '@loong/ui'
import { LogOut } from 'lucide-react'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router'
import { t } from '../i18n'
import { focusFirstInvalid, isEmail } from '../lib/forms'
import { readPrefs, writePrefs, type Prefs } from '../lib/prefs'

type Errors = Partial<Record<'name' | 'email' | 'phone' | 'form', string>>

export function Account() {
  const api = useApi()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const form = useRef<HTMLFormElement>(null)
  const { data: profile, isLoading, isError, error, refetch, isRefetching } = useProfile()
  const [v, setV] = useState({ name: '', email: '', phone: '' })
  const [errors, setErrors] = useState<Errors>({})
  const [prefs, setPrefs] = useState(readPrefs)

  // isi form saat profil pertama kali dimuat (tidak menimpa ketikan setelahnya)
  const loaded = useRef(false)
  useEffect(() => {
    if (profile && !loaded.current) {
      loaded.current = true
      setV({ name: profile.name, email: profile.email, phone: profile.phone })
    }
  }, [profile])

  const setPref = <K extends keyof Prefs>(k: K, value: Prefs[K]) => {
    const next = { ...prefs, [k]: value }
    setPrefs(next)
    writePrefs(next)
  }

  const save = useMutation({
    mutationFn: () => api.account.update({ name: v.name, email: v.email, phone: v.phone }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: qk.profile })
      toast.success(t('dash.account.saved'))
    },
    onError: (err) => {
      const f = errorField(err)
      setErrors(f && f in v ? { [f]: errorMessage(err) } : { form: errorMessage(err) })
      focusFirstInvalid(form.current)
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    const next: Errors = {}
    if (!v.name.trim()) next.name = t('dash.account.errName')
    if (!isEmail(v.email)) next.email = t('dash.app.errEmail')
    if (v.phone.replace(/\D/g, '').length < 9) next.phone = t('dash.app.errPhone')
    setErrors(next)
    if (Object.keys(next).length) return focusFirstInvalid(form.current)
    save.mutate()
  }

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <PageHeader title={t('dash.account.title')} description={t('dash.account.lead')} />

      {isError ? (
        <Card>
          <ErrorState message={errorMessage(error)} onRetry={() => void refetch()} retrying={isRefetching} />
        </Card>
      ) : isLoading ? (
        <Skeleton className="h-72 rounded-card" />
      ) : (
        <>
          <Card className="p-card">
            <CardHeader title={t('dash.account.profile')} />
            <form ref={form} onSubmit={submit} noValidate className="mt-5 space-y-4">
              {errors.form && <Notice tone="danger">{errors.form}</Notice>}
              <Field label={t('dash.account.name')} error={errors.name}>
                <Input value={v.name} onChange={(e) => setV((s) => ({ ...s, name: e.target.value }))} autoComplete="name" />
              </Field>
              <Field label={t('dash.account.email')} error={errors.email}>
                <Input type="email" value={v.email} onChange={(e) => setV((s) => ({ ...s, email: e.target.value }))} autoComplete="email" />
              </Field>
              <Field label={t('dash.account.phone')} error={errors.phone}>
                <Input type="tel" inputMode="tel" value={v.phone} onChange={(e) => setV((s) => ({ ...s, phone: e.target.value }))} autoComplete="tel" />
              </Field>
              <div>
                <Button type="submit" variant="primary" loading={save.isPending}>
                  {t('dash.account.save')}
                </Button>
              </div>
            </form>
          </Card>

          <Card className="space-y-5 p-card">
            <CardHeader title={t('dash.account.prefsTitle')} description={t('dash.account.prefsHint')} />
            <Field label={t('dash.account.payment')}>
              <Segmented
                aria-label={t('dash.account.payment')}
                value={prefs.payment}
                onValueChange={(p) => setPref('payment', p)}
                options={[
                  { value: 'saldo', label: t('dash.account.payBalance') },
                  { value: 'transfer', label: t('dash.account.payTransfer') },
                  { value: 'tunai', label: t('dash.account.payCash') },
                ]}
              />
            </Field>
            <Switch checked={prefs.notifDriver} onCheckedChange={(on) => setPref('notifDriver', on)} label={t('dash.account.notifDriver')} description={t('dash.account.notifDriverHint')} />
            <Switch checked={prefs.notifPromo} onCheckedChange={(on) => setPref('notifPromo', on)} label={t('dash.account.notifPromo')} description={t('dash.account.notifPromoHint')} />
          </Card>

          <Card className="space-y-4 p-card">
            <CardHeader title={t('dash.account.languageTitle')} description={t('dash.account.languageHint')} />
            <LocaleSwitcher />
          </Card>

          <div>
            <Button
              variant="secondary"
              iconLeft={<LogOut className="size-4" aria-hidden />}
              onClick={async () => {
                await api.auth.logout()
                navigate('/login', { replace: true })
              }}
            >
              {t('dash.account.signOut')}
            </Button>
          </div>
        </>
      )}
    </div>
  )
}
