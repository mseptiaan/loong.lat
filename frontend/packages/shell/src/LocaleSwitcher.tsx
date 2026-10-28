import { LOCALES, LOCALE_NAME, LOCALE_SHORT, setLocale, t, type Locale } from '@loong/i18n'
import { useLocale } from '@loong/i18n/react'
import { Segmented } from '@loong/ui'

/**
 * Pengganti bahasa EN | ID. Pilihan disimpan per aplikasi; seluruh layar dirender ulang (lihat I18nBoundary).
 * Setiap pilihan ditulis dalam bahasanya sendiri dan memakai `lang` agar pembaca layar melafalkannya benar.
 */
export function LocaleSwitcher({ className }: { className?: string }) {
  const locale = useLocale()
  return (
    <Segmented<Locale>
      aria-label={t('shell.locale.label')}
      value={locale}
      onValueChange={setLocale}
      className={className}
      options={LOCALES.map((l) => ({
        value: l,
        label: (
          <span lang={l} title={LOCALE_NAME[l]}>
            <span aria-hidden>{LOCALE_SHORT[l]}</span>
            <span className="sr-only">{LOCALE_NAME[l]}</span>
          </span>
        ),
      }))}
    />
  )
}
