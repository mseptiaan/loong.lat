import * as RadixPopover from '@radix-ui/react-popover'
import type { ComponentProps, ReactNode } from 'react'
import { cn } from '../lib/cn'

/** Popover ringan: daftar pilihan, notifikasi, pemilih. Pemisah bidang dengan garis tipis + bayangan (menu perlu "lantai" sendiri). */
export const Popover = RadixPopover.Root
export const PopoverTrigger = RadixPopover.Trigger
export const PopoverAnchor = RadixPopover.Anchor

export function PopoverContent({ className, align = 'end', sideOffset = 8, ...props }: ComponentProps<typeof RadixPopover.Content>) {
  return (
    <RadixPopover.Portal>
      <RadixPopover.Content
        align={align}
        sideOffset={sideOffset}
        collisionPadding={12}
        className={cn(
          'z-[90] rounded-card border border-line bg-raised shadow-overlay outline-none',
          'data-[state=open]:animate-[fade-up_180ms_var(--ease-out)]',
          className,
        )}
        {...props}
      />
    </RadixPopover.Portal>
  )
}

export function SimplePopover({ trigger, children, className, align }: { trigger: ReactNode; children: ReactNode; className?: string; align?: 'start' | 'center' | 'end' }) {
  return (
    <Popover>
      <PopoverTrigger asChild>{trigger}</PopoverTrigger>
      <PopoverContent className={className} align={align}>
        {children}
      </PopoverContent>
    </Popover>
  )
}
