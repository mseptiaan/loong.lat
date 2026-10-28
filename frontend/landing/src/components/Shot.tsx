import { getLocale } from '@loong/i18n'
import { cn } from '@loong/ui'
import { ImageOff } from 'lucide-react'
import { useState } from 'react'
import { t } from '../i18n'

export type ShotName = 'options' | 'items' | 'tracking' | 'ai' | 'backoffice' | 'driver'

/** Ukuran asli tangkapan (CSS px, diambil 2x). Dipakai untuk width/height agar tata letak tidak bergeser saat gambar dimuat. */
const SIZE: Record<ShotName, [number, number]> = {
  // dipotong ke isi halaman (tanpa header dan margin samping aplikasi) supaya selalu memenuhi bingkai hero
  options: [1006, 836],
  items: [1440, 900],
  tracking: [1440, 900],
  ai: [1440, 900],
  backoffice: [1440, 900],
  driver: [390, 844],
}

/**
 * Tangkapan layar asli aplikasi (mode demo), dalam bahasa yang sedang aktif. Sumbernya public/shots/<bahasa>/.
 * Bila gagal dimuat, kotak tetap di tempatnya dan menyebutkan isinya lewat teks alternatif.
 */
export function Shot({
  name,
  alt,
  priority = false,
  className,
}: {
  name: ShotName
  alt: string
  priority?: boolean
  className?: string
}) {
  const [failed, setFailed] = useState(false)
  const [w, h] = SIZE[name]
  if (failed) {
    return (
      <div role="img" aria-label={alt} className={cn('grid place-items-center bg-sunken p-6 text-center text-small text-subtle', className)}>
        <span className="flex flex-col items-center gap-2">
          <ImageOff className="size-5" aria-hidden />
          {t('land.shot.missing')}
        </span>
      </div>
    )
  }
  return (
    <img
      src={`/shots/${getLocale()}/${name}.webp`}
      alt={alt}
      width={w}
      height={h}
      loading={priority ? 'eager' : 'lazy'}
      fetchPriority={priority ? 'high' : 'auto'}
      decoding="async"
      onError={() => setFailed(true)}
      className={cn('block bg-sunken', className)}
    />
  )
}
