import { ApiError } from '@loong/domain'
import { REAL_BACKEND, errorMessage, useApi, useMutation, usePlan, useProfile, useVehicles, useVirtualNow, useWallet } from '@loong/data'
import { formatAgo, formatRupiah, type OptionSet, type PaymentMethod, type Plan, type Priority, type Schedule, type Shipment, type User } from '@loong/domain'
import { Button, Card, ErrorState, IconButton, Logo, Skeleton, toast } from '@loong/ui'
import { ArrowLeft, ArrowRight, Check, X } from 'lucide-react'
import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, Navigate, useNavigate, useSearchParams } from 'react-router'
import { focusFirstInvalid } from '../../lib/forms'
import { readPrefs } from '../../lib/prefs'
import { renderMsg } from '@loong/i18n'
import { useDocumentTitle } from '@loong/shell'
import { t } from '../../i18n'
import { Done } from './Done'
import { StepItems } from './StepItems'
import { StepOptions } from './StepOptions'
import { StepReview } from './StepReview'
import { StepRoute } from './StepRoute'
import { WizardProgress } from './WizardProgress'
import { STEPS, emptyShipment, validateItems, validateRoute, type FieldErrors } from './model'

type Form = { shipments: Shipment[]; schedule: Schedule; priority: Priority }
type SaveState = { kind: 'idle' } | { kind: 'dirty' } | { kind: 'saving' } | { kind: 'saved'; at: number } | { kind: 'error' }

/** Nomor langkah yang dirujuk logika wizard (1 rute, 2 barang, 3 pilih opsi, 4 tinjau). */
const OPTIONS_STEP = 3
const REVIEW_STEP = 4

const clampStep = (n: number) => Math.min(STEPS.length, Math.max(1, Number.isFinite(n) ? Math.trunc(n) : 1))

/** "Tersimpan 3 dtk lalu", dihitung ulang tiap 5 dtk. Dipasang ulang (key = waktu simpan) agar hitungan mulai dari nol. */
function SavedAgo({ at }: { at: number }) {
  const [secs, setSecs] = useState(0)
  useEffect(() => {
    const id = setInterval(() => setSecs(Math.round((Date.now() - at) / 1000)), 5000)
    return () => clearInterval(id)
  }, [at])
  return <>{t('dash.wizard.savedAgo', { ago: formatAgo(at, new Date(at + secs * 1000)) })}</>
}

/** Tanpa spinner: penyimpanan otomatis harus tak terasa. */
function SaveStatus({ state }: { state: SaveState }) {
  if (state.kind === 'idle') return null
  return (
    <>
      {/* hanya perubahan bermakna yang diumumkan ("Draft tersimpan" sekali per simpan); teks "dtk lalu" di bawah diam */}
      <span role="status" className="sr-only">
        {state.kind === 'saved' ? t('dash.wizard.draftSaved') : state.kind === 'error' ? t('dash.wizard.srSaveError') : ''}
      </span>
      <p className="flex items-center gap-1.5 text-small text-muted" aria-hidden>
        {state.kind === 'saved' && <Check className="size-3.5 text-success-fg" aria-hidden />}
        {state.kind === 'saved' ? <SavedAgo key={state.at} at={state.at} /> : state.kind === 'error' ? t('dash.wizard.saveRetry') : state.kind === 'dirty' ? t('dash.wizard.saveDirty') : t('dash.wizard.saving')}
      </p>
    </>
  )
}

function WizardSkeleton() {
  return (
    <div className="mx-auto max-w-5xl space-y-6 px-4 py-8 sm:px-6">
      <Skeleton className="h-6 w-64" />
      <Skeleton className="h-96 rounded-card" />
    </div>
  )
}

