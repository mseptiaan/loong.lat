import { t } from '@loong/i18n'
import { BRAND } from '@loong/ui'
import { useEffect } from 'react'

/** Judul tab mengikuti halaman: "Rute · loong.lat Driver". Pembaca layar membacanya saat berpindah halaman. */
export function useDocumentTitle(label: string, app: string) {
  useEffect(() => {
    document.title = `${label} · ${BRAND.name} ${app}`
  }, [label, app])
}

/**
 * Tautan pertama di halaman: pengguna keyboard dan pembaca layar melompati navigasi menuju konten.
 * Target: elemen dengan id "utama" (beri `tabIndex={-1}` agar bisa menerima fokus).
 */
export function SkipLink() {
  return (
    <a
      href="#utama"
      className="fixed left-3 top-3 z-[100] -translate-y-24 rounded-input bg-fg px-4 py-2.5 text-body font-semibold text-surface shadow-overlay transition-transform focus:translate-y-0 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
      onClick={(e) => {
        // SPA: pindahkan fokus ke konten (hash saja tidak memindahkan fokus di semua peramban)
        const target = document.getElementById('utama')
        if (target) {
          e.preventDefault()
          target.focus()
          target.scrollIntoView()
        }
      }}
    >
      {t('shell.skipLink')}
    </a>
  )
}
