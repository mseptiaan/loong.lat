/**
 * Backend tiruan loong.lat.
 *
 *  - Di aplikasi (console/backoffice/driver): `createRemoteApi()` -> ApiClient yang bicara ke host api.<domain>.
 *  - Di host api.<domain>: `startApiHost()` menjalankan server tiruan dan melayani RPC.
 *  - Di tes: `createApi(new MockServer({ storage: memoryRawStorage(), latency: [0, 0] }), sesi)`.
 */
export { createRemoteApi, type RemoteApi, type RemoteApiOptions, type ApiKind, type ApiStatus } from './rpc/client'
export { startApiHost, isAllowedOrigin, type ApiHost } from './rpc/host'
export { createApi, tickAutopilot } from './server/api'
export { tickDispatch } from './server/dispatch'
export { MockServer, localRawStorage, memoryRawStorage, type RawStorage, type ServerOptions } from './server/server'
export { createSeed, SCENARIO } from './seed'
export { createMapboxRoads, type MapboxRoadsOptions } from './roads/mapbox'
export { localRoadCache, memoryRoadCache, type RoadCache, type RoadProvider, type RoadStatus } from './roads/provider'
export { ITEM_CATALOG, catalogItem, makeItem, nextId, type CatalogItem } from './seed/catalog'
export { computeOptionSet, inputHash } from './engine/solve'
export { aggregateItems, aggregateShipments, fitsVehicle, validateShipments } from './engine/model'
export { makeVehicles } from './seed/fleet'
export type { DB } from './db'
