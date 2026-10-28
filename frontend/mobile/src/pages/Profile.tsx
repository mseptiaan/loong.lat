import { errorMessage, useApi, useDriverMe } from '@loong/data'
import { VEHICLE_TYPE_LABEL, formatDecimal, formatPhone } from '@loong/domain'
import { Avatar, Button, Card, CardHeader, ErrorState, Skeleton, StatusDot, Switch } from '@loong/ui'
import { FileCheck2, LogOut, Star } from 'lucide-react'
import { LocaleSwitcher } from '@loong/shell'
import { useNavigate } from 'react-router'
import { t } from '../i18n'
import { useDriverPrefs } from '../lib/prefs'

// kunci (bukan teks) agar nama dokumen mengikuti bahasa saat render
const DOCS = ['drv.account.docLicense', 'drv.account.docRegistration'] as const

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2.5 first:pt-0 last:pb-0">
      <dt className="text-small text-muted">{label}</dt>
      <dd className="text-right text-body font-semibold tabular-nums">{value}</dd>
    </div>
  )
}

export function Profile() {
  const api = useApi()
  const navigate = useNavigate()
  const me = useDriverMe()
  const [prefs, setPrefs] = useDriverPrefs()
  const driver = me.data?.driver
  const vehicle = me.data?.vehicle

  return (
    <main className="space-y-5 px-4 pb-6 pt-5">
      <h1 className="text-heading">{t('drv.account.title')}</h1>

      {me.isError ? (
        <Card>
          <ErrorState message={errorMessage(me.error)} onRetry={() => void me.refetch()} />
        </Card>
      ) : !driver ? (
        <>
          <Skeleton className="h-32 rounded-card" />
          <Skeleton className="h-44 rounded-card" />
        </>
      ) : (
        <>
          <Card className="flex items-center gap-4 p-card">
            <Avatar name={driver.name} size="lg" />
            <div className="min-w-0">
              <p className="truncate text-title">{driver.name}</p>
              <p className="text-small tabular-nums text-muted">{formatPhone(driver.phone)}</p>
              <p className="mt-1 flex items-center gap-1.5 text-small text-muted">
                <Star className="size-3.5 fill-current text-warning" aria-hidden />
                <span className="font-semibold tabular-nums text-fg">{formatDecimal(driver.rating, 2)}</span>
                · {t('drv.account.stats', { count: driver.completedTrips, joined: driver.joinedAt })}
              </p>
            </div>
          </Card>

          <Card className="p-card">
            <CardHeader title={t('drv.account.vehicle')} />
            {vehicle ? (
              <dl className="mt-3 divide-y divide-line">
                <Row label={t('drv.account.vehicleType')} value={VEHICLE_TYPE_LABEL[vehicle.type]} />
                <Row label={t('drv.account.vehicleModel')} value={vehicle.model} />
                <Row label={t('drv.account.vehiclePlate')} value={vehicle.plate} />
                <Row label={t('drv.account.vehicleLoad')} value={t('drv.account.vehicleLoadValue', { kg: vehicle.maxWeightKg, volume: vehicle.maxVolumeM3 })} />
                <Row label={t('drv.account.vehicleLargest')} value={`${vehicle.maxDimensionCm} cm`} />
              </dl>
            ) : (
              <p className="mt-2 text-body text-muted">{t('drv.account.vehicleNone')}</p>
            )}
          </Card>

          <Card className="p-card">
            <CardHeader title={t('drv.account.documents')} description={t('drv.account.documentsHint')} />
            <ul className="mt-3 divide-y divide-line">
              {DOCS.map((doc) => (
                <li key={doc} className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0">
                  <span className="flex items-center gap-2.5 text-body">
                    <FileCheck2 className="size-4 text-subtle" aria-hidden />
                    {t(doc)}
                  </span>
                  <span className="flex items-center gap-1.5 text-small text-muted">
                    <StatusDot tone="success" /> {t('drv.account.verified')}
                  </span>
                </li>
              ))}
            </ul>
          </Card>
        </>
      )}

      <Card className="p-card">
        <Switch
          checked={prefs.keepAwake}
          onCheckedChange={(v) => setPrefs({ keepAwake: v })}
          label={t('drv.account.keepAwake')}
          description={t('drv.account.keepAwakeHint')}
        />
      </Card>

      <Card className="flex items-center justify-between gap-4 p-card">
        <h2 className="text-body font-semibold">{t('drv.account.language')}</h2>
        <LocaleSwitcher className="[&>[role=radio]]:h-12 [&>[role=radio]]:min-w-12" />
      </Card>

      <Button
        variant="secondary"
        size="lg"
        className="w-full"
        iconLeft={<LogOut className="size-4" aria-hidden />}
        onClick={async () => {
          await api.auth.logout()
          navigate('/login', { replace: true })
        }}
      >
        {t('drv.account.signOut')}
      </Button>
    </main>
  )
}
