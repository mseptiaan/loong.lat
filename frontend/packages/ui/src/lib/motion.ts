/** Gulir halus kecuali pengguna meminta gerak dikurangi (prefers-reduced-motion). */
export const scrollBehavior = (): ScrollBehavior =>
  typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth'
