import { DataProvider, useQueryClient } from '@loong/data'
import { useLocale, I18nBoundary } from '@loong/i18n/react'
import type { RemoteApi } from '@loong/mock-api'
import { UIProvider } from '@loong/ui'
import { useEffect, useRef, type ReactNode } from 'react'
import { ConnectionGate } from './ConnectionGate'
import { DemoPanel } from './DemoPanel'
import { hasDemoPanel } from './demoAvailability'

/** Teks dari backend tiruan (jawaban AI, alasan penugasan) dibuat per bahasa: muat ulang semua data saat bahasa berganti. */
function LocaleDataSync() {
  const qc = useQueryClient()
  const locale = useLocale()
  const first = useRef(true)
  useEffect(() => {
    if (first.current) first.current = false
    else void qc.invalidateQueries()
  }, [locale, qc])
  return null
}

/** Akar tiap aplikasi: data, UI (tooltip/toast), gerbang koneksi, dan Panel Demo (hanya untuk backend tiruan). */
export function AppRoot({
  api,
  children,
  toastPosition,
}: {
  api: RemoteApi
  children: ReactNode
  toastPosition?: 'top-center' | 'bottom-center' | 'top-right'
}) {
  return (
    <DataProvider api={api}>
      <LocaleDataSync />
      {/* bahasa berganti -> pohon di bawah dipasang ulang; router, klien API, dan cache data di atasnya tetap */}
      <I18nBoundary>
        <UIProvider toastPosition={toastPosition}>
          <ConnectionGate>{children}</ConnectionGate>
          {hasDemoPanel(api) && <DemoPanel />}
        </UIProvider>
      </I18nBoundary>
    </DataProvider>
  )
}
