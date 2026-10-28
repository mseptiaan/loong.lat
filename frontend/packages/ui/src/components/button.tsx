import { Slot } from '@radix-ui/react-slot'
import { cva, type VariantProps } from 'class-variance-authority'
import { Loader2 } from 'lucide-react'
import type { ComponentProps, ReactNode } from 'react'
import { cn } from '../lib/cn'
import { Tooltip } from './tooltip'

/**
 * Tombol. Satu primer per konteks; sekunder = outline berkontras; ghost untuk toolbar; link bergaris bawah.
 * Hindari `disabled`: tombol tetap aktif dan memvalidasi saat ditekan (forms.md). `loading` mempertahankan label.
 */
export const buttonStyles = cva(
  [
    'relative inline-flex shrink-0 select-none items-center justify-center gap-2 whitespace-nowrap rounded-input font-semibold',
    'transition-[background-color,border-color,color,box-shadow,transform] duration-[140ms] ease-out',
    'active:scale-[0.98] aria-disabled:cursor-not-allowed aria-disabled:opacity-55 aria-busy:cursor-progress',
    'disabled:pointer-events-none disabled:opacity-55',
  ],
  {
    variants: {
      variant: {
        primary: 'bg-accent text-on-accent shadow-press hover:bg-accent-hover',
        secondary: 'border border-line-input bg-surface text-fg hover:bg-sunken',
        ghost: 'text-fg hover:bg-sunken',
        tint: 'bg-accent-tint text-accent hover:bg-accent-tint-strong',
        danger: 'bg-danger-fg text-on-accent shadow-press hover:opacity-90',
        link: 'h-auto min-h-hit rounded-chip px-1 text-accent underline underline-offset-4 hover:text-accent-hover active:scale-100',
      },
      size: {
        sm: 'h-8 px-3 text-small',
        md: 'h-control px-4 text-body',
        lg: 'h-[calc(var(--control-h)+0.25rem)] px-5 text-body',
      },
    },
    defaultVariants: { variant: 'secondary', size: 'md' },
  },
)

export interface ButtonProps extends ComponentProps<'button'>, VariantProps<typeof buttonStyles> {
  loading?: boolean
  asChild?: boolean
  /** Ikon di kiri label (aksi) atau kanan (tujuan/akibat). */
  iconLeft?: ReactNode
  iconRight?: ReactNode
}

export function Button({
  className,
  variant,
  size,
  loading,
  asChild,
  iconLeft,
  iconRight,
  children,
  onClick,
  type = 'button',
  ...props
}: ButtonProps) {
  const Comp = asChild ? Slot : 'button'
  return (
    <Comp
      type={asChild ? undefined : type}
      className={cn(buttonStyles({ variant, size }), className)}
      aria-busy={loading || undefined}
      // saat loading tombol tetap fokus-able (tidak `disabled`), tetapi tidak bisa dipicu dua kali
      onClick={loading ? (e: React.MouseEvent) => e.preventDefault() : onClick}
      {...props}
    >
      {asChild ? (
        children
      ) : (
        <>
          {loading ? <Loader2 className="size-4 animate-spin" aria-hidden /> : iconLeft}
          {children}
          {!loading && iconRight}
        </>
      )}
    </Comp>
  )
}

/** Tombol hanya ikon: `label` wajib (aria-label + tooltip). Target sentuh mengikuti kepadatan. */
export function IconButton({
  label,
  icon,
  className,
  variant = 'ghost',
  tooltip = true,
  ...props
}: Omit<ButtonProps, 'children' | 'iconLeft' | 'iconRight' | 'asChild'> & { label: string; icon: ReactNode; tooltip?: boolean }) {
  const button = (
    <Button variant={variant} aria-label={label} className={cn('size-control px-0', className)} {...props}>
      {icon}
    </Button>
  )
  return tooltip ? <Tooltip content={label}>{button}</Tooltip> : button
}
