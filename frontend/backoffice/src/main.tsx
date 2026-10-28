import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createBrowserRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import '@fontsource-variable/plus-jakarta-sans'
import './index.css'
import { createApi } from '@loong/http-api'
import { initLocale } from '@loong/i18n'
import { AppRoot } from '@loong/shell'
import { AppShell } from './layouts/AppShell'
import { RedirectIfAuthed, RequireAdmin } from './lib/auth'
import { initSettings } from './lib/settings'
import { Analytics } from './pages/Analytics'
import { Balances } from './pages/Balances'
import { Command } from './pages/Command'
import { Drivers } from './pages/Drivers'
import { LivePage } from './pages/LivePage'
import { Login } from './pages/Login'
import { NotFound } from './pages/NotFound'
import { PlanDetail } from './pages/PlanDetail'
import { Plans } from './pages/Plans'
import { Settings } from './pages/Settings'
import { UserDetail } from './pages/UserDetail'
import { Users } from './pages/Users'
import { Vehicles } from './pages/Vehicles'

// bahasa dipilih sebelum render pertama (bawaan Inggris; lihat packages/i18n/README.md)
initLocale()
initSettings()

// Satu klien API untuk seluruh aplikasi: backend tiruan di browser (bawaan) atau backend asli bila VITE_API_MODE=http.
// Hook window.__api hanya untuk debugging di dev.
const api = createApi('backoffice')
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
      <RequireAdmin>
        <AppShell />
      </RequireAdmin>
    ),
    children: [
      { index: true, element: <Command /> },
      { path: 'plan', element: <Plans /> },
      { path: 'plan/:id', element: <PlanDetail /> },
      { path: 'map', element: <LivePage /> },
      { path: 'user', element: <Users /> },
      { path: 'user/:id', element: <UserDetail /> },
      { path: 'balances', element: <Balances /> },
      { path: 'driver', element: <Drivers /> },
      { path: 'vehicles', element: <Vehicles /> },
      { path: 'analytics', element: <Analytics /> },
      { path: 'settings', element: <Settings /> },
      { path: '*', element: <NotFound /> },
    ],
  },
])

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppRoot api={api}>
      <RouterProvider router={router} />
    </AppRoot>
  </StrictMode>,
)
