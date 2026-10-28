import { t } from '@loong/i18n'
import { Command } from 'cmdk'
import { CornerDownLeft, Search } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { cn } from '../lib/cn'
import { Kbd } from './layout'

export interface CommandEntry {
  id: string
  label: string
  /** Keterangan di kanan (mis. "Mencari driver · Rp 46.500"). */
  hint?: string
  icon?: ReactNode
  /** Kata tambahan yang ikut dicocokkan saat mencari (kode, nama pelanggan, lokasi). */
  keywords?: string[]
  onSelect: () => void
}

export interface CommandSection {
  heading: string
  items: CommandEntry[]
  /** Tampilan awal (kolom cari kosong) hanya memuat sekian item teratas; mengetik menyaring seluruh daftar. */
  limitWhenEmpty?: number
}

/**
 * Palet perintah (⌘K): satu kotak untuk lompat ke halaman, membuka plan/user/driver, atau menjalankan aksi cepat.
 * Penyaringan dilakukan di klien oleh cmdk pada daftar yang sudah dimuat; tidak ada permintaan per ketukan.
 */
export function CommandPalette({
  open,
  onOpenChange,
  sections,
  placeholder = t('ui.command.placeholder'),
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  sections: CommandSection[]
  placeholder?: string
}) {
  const [query, setQuery] = useState('')
  const change = (next: boolean) => {
    if (!next) setQuery('')
    onOpenChange(next)
  }
  const searching = query.trim().length > 0
  return (
    <Command.Dialog
      open={open}
      onOpenChange={change}
      label={t('ui.command.label')}
      overlayClassName="fixed inset-0 z-[80] bg-fg/35 backdrop-blur-[2px] data-[state=open]:animate-[overlay-in_220ms_var(--ease-out)]"
      contentClassName={cn(
        'fixed left-1/2 top-[14vh] z-[81] w-[min(38rem,calc(100vw-2rem))] -translate-x-1/2 overflow-hidden rounded-modal border border-line bg-raised shadow-overlay outline-none',
        'data-[state=open]:animate-[dialog-in_220ms_var(--ease-out)]',
      )}
    >
      <div className="flex items-center gap-3 border-b border-line px-4 focus-within:border-accent">
        <Search className="size-4 shrink-0 text-subtle" aria-hidden />
        <Command.Input
          value={query}
          onValueChange={setQuery}
          placeholder={placeholder}
          className="h-13 min-w-0 flex-1 bg-transparent text-body outline-none placeholder:text-subtle"
        />
        <Kbd>Esc</Kbd>
      </div>

      <Command.List className="max-h-[min(26rem,58vh)] overflow-y-auto overscroll-contain p-1.5">
        <Command.Empty className="px-3 py-10 text-center text-body text-muted">{t('ui.command.empty')}</Command.Empty>
        {sections
          .filter((s) => s.items.length > 0)
          .map((section) => ({ ...section, items: !searching && section.limitWhenEmpty ? section.items.slice(0, section.limitWhenEmpty) : section.items }))
          .map((section) => (
            <Command.Group
              key={section.heading}
              heading={section.heading}
              className="[&_[cmdk-group-heading]]:px-3 [&_[cmdk-group-heading]]:pb-1 [&_[cmdk-group-heading]]:pt-2.5 [&_[cmdk-group-heading]]:text-caption [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-subtle"
            >
              {section.items.map((item) => (
                <Command.Item
                  key={item.id}
                  value={item.id}
                  keywords={[item.label, ...(item.hint ? [item.hint] : []), ...(item.keywords ?? [])]}
                  onSelect={() => {
                    change(false)
                    item.onSelect()
                  }}
                  className="flex min-h-10 cursor-pointer items-center gap-3 rounded-input px-3 py-1.5 text-body data-[selected=true]:bg-sunken data-[selected=true]:shadow-[inset_0_0_0_2px_var(--accent)]"
                >
                  {item.icon && <span className="grid size-5 shrink-0 place-items-center text-subtle [&_svg]:size-4">{item.icon}</span>}
                  <span className="min-w-0 flex-1 truncate">{item.label}</span>
                  {item.hint && <span className="max-w-[45%] shrink-0 truncate text-small text-muted">{item.hint}</span>}
                </Command.Item>
              ))}
            </Command.Group>
          ))}
      </Command.List>

      <div className="flex items-center gap-4 border-t border-line px-4 py-2 text-caption text-muted">
        <span className="inline-flex items-center gap-1.5">
          <Kbd>↑</Kbd>
          <Kbd>↓</Kbd> {t('ui.command.select')}
        </span>
        <span className="inline-flex items-center gap-1.5">
          <Kbd>
            <CornerDownLeft className="size-3" aria-hidden />
          </Kbd>{' '}
          {t('ui.command.open')}
        </span>
      </div>
    </Command.Dialog>
  )
}
