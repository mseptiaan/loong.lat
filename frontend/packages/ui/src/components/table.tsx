import type { ComponentProps } from 'react'
import { cn } from '../lib/cn'

/**
 * Primitif tabel untuk data padat: header sticky, angka rata-kanan bertabular, baris dengan hover.
 * Pembungkus `overflow-x: auto` (tabel tak bisa reflow) dan `scrollbar-gutter: stable` agar kolom tidak melompat.
 */
export function TableWrap({ className, children, ...props }: ComponentProps<'div'>) {
  return (
    <div className={cn('overflow-auto [scrollbar-gutter:stable]', className)} {...props}>
      <table className="w-full border-separate border-spacing-0 text-body">{children}</table>
    </div>
  )
}

export const THead = (props: ComponentProps<'thead'>) => <thead {...props} />
export const TBody = (props: ComponentProps<'tbody'>) => <tbody {...props} />

export function TR({ className, interactive, ...props }: ComponentProps<'tr'> & { interactive?: boolean }) {
  return (
    <tr
      className={cn(
        'group transition-colors',
        interactive && 'cursor-pointer hover:bg-sunken/70 focus-visible:bg-sunken/70 data-[active=true]:bg-accent-tint',
        className,
      )}
      {...props}
    />
  )
}

export function TH({ className, numeric, ...props }: ComponentProps<'th'> & { numeric?: boolean }) {
  return (
    <th
      scope="col"
      className={cn(
        'sticky top-0 z-10 whitespace-nowrap border-b border-line bg-surface px-3 py-2.5 text-left text-small font-medium text-muted',
        numeric && 'text-right',
        className,
      )}
      {...props}
    />
  )
}

export function TD({ className, numeric, ...props }: ComponentProps<'td'> & { numeric?: boolean }) {
  return (
    <td
      className={cn('h-row border-b border-line px-3 align-middle', numeric && 'text-right tabular-nums', className)}
      {...props}
    />
  )
}
