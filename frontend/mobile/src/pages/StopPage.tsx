import { errorField, errorMessage, qk, useApi, useDriverRoutes, useMutation, useQueryClient } from '@loong/data'
import { ISSUE_LABEL, formatPhone, formatTime, itemLabel, type DriverRoute, type IssueKind, type Plan, type RouteStop, type StopProof } from '@loong/domain'
import { Button, Card, EmptyState, ErrorState, Field, IconButton, Input, Menu, MenuItem, Notice, Sheet, Skeleton, StopPin, StopStatusLabel, Textarea, cn, scrollBehavior, toast } from '@loong/ui'
import { ArrowLeft, Camera, ChevronDown, Compass, ExternalLink, MessageCircle, Navigation, Phone, TriangleAlert } from 'lucide-react'
import { renderMsg } from '@loong/i18n'
import { useEffect, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router'
import { SwipeToConfirm } from '../components/SwipeToConfirm'
import { t, tr } from '../i18n'
import { googleMapsUrl, openExternal, telUrl, wazeUrl, whatsappUrl } from '../lib/links'
import { compressPhoto } from '../lib/image'
import { routeDonePath, routePath } from '../lib/paths'

const demoEnabled = () => {
  try {
    return localStorage.getItem('loong:demo') === '1'
  } catch {
    return false
  }
}

const SAMPLE_PHOTO_NAME = 'bukti-penerima-contoh.jpg'

type Photo = { name: string; url: string; file: Blob }

function PhotoPicker({ photo, onPhoto, label }: { photo: Photo | null; onPhoto: (p: Photo | null) => void; label: string }) {
  const input = useRef<HTMLInputElement>(null)
  // lepaskan URL pratinjau saat foto berganti atau halaman ditutup
  useEffect(() => {
    const url = photo?.url
    return () => {
      if (url) URL.revokeObjectURL(url)
    }
  }, [photo])

  return (
    <div className="flex items-center gap-3">
      <input
        ref={input}
        type="file"
        accept="image/*"
        capture="environment"
        className="sr-only"
        tabIndex={-1}
        onChange={(e) => {
          const file = e.target.files?.[0]
          if (file) onPhoto({ name: file.name, url: URL.createObjectURL(file), file })
          e.target.value = ''
        }}
      />
      {photo ? (
        <>
          <img src={photo.url} alt={t('drv.stop.photoPreview')} className="size-16 shrink-0 rounded-input border border-line object-cover" />
          <div className="min-w-0 flex-1">
            <p className="truncate text-body font-semibold">{photo.name}</p>
            <Button variant="link" className="text-small" onClick={() => onPhoto(null)}>
              {t('drv.stop.photoRemove')}
            </Button>
          </div>
        </>
      ) : (
        <Button variant="secondary" className="w-full" iconLeft={<Camera className="size-4" aria-hidden />} onClick={() => input.current?.click()}>
          {label}
        </Button>
      )}
    </div>
  )
}

/** Bagian "bukti": OTP penerima (wajib untuk antar) atau foto; pengambilan cukup foto opsional. */
function ProofCard({
  stop,
  otp,
  onOtp,
  photo,
  onPhoto,
  photoMode,
  onPhotoMode,
  error,
}: {
  stop: RouteStop
  otp: string
  onOtp: (v: string) => void
  photo: Photo | null
  onPhoto: (p: Photo | null) => void
  photoMode: boolean
  onPhotoMode: (v: boolean) => void
  error?: string
}) {
  const [demo] = useState(demoEnabled)
  /** Demo: foto contoh penerima (berkas statis), supaya ringkasan AI di console cocok dengan isi fotonya. */
  async function attachSamplePhoto() {
    try {
      const res = await fetch(`${import.meta.env.BASE_URL}demo/${SAMPLE_PHOTO_NAME}`)
      if (!res.ok) throw new Error(String(res.status))
      const file = await res.blob()
      onPhoto({ name: SAMPLE_PHOTO_NAME, url: URL.createObjectURL(file), file })
    } catch {
      toast.error(t('drv.stop.photoReadError'))
    }
  }
  if (stop.kind === 'ambil') {
    return (
      <Card className="space-y-3 p-card">
        <h2 className="text-title">{t('drv.stop.pickupPhotoTitle')}</h2>
        <p className="text-small text-muted">{t('drv.stop.pickupPhotoHint')}</p>
        <PhotoPicker photo={photo} onPhoto={onPhoto} label={t('drv.stop.pickupPhotoButton')} />
      </Card>
    )
  }
  return (
    <Card className="space-y-4 p-card">
      <h2 className="text-title">{t('drv.stop.proofTitle')}</h2>
      {photoMode ? (
        <Field label={t('drv.stop.proofPhotoLabel')} hint={t('drv.stop.proofPhotoHint')} error={error}>
          <PhotoPicker photo={photo} onPhoto={onPhoto} label={t('drv.stop.proofPhotoButton')} />
        </Field>
      ) : (
        <Field label={t('drv.stop.otpLabel')} hint={t('drv.stop.otpHint')} error={error}>
          <Input
            inputMode="numeric"
            pattern="[0-9]*"
            maxLength={4}
            autoComplete="one-time-code"
            placeholder="0000"
            value={otp}
            onChange={(e) => onOtp(e.target.value.replace(/\D/g, ''))}
            className="text-center text-title tracking-[0.5em]"
          />
        </Field>
      )}
      {demo && stop.deliveryCode && !photoMode && (
        <button type="button" onClick={() => onOtp(stop.deliveryCode!)} className="w-full rounded-input border border-dashed border-line-strong p-2.5 text-left text-small text-muted hover:bg-sunken">
          {tr('drv.stop.demoFill', { code: stop.deliveryCode, strong: (c) => <span className="font-semibold text-fg">{c}</span> })}
        </button>
      )}
      {demo && photoMode && !photo && (
        <button type="button" onClick={() => void attachSamplePhoto()} className="w-full rounded-input border border-dashed border-line-strong p-2.5 text-left text-small text-muted hover:bg-sunken">
          {tr('drv.stop.demoSamplePhoto', { strong: (c) => <span className="font-semibold text-fg">{c}</span> })}
        </button>
      )}
      <Button variant="link" className="text-small" onClick={() => onPhotoMode(!photoMode)}>
        {photoMode ? t('drv.stop.useOtp') : t('drv.stop.usePhoto')}
      </Button>
    </Card>
  )
}

function IssueSheet({
  open,
  onOpenChange,
  onSubmit,
  pending,
  error,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSubmit: (kind: IssueKind, note: string) => void
  pending: boolean
  error?: string
}) {
  const [kind, setKind] = useState<IssueKind | null>(null)
  const [note, setNote] = useState('')
  const [attempted, setAttempted] = useState(false)
  const kindError = attempted && !kind ? t('drv.stop.issueKindError') : undefined
  const noteError = attempted && kind === 'lainnya' && !note.trim() ? t('drv.stop.issueNoteError') : undefined

  function submit() {
    setAttempted(true)
    if (!kind || (kind === 'lainnya' && !note.trim())) return
    onSubmit(kind, note)
  }

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      side="bottom"
      title={t('drv.stop.issueTitle')}
      description={t('drv.stop.issueHint')}
      footer={
        <Button variant="primary" size="lg" className="w-full" loading={pending} onClick={submit}>
          {t('drv.stop.issueSend')}
        </Button>
      }
    >
      <div className="space-y-4">
        {error && <Notice tone="danger">{error}</Notice>}
        <fieldset className="space-y-2">
          <legend className="mb-1 text-small font-semibold">{t('drv.stop.issueKind')}</legend>
          {(Object.keys(ISSUE_LABEL) as IssueKind[]).map((k) => (
            <label
              key={k}
              className={cn(
                'flex min-h-14 cursor-pointer items-center gap-3 rounded-input border border-line-input bg-surface px-3.5 text-body transition-colors',
                'has-[:checked]:border-accent has-[:checked]:bg-accent-tint has-[:focus-visible]:outline-2 has-[:focus-visible]:outline-accent',
              )}
            >
              <input type="radio" name="jenis-masalah" value={k} checked={kind === k} onChange={() => setKind(k)} className="size-5 accent-[var(--accent)]" aria-invalid={kindError ? true : undefined} />
              {ISSUE_LABEL[k]}
            </label>
          ))}
          {kindError && (
            <p role="alert" className="text-small text-danger-fg">
              {kindError}
            </p>
          )}
        </fieldset>
        <Field label={t('drv.stop.issueNote')} optional={kind !== 'lainnya'} error={noteError}>
          <Textarea value={note} onChange={(e) => setNote(e.target.value)} placeholder={t('drv.stop.issueNotePlaceholder')} />
        </Field>
      </div>
    </Sheet>
  )
}

