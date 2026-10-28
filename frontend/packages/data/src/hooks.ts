import { useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { DEFAULT_SETTINGS, routePositionAt, virtualNowOf, type PlanFilter, type Route, type TopUpFilter } from '@loong/domain'
import { qk } from './keys'
import { planPollMs, plansPollMs } from './pending'
import { useApi, useSession } from './provider'

/* Hook baca. Semuanya hanya bicara lewat useApi(); kesegarannya dijaga oleh peristiwa backend. */

export const usePlans = (filter?: PlanFilter) => {
  const api = useApi()
  return useQuery({ queryKey: qk.plans(filter), queryFn: () => api.plans.list(filter), refetchInterval: (q) => plansPollMs(q.state.data) })
}

export const usePlan = (id: string | undefined) => {
  const api = useApi()
  return useQuery({ queryKey: qk.plan(id ?? ''), queryFn: () => api.plans.get(id!), enabled: !!id, refetchInterval: (q) => planPollMs(q.state.data) })
}

/** Daftar kendaraan. `enabled: false` tidak memanggil API sama sekali (aplikasi pelanggan tidak mendapat data armada dari backend asli). */
export const useVehicles = ({ enabled = true }: { enabled?: boolean } = {}) => {
  const api = useApi()
  return useQuery({ queryKey: qk.vehicles, queryFn: () => api.fleet.vehicles.list(), enabled })
}

/** Daftar driver. `enabled: false` tidak memanggil API (aplikasi pelanggan tidak mendapat data driver dari backend asli). */
export const useDrivers = ({ enabled = true }: { enabled?: boolean } = {}) => {
  const api = useApi()
  return useQuery({ queryKey: qk.drivers, queryFn: () => api.fleet.drivers.list(), enabled })
}

export const useUsers = (q?: string) => {
  const api = useApi()
  return useQuery({ queryKey: qk.users(q), queryFn: () => api.users.list(q) })
}

export const useUser = (id: string | undefined) => {
  const api = useApi()
  return useQuery({ queryKey: qk.user(id ?? ''), queryFn: () => api.users.get(id!), enabled: !!id })
}

export const useNotifications = () => {
  const api = useApi()
  const session = useSession()
  return useQuery({ queryKey: qk.notifications, queryFn: () => api.notifications.list(), enabled: !!session })
}

export const useProfile = () => {
  const api = useApi()
  const session = useSession()
  return useQuery({ queryKey: qk.profile, queryFn: () => api.account.profile(), enabled: session?.role === 'customer' })
}

export const useWallet = () => {
  const api = useApi()
  const session = useSession()
  return useQuery({ queryKey: qk.wallet, queryFn: () => api.wallet.get(), enabled: session?.role === 'customer' })
}

export const useWalletTransactions = () => {
  const api = useApi()
  const session = useSession()
  return useQuery({ queryKey: qk.walletTx, queryFn: () => api.wallet.transactions(), enabled: session?.role === 'customer' })
}

export const useTopUp = (id: string | undefined) => {
  const api = useApi()
  return useQuery({ queryKey: qk.topUp(id ?? ''), queryFn: () => api.wallet.getTopUp(id!), enabled: !!id })
}

export const useFinanceOverview = () => {
  const api = useApi()
  const session = useSession()
  return useQuery({ queryKey: qk.financeOverview, queryFn: () => api.finance.overview(), enabled: session?.role === 'admin' })
}

export const useTopUps = (filter?: TopUpFilter) => {
  const api = useApi()
  const session = useSession()
  return useQuery({ queryKey: qk.topUps(filter), queryFn: () => api.finance.topUps(filter), enabled: session?.role === 'admin' })
}

export const useUserWalletTx = (userId: string | undefined) => {
  const api = useApi()
  const session = useSession()
  return useQuery({ queryKey: qk.userWalletTx(userId ?? ''), queryFn: () => api.finance.transactions(userId!), enabled: !!userId && session?.role === 'admin' })
}

export const useDriverMe = () => {
  const api = useApi()
  return useQuery({ queryKey: qk.driverMe, queryFn: () => api.driver.me() })
}

export const useDriverRoutes = () => {
  const api = useApi()
  return useQuery({ queryKey: qk.driverRoutes, queryFn: () => api.driver.routes() })
}

export const useDriverHistory = () => {
  const api = useApi()
  return useQuery({ queryKey: qk.driverHistory, queryFn: () => api.driver.history() })
}

export const useDriverEarnings = () => {
  const api = useApi()
  return useQuery({ queryKey: qk.driverEarnings, queryFn: () => api.driver.earnings() })
}

export const useLiveRoutes = () => {
  const api = useApi()
  return useQuery({ queryKey: qk.liveRoutes, queryFn: () => api.live.routes() })
}

export const useIdleDrivers = () => {
  const api = useApi()
  return useQuery({ queryKey: qk.idleDrivers, queryFn: () => api.live.idleDrivers() })
}

export const useSuggestions = (planId: string | undefined, routeId: string | undefined) => {
  const api = useApi()
  return useQuery({
    queryKey: qk.suggestions(planId ?? '', routeId ?? ''),
    queryFn: () => api.dispatch.suggest(planId!, routeId!),
    enabled: !!planId && !!routeId,
  })
}

/** Pengaturan bersama (fitur AI hidup/mati). Selalu ada nilai bawaan, jadi layar tidak perlu menunggu untuk merender. */
export const useSettings = () => {
  const api = useApi()
  const session = useSession()
  return useQuery({ queryKey: qk.settings, queryFn: () => api.settings.get(), enabled: !!session, placeholderData: DEFAULT_SETTINGS })
}

/** Status Panel Demo: kecepatan jam, autopilot, kegagalan tiruan. Selalu tersedia tanpa login. */
export const useDemoState = () => {
  const api = useApi()
  return useQuery({ queryKey: qk.demo, queryFn: () => api.demo.state(), staleTime: Infinity })
}

/**
 * Waktu virtual sekarang (ms), diperbarui tiap `intervalMs`. Jam bisa dipercepat dari Panel Demo;
 * semua tampilan ETA dan posisi memakai ini, bukan Date.now().
 */
export function useVirtualNow(intervalMs = 1000): number {
  const clock = useDemoState().data?.clock
  // waktu nyata disimpan di state dan diperbarui oleh interval (render tetap murni); jam virtual diturunkan darinya
  const [realNow, setRealNow] = useState(() => Date.now())
  useEffect(() => {
    const id = setInterval(() => setRealNow(Date.now()), intervalMs)
    return () => clearInterval(id)
  }, [intervalMs])
  return clock ? virtualNowOf(clock, realNow) : realNow
}

/** Posisi kendaraan pada sebuah rute, dianimasikan lewat jam virtual (tanpa event per detik dari backend). */
export function useRoutePosition(route: Route | undefined, intervalMs = 500) {
  const now = useVirtualNow(intervalMs)
  return route ? routePositionAt(route, now) : undefined
}
