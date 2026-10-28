import {
  PLAN_PENDING_LABEL,
  PLAN_STATUS_LABEL,
  ROUTE_STATUS_LABEL,
  STOP_STATUS_LABEL,
  type DriverStatus,
  type Plan,
  type PlanStatus,
  type RouteStatus,
  type StopStatus,
} from '@loong/domain'
import { cn } from '../lib/cn'

export type Tone = 'neutral' | 'info' | 'success' | 'warning' | 'danger'

const dotColor: Record<Tone, string> = {
  neutral: 'bg-subtle',
  info: 'bg-accent',
  success: 'bg-success',
  warning: 'bg-warning',
  danger: 'bg-danger',
}

/** Titik status 6px. Status dibawa oleh titik + teks, bukan lencana berwarna (lihat dashboard.md). */
export function StatusDot({ tone, pulse = false, className }: { tone: Tone; pulse?: boolean; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn('inline-block size-1.5 shrink-0 rounded-full', dotColor[tone], pulse && 'animate-[pulse-dot_1.8s_ease-in-out_infinite]', className)}
    />
  )
}

export function StatusLabel({ tone, children, pulse, className }: { tone: Tone; children: React.ReactNode; pulse?: boolean; className?: string }) {
  return (
    <span className={cn('inline-flex items-center gap-2 whitespace-nowrap', className)}>
      <StatusDot tone={tone} pulse={pulse} />
      {children}
    </span>
  )
}

export const planTone: Record<PlanStatus, Tone> = {
  draft: 'neutral',
  opsi_siap: 'neutral',
  menunggu_driver: 'warning',
  ditugaskan: 'info',
  berjalan: 'info',
  selesai: 'success',
  dibatalkan: 'neutral',
}

export const routeTone: Record<RouteStatus, Tone> = {
  menunggu_driver: 'warning',
  ditugaskan: 'info',
  berjalan: 'info',
  selesai: 'success',
}

export const stopTone: Record<StopStatus, Tone> = {
  menunggu: 'neutral',
  menuju: 'info',
  tiba: 'info',
  selesai: 'success',
  bermasalah: 'danger',
}

export const driverTone: Record<DriverStatus, Tone> = {
  online: 'success',
  bertugas: 'info',
  offline: 'neutral',
}

/** `pendingAction` (mis. pembatalan menunggu driver) menggantikan label status selama berlangsung: peringatan berdenyut. */
export function PlanStatusLabel({ status, pendingAction, className }: { status: PlanStatus; pendingAction?: Plan['pendingAction']; className?: string }) {
  if (pendingAction) {
    return (
      <StatusLabel tone="warning" pulse className={className}>
        {PLAN_PENDING_LABEL[pendingAction]}
      </StatusLabel>
    )
  }
  return (
    <StatusLabel tone={planTone[status]} pulse={status === 'berjalan'} className={className}>
      {PLAN_STATUS_LABEL[status]}
    </StatusLabel>
  )
}

export function RouteStatusLabel({ status, className }: { status: RouteStatus; className?: string }) {
  return (
    <StatusLabel tone={routeTone[status]} pulse={status === 'berjalan'} className={className}>
      {ROUTE_STATUS_LABEL[status]}
    </StatusLabel>
  )
}

export function StopStatusLabel({ status, className }: { status: StopStatus; className?: string }) {
  return (
    <StatusLabel tone={stopTone[status]} className={className}>
      {STOP_STATUS_LABEL[status]}
    </StatusLabel>
  )
}