export function StopPage() {
  const { routeId, stopId } = useParams()
  const api = useApi()
  const qc = useQueryClient()
  const navigate = useNavigate()
  const routes = useDriverRoutes()
  const item: DriverRoute | undefined = routes.data?.find((r) => r.route.id === routeId)
  const stop = item?.route.stops.find((s) => s.id === stopId)

  const [otp, setOtp] = useState('')
  const [photo, setPhoto] = useState<Photo | null>(null)
  const [photoMode, setPhotoMode] = useState(false)
  const [proofError, setProofError] = useState<string>()
  const [issueOpen, setIssueOpen] = useState(false)
  const [issueError, setIssueError] = useState<string>()
  const proofRef = useRef<HTMLDivElement>(null)

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: qk.driverRoutes })
    void qc.invalidateQueries({ queryKey: qk.driverMe })
    void qc.invalidateQueries({ queryKey: qk.driverHistory })
    void qc.invalidateQueries({ queryKey: qk.driverEarnings })
  }

  /** Setelah stop ditutup: rute selesai -> ringkasan; kalau tidak, kembali ke rute dan sebut tujuan berikutnya. */
  function afterClose(plan: Plan, message: string) {
    refresh()
    const route = plan.routes.find((r) => r.id === routeId)
    if (!route || route.status === 'selesai') {
      navigate(routeDonePath(routeId!), { replace: true })
      return
    }
    const next = route.stops.find((s) => s.status === 'menuju')
    toast.success(next ? t('drv.stop.doneNext', { message, name: next.waypoint.name }) : message)
    navigate(routePath(routeId!), { replace: true })
  }

  const arrive = useMutation({
    mutationFn: () => api.driver.arrive(item!.planId, item!.route.id, stop!.id),
    onSuccess: () => {
      refresh()
      toast.success(t('drv.app.toastArrived'))
    },
    onError: (err) => toast.error(errorMessage(err)),
  })

  const complete = useMutation({
    mutationFn: (proof: StopProof) => api.driver.complete(item!.planId, item!.route.id, stop!.id, proof),
    onSuccess: (plan) => afterClose(plan, stop?.kind === 'ambil' ? t('drv.stop.donePickup') : t('drv.stop.doneDropoff')),
    onError: (err) => {
      if (errorField(err) === 'proof') {
        setProofError(errorMessage(err))
        proofRef.current?.scrollIntoView({ block: 'center', behavior: scrollBehavior() })
      } else toast.error(errorMessage(err))
    },
  })

  const report = useMutation({
    mutationFn: ({ kind, note }: { kind: IssueKind; note: string }) => api.driver.reportIssue(item!.planId, item!.route.id, stop!.id, kind, note),
    onSuccess: (plan) => {
      setIssueOpen(false)
      afterClose(plan, t('drv.stop.issueSent'))
    },
    // galat ditampilkan di dalam sheet: toast berada di luar modal dan tidak diumumkan pembaca layar saat modal terbuka
    onError: (err) => setIssueError(errorMessage(err)),
  })

  if (routes.isLoading) {
    return (
      <div className="space-y-4 p-4">
        <Skeleton className="h-10 w-48" />
        <Skeleton className="h-44 rounded-card" />
        <Skeleton className="h-36 rounded-card" />
      </div>
    )
  }
  if (routes.isError) {
    return (
      <div className="p-4">
        <Card>
          <ErrorState message={errorMessage(routes.error)} onRetry={() => void routes.refetch()} />
        </Card>
      </div>
    )
  }
  if (!item || !stop) {
    return (
      <main className="p-4">
        <EmptyState
          icon={<Compass />}
          title={t('drv.stop.notFoundTitle')}
          description={t('drv.stop.notFoundHint')}
          action={
            <Button variant="primary" size="lg" onClick={() => navigate('/')}>
              {t('drv.app.backHome')}
            </Button>
          }
        />
      </main>
    )
  }

  const { route } = item
  const activeIdx = route.stops.findIndex((s) => s.status !== 'selesai' && s.status !== 'bermasalah')
  const isActive = route.status === 'berjalan' && route.stops[activeIdx]?.id === stop.id
  const closed = stop.status === 'selesai' || stop.status === 'bermasalah'

  /** Validasi di klien dulu supaya kesalahan umum langsung terlihat tanpa menunggu server. */
  async function finish() {
    setProofError(undefined)
    let proof: StopProof = { kind: 'tanpa' }
    if (stop!.kind === 'antar') {
      if (photoMode) {
        if (!photo) {
          setProofError(t('drv.stop.proofNeedPhoto'))
          proofRef.current?.scrollIntoView({ block: 'center', behavior: scrollBehavior() })
          throw new Error('incomplete proof')
        }
        try {
          proof = { kind: 'foto', value: photo.name, photo: await compressPhoto(photo.file) }
        } catch {
          setProofError(t('drv.stop.photoReadError'))
          proofRef.current?.scrollIntoView({ block: 'center', behavior: scrollBehavior() })
          throw new Error('unreadable proof photo')
        }
      } else {
        if (!/^\d{4}$/.test(otp)) {
          setProofError(t('drv.stop.proofNeedOtp'))
          proofRef.current?.scrollIntoView({ block: 'center', behavior: scrollBehavior() })
          throw new Error('incomplete proof')
        }
        proof = { kind: 'otp', value: otp }
      }
    } else if (photo) {
      proof = { kind: 'foto', value: photo.name }
    }
    await complete.mutateAsync(proof)
  }

  const target = stop.waypoint.position

  return (
    <div className="min-h-dvh pb-40">
      <header className="sticky top-0 z-30 border-b border-line bg-bg/95 backdrop-blur-md">
        <div className="flex h-16 items-center gap-2 px-2">
          <IconButton label={t('drv.stop.back')} icon={<ArrowLeft className="size-5" />} onClick={() => navigate(routePath(route.id))} />
          <div className="min-w-0 flex-1">
            <h1 className="truncate text-title">{t('drv.stop.heading', { seq: stop.seq, total: route.stops.length })}</h1>
            <p className="truncate text-small text-muted">{item.planCode}</p>
          </div>
          <StopStatusLabel status={stop.status} className="pr-3 text-small" />
        </div>
      </header>

      <main className="space-y-4 px-4 pt-4">
        {!isActive && !closed && (
          <Notice tone="info">
            {route.status !== 'berjalan' ? t('drv.stop.notStartedNotice') : t('drv.stop.notActiveNotice', { seq: route.stops[activeIdx]?.seq ?? '' })}
          </Notice>
        )}

        <Card className="space-y-4 p-card">
          <div className="flex items-start gap-3">
            <StopPin index={stop.seq} kind={stop.kind} state={isActive ? 'active' : 'default'} className="mt-0.5 shrink-0" />
            <div className="min-w-0 flex-1">
              <p className="text-small font-semibold text-muted">
                {stop.kind === 'ambil' ? t('drv.stop.scheduledPickup', { time: stop.arriveAt }) : t('drv.stop.scheduledDropoff', { time: stop.arriveAt })}
              </p>
              <h2 className="text-title">{stop.waypoint.name}</h2>
              <p className="text-body text-muted">{stop.waypoint.address}</p>
              {stop.waypoint.note && <p className="mt-1 text-small text-subtle">{t('drv.app.landmark', { note: stop.waypoint.note })}</p>}
            </div>
          </div>

          <Menu
            align="start"
            trigger={
              <Button variant="secondary" className="w-full" iconLeft={<Navigation className="size-4" aria-hidden />} iconRight={<ChevronDown className="size-4" aria-hidden />}>
                {t('drv.stop.openNavigation')}
              </Button>
            }
          >
            <MenuItem icon={<ExternalLink />} onSelect={() => openExternal(googleMapsUrl(target, route.vehicleType))}>
              Google Maps
            </MenuItem>
            <MenuItem icon={<ExternalLink />} onSelect={() => openExternal(wazeUrl(target))}>
              Waze
            </MenuItem>
          </Menu>

          <div className="border-t border-line pt-4">
            <p className="text-small text-muted">{stop.kind === 'ambil' ? t('drv.stop.senderContact') : t('drv.stop.recipientContact')}</p>
            <p className="text-body font-semibold">{stop.waypoint.contactName}</p>
            <p className="text-small tabular-nums text-muted">{formatPhone(stop.waypoint.contactPhone)}</p>
            <div className="mt-3 grid grid-cols-2 gap-2.5">
              <Button variant="secondary" iconLeft={<Phone className="size-4" aria-hidden />} asChild>
                <a href={telUrl(stop.waypoint.contactPhone)}>{t('drv.stop.call')}</a>
              </Button>
              <Button
                variant="secondary"
                iconLeft={<MessageCircle className="size-4" aria-hidden />}
                asChild
              >
                <a
                  href={whatsappUrl(stop.waypoint.contactPhone, t('drv.stop.whatsappMessage', { contact: stop.waypoint.contactName, code: item.planCode, place: stop.waypoint.name }))}
                  target="_blank"
                  rel="noopener noreferrer"
                >
                  WhatsApp
                </a>
              </Button>
            </div>
          </div>
        </Card>

        <Card className="p-card">
          <h2 className="text-title">{stop.kind === 'ambil' ? t('drv.stop.itemsPickup') : t('drv.stop.itemsDropoff')}</h2>
          {stop.kind === 'ambil' && <p className="mt-0.5 text-small text-muted">{t('drv.stop.itemsCheck')}</p>}
          {stop.items?.length ? (
            <ul className="mt-3 divide-y divide-line">
              {stop.items.map((it, i) => (
                <li key={i} className="py-2.5 first:pt-0 last:pb-0">
                  <p className="text-body font-semibold">
                    {it.qty} × {itemLabel(it)}
                  </p>
                  <p className="text-small tabular-nums text-muted">{renderMsg(it.detail)}</p>
                  {it.fragile && (
                    <p className="mt-1 flex items-center gap-1.5 text-small font-medium text-warning-fg">
                      <TriangleAlert className="size-3.5" aria-hidden /> {t('drv.stop.fragile')}
                    </p>
                  )}
                </li>
              ))}
            </ul>
          ) : (
            <p className="mt-2 text-body text-muted">{renderMsg(stop.itemsSummary)}</p>
          )}
        </Card>

        {isActive && stop.status === 'tiba' && (
          <div ref={proofRef}>
            <ProofCard stop={stop} otp={otp} onOtp={(v) => { setOtp(v); setProofError(undefined) }} photo={photo} onPhoto={(p) => { setPhoto(p); setProofError(undefined) }} photoMode={photoMode} onPhotoMode={setPhotoMode} error={proofError} />
          </div>
        )}

        {closed && (
          <Notice tone={stop.status === 'selesai' ? 'success' : 'danger'} title={stop.status === 'selesai' ? t('drv.stop.closedDoneTitle') : t('drv.stop.closedIssueTitle')}>
            {stop.status === 'selesai' ? t('drv.stop.closedDoneAt', { time: stop.completedAt ? formatTime(stop.completedAt) : '—' }) : (stop.issue ? renderMsg(stop.issue) : t('drv.stop.closedIssueFallback'))}
          </Notice>
        )}

        {isActive && (
          <Button variant="ghost" className="w-full text-danger-fg" iconLeft={<TriangleAlert className="size-4" aria-hidden />} onClick={() => { setIssueError(undefined); setIssueOpen(true) }}>
            {t('drv.stop.reportIssue')}
          </Button>
        )}
      </main>

      <IssueSheet open={issueOpen} onOpenChange={setIssueOpen} onSubmit={(kind, note) => report.mutate({ kind, note })} pending={report.isPending} error={issueError} />

      <div className="fixed inset-x-0 bottom-0 z-40 mx-auto max-w-[28rem] border-t border-line bg-surface/95 p-4 pb-[calc(1rem+env(safe-area-inset-bottom))] backdrop-blur-md">
        {isActive && stop.status === 'menuju' ? (
          <SwipeToConfirm key={`${stop.id}-arrive`} label={t('drv.stop.swipeArrive')} onConfirm={() => arrive.mutateAsync()} />
        ) : isActive && stop.status === 'tiba' ? (
          <SwipeToConfirm key={`${stop.id}-done`} label={stop.kind === 'ambil' ? t('drv.stop.swipePickedUp') : t('drv.stop.swipeReceived')} onConfirm={finish} />
        ) : (
          <Button variant="secondary" size="lg" className="w-full" onClick={() => navigate(routePath(route.id))}>
            {t('drv.stop.back')}
          </Button>
        )}
      </div>
    </div>
  )
}
