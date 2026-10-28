import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createContext, useContext, useEffect, useState, useSyncExternalStore, type ReactNode } from 'react'
import { type AppEvent } from '@loong/domain'
import type { Msg } from '@loong/i18n'
import type { ApiStatus, RemoteApi } from '@loong/mock-api'
import { isRetryable } from './errors'

const ApiContext = createContext<RemoteApi | null>(null)

/** Satu-satunya jalan ke "backend": `useApi()`. Layar tidak boleh mengimpor mock-api langsung. */
export function useApi(): RemoteApi {
  const api = useContext(ApiContext)
  if (!api) throw new Error('useApi harus dipakai di dalam <DataProvider>.') // i18n-ignore: galat pemrogram (tidak tampil ke pengguna)
  return api
}

export function useSession() {
  const api = useApi()
  return useSyncExternalStore(
    (cb) => api.onSession(cb),
    () => api.getSession(),
  )
}

export function useApiStatus(): { status: ApiStatus; detail?: Msg } {
  const api = useApi()
  const [state, setState] = useState<{ status: ApiStatus; detail?: Msg }>({ status: api.status() })
  useEffect(() => api.onStatus((status, detail) => setState({ status, detail })), [api])
  return state
}

/** Peristiwa dari backend -> invalidasi query yang terpengaruh. */
function invalidateFor(qc: QueryClient, event: AppEvent) {
  const inv = (...keys: string[][]) => keys.forEach((queryKey) => void qc.invalidateQueries({ queryKey }))
  switch (event.type) {
    case 'plans.changed':
      inv(['plans'], ['plan'], ['driver'], ['live'], ['suggest'], ['users'], ['user'])
      break
    case 'fleet.changed':
      inv(['vehicles'], ['drivers'], ['driver'], ['live'], ['suggest'])
      break
    case 'users.changed':
      inv(['users'], ['user'], ['profile'])
      break
    case 'notifications.changed':
      inv(['notifications'])
      break
    case 'wallet.changed':
      inv(['wallet'], ['finance'], ['users'], ['user'])
      break
    case 'settings.changed':
      inv(['settings'])
      break
    case 'clock.changed':
    case 'demo.changed':
      inv(['demo'], ['live'], ['plan'], ['plans'], ['driver'])
      break
    case 'data.reset':
      void qc.invalidateQueries()
      break
  }
}

export function DataProvider({ api, children }: { api: RemoteApi; children: ReactNode }) {
  const [client] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            // kesegaran dijaga oleh peristiwa dari backend, bukan polling atau fokus jendela
            staleTime: 15_000,
            refetchOnWindowFocus: false,
            retry: (count, err) => count < 1 && isRetryable(err),
          },
          mutations: { retry: false },
        },
      }),
  )

  useEffect(() => {
    const off = api.events.subscribe((event) => invalidateFor(client, event))
    // setelah (re)koneksi, ambil ulang apa pun yang gagal saat backend belum siap
    const offStatus = api.onStatus((status) => {
      if (status === 'siap') void client.invalidateQueries()
    })
    return () => {
      off()
      offStatus()
    }
  }, [api, client])

  return (
    <ApiContext.Provider value={api}>
      <QueryClientProvider client={client}>{children}</QueryClientProvider>
    </ApiContext.Provider>
  )
}
