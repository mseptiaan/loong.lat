import { appOrigin, currentApp } from '@loong/domain'
import { initLocale, subscribeLocale, t } from '@loong/i18n'
import { createMapboxRoads, localRoadCache, startApiHost } from '@loong/mock-api'

// bahasa dipilih sebelum render pertama (bawaan Inggris; `?lang=id` mengubahnya; lihat packages/i18n/README.md)
initLocale()

// api.<domain>: backend tiruan. Melayani RPC dari console/backoffice/driver lewat postMessage dan
// memegang satu-satunya salinan data (localStorage origin ini) sehingga ketiga aplikasi berbagi state.
// Jalan sungguhan (jarak + jalur) dari Mapbox bila ada token; tanpa token: perkiraan dan garis halus.
const token = import.meta.env.VITE_MAPBOX_TOKEN?.trim()
const roads = token ? createMapboxRoads({ token, cache: localRoadCache() }) : undefined
const host = startApiHost({ roads })

const status = document.getElementById('status')
function roadsLine(): string {
  if (!roads) return t('shell.apiHost.roadsInactive')
  const s = roads.status()
  const summary = t('shell.apiHost.roadsActive', { provider: s.provider, requests: s.requests, cacheHits: s.cacheHits, failures: s.failures })
  return s.lastError ? t('shell.apiHost.roadsWithError', { summary, error: s.lastError }) : summary
}

/** Teks tetap halaman (judul tab, judul, pengantar) mengikuti bahasa. */
function renderStatic() {
  document.title = t('shell.apiHost.title')
  const heading = document.getElementById('heading')
  if (heading) heading.textContent = t('shell.apiHost.heading')
  const intro = document.getElementById('intro')
  if (intro) intro.textContent = t('shell.apiHost.intro')
}

function render() {
  if (!status) return
  const db = host.server.db()
  const rows: [string, string][] = [
    [t('shell.apiHost.rowApp'), currentApp() ?? t('shell.apiHost.unknownApp')],
    [t('shell.apiHost.rowOrigin'), location.origin],
    [t('shell.apiHost.rowAllowed'), (['console', 'backoffice', 'driver'] as const).map((a) => appOrigin(a)).join(', ')],
    [t('shell.apiHost.rowScenario'), t('shell.apiHost.scenarioValue', { scenario: db.scenario, seeded: db.seededAt })],
    [
      t('shell.apiHost.rowData'),
      [
        t('shell.apiHost.plans', { count: db.plans.length }),
        t('shell.apiHost.users', { count: db.users.length }),
        t('shell.apiHost.drivers', { count: db.drivers.length }),
        t('shell.apiHost.vehicles', { count: db.vehicles.length }),
      ].join(' · '),
    ],
    [t('shell.apiHost.rowClock'), `${db.clock.speed}x`],
    [t('shell.apiHost.rowAutopilot'), db.autopilot ? t('shell.apiHost.autopilotOn') : t('shell.apiHost.autopilotOff')],
    [t('shell.apiHost.rowFault'), db.fault],
    [t('shell.apiHost.rowRoads'), roadsLine()],
  ]
  status.replaceChildren(
    ...rows.flatMap(([k, v]) => {
      const dt = document.createElement('dt')
      dt.textContent = k
      const dd = document.createElement('dd')
      const code = document.createElement('code')
      code.textContent = v
      dd.append(code)
      return [dt, dd]
    }),
  )
}
renderStatic()
render()
subscribeLocale(() => {
  renderStatic()
  render()
})
host.server.subscribe(render)
// jumlah permintaan Mapbox berubah tanpa peristiwa data; segarkan ringan agar status tetap akurat
setInterval(render, 2000)
