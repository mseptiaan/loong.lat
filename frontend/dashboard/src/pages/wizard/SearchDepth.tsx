import { EFFORTS, type Effort } from '@loong/domain'
import { Button, cn } from '@loong/ui'
import { Check, RefreshCw, Sparkles, Timer } from 'lucide-react'
import { useId } from 'react'
import { t } from '../../i18n'
import { effortText, needsSearch, type SearchPick } from './search'

/**
 * Seberapa keras mesin mencari opsi: tiga pilihan yang semuanya terlihat (kartu radio, panah kiri/kanan berpindah). Mengganti
 * pilihan tidak menghitung apa pun; baru "Cari lagi" yang menjalankannya dan menggantikan opsi di bawah.
 */
export function SearchDepth({
  pick,
  aiOn,
  onChoose,
  onSearch,
}: {
  pick: SearchPick
  /** Bendera AI di Pengaturan: bila mati, "reviewed" tetap ditawarkan tetapi keterangannya menyebut peninjau bawaan. */
  aiOn: boolean
  onChoose: (effort: Effort) => void
  onSearch: () => void
}) {
  const id = useId()
  return (
    <fieldset className="min-w-0 space-y-3">
      <legend className="text-body font-semibold">{t('dash.wizard.depth.legend')}</legend>
      <p className="-mt-1 text-small text-muted">{t('dash.wizard.depth.hint')}</p>
      <div className="grid gap-2.5 sm:grid-cols-3">
        {EFFORTS.map((effort) => {
          const text = effortText(effort, aiOn)
          const selected = pick.chosen === effort
          const base = `${id}-${effort}`
          return (
            <label
              key={effort}
              className={cn(
                'relative flex cursor-pointer flex-col gap-1 rounded-card border bg-surface p-3.5 transition-[border-color,background-color] duration-[140ms]',
                'has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-offset-2 has-[:focus-visible]:outline-accent',
                selected ? 'border-accent bg-accent-tint' : 'border-line hover:border-line-strong',
              )}
            >
              <input
                type="radio"
                name={`${id}-depth`}
                className="sr-only"
                checked={selected}
                onChange={() => onChoose(effort)}
                aria-labelledby={`${base}-label`}
                aria-describedby={`${base}-caption ${base}-time`}
              />
              <span className="flex items-start justify-between gap-2">
                <span id={`${base}-label`} className="text-body font-semibold">
                  {text.label}
                  {effort === 'reviewed' && aiOn && <Sparkles className="ml-1.5 inline size-3.5 -translate-y-px text-accent" aria-hidden />}
                </span>
                <span
                  aria-hidden
                  className={cn('mt-0.5 grid size-5 shrink-0 place-items-center rounded-full border transition-colors', selected ? 'border-accent bg-accent text-on-accent' : 'border-line-input bg-surface')}
                >
                  {selected && <Check className="size-3.5" />}
                </span>
              </span>
              <span id={`${base}-caption`} className="block text-small text-muted">
                {text.caption}
              </span>
              <span id={`${base}-time`} className="mt-auto flex items-center gap-1.5 pt-1.5 text-small text-muted">
                <Timer className="size-3.5 shrink-0" aria-hidden />
                {text.time}
              </span>
            </label>
          )
        })}
      </div>
      {/* selalu ada di pohon agar kemunculan tombol diumumkan pembaca layar */}
      <div aria-live="polite">
        {needsSearch(pick) && (
          <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
            <Button variant="primary" iconLeft={<RefreshCw className="size-4" aria-hidden />} onClick={onSearch}>
              {t('dash.wizard.depth.searchAgain')}
            </Button>
            <p className="min-w-0 text-small text-muted">{t('dash.wizard.depth.searchAgainHint')}</p>
          </div>
        )}
      </div>
    </fieldset>
  )
}
