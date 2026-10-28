import { errorMessage, useApi } from '@loong/data'
import { type Item } from '@loong/domain'
import { Button, Field, Notice, Select, Sheet, cn } from '@loong/ui'
import { Camera, Check, ImageUp, Loader2, RotateCcw } from 'lucide-react'
import { useCallback, useEffect, useRef, useState, type ChangeEvent, type DragEvent } from 'react'
import { t } from '../../i18n'
import { DetectedReview } from './DetectedReview'
import { toItems, toRows, type ReviewRow } from './rows'

type Phase =
  | { kind: 'idle'; error?: string }
  | { kind: 'reading'; preview: string; step: number; total: number; labels: string[] }
  | { kind: 'review'; preview: string; rows: ReviewRow[]; summary: string }

const MAX_BYTES = 8 * 1024 * 1024

/**
 * "Foto barang": unggah/seret/ambil foto, lihat tahapan membaca, lalu periksa hasilnya sebelum menambahkannya.
 * AI hanya mengusulkan: tidak ada barang yang masuk sebelum pengguna menekan "Tambahkan".
 */
export function PhotoSheet({
  open,
  onOpenChange,
  shipmentLabels,
  defaultShipment,
  onAdd,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  shipmentLabels: string[]
  defaultShipment: number
  onAdd: (items: Item[], shipmentIndex: number) => void
}) {
  const api = useApi()
  const [phase, setPhase] = useState<Phase>({ kind: 'idle' })
  const [target, setTarget] = useState(String(defaultShipment))
  const [dragging, setDragging] = useState(false)
  const gallery = useRef<HTMLInputElement>(null)
  const camera = useRef<HTMLInputElement>(null)
  const run = useRef(0)

  // lepaskan URL pratinjau saat foto berganti atau sheet ditutup
  const previewUrl = phase.kind === 'idle' ? undefined : phase.preview
  useEffect(() => {
    return () => {
      if (previewUrl) URL.revokeObjectURL(previewUrl)
    }
  }, [previewUrl])

  const reset = useCallback(() => {
    run.current++
    setPhase({ kind: 'idle' })
  }, [])

  async function read(file: File) {
    if (!file.type.startsWith('image/')) return setPhase({ kind: 'idle', error: t('dash.ai.photoErrType') })
    if (file.size > MAX_BYTES) return setPhase({ kind: 'idle', error: t('dash.ai.photoErrSize') })
    const mine = ++run.current
    const preview = URL.createObjectURL(file)
    setPhase({ kind: 'reading', preview, step: 0, total: 4, labels: [] })
    try {
      // data biner ikut dikirim agar kontraknya sama dengan model sungguhan; tiruan tidak membacanya
      const data = await file.arrayBuffer()
      const labels: string[] = []
      for await (const e of api.ai.readImage({ name: file.name, type: file.type, size: file.size, data })) {
        if (run.current !== mine) return
        if (e.type === 'progress') {
          labels.push(e.label)
          setPhase({ kind: 'reading', preview, step: e.step, total: e.total, labels: [...labels] })
        } else {
          setPhase({ kind: 'review', preview, rows: toRows(e.items), summary: e.summary })
        }
      }
    } catch (err) {
      if (run.current === mine) setPhase({ kind: 'idle', error: errorMessage(err) })
    }
  }

  const pick = (files: FileList | null) => {
    const f = files?.[0]
    if (f) void read(f)
  }
  const onFile = (e: ChangeEvent<HTMLInputElement>) => {
    pick(e.target.files)
    e.target.value = '' // permitkan memilih berkas yang sama lagi
  }
  const drop = (e: DragEvent) => {
    e.preventDefault()
    setDragging(false)
    pick(e.dataTransfer.files)
  }

  const included = phase.kind === 'review' ? phase.rows.filter((r) => r.include && r.qty > 0) : []

  return (
    <Sheet
      open={open}
      onOpenChange={(o) => {
        if (!o) reset()
        onOpenChange(o)
      }}
      title={t('dash.ai.photoTitle')}
      description={t('dash.ai.photoDesc')}
      footer={
        phase.kind === 'review' ? (
          <>
            <Button variant="secondary" iconLeft={<RotateCcw className="size-4" aria-hidden />} onClick={reset}>
              {t('dash.ai.photoAnother')}
            </Button>
            <Button
              variant="primary"
              aria-disabled={included.length === 0 || undefined}
              onClick={() => {
                if (included.length === 0) return
                onAdd(toItems(phase.rows, 'foto'), Number(target))
                reset()
                onOpenChange(false)
              }}
              iconLeft={<Check className="size-4" aria-hidden />}
            >
              {included.length > 0 ? t('dash.ai.addItems', { count: included.length }) : t('dash.ai.addItemsNone')}
            </Button>
          </>
        ) : (
          <Button variant="secondary" onClick={() => onOpenChange(false)}>
            {t('dash.ai.manualInstead')}
          </Button>
        )
      }
    >
      <input ref={gallery} type="file" accept="image/*" className="sr-only" tabIndex={-1} onChange={onFile} />
      <input ref={camera} type="file" accept="image/*" capture="environment" className="sr-only" tabIndex={-1} onChange={onFile} />

      {phase.kind === 'idle' && (
        <div className="space-y-4">
          {phase.error && <Notice tone="danger">{phase.error}</Notice>}
          <div
            onDragOver={(e) => {
              e.preventDefault()
              setDragging(true)
            }}
            onDragLeave={() => setDragging(false)}
            onDrop={drop}
            className={cn('grid place-items-center gap-3 rounded-card border-2 border-dashed px-6 py-12 text-center transition-colors', dragging ? 'border-accent bg-accent-tint' : 'border-line-strong bg-sunken/60')}
          >
            <span className="grid size-12 place-items-center rounded-card bg-surface text-muted shadow-card" aria-hidden>
              <ImageUp className="size-6" />
            </span>
            <div>
              <p className="text-title">{t('dash.ai.photoDrag')}</p>
              <p className="mt-0.5 text-small text-muted">{t('dash.ai.photoFormats')}</p>
            </div>
            <div className="flex flex-wrap justify-center gap-2.5">
              <Button variant="primary" iconLeft={<ImageUp className="size-4" aria-hidden />} onClick={() => gallery.current?.click()}>
                {t('dash.ai.photoChoose')}
              </Button>
              <Button variant="secondary" iconLeft={<Camera className="size-4" aria-hidden />} onClick={() => camera.current?.click()}>
                {t('dash.ai.photoTake')}
              </Button>
            </div>
          </div>
          <p className="text-small text-muted">{t('dash.ai.photoTip')}</p>
        </div>
      )}

      {/* pengumuman bagi pembaca layar: tahap membaca dan hasil siap (ikon dan huruf tebal saja tidak terbaca AT) */}
      <p role="status" className="sr-only">
        {phase.kind === 'reading'
          ? t('dash.ai.photoReadingStatus', { step: Math.max(1, phase.step), total: phase.total })
          : phase.kind === 'review'
            ? t('dash.ai.photoReadyStatus', { summary: phase.summary })
            : ''}
      </p>

      {phase.kind === 'reading' && (
        <div className="space-y-5">
          <img src={phase.preview} alt={t('dash.ai.photoReadingAlt')} className="h-52 w-full rounded-card border border-line object-cover" />
          <ol className="space-y-2.5">
            {[t('dash.ai.photoStep1'), t('dash.ai.photoStep2'), t('dash.ai.photoStep3'), t('dash.ai.photoStep4')].map((label, i) => {
              const done = i + 1 < phase.step
              const current = i + 1 === phase.step
              return (
                <li key={label} className={cn('flex items-center gap-3 text-body', !done && !current && 'text-subtle')}>
                  <span className="grid size-5 shrink-0 place-items-center" aria-hidden>
                    {done ? <Check className="size-4 text-accent" /> : current ? <Loader2 className="size-4 animate-spin text-accent" /> : <span className="size-1.5 rounded-full bg-line-strong" />}
                  </span>
                  <span className={cn(current && 'font-semibold')}>{label}</span>
                </li>
              )
            })}
          </ol>
        </div>
      )}

      {phase.kind === 'review' && (
        <div className="space-y-5">
          <div className="flex gap-4">
            <img src={phase.preview} alt={t('dash.ai.photoTitle')} className="size-24 shrink-0 rounded-card border border-line object-cover" />
            <div className="min-w-0">
              <p className="text-title">{t('dash.ai.photoCheckTitle')}</p>
              <p className="mt-0.5 text-body text-muted">{phase.summary}</p>
            </div>
          </div>
          <DetectedReview rows={phase.rows} onChange={(rows) => setPhase({ ...phase, rows })} />
          {shipmentLabels.length > 1 && (
            <Field label={t('dash.ai.addTo')}>
              <Select aria-label={t('dash.ai.addToShipment')} value={target} onValueChange={setTarget} options={shipmentLabels.map((l, i) => ({ value: String(i), label: l }))} />
            </Field>
          )}
        </div>
      )}
    </Sheet>
  )
}
