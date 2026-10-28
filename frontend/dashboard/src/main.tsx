import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createBrowserRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import '@fontsource-variable/plus-jakarta-sans'
import './index.css'
import { createApi } from '@loong/http-api'
import { initLocale } from '@loong/i18n'
import { AppRoot } from '@loong/shell'
import { KitPage } from './dev/KitPage'
import { AppLayout } from './layouts/AppLayout'
import { AuthLayout } from './layouts/AuthLayout'
import { RequireAuth } from './lib/auth'
import { Forgot } from './pages/auth/Forgot'
import { Login } from './pages/auth/Login'
import { Register } from './pages/auth/Register'
import { Account } from './pages/Account'
import { Addresses } from './pages/Addresses'
import { Home } from './pages/Home'
import { NotFound } from './pages/NotFound'
import { PlanDetail } from './pages/plans/PlanDetail'
import { PlanList } from './pages/plans/PlanList'
import { TopUpPage } from './pages/wallet/TopUpPage'
import { TopUpPayment } from './pages/wallet/TopUpPayment'
import { WalletPage } from './pages/wallet/WalletPage'
import { NewPlan } from './pages/wizard/NewPlan'

// bahasa dipilih sebelum render pertama (bawaan Inggris; lihat packages/i18n/README.md)
initLocale()

// Satu klien API untuk seluruh aplikasi: backend tiruan di browser (bawaan) atau backend asli bila VITE_API_MODE=http.
// Hook window.__api hanya untuk debugging di dev.
const api = createApi('console')
if (import.meta.env.DEV) (window as unknown as { __api: typeof api }).__api = api

const router = createBrowserRouter([
  {
    element: <AuthLayout />,
    children: [
      { path: '/login', element: <Login /> },
      { path: '/register', element: <Register /> },
      { path: '/forgot-password', element: <Forgot /> },
    ],
  },
  {
    element: (
      <RequireAuth>
        <AppLayout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Home /> },
      { path: 'plan', element: <PlanList /> },
      { path: 'plan/:id', element: <PlanDetail /> },
      { path: 'addresses', element: <Addresses /> },
      { path: 'wallet', element: <WalletPage /> },
      { path: 'wallet/top-up', element: <TopUpPage /> },
      { path: 'wallet/top-up/:id', element: <TopUpPayment /> },
      { path: 'account', element: <Account /> },
    ],
  },
  {
    // wizard: tampilan fokus tanpa navigasi utama
    path: '/plan/new',
    element: (
      <RequireAuth>
        <NewPlan />
      </RequireAuth>
    ),
  },
  ...(import.meta.env.DEV ? [{ path: '/_kit', element: <KitPage /> }] : []),
  { path: '*', element: <NotFound /> },
])

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppRoot api={api}>
      <RouterProvider router={router} />
    </AppRoot>
  </StrictMode>,
)