/** Pemuat: menunggu profil (dan draft bila melanjutkan) lalu memasang Wizard sekali; Wizard mengelola state sendiri. */
export function NewPlan() {
  const [params] = useSearchParams()
  const navigate = useNavigate()
  // id draft dibaca SEKALI; setelah draft dibuat, URL berubah tetapi pemuat ini tidak boleh memasang ulang Wizard
  const [initialDraft, setInitialDraft] = useState<string | null>(params.get('draft'))
  const [runKey, setRunKey] = useState(0)
  const profile = useProfile()
  // aturan muat dipakai untuk membatasi langkah; tunggu agar tidak ada kedipan langkah palsu. Backend asli tidak memberi data armada
  // ke pelanggan, jadi di sana aturan muat tidak dipakai (backend yang menolak muatan terlalu besar saat opsi dihitung).
  const vehicles = useVehicles({ enabled: !REAL_BACKEND })
  const plan = usePlan(initialDraft ?? undefined)
  // Potret plan saat pertama dimuat. Query plan terus hidup dan berubah setelah konfirmasi (status -> menunggu_driver);
  // tanpa potret, perubahan itu mengalihkan halaman sebelum layar sukses sempat tampil.
  const [snapshot, setSnapshot] = useState<Plan | undefined>()
  if (initialDraft && plan.data && !snapshot) setSnapshot(plan.data)

  if (profile.isLoading || vehicles.isLoading || (initialDraft && !snapshot && plan.isLoading)) return <WizardSkeleton />
  if (profile.isError || !profile.data) {
    return (
      <div className="mx-auto max-w-lg px-4 py-16">
        <Card>
          <ErrorState message={errorMessage(profile.error)} onRetry={() => void profile.refetch()} />
        </Card>
      </div>
    )
  }
  // Tanpa aturan muat wizard tidak boleh dipasang: Wizard memasang pengamat useVehicles sendiri, dan pada query yang gagal
  // pemasangan itu memicu pengambilan ulang (status kembali "pending"), sehingga pemuat ini kembali ke kerangka dan
  // memasang ulang Wizard tanpa henti. Tampilkan galat dengan tombol coba lagi seperti untuk profil.
  if (!REAL_BACKEND && (vehicles.isError || !vehicles.data)) {
    return (
      <div className="mx-auto max-w-lg px-4 py-16">
        <Card>
          <ErrorState message={errorMessage(vehicles.error)} onRetry={() => void vehicles.refetch()} />
        </Card>
      </div>
    )
  }
  if (initialDraft && !snapshot) {
    return (
      <div className="mx-auto max-w-lg px-4 py-16">
        <Card>
          <ErrorState
            title={t('dash.wizard.draftMissingTitle')}
            message={t('dash.wizard.draftMissing')}
            onRetry={() => {
              setInitialDraft(null)
              navigate('/plan/new', { replace: true })
            }}
          />
        </Card>
      </div>
    )
  }
  if (snapshot && snapshot.status !== 'draft' && snapshot.status !== 'opsi_siap') {
    return <Navigate to={`/plan/${snapshot.id}`} replace />
  }

  return (
    <Wizard
      key={runKey}
      initial={snapshot}
      profile={profile.data}
      onRestart={() => {
        setInitialDraft(null)
        setSnapshot(undefined)
        setRunKey((k) => k + 1)
        navigate('/plan/new', { replace: true })
      }}
    />
  )
}

