import { errorField, errorMessage, useApi, useMutation, useWallet } from '@loong/data'
import {
  ApiError,
  TOPUP_METHODS,
  TOPUP_METHOD_HINT,
  TOPUP_METHOD_LABEL,
  TOPUP_MAX_RP,
  TOPUP_MIN_RP,
  TOPUP_PRESETS_RP,
  formatRupiah,
  validateTopUpAmount,
  type TopUpMethod,
} from '@loong/domain'
import { renderMsg } from '@loong/i18n'
import { Button, Card, Field, Input, Notice, PageHeader, cn } from '@loong/ui'
import { Check } from 'lucide-react'
import { useRef, useState, type FormEvent } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import { t, tr } from '../../i18n'
import { focusFirstInvalid } from '../../lib/forms'
import { formatAmountInput, parseAmount, safeNext } from '../../lib/money'

/** Top up saldo: pilih jumlah dan cara bayar, lalu menuju instruksi pembayaran. */
export function TopUpPage() {
  const api = useApi()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'))
  const { data: wallet } = useWallet()
  const form = useRef<HTMLFormElement>(null)

  const suggested = Number(params.get('amount'))
  const [raw, setRaw] = useState(() => (Number.isFinite(suggested) && suggested > 0 ? formatAmountInput(Math.min(TOPUP_MAX_RP, Math.max(TOPUP_MIN_RP, Math.round(suggested)))) : ''))
  const [method, setMethod] = useState<TopUpMethod>('va_bca')
  const [touched, setTouched] = useState(false)
  // pending: galat karena sudah ada top up yang menunggu pembayaran (kode 'bentrok'), agar ada tautan ke saldo
  const [serverError, setServerError] = useState<{ field?: string; message: string; pending: boolean }>()

  const amount = parseAmount(raw)
  const invalid = touched ? validateTopUpAmount(amount) : undefined
  const amountError = (invalid ? renderMsg(invalid) : undefined) ?? (serverError?.field === 'amountRp' ? serverError.message : undefined)

  const create = useMutation({
    mutationFn: () => api.wallet.createTopUp({ amountRp: amount, method }),
    onSuccess: (top) => navigate(`/wallet/top-up/${top.id}${next ? `?next=${encodeURIComponent(next)}` : ''}`),
    onError: (err) => {
      setServerError({ field: errorField(err), message: errorMessage(err), pending: err instanceof ApiError && err.code === 'bentrok' })
      if (errorField(err) === 'amountRp') focusFirstInvalid(form.current)
    },
  })

  function submit(e: FormEvent) {
    e.preventDefault()
    setTouched(true)
    setServerError(undefined)
    if (validateTopUpAmount(amount)) return focusFirstInvalid(form.current)
    create.mutate()
  }

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <PageHeader
        back={{ label: next ? t('dash.wallet.backToOrder') : t('dash.wallet.back'), href: next ?? '/wallet' }}
        title={t('dash.wallet.topUpTitle')}
        description={wallet ? t('dash.wallet.currentBalance', { amount: wallet.balanceRp }) : t('dash.wallet.topUpLead')}
      />

      <form ref={form} onSubmit={submit} noValidate className="space-y-6">
        <Card className="space-y-4 p-card">
          <Field label={t('dash.wallet.amountLabel')} hint={t('dash.wallet.amountHint', { min: TOPUP_MIN_RP, max: TOPUP_MAX_RP })} error={amountError}>
            <Input
              inputMode="numeric"
              autoComplete="off"
              placeholder={t('dash.wallet.amountPlaceholder', { example: 150_000 })}
              leading={<span className="text-body text-muted">Rp</span>}
              value={raw}
              onChange={(e) => {
                const n = parseAmount(e.target.value)
                setRaw(Number.isFinite(n) ? formatAmountInput(n) : '')
                setServerError(undefined)
              }}
              onBlur={() => setTouched(true)}
            />
          </Field>
          <div className="flex flex-wrap gap-2" role="group" aria-label={t('dash.wallet.quickAmounts')}>
            {TOPUP_PRESETS_RP.map((p) => (
              <button
                key={p}
                type="button"
                aria-pressed={amount === p}
                onClick={() => {
                  setRaw(formatAmountInput(p))
                  setTouched(true)
                  setServerError(undefined)
                }}
                className={cn(
                  'h-10 rounded-chip border px-4 text-body font-medium tabular-nums transition-colors',
                  amount === p ? 'border-accent bg-accent-tint text-accent' : 'border-line bg-surface hover:border-line-strong',
                )}
              >
                {formatRupiah(p)}
              </button>
            ))}
          </div>
          {wallet && Number.isFinite(amount) && !validateTopUpAmount(amount) && (
            <p className="text-small text-muted">
              {tr('dash.wallet.balanceAfterTopUp', {
                balance: wallet.balanceRp + amount,
                b: (c) => <span className="font-semibold tabular-nums text-fg">{c}</span>,
              })}
            </p>
          )}
        </Card>

        <fieldset>
          <legend className="mb-3 text-title">{t('dash.wallet.payWith')}</legend>
          <div className="space-y-2" role="radiogroup" aria-label={t('dash.wallet.paymentMethod')}>
            {TOPUP_METHODS.map((m) => {
              const selected = method === m
              return (
                <label
                  key={m}
                  className={cn(
                    'flex cursor-pointer items-start gap-3 rounded-card border bg-surface p-card shadow-card transition-[border-color,background-color] duration-[140ms]',
                    'has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-accent',
                    selected ? 'border-accent bg-accent-tint' : 'border-line hover:border-line-strong',
                  )}
                >
                  <input type="radio" name="cara" value={m} checked={selected} onChange={() => setMethod(m)} className="sr-only" />
                  <span className="min-w-0 flex-1">
                    <span className="block text-body font-semibold">{TOPUP_METHOD_LABEL[m]}</span>
                    <span className="block text-small text-muted">{TOPUP_METHOD_HINT[m]}</span>
                  </span>
                  {selected && <Check className="mt-0.5 size-4 shrink-0 text-accent" aria-hidden />}
                </label>
              )
            })}
          </div>
        </fieldset>

        {serverError && serverError.field !== 'amountRp' && (
          <Notice
            tone="danger"
            action={
              serverError.pending ? (
                <Button variant="secondary" size="sm" asChild>
                  <Link to="/wallet">{t('dash.wallet.viewBalance')}</Link>
                </Button>
              ) : undefined
            }
          >
            {serverError.message}
          </Notice>
        )}

        <div className="flex flex-wrap items-center gap-3">
          <Button type="submit" variant="primary" size="lg" loading={create.isPending}>
            {t('dash.wallet.continue')}
          </Button>
          <Button variant="ghost" asChild>
            <Link to={next ?? '/wallet'}>{t('dash.wallet.cancel')}</Link>
          </Button>
        </div>
      </form>
    </div>
  )
}
