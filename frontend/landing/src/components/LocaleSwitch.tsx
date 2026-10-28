import { LOCALES, LOCALE_NAME, LOCALE_SHORT, setLocale, type Locale } from '@loong/i18n'
import { useLocale } from '@loong/i18n/react'
import { Segmented } from '@loong/ui'
import { t } from '../i18n'

/**
 * Pengganti bahasa EN | ID. Sama dengan LocaleSwitcher di @loong/shell, ditulis ulang di sini supaya halaman depan
 * tidak ikut memuat kerangka aplikasi (klien API, data, notifikasi).
 */
export function LocaleSwitch({ className }: { className?: string }) {
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
