import * as RadixMenu from '@radix-ui/react-dropdown-menu'
import type { ComponentProps, ReactNode } from 'react'
import { cn } from '../lib/cn'

/** Menu aksi (<= 5 butir, mis. menu akun). Untuk pilihan data pakai Segmented/Select. */
export function Menu({ trigger, children, align = 'end', className }: { trigger: ReactNode; children: ReactNode; align?: 'start' | 'center' | 'end'; className?: string }) {
  return (
    <RadixMenu.Root>
      <RadixMenu.Trigger asChild>{trigger}</RadixMenu.Trigger>
      <RadixMenu.Portal>
        <RadixMenu.Content
          align={align}
          sideOffset={8}
          collisionPadding={12}
          className={cn(
            'z-[90] min-w-52 rounded-card border border-line bg-raised p-1.5 shadow-overlay',
            'data-[state=open]:animate-[fade-up_180ms_var(--ease-out)]',
            className,
          )}
        >
          {children}
        </RadixMenu.Content>
      </RadixMenu.Portal>
    </RadixMenu.Root>
  )
}

export function MenuItem({
  icon,
  danger,
  className,
  children,
  ...props
}: ComponentProps<typeof RadixMenu.Item> & { icon?: ReactNode; danger?: boolean }) {
  return (
    <RadixMenu.Item
      className={cn(
        'flex min-h-hit cursor-pointer select-none items-center gap-2.5 rounded-input px-3 py-2 text-body outline-none transition-colors data-[highlighted]:bg-sunken data-[highlighted]:shadow-[inset_0_0_0_2px_var(--accent)]',
        danger && 'text-danger-fg',
        '[&_svg]:size-4 [&_svg]:shrink-0',
        className,
      )}
      {...props}
    >
      {icon}
      {children}
    </RadixMenu.Item>
  )
}

export const MenuLabel = ({ className, ...props }: ComponentProps<typeof RadixMenu.Label>) => (
  <RadixMenu.Label className={cn('px-3 py-2 text-small text-muted', className)} {...props} />
)

export const MenuSeparator = ({ className, ...props }: ComponentProps<typeof RadixMenu.Separator>) => (
  <RadixMenu.Separator className={cn('my-1 h-px bg-line', className)} {...props} />
)
