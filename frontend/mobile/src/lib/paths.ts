/** Jalur layar rute. Slug-nya kode (bukan teks tampilan); lihat main.tsx. */
export const routePath = (routeId: string) => `/route/${routeId}`
export const stopPath = (routeId: string, stopId: string) => `/route/${routeId}/stop/${stopId}`
export const routeDonePath = (routeId: string) => `/route/${routeId}/done`
