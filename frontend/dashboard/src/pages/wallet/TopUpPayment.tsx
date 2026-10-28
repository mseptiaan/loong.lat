import { SIMULATED_PAYMENTS, errorMessage, qk, useApi, useMutation, useQueryClient, useTopUp, useVirtualNow, useWallet } from '@loong/data'
import { TOPUP_METHOD_LABEL, formatRupiah, topUpIsVa, type TopUp } from '@loong/domain'
import { Button, Card, EmptyState, ErrorState, Notice, PageHeader, Skeleton, toast } from '@loong/ui'
import { CircleCheck, Clock, Copy } from 'lucide-react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import { QrCode } from '../../components/QrCode'
import { t, tr } from '../../i18n'
import { formatRemaining, safeNext } from '../../lib/money'

// langkah-langkah dibaca saat render (bukan di tingkat modul) agar mengikuti bahasa
const stepsVa = () => [t('dash.wallet.stepsVa1'), t('dash.wallet.stepsVa2'), t('dash.wallet.stepsVa3')]
const stepsQris = () => [t('dash.wallet.stepsQris1'), t('dash.wallet.stepsQris2'), t('dash.wallet.stepsQris3')]

function CopyButton({ text, kind }: { text: string; kind: 'amount' | 'va' }) {
  return (
    <Button
      variant="secondary"
      size="sm"
      iconLeft={<Copy className="size-3.5" aria-hidden />}
      aria-label={kind === 'amount' ? t('dash.wallet.copyAmountLabel') : t('dash.wallet.copyVaLabel')}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text)
          toast.success(kind === 'amount' ? t('dash.wallet.copiedAmount') : t('dash.wallet.copiedVa'))
        } catch {
          toast.error(t('dash.wallet.copyFailed'))
        }
      }}
    >
      {t('dash.wallet.copy')}
    </Button>
  )
}

function Waiting({ top, next }: { top: TopUp; next?: string }) {
  const api = useApi()
  const qc = useQueryClient()
  const now = useVirtualNow(1000)
  const remaining = Date.parse(top.expiresAt) - now
  const isVa = topUpIsVa(top.method)

  const pay = useMutation({
    mutationFn: () => api.wallet.simulatePay(top.id),
    onSuccess: (paid) => {
      qc.setQueryData(qk.topUp(top.id), paid)
      toast.success(t('dash.wallet.paidToast', { amount: paid.amountRp }))
    },
    onError: (err) => toast.error(errorMessage(err)),
  })
  const cancel = useMutation({
    mutationFn: () => api.wallet.cancelTopUp(top.id),
    onSuccess: (cancelled) => qc.setQueryData(qk.topUp(top.id), cancelled),
    onError: (err) => toast.error(errorMessage(err)),
  })

  return (
    <div className="space-y-5">
      <Card className="space-y-5 p-card">
        <div>
          <p className="text-small font-medium text-muted">{t('dash.wallet.amountToPay')}</p>
          <div className="mt-1 flex flex-wrap items-center gap-3">
            <p className="text-metric tabular-nums">{formatRupiah(top.amountRp)}</p>
            <CopyButton text={String(top.amountRp)} kind="amount" />
          </div>
        </div>

        <div className="border-t border-line pt-5">
          <p className="text-small font-medium text-muted">{TOPUP_METHOD_LABEL[top.method]}</p>
          {isVa ? (
            <div className="mt-1 flex flex-wrap items-center gap-3">
              <p className="font-mono text-title tabular-nums tracking-wide" aria-label={t('dash.wallet.vaLabel', { digits: top.reference.split('').join(' ') })}>
                {top.reference.replace(/(\d{4})(?=\d)/g, '$1 ')}
              </p>
              <CopyButton text={top.reference} kind="va" />
            </div>
          ) : (
            <div className="mt-3 flex flex-wrap items-center gap-5">
              <QrCode value={top.reference} />
              <p className="max-w-xs text-small text-muted">{t('dash.wallet.qrNote')}</p>
            </div>
          )}
        </div>

        <p className="flex items-center gap-2 border-t border-line pt-4 text-body">
          <Clock className="size-4 shrink-0 text-subtle" aria-hidden />
          <span>
            {tr(remaining > 0 ? 'dash.wallet.payBefore' : 'dash.wallet.payBeforeExpired', {
              when: top.expiresAt,
              remaining: formatRemaining(remaining),
              b: (c) => <span className="font-semibold">{c}</span>,
              m: (c) => <span className="text-muted">{c}</span>,
            })}
          </span>
        </p>
      </Card>

      <Card className="p-card">
        <h2 className="text-title">{t('dash.wallet.howTitle')}</h2>
        <ol className="mt-3 list-decimal space-y-1.5 pl-5 text-body text-muted marker:text-subtle">
          {(isVa ? stepsVa() : stepsQris()).map((s) => (
            <li key={s}>{s}</li>
          ))}
        </ol>
      </Card>

      {SIMULATED_PAYMENTS && (
        <Notice tone="info" title={t('dash.wallet.demoTitle')}>
          {t('dash.wallet.demoText')}
        </Notice>
      )}

      <div className="flex flex-wrap items-center gap-3">
        {SIMULATED_PAYMENTS && (
          <Button variant="primary" size="lg" loading={pay.isPending} onClick={() => pay.mutate()}>
            {t('dash.wallet.simulate')}
          </Button>
        )}
        <Button variant="ghost" loading={cancel.isPending} onClick={() => cancel.mutate()}>
          {t('dash.wallet.cancelTopUp')}
        </Button>
        {next && <span className="sr-only">{t('dash.wallet.continueHint')}</span>}
      </div>
    </div>
  )
}

