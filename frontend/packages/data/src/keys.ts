import type { PlanFilter, TopUpFilter } from '@loong/domain'

/** Kunci query terpusat; peristiwa dari backend meng-invalidasi berdasarkan awalan kunci ini. */
export const qk = {
  plans: (filter?: PlanFilter) => ['plans', filter ?? {}] as const,
  plan: (id: string) => ['plan', id] as const,
  vehicles: ['vehicles'] as const,
  drivers: ['drivers'] as const,
  users: (q?: string) => ['users', q ?? ''] as const,
  user: (id: string) => ['user', id] as const,
  notifications: ['notifications'] as const,
  profile: ['profile'] as const,
  wallet: ['wallet'] as const,
  walletTx: ['wallet', 'tx'] as const,
  topUp: (id: string) => ['wallet', 'topup', id] as const,
  financeOverview: ['finance', 'overview'] as const,
  topUps: (filter?: TopUpFilter) => ['finance', 'topups', filter ?? {}] as const,
  userWalletTx: (userId: string) => ['finance', 'tx', userId] as const,
  driverMe: ['driver', 'me'] as const,
  driverRoutes: ['driver', 'routes'] as const,
  driverHistory: ['driver', 'history'] as const,
  driverEarnings: ['driver', 'earnings'] as const,
  liveRoutes: ['live', 'routes'] as const,
  idleDrivers: ['live', 'idle'] as const,
  suggestions: (planId: string, routeId: string) => ['suggest', planId, routeId] as const,
  demo: ['demo'] as const,
  settings: ['settings'] as const,
}
