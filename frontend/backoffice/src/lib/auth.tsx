import { useSession } from '@loong/data'
import type { ReactNode } from 'react'
import { Navigate, useLocation } from 'react-router'

/** Hanya admin yang boleh masuk; yang lain diarahkan ke layar masuk dengan tujuan semula di `?next=`. */
export function RequireAdmin({ children }: { children: ReactNode }) {
  const session = useSession()
  const location = useLocation()
  if (!session || session.role !== 'admin') {
    const next = location.pathname + location.search
    return <Navigate to={`/login${next && next !== '/' ? `?next=${encodeURIComponent(next)}` : ''}`} replace />
  }
  return children
}

/** Layar masuk tidak berguna bila sudah login. */
export function RedirectIfAuthed({ children }: { children: ReactNode }) {
  const session = useSession()
  const search = useLocation().search
  if (session?.role === 'admin') return <Navigate to={safeNext(search)} replace />
  return children
}

/** `?next=` hanya boleh berupa jalur internal (cegah pengalihan terbuka). */
export const safeNext = (search: string): string => {
  const next = new URLSearchParams(search).get('next')
  return next && next.startsWith('/') && !next.startsWith('//') ? next : '/'
}
