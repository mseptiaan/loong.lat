import * as RadixTabs from '@radix-ui/react-tabs'
import type { ComponentProps } from 'react'
import { cn } from '../lib/cn'

export const Tabs = RadixTabs.Root

export function TabsList({ className, ...props }: ComponentProps<typeof RadixTabs.List>) {
  return <RadixTabs.List className={cn('flex gap-1 overflow-x-auto border-b border-line [scrollbar-width:none]', className)} {...props} />
}

export function TabsTrigger({ className, ...props }: ComponentProps<typeof RadixTabs.Trigger>) {
  return (
    <RadixTabs.Trigger
      className={cn(
        'relative inline-flex h-control shrink-0 items-center gap-2 px-3 text-body font-medium text-muted transition-colors hover:text-fg',
        'after:absolute after:inset-x-3 after:bottom-[-1px] after:h-0.5 after:rounded-full after:bg-accent after:opacity-0 after:transition-opacity',
        'data-[state=active]:text-fg data-[state=active]:after:opacity-100',
        className,
      )}
      {...props}
    />
  )
}

export const TabsContent = RadixTabs.Content