function Paid({ top, next }: { top: TopUp; next?: string }) {
  const navigate = useNavigate()
  const { data: wallet } = useWallet()
  return (
    <Card className="space-y-4 p-card">
      <div className="flex items-start gap-3">
        <span className="grid size-10 shrink-0 place-items-center rounded-full bg-success-tint text-success-fg" aria-hidden>
          <CircleCheck className="size-5" />
        </span>
        <div>
          <h2 className="text-title">{t('dash.wallet.paidTitle')}</h2>
          <p className="mt-1 text-body text-muted">
            {top.paidAt ? t('dash.wallet.paidAddedAt', { amount: top.amountRp, when: top.paidAt }) : t('dash.wallet.paidAdded', { amount: top.amountRp })}
            {wallet && (
              <>
                {' '}
                {tr('dash.wallet.paidNow', { balance: wallet.balanceRp, b: (c) => <span className="font-semibold tabular-nums text-fg">{c}</span> })}
              </>
            )}
          </p>
        </div>
      </div>
      <div className="flex flex-wrap gap-3">
        {next ? (
          <>
            <Button variant="primary" size="lg" onClick={() => navigate(next)}>
              {t('dash.wallet.continueOrder')}
            </Button>
            <Button variant="secondary" asChild>
              <Link to="/wallet">{t('dash.wallet.viewBalance')}</Link>
            </Button>
          </>
        ) : (
          <>
            <Button variant="primary" size="lg" asChild>
              <Link to="/wallet">{t('dash.wallet.viewBalance')}</Link>
            </Button>
            <Button variant="secondary" asChild>
              <Link to="/plan/new">{t('dash.app.newDelivery')}</Link>
            </Button>
          </>
        )}
      </div>
    </Card>
  )
}

/** Instruksi pembayaran sebuah top up, dan hasilnya (menunggu, berhasil, kedaluwarsa, dibatalkan). */
export function TopUpPayment() {
  const { id } = useParams()
  const [params] = useSearchParams()
  const next = safeNext(params.get('next'))
  const { data: top, isLoading, isError, error, refetch, isRefetching } = useTopUp(id)
  const retryHref = `/wallet/top-up?amount=${top?.amountRp ?? ''}${next ? `&next=${encodeURIComponent(next)}` : ''}`

  return (
    <div className="mx-auto max-w-2xl space-y-6">
      <PageHeader
        back={{ label: t('dash.wallet.back'), href: '/wallet' }}
        title={top?.status === 'berhasil' ? t('dash.wallet.doneTitle') : t('dash.wallet.payTitle')}
        description={top ? t('dash.wallet.payDescription', { amount: top.amountRp, method: TOPUP_METHOD_LABEL[top.method] }) : undefined}
      />
      {isLoading ? (
        <div className="space-y-4">
          <Skeleton className="h-56 rounded-card" />
          <Skeleton className="h-32 rounded-card" />
        </div>
      ) : isError || !top ? (
        <Card>
          <ErrorState title={t('dash.wallet.notFound')} message={errorMessage(error)} onRetry={() => void refetch()} retrying={isRefetching} />
        </Card>
      ) : top.status === 'menunggu' ? (
        <Waiting top={top} next={next} />
      ) : top.status === 'berhasil' ? (
        <Paid top={top} next={next} />
      ) : (
        <Card>
          <EmptyState
            icon={<Clock />}
            title={top.status === 'kedaluwarsa' ? t('dash.wallet.expiredTitle') : t('dash.wallet.cancelledTitle')}
            description={top.status === 'kedaluwarsa' ? t('dash.wallet.expiredHint') : t('dash.wallet.cancelledHint')}
            action={
              <Button variant="primary" asChild>
                <Link to={retryHref}>{t('dash.wallet.createNew')}</Link>
              </Button>
            }
          />
        </Card>
      )}
    </div>
  )
}
