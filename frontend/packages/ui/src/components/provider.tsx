import type { ReactNode } from 'react'
import { Toaster } from './toast'
import { TooltipProvider } from './tooltip'

/** Membungkus aplikasi dengan penyedia tooltip dan toast. Pasang sekali di akar. */
export function UIProvider({ children, toastPosition }: { children: ReactNode; toastPosition?: 'top-center' | 'bottom-center' | 'top-right' }) {
  return (
    <TooltipProvider>
      {children}
      <Toaster position={toastPosition} />
    </TooltipProvider>
  )
}
