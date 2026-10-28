import {
  VEHICLE_TYPE_LABEL,
  formatDuration,
  formatKm,
  formatRupiah,
  formatTime,
  type PlanOption,
  type Route,
  type RouteStop,
  type Totals,
} from '@loong/domain'
import { msg, renderMsg, t, type Msg } from '@loong/i18n'
import { Check, Motorbike, Route as RouteIcon, Timer, Van } from 'lucide-react'
import { Fragment, type ReactNode } from 'react'
import { cn } from '../lib/cn'
import { Chip } from './feedback'
import { StopPin, type StopPinState } from '../map/markers'
import { StopStatusLabel } from './status'

/* ---------------- penggabungan stop di tempat yang sama ---------------- */

export interface StopGroup {
  first: RouteStop
  stops: RouteStop[]
}

/** Selisih derajat yang masih dianggap titik yang sama (±1 m). */
const SAME_PLACE_DEG = 1e-5

/**
 * Dua waypoint satu tempat bila `placeId`-nya sama, atau (tanpa `placeId`) koordinatnya sama. Nama tidak boleh jadi dasar:
 * semua titik yang ditandai di peta bernama sama ("Titik di peta"), padahal letaknya bisa berjauhan.
 */
function samePlace(a: RouteStop['waypoint'], b: RouteStop['waypoint']): boolean {
  if (a.placeId && b.placeId) return a.placeId === b.placeId
  return Math.abs(a.position.lat - b.position.lat) < SAME_PLACE_DEG && Math.abs(a.position.lng - b.position.lng) < SAME_PLACE_DEG
}

/** Stop berurutan di tempat dan jenis yang sama digabung ("Ambil 2 paket di Kemang Village"). */
export function groupStops(stops: RouteStop[]): StopGroup[] {
  const out: StopGroup[] = []
  for (const s of stops) {
    const last = out.at(-1)
    if (last && last.first.kind === s.kind && samePlace(last.first.waypoint, s.waypoint)) last.stops.push(s)
    else out.push({ first: s, stops: [s] })
  }
  return out
}

/** Kunci ringkasan barang yang dibuat mesin rute (`RouteStop.itemsSummary`): `{count}` barang dan `{kg}` kilogram. */
const ITEMS_SUMMARY_KEY = 'srv.engine.itemsSummary'

/**
 * Ringkasan barang satu kelompok stop: jumlah barang dan berat dijumlahkan ("4 barang · 3,2 kg"), bukan ringkasan tiap paket
 * berderet. Kosong bila ada ringkasan yang bukan buatan mesin rute; pemanggil lalu menampilkan ringkasan satu per satu.
 */
export function groupItemsSummary(stops: RouteStop[]): Msg | undefined {
  let count = 0
  let kg = 0
  for (const s of stops) {
    const m = s.itemsSummary
    const c = m.params?.count
    const w = m.params?.kg
    if (m.key !== ITEMS_SUMMARY_KEY || typeof c !== 'number' || typeof w !== 'number') return undefined
    count += c
    kg += w
  }
  return msg(ITEMS_SUMMARY_KEY, { count, kg: Math.round(kg * 1000) / 1000 })
}

/** "Ambil · Kemang Village" atau, untuk stop yang digabung, "Ambil 2 paket · Kemang Village". */
function stopTitle(stop: RouteStop, count: number): string {
  const place = stop.waypoint.name
  if (stop.kind === 'ambil') return count > 1 ? t('ui.stopList.pickupMany', { count, place }) : t('ui.stopList.pickup', { place })
  return count > 1 ? t('ui.stopList.dropoffMany', { count, place }) : t('ui.stopList.dropoff', { place })
}

/** Keadaan penanda satu kelompok stop: selesai bila semuanya selesai, bermasalah bila ada satu yang bermasalah. */
export function groupPinState(g: StopGroup, active: boolean): StopPinState {
  if (g.stops.every((x) => x.status === 'selesai')) return 'done'
  if (g.stops.some((x) => x.status === 'bermasalah')) return 'problem'
  return active ? 'active' : 'default'
}

/** Baris barang sebuah kelompok: jumlah gabungan, atau (tanpa ringkasan mesin) ringkasan tiap stop berderet. */
function itemsLine(g: StopGroup): string {
  if (g.stops.length === 1) return renderMsg(g.first.itemsSummary)
  const total = groupItemsSummary(g.stops)
  return total ? renderMsg(total) : g.stops.map((x) => renderMsg(x.itemsSummary)).join(' · ')
}