function Wizard({ initial, profile, onRestart }: { initial?: Plan; profile: User; onRestart: () => void }) {
  const api = useApi()
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const nowMs = useVirtualNow(1000)
  useDocumentTitle(t('dash.wizard.docTitle'), t('dash.wizard.docApp'))
  const fleet = useVehicles({ enabled: !REAL_BACKEND })
  const vehicles = REAL_BACKEND ? undefined : (fleet.data ?? [])
  const vehiclesReady = REAL_BACKEND || fleet.isSuccess
  const main = useRef<HTMLElement>(null)
  const contact = { name: profile.name, phone: profile.phone }
  const { data: wallet } = useWallet()

  const [form, setForm] = useState<Form>(() => ({
    shipments: initial?.shipments.length ? initial.shipments : [emptyShipment(contact)],
    schedule: initial?.schedule ?? { mode: 'sekarang' },
    priority: initial?.priority ?? { preset: 'seimbang' },
  }))
  const [draftId, setDraftId] = useState<string | null>(initial?.id ?? null)
  const draftRef = useRef(draftId)
  const [saveState, setSaveState] = useState<SaveState>({ kind: 'idle' })
  const [options, setOptions] = useState<OptionSet | null>(initial?.optionSet ?? null)
  const [selectedId, setSelectedId] = useState<string | undefined>(initial?.selectedOptionId ?? initial?.optionSet?.options.find((o) => o.recommended)?.id)
  // pilihan tersimpan dari mode tiruan (transfer, tunai) tidak berlaku di backend asli
  const [payment, setPayment] = useState<PaymentMethod>(() => (REAL_BACKEND ? 'saldo' : readPrefs().payment))
  const [terms, setTerms] = useState(false)
  const [attempted, setAttempted] = useState<Record<number, boolean>>({})
  const [confirmed, setConfirmed] = useState<Plan | null>(null)

  // ?step= hanya dipercaya bila ?draft= di URL memang draft milik wizard ini (null = belum ada draft). Tanpa pemeriksaan ini,
  // wizard yang dimulai ulang sempat membaca URL lama (draft dan langkah milik pesanan sebelumnya) sebelum navigasi selesai.
  const urlStep = params.get('draft') === draftId ? Number(params.get('step')) : 0
  const rawStep = clampStep(urlStep || initial?.draftStep || 1)

  /* ---------- validasi per langkah (galat hilang sendiri saat diperbaiki) ---------- */
  const validators: Record<number, () => FieldErrors> = {
    1: () => validateRoute(form.shipments, form.schedule, nowMs),
    2: () => validateItems(form.shipments, vehicles),
  }
  // Batasi ke langkah pertama yang datanya belum valid: tautan dalam ke langkah 3-4 dari draft yang belum lengkap
  // tidak boleh menabrak engine. Daftar kendaraan sudah dimuat pemuat, jadi aturan muat bisa dipercaya.
  const firstInvalid = vehiclesReady ? ([1, 2].find((n) => Object.keys(validators[n]!()).length > 0) ?? STEPS.length) : STEPS.length
  const bounced = rawStep > firstInvalid
  const step = Math.min(rawStep >= STEPS.length && !options ? OPTIONS_STEP : rawStep, firstInvalid)
  const [maxReached, setMaxReached] = useState(() => Math.max(initial?.draftStep ?? 1, step))

  // selaraskan URL dengan langkah yang dibatasi, supaya memperbaiki isian tidak melompatkan wizard maju sendiri
  useEffect(() => {
    if (!bounced) return
    setParams(
      (prev) => {
        const next = new URLSearchParams(prev)
        next.set('step', String(step))
        return next
      },
      { replace: true },
    )
  }, [bounced, step, setParams])

  /* ---------- penyimpanan draft ---------- */
  const formRef = useRef(form)
  const stepRef = useRef(step)
  useEffect(() => {
    formRef.current = form
    stepRef.current = step
  })

  const persist = useCallback(async (): Promise<string> => {
    setSaveState({ kind: 'saving' })
    try {
      let id = draftRef.current
      if (!id) {
        const created = await api.plans.createDraft()
        id = created.id
        draftRef.current = id
        setDraftId(id)
        setParams(
          (prev) => {
            const next = new URLSearchParams(prev)
            next.set('draft', id!)
            return next
          },
          { replace: true },
        )
      }
      const f = formRef.current
      await api.plans.saveDraft(id, { shipments: f.shipments, schedule: f.schedule, priority: f.priority, draftStep: stepRef.current })
      setSaveState({ kind: 'saved', at: Date.now() })
      return id
    } catch (err) {
      setSaveState({ kind: 'error' })
      throw err
    }
  }, [api, setParams])

  // pilihan opsi langsung disimpan agar Copilot menjelaskan opsi yang sedang dipilih, bukan yang lama
  const selectOption = useCallback(
    (optionId: string) => {
      setSelectedId(optionId)
      const id = draftRef.current
      if (id) void api.plans.chooseOption(id, optionId).catch(() => undefined)
    },
    [api],
  )

  const edit = useCallback((patch: Partial<Form>) => {
    setForm((f) => ({ ...f, ...patch }))
    setSaveState({ kind: 'dirty' })
    // masukan berubah -> opsi lama basi
    setOptions(null)
    setSelectedId(undefined)
  }, [])

  // simpan otomatis 1,5 dtk setelah ketikan terakhir; gagal -> coba lagi
  useEffect(() => {
    if (saveState.kind !== 'dirty' && saveState.kind !== 'error') return
    const timer = setTimeout(() => void persist().catch(() => undefined), saveState.kind === 'error' ? 4000 : 1500)
    return () => clearTimeout(timer)
  }, [form, saveState.kind, persist])

  // peringatan bila menutup tab saat ada perubahan yang belum tersimpan
  useEffect(() => {
    const warn = saveState.kind === 'dirty' || saveState.kind === 'saving'
    if (!warn) return
    const handler = (e: BeforeUnloadEvent) => e.preventDefault()
    window.addEventListener('beforeunload', handler)
    return () => window.removeEventListener('beforeunload', handler)
  }, [saveState.kind])

  /* ---------- navigasi ---------- */
  const goTo = useCallback(
    async (n: number) => {
      if (saveState.kind === 'dirty' || draftRef.current) {
        try {
          await persist()
        } catch (err) {
          toast.error(errorMessage(err))
          return
        }
      }
      setMaxReached((m) => Math.max(m, n))
      setParams(
        (prev) => {
          const next = new URLSearchParams(prev)
          next.set('step', String(n))
          if (draftRef.current) next.set('draft', draftRef.current)
          return next
        },
        { replace: false },
      )
      window.scrollTo({ top: 0 })
    },
    [persist, saveState.kind, setParams],
  )

  // langkah yang pernah dilewati dianggap sudah "dicoba": galatnya langsung tampil (mis. draft yang dibuka dari langkah pilih opsi)
  const errors: FieldErrors = (attempted[step] || maxReached > step) && validators[step] ? validators[step]!() : {}

  const selected = options?.options.find((o) => o.id === selectedId) ?? options?.options.find((o) => o.recommended) ?? options?.options[0]

  const confirm = useMutation({
    mutationFn: async () => {
      const id = await persist()
      if (!selected) throw new ApiError('tidak_valid', t('dash.wizard.errPickOption'))
      await api.plans.chooseOption(id, selected.id)
      return api.plans.confirm(id, payment)
    },
    onSuccess: (plan) => {
      setConfirmed(plan)
      window.scrollTo({ top: 0 })
    },
    onError: (err) => {
      toast.error(errorMessage(err))
      // data berubah sejak opsi dihitung: hitung ulang di langkah pilih opsi
      if (err instanceof ApiError && err.code === 'bentrok') {
        setOptions(null)
        void goTo(OPTIONS_STEP)
      }
    },
  })

  const advance = useMutation({
    mutationFn: async () => {
      const id = await persist()
      if (selected) await api.plans.chooseOption(id, selected.id)
    },
    onSuccess: () => void goTo(REVIEW_STEP),
    onError: (err) => toast.error(errorMessage(err)),
  })

  function next() {
    if (step < OPTIONS_STEP) {
      setAttempted((a) => ({ ...a, [step]: true }))
      if (Object.keys(validators[step]?.() ?? {}).length > 0) return focusFirstInvalid(main.current)
      void goTo(step + 1)
      return
    }
    if (step === OPTIONS_STEP) {
      if (!selected) return void toast.info(t('dash.wizard.noOptionsYet'))
      advance.mutate()
      return
    }
    // langkah terakhir: tinjau
    if (payment === 'saldo' && wallet && selected && selected.totals.costRp > wallet.balanceRp) {
      return void toast.error(t('dash.wizard.balanceShort'))
    }
    if (!terms) {
      setAttempted((a) => ({ ...a, [REVIEW_STEP]: true }))
      return focusFirstInvalid(main.current)
    }
    confirm.mutate()
  }

  async function close() {
    if (saveState.kind === 'dirty' || draftRef.current) {
      try {
        await persist()
        toast.success(t('dash.wizard.draftSaved'))
      } catch {
        /* biarkan menutup; autosave sudah mencoba */
      }
    }
    navigate('/plan')
  }

  if (confirmed) {
    return (
      <div className="min-h-screen">
        <header className="mx-auto flex h-16 max-w-5xl items-center px-4 sm:px-6">
          <Link to="/" aria-label={t('dash.wizard.logoLabel')}>
            <Logo />
          </Link>
        </header>
        <main className="px-4 sm:px-6">
          <Done plan={confirmed} onRestart={onRestart} />
        </main>
      </div>
    )
  }

  const last = step === STEPS.length

  return (
    <div className="min-h-screen pb-32">
      <header className="sticky top-0 z-30 border-b border-line bg-surface/90 backdrop-blur-md">
        <div className="mx-auto flex h-16 max-w-5xl items-center justify-between gap-4 px-4 sm:px-6">
          <Link to="/" aria-label={t('dash.wizard.logoLabel')} className="rounded-input">
            <Logo />
          </Link>
          <div className="flex items-center gap-3">
            <SaveStatus state={saveState} />
            <IconButton label={t('dash.wizard.closeSave')} icon={<X className="size-5" />} onClick={() => void close()} />
          </div>
        </div>
      </header>

      <main ref={main} className="mx-auto max-w-5xl space-y-6 px-4 py-6 sm:px-6 sm:py-8">
        <div className="space-y-4">
          <WizardProgress step={step} maxReached={maxReached} onGo={(n) => void goTo(n)} />
          <div>
            <h1 className="text-heading">{[t('dash.wizard.title1'), t('dash.wizard.title2'), t('dash.wizard.title3'), t('dash.wizard.title4')][step - 1]}</h1>
            <p className="mt-1 max-w-2xl text-body text-muted">
              {[t('dash.wizard.hint1'), t('dash.wizard.hint2'), t('dash.wizard.hint3'), t('dash.wizard.hint4')][step - 1]}
            </p>
          </div>
        </div>

        {step === 1 && (
          <StepRoute
            shipments={form.shipments}
            onShipments={(shipments) => edit({ shipments })}
            schedule={form.schedule}
            onSchedule={(schedule) => edit({ schedule })}
            priority={form.priority}
            onPriority={(priority) => edit({ priority })}
            errors={errors}
            saved={profile.savedAddresses}
            nowMs={nowMs}
            contact={contact}
          />
        )}
        {step === 2 && <StepItems shipments={form.shipments} onShipments={(shipments) => edit({ shipments })} errors={errors} />}
        {step === OPTIONS_STEP && (
          <StepOptions
            ensureSaved={persist}
            optionSet={options}
            onOptionSet={(set, opts) => {
              setOptions(set)
              // opsi baru dari perhitungan (juga "Cari lagi") membuang pilihan lama; menyimpan ringkasan AI tidak mengubah pilihan
              setSelectedId((cur) => (!opts?.reset && cur && set.options.some((o) => o.id === cur) ? cur : (set.options.find((o) => o.recommended) ?? set.options[0])?.id))
            }}
            selectedId={selected?.id}
            onSelect={selectOption}
            planId={draftId ?? undefined}
          />
        )}
        {step === REVIEW_STEP && selected && (
          <StepReview
            shipments={form.shipments}
            schedule={form.schedule}
            option={selected}
            nowMs={nowMs}
            payment={payment}
            onPayment={setPayment}
            terms={terms}
            onTerms={setTerms}
            termsError={attempted[REVIEW_STEP] && !terms ? t('dash.wizard.termsRequired') : undefined}
            goTo={(n) => void goTo(n)}
            balanceRp={wallet?.balanceRp}
          />
        )}
      </main>

      <div className="fixed inset-x-0 bottom-0 z-30 border-t border-line bg-surface/95 pb-[env(safe-area-inset-bottom)] backdrop-blur-md">
        <div className="mx-auto flex max-w-5xl items-center justify-between gap-3 px-4 py-3 sm:px-6">
          <div className="min-w-0">
            {step > 1 ? (
              <Button variant="secondary" iconLeft={<ArrowLeft className="size-4" aria-hidden />} onClick={() => void goTo(step - 1)}>
                {t('dash.wizard.back')}
              </Button>
            ) : (
              <Button variant="ghost" onClick={() => void close()}>
                {t('dash.wizard.saveDraft')}
              </Button>
            )}
          </div>
          {selected && step === OPTIONS_STEP && (
            <p className="hidden min-w-0 text-right sm:block">
              <span className="block text-small text-muted">{renderMsg(selected.label)}</span>
              <span className="text-title tabular-nums">{formatRupiah(selected.totals.costRp)}</span>
            </p>
          )}
          <Button variant="primary" size="lg" loading={advance.isPending || confirm.isPending} iconRight={last ? undefined : <ArrowRight className="size-4" aria-hidden />} onClick={next}>
            {last
              ? selected
                ? t('dash.wizard.confirmOrderPrice', { cost: selected.totals.costRp })
                : t('dash.wizard.confirmOrder')
              : step === OPTIONS_STEP
                ? t('dash.wizard.nextReview')
                : t('dash.wizard.next')}
          </Button>
        </div>
      </div>
    </div>
  )
}
