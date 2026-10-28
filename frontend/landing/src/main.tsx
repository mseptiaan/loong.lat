import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import '@fontsource-variable/plus-jakarta-sans'
import './index.css'
import { initLocale } from '@loong/i18n'
import { I18nBoundary } from '@loong/i18n/react'
import { Landing } from './Landing'

// bahasa dipilih sebelum render pertama (bawaan Inggris, ?lang=id menetapkan Indonesia; lihat packages/i18n/README.md)
initLocale()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nBoundary>
      <Landing />
    </I18nBoundary>
  </StrictMode>,
)