/**
 * Daftar stop berurutan. Padanan teks dari peta (juga jalan keluar bila tile peta gagal dimuat).
 * `activeIndex` menandai stop yang sedang dituju.
 */
export function StopList({
  stops,
  activeIndex,
  onSelect,
  group = false,
  showStatus = false,
  extra,
  className,
}: {
  stops: RouteStop[]
  activeIndex?: number
  onSelect?: (stop: RouteStop) => void
  group?: boolean
  showStatus?: boolean
  /** Konten tambahan di bawah tiap stop (mis. kode serah-terima, bukti antar); untuk stop yang digabung, satu per stop. */
  extra?: (stop: RouteStop) => ReactNode
  className?: string
}) {
  const groups = group ? groupStops(stops) : stops.map((s) => ({ first: s, stops: [s] }))
  return (
    <ol className={cn('relative', className)} aria-label={t('ui.stopList.label')}>
      {groups.map((g, gi) => {
        const s = g.first
        const active = g.stops.some((x) => stops.indexOf(x) === activeIndex)
        const last = gi === groups.length - 1
        const body = (
          <div className="flex min-w-0 flex-1 items-start justify-between gap-3">
            <div className="min-w-0">
              <p className={cn('text-body font-semibold', s.status === 'selesai' && 'text-muted')}>
                {stopTitle(s, g.stops.length)}
              </p>
              <p className="truncate text-small text-muted">{s.waypoint.address}</p>
              <p className="mt-0.5 text-small text-subtle">
                {itemsLine(g)}
                {s.waypoint.note && ` · ${s.waypoint.note}`}
              </p>
              {showStatus && s.status !== 'menunggu' && <StopStatusLabel status={s.status} className="mt-1 text-small text-muted" />}
              {s.issue && <p className="mt-1 text-small text-danger-fg">{renderMsg(s.issue)}</p>}
              {extra && g.stops.map((x) => <Fragment key={x.id}>{extra(x)}</Fragment>)}
            </div>
            <p className="shrink-0 text-small font-medium tabular-nums text-muted">{formatTime(s.arriveAt)}</p>
          </div>
        )
        return (
          <li key={s.id} className="relative flex gap-3 pb-4 last:pb-0">
            {/* rel penghubung antar pin */}
            {!last && <span aria-hidden className="absolute bottom-0 left-[12px] top-[30px] w-px bg-line-strong" />}
            <StopPin index={s.seq} to={g.stops.at(-1)?.seq} kind={s.kind} state={groupPinState(g, active)} className="relative z-10 mt-0.5 shrink-0" />
            {onSelect ? (
              <button type="button" onClick={() => onSelect(s)} className="-m-1 flex min-w-0 flex-1 rounded-input p-1 text-left transition-colors hover:bg-sunken">
                {body}
              </button>
            ) : (
              body
            )}
          </li>
        )
      })}
    </ol>
  )
}

/* ---------------- kartu perbandingan opsi (langkah 3 wizard) ---------------- */

function VehicleSummary({ totals }: { totals: Totals }) {
  return (
    <span className="inline-flex items-center gap-1.5">
      {totals.motor > 0 && (
        <span className="inline-flex items-center gap-1">
          <Motorbike className="size-4" aria-hidden />
          {t('ui.route.motorbikes', { count: totals.motor })}
        </span>
      )}
      {totals.motor > 0 && totals.blindvan > 0 && <span aria-hidden>+</span>}
      {totals.blindvan > 0 && (
        <span className="inline-flex items-center gap-1">
          <Van className="size-4" aria-hidden />
          {t('ui.route.blindVans', { count: totals.blindvan })}
        </span>
      )}
    </span>
  )
}

/**
 * Satu opsi rute. Hemat ditulis sebagai teks biasa (rupiah + persen terhadap "kirim terpisah"), tanpa lencana
 * hijau/panah. Opsi terpilih ditandai garis aksen + tint + centang, bukan warna saja.
 */
