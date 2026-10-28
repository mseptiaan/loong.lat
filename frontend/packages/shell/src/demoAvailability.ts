import type { RemoteApi } from '@loong/mock-api'

/**
 * Panel Demo hanya masuk akal untuk backend tiruan: jam virtual, autopilot, kegagalan tiruan, dan "buat plan acak" tidak
 * ada di backend asli. Di mode http panel tidak dirender, jadi tidak ada layar yang memanggil `demo.*` (kecuali `demo.state`,
 * jam nyata yang dipakai `useVirtualNow`).
 */
export const hasDemoPanel = (api: Pick<RemoteApi, 'kind'>): boolean => api.kind === 'mock'
