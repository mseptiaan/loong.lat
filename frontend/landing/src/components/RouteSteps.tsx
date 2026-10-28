import { cn } from '@loong/ui'
import { useEffect, useRef, useState } from 'react'
import { t } from '../i18n'

type Step = { title: string; body: string }

/**
 * Ciri khas halaman: langkah kerja digambar sebagai rute di peta aplikasi. Penanda memakai bahasa bentuk yang sama
 * dengan StopPin (ambil = lingkaran bergaris, antar = kotak membulat terisi), garis rute memakai aksen seperti rute terpilih.
 * Garis digambar sekali saat bagian ini terlihat; dengan prefers-reduced-motion langsung utuh.
 */
export function RouteSteps({ steps }: { steps: Step[] }) {
  const ref = useRef<HTMLOListElement>(null)
  const [drawn, setDrawn] = useState(false)

  useEffect(() => {
    const el = ref.current
    if (!el || typeof IntersectionObserver === 'undefined') return setDrawn(true)
    const io = new IntersectionObserver(
      ([entry]) => {
        if (entry?.isIntersecting) {
          setDrawn(true)
          io.disconnect()
        }
      },
      { threshold: 0.4 },
    )
    io.observe(el)
    return () => io.disconnect()
  }, [])

  return (
    <ol ref={ref} className="relative grid gap-10 md:grid-cols-4 md:gap-8">
      {/* garis rute di layar lebar: dari pusat penanda pertama ke pusat penanda terakhir */}
      <span
        aria-hidden
        className={cn(
          'absolute left-5 right-[calc(25%-2.75rem)] top-[19px] hidden h-[3px] origin-left rounded-full bg-accent md:block',
          'motion-safe:transition-transform motion-safe:duration-[400ms] motion-safe:ease-out',
          drawn ? 'scale-x-100' : 'scale-x-0',
        )}
      />
      {steps.map((step, i) => {
        const n = i + 1
        const pickup = i === 0
        return (
          <li key={step.title} className="relative grid content-start grid-cols-[40px_1fr] gap-x-4 md:grid-cols-1 md:gap-y-5">
            {/* garis rute di ponsel: satu ruas dari penanda ini ke penanda berikutnya */}
            {n < steps.length && (
              <span
                aria-hidden
                className={cn(
                  'absolute left-[18.5px] top-10 h-full w-[3px] origin-top bg-accent md:hidden',
                  'motion-safe:transition-transform motion-safe:duration-[400ms] motion-safe:ease-out',
                  drawn ? 'scale-y-100' : 'scale-y-0',
                )}
              />
            )}
            <span
              role="img"
              aria-label={pickup ? t('land.how.pickup', { n }) : t('land.how.dropoff', { n })}
              className={cn(
                'relative z-10 grid size-10 place-items-center border-[3px] border-fg text-small font-bold tabular-nums shadow-card',
                pickup ? 'rounded-full bg-surface text-fg' : 'rounded-[11px] bg-fg text-white',
              )}
            >
              <span aria-hidden>{n}</span>
            </span>
            <div className="max-w-[34ch]">
              <h3 className="text-title text-fg">{step.title}</h3>
              <p className="mt-1.5 text-muted">{step.body}</p>
            </div>
          </li>
        )
      })}
    </ol>
  )
}