export function RouteOptionCard({
  option,
  baseline,
  selected,
  onSelect,
  name,
}: {
  option: PlanOption
  baseline: Totals
  selected: boolean
  onSelect: () => void
  name: string
}) {
  const { savings, totals } = option
  // klaim "lebih cepat" hanya bila angkanya memang lebih cepat dibanding kirim terpisah
  const fasterBy = Math.round(baseline.avgDeliveryMin - totals.avgDeliveryMin)
  const saving =
    savings.costRp > 0
      ? t('ui.route.saving', { cost: savings.costRp, pct: savings.pct })
      : savings.costRp < 0
        ? fasterBy >= 1
          ? t('ui.route.pricierFaster', { extra: -savings.costRp, faster: fasterBy })
          : t('ui.route.pricier', { extra: -savings.costRp })
        : t('ui.route.sameCost')
  return (
    <label
      className={cn(
        'relative block cursor-pointer rounded-card border bg-surface p-card shadow-card transition-[border-color,background-color,box-shadow] duration-[140ms]',
        'has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-accent',
        selected ? 'border-accent bg-accent-tint' : 'border-line hover:border-line-strong',
      )}
    >
      <input type="radio" name={name} checked={selected} onChange={onSelect} className="sr-only" aria-label={t('ui.route.optionLabel', { label: option.label, cost: totals.costRp })} />
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="flex flex-wrap items-center gap-2">
            <p className="text-title">{renderMsg(option.label)}</p>
            {option.recommended && <Chip>{t('ui.route.recommended')}</Chip>}
          </div>
          <p className="mt-0.5 text-small text-muted">{renderMsg(option.headline)}</p>
        </div>
        <span
          aria-hidden
          className={cn('mt-1 grid size-5 shrink-0 place-items-center rounded-full border transition-colors', selected ? 'border-accent bg-accent text-on-accent' : 'border-line-input bg-surface')}
        >
          {selected && <Check className="size-3.5" />}
        </span>
      </div>
      <p className="mt-3 text-metric-sm tabular-nums">{formatRupiah(totals.costRp)}</p>
      <p className="mt-0.5 text-small text-muted">{saving}</p>
      <dl className="mt-3 grid grid-cols-3 gap-2 border-t border-line pt-3 text-small">
        <div>
          <dt className="flex items-center gap-1 text-muted">
            <RouteIcon className="size-3.5" aria-hidden />
            {t('ui.route.distance')}
          </dt>
          <dd className="mt-0.5 font-semibold tabular-nums">{formatKm(totals.distanceKm)}</dd>
        </div>
        <div>
          <dt className="flex items-center gap-1 text-muted">
            <Timer className="size-3.5" aria-hidden />
            {t('ui.route.finishesIn')}
          </dt>
          <dd className="mt-0.5 font-semibold tabular-nums">{formatDuration(totals.durationMin)}</dd>
        </div>
        <div>
          <dt className="text-muted">{t('ui.route.avgArrival')}</dt>
          <dd className="mt-0.5 font-semibold tabular-nums">{formatDuration(totals.avgDeliveryMin)}</dd>
        </div>
      </dl>
      <p className="mt-2.5 flex items-center gap-1.5 text-small text-muted">
        <VehicleSummary totals={totals} />
        <span aria-hidden>·</span>
        {t('ui.route.stops', { count: totals.stops })}
      </p>
      <span className="sr-only">{t('ui.route.compare', { cost: baseline.costRp, avg: baseline.avgDeliveryMin })}</span>
    </label>
  )
}

/* ---------------- rincian harga ---------------- */

/** Rincian biaya per rute + total. Setiap rupiah punya asal (prinsip 2). */
export function FareBreakdown({ routes, className }: { routes: Route[]; className?: string }) {
  const total = routes.reduce((s, r) => s + r.costRp, 0)
  return (
    <div className={cn('space-y-3', className)}>
      {routes.map((r, i) => (
        <div key={r.id}>
          {routes.length > 1 && (
            <p className="mb-1 text-small font-semibold text-muted">
              {t('ui.fare.routeTitle', { n: i + 1, vehicle: VEHICLE_TYPE_LABEL[r.vehicleType] })}
            </p>
          )}
          <dl className="space-y-1">
            {r.fare.map((l, li) => (
              <div key={`${l.label.key}:${li}`} className="flex justify-between gap-4 text-body">
                <dt className="text-muted">{renderMsg(l.label)}</dt>
                <dd className="tabular-nums">{l.amountRp < 0 ? `−${formatRupiah(-l.amountRp)}` : formatRupiah(l.amountRp)}</dd>
              </div>
            ))}
          </dl>
        </div>
      ))}
      <div className="flex justify-between gap-4 border-t border-line pt-3">
        <span className="text-title">{t('ui.fare.total')}</span>
        <span className="text-title tabular-nums">{formatRupiah(total)}</span>
      </div>
    </div>
  )
}
