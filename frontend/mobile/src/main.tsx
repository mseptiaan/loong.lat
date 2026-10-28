import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createBrowserRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import '@fontsource-variable/plus-jakarta-sans'
import './index.css'
import { createApi } from '@loong/http-api'
import { initLocale } from '@loong/i18n'
import { AppRoot } from '@loong/shell'
import { DriverLayout } from './layouts/DriverLayout'
import { RedirectIfAuthed, RequireDriver } from './lib/auth'
import { Earnings } from './pages/Earnings'
import { History } from './pages/History'
import { Home } from './pages/Home'
import { Login } from './pages/Login'
import { NotFound } from './pages/NotFound'
import { Profile } from './pages/Profile'
import { RouteDone } from './pages/RouteDone'
import { RoutePage } from './pages/RoutePage'
import { StopPage } from './pages/StopPage'

// bahasa dipilih sebelum render pertama (bawaan Inggris; lihat packages/i18n/README.md)
initLocale()

// Satu klien API untuk seluruh aplikasi: backend tiruan di browser (bawaan) atau backend asli bila VITE_API_MODE=http.
// Hook window.__api hanya untuk debugging di dev.
const api = createApi('driver')
if (import.meta.env.DEV) (window as unknown as { __api: typeof api }).__api = api

const router = createBrowserRouter([
  {
    path: '/login',
    element: (
      <RedirectIfAuthed>
        <Login />
      </RedirectIfAuthed>
    ),
  },
  {
    element: (
      <RequireDriver>
        <DriverLayout />
      </RequireDriver>
    ),
    children: [
      { index: true, element: <Home /> },
      { path: 'route/:routeId', element: <RoutePage /> },
      { path: 'route/:routeId/stop/:stopId', element: <StopPage /> },
      { path: 'route/:routeId/done', element: <RouteDone /> },
      { path: 'history', element: <History /> },
      { path: 'earnings', element: <Earnings /> },
      { path: 'profile', element: <Profile /> },
    ],
  },
  { path: '*', element: <NotFound /> },
])

// PWA: aplikasi tetap terbuka saat sinyal putus. Hanya di build produksi agar tidak mengganggu HMR saat pengembangan.
if (import.meta.env.PROD && 'serviceWorker' in navigator) {
  window.addEventListener('load', () => {
    navigator.serviceWorker.register('/sw.js').catch(() => undefined)
  })
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppRoot api={api}>
      <RouterProvider router={router} />
    </AppRoot>
  </StrictMode>,
)
