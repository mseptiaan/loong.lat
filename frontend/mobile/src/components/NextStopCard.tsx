import { useRoutePosition } from '@loong/data'
import type { Route } from '@loong/domain'
import { Button, Menu, MenuItem, cn } from '@loong/ui'
import { ChevronDown, ExternalLink, Navigation } from 'lucide-react'
import { Link } from 'react-router'
import { t } from '../i18n'
import { googleMapsUrl, openExternal, wazeUrl } from '../lib/links'
import { stopPath } from '../lib/paths'

/**
 * Kartu "Berikutnya": stop yang sedang dituju, ETA yang berjalan, dan jalan pintas navigasi.
 * Menempel di atas daftar/peta supaya driver tidak perlu mencari "sekarang harus ke mana".
 */
export function NextStopCard({ route, className }: { route: Route; className?: string }) {
  const pos = useRoutePosition(route, 1000)
  const stop = route.stops.find((s) => s.status !== 'selesai' && s.status !== 'bermasalah')
  if (!stop) return null
  const arrived = stop.status === 'tiba'
  const minutes = pos ? Math.max(1, Math.ceil(pos.remainingLegMin)) : undefined
  const target = stop.waypoint.position

  return (
    <section aria-label={t('drv.route.nextStopLabel')} className={cn('rounded-card border border-accent/30 bg-surface p-card shadow-overlay', className)}>
      <div className="flex items-baseline justify-between gap-3">
        <p className="text-body font-semibold text-accent">{stop.kind === 'ambil' ? t('drv.app.nextPickup') : t('drv.app.nextDropoff')}</p>
        <p className="text-body font-semibold tabular-nums text-fg" aria-live="off">
          {arrived ? t('drv.app.arrived') : minutes ? t('drv.app.minutesLeft', { minutes }) : ''}
        </p>
      </div>
      <p className="mt-1 text-title">{stop.waypoint.name}</p>
      <p className="text-body text-muted">{stop.waypoint.address}</p>
      {stop.waypoint.note && <p className="mt-1 text-body text-muted">{t('drv.app.landmark', { note: stop.waypoint.note })}</p>}
      <div className="mt-3 grid grid-cols-2 gap-2.5">
        <Menu
          align="start"
          trigger={
            <Button variant="secondary" iconLeft={<Navigation className="size-4" aria-hidden />} iconRight={<ChevronDown className="size-4" aria-hidden />}>
              {t('drv.route.navigate')}
            </Button>
          }
        >
          <MenuItem icon={<ExternalLink />} onSelect={() => openExternal(googleMapsUrl(target, route.vehicleType))}>
            Google Maps
          </MenuItem>
          <MenuItem icon={<ExternalLink />} onSelect={() => openExternal(wazeUrl(target))}>
            Waze
          </MenuItem>
        </Menu>
        <Button variant="secondary" asChild>
          <Link to={stopPath(route.id, stop.id)}>{t('drv.route.stopDetails')}</Link>
        </Button>
      </div>
    </section>
  )
}
