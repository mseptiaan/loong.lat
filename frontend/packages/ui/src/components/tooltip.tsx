import * as RadixTooltip from '@radix-ui/react-tooltip'
import type { ReactNode } from 'react'

export const TooltipProvider = ({ children }: { children: ReactNode }) => (
  <RadixTooltip.Provider delayDuration={350} skipDelayDuration={200}>
    {children}
  </RadixTooltip.Provider>
)

/** Tooltip singkat untuk tombol ikon. Isi penting jangan hanya di tooltip (tidak ada di layar sentuh). */
export function Tooltip({ content, children, side = 'top' }: { content: ReactNode; children: ReactNode; side?: 'top' | 'bottom' | 'left' | 'right' }) {
  return (
    <RadixTooltip.Root>
      <RadixTooltip.Trigger asChild>{children}</RadixTooltip.Trigger>
      <RadixTooltip.Portal>
        <RadixTooltip.Content
          side={side}
          sideOffset={6}
          className="z-[100] max-w-xs rounded-chip bg-fg px-2.5 py-1.5 text-caption text-white shadow-overlay data-[state=delayed-open]:animate-[tip-in_140ms_var(--ease-out)]"
        >
          {content}
        </RadixTooltip.Content>
      </RadixTooltip.Portal>
    </RadixTooltip.Root>
  )
}
