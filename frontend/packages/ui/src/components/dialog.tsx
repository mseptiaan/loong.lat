import * as RadixDialog from '@radix-ui/react-dialog'
import { t } from '@loong/i18n'
import { X } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { cn } from '../lib/cn'
import { Button } from './button'
import { Field, Input } from './field'

/**
 * Dialog dan sheet. Tiga jalan keluar selalu ada: tombol tutup, aksi batal, klik overlay (+ Escape).
 * Dialog dipakai untuk keputusan yang memang menginterupsi; tugas biasa jangan dimasukkan ke modal.
 */

const overlay =
  'fixed inset-0 z-[80] bg-fg/35 backdrop-blur-[2px] data-[state=open]:animate-[overlay-in_220ms_var(--ease-out)] data-[state=closed]:animate-[overlay-out_160ms_ease-in]'

export interface DialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  children?: ReactNode
  footer?: ReactNode
  className?: string
}

export function Dialog({ open, onOpenChange, title, description, children, footer, className }: DialogProps) {
  return (
    <RadixDialog.Root open={open} onOpenChange={onOpenChange}>
      <RadixDialog.Portal>
        <RadixDialog.Overlay className={overlay} />
        <RadixDialog.Content
          className={cn(
            'fixed left-1/2 top-1/2 z-[81] flex max-h-[min(88vh,44rem)] w-[calc(100vw-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 flex-col rounded-modal border border-line bg-raised shadow-overlay',
            'data-[state=open]:animate-[dialog-in_260ms_var(--ease-out)] data-[state=closed]:animate-[dialog-out_160ms_ease-in]',
            className,
          )}
        >
          <div className="flex items-start justify-between gap-4 px-6 pt-6">
            <div>
              <RadixDialog.Title className="text-title">{title}</RadixDialog.Title>
              {description ? (
                <RadixDialog.Description className="mt-1 text-body text-muted">{description}</RadixDialog.Description>
              ) : (
                <RadixDialog.Description className="sr-only">{typeof title === 'string' ? title : t('ui.dialog.defaultTitle')}</RadixDialog.Description>
              )}
            </div>
            <RadixDialog.Close
              aria-label={t('ui.dialog.close')}
              className="-mr-2 -mt-1 grid size-9 shrink-0 place-items-center [[data-density=touch]_&]:size-12 rounded-input text-subtle transition-colors hover:bg-sunken hover:text-fg"
            >
              <X className="size-5" aria-hidden />
            </RadixDialog.Close>
          </div>
          {children && <div className="min-h-0 flex-1 overflow-y-auto px-6 py-4">{children}</div>}
          {footer && <div className="flex flex-wrap items-center justify-end gap-2 px-6 pb-6 pt-2">{footer}</div>}
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  )
}

/** Panel samping (desktop) atau dari bawah (HP) untuk formulir yang tidak boleh kehilangan konteks halaman. */
export function Sheet({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  side = 'right',
  className,
}: DialogProps & { side?: 'right' | 'bottom' }) {
  const bottom = side === 'bottom'
  return (
    <RadixDialog.Root open={open} onOpenChange={onOpenChange}>
      <RadixDialog.Portal>
        <RadixDialog.Overlay className={overlay} />
        <RadixDialog.Content
          className={cn(
            'fixed z-[81] flex flex-col bg-raised shadow-overlay outline-none',
            bottom
              ? 'inset-x-0 bottom-0 max-h-[92vh] rounded-t-modal border-t border-line data-[state=open]:animate-[sheet-in-bottom_300ms_var(--ease-out)] data-[state=closed]:animate-[sheet-out-bottom_180ms_ease-in]'
              : 'inset-y-0 right-0 w-full max-w-lg border-l border-line data-[state=open]:animate-[sheet-in-right_300ms_var(--ease-out)] data-[state=closed]:animate-[sheet-out-right_180ms_ease-in]',
            className,
          )}
        >
          <div className="flex items-start justify-between gap-4 border-b border-line px-6 py-4">
            <div>
              <RadixDialog.Title className="text-title">{title}</RadixDialog.Title>
              {description ? (
                <RadixDialog.Description className="mt-0.5 text-small text-muted">{description}</RadixDialog.Description>
              ) : (
                <RadixDialog.Description className="sr-only">{typeof title === 'string' ? title : t('ui.sheet.defaultTitle')}</RadixDialog.Description>
              )}
            </div>
            <RadixDialog.Close
              aria-label={t('ui.dialog.close')}
              className="-mr-2 grid size-9 shrink-0 place-items-center [[data-density=touch]_&]:size-12 rounded-input text-subtle transition-colors hover:bg-sunken hover:text-fg"
            >
              <X className="size-5" aria-hidden />
            </RadixDialog.Close>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto px-6 py-5">{children}</div>
          {footer && <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line px-6 py-4">{footer}</div>}
        </RadixDialog.Content>
      </RadixDialog.Portal>
    </RadixDialog.Root>
  )
}

/**
 * Konfirmasi dengan tombol berupa kata kerja (bukan Ya/Tidak). Untuk aksi tak bisa dibatalkan, `typeToConfirm`
 * mewajibkan mengetik nama sumber daya sebelum tombol aktif.
 */
export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  confirmLabel,
  cancelLabel = t('ui.dialog.cancel'),
  tone = 'default',
  typeToConfirm,
  loading,
  onConfirm,
  children,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: ReactNode
  description?: ReactNode
  confirmLabel: string
  cancelLabel?: string
  tone?: 'default' | 'danger'
  typeToConfirm?: string
  loading?: boolean
  onConfirm: () => void
  children?: ReactNode
}) {
  const [typed, setTyped] = useState('')
  const needsTyping = !!typeToConfirm && typed.trim().toLowerCase() !== typeToConfirm.trim().toLowerCase()
  const close = (next: boolean) => {
    if (!next) setTyped('')
    onOpenChange(next)
  }
  return (
    <Dialog
      open={open}
      onOpenChange={close}
      title={title}
      description={description}
      footer={
        <>
          <Button variant="secondary" onClick={() => close(false)}>
            {cancelLabel}
          </Button>
          <Button
            variant={tone === 'danger' ? 'danger' : 'primary'}
            loading={loading}
            aria-disabled={needsTyping || undefined}
            onClick={() => {
              if (needsTyping) return
              onConfirm()
            }}
          >
            {confirmLabel}
          </Button>
        </>
      }
    >
      {children}
      {typeToConfirm && (
        <Field label={t('ui.dialog.typeToConfirm', { value: typeToConfirm })} className="mt-3">
          <Input value={typed} onChange={(e) => setTyped(e.target.value)} autoComplete="off" />
        </Field>
      )}
    </Dialog>
  )
}
