import { errorMessage, useApi, useMutation, useSettings } from '@loong/data'
import { type Item, type SpeechParse } from '@loong/domain'
import { getLocale } from '@loong/i18n'
import { Button, Field, Notice, Select, Sheet, Textarea, cn } from '@loong/ui'
import { Check, Mic, RotateCcw, Square } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'
import { t } from '../../i18n'
import { DetectedReview } from './DetectedReview'
import { toItems, toRows, type ReviewRow } from './rows'
import { browserRecordingDeps, canRecordAudio, captureErrorKind, pickCaptureMode, startRecording, transcribeRecording, type Recording } from './voice'
import { browserAudioDecoder } from './wav'

/* ---- Web Speech API: belum ada di tipe standar TS ---- */
interface RecognitionResult {
  0: { transcript: string }
  length: number
}
interface Recognition {
  lang: string
  interimResults: boolean
  continuous: boolean
  onresult: ((e: { results: ArrayLike<RecognitionResult> }) => void) | null
  onerror: ((e: { error: string }) => void) | null
  onend: (() => void) | null
  start(): void
  stop(): void
  abort(): void
}
type RecognitionCtor = new () => Recognition
const recognitionCtor = (): RecognitionCtor | undefined => {
  const w = window as unknown as { SpeechRecognition?: RecognitionCtor; webkitSpeechRecognition?: RecognitionCtor }
  return w.SpeechRecognition ?? w.webkitSpeechRecognition
}

/** Bahasa pengenalan suara mengikuti bahasa tampilan. */
const speechLang = () => (getLocale() === 'id' ? 'id-ID' : 'en-US')

/** Pesan untuk kode galat pengenalan suara; `code` yang disimpan di state, teksnya dibuat saat render. */
function recognitionError(code: string): string {
  switch (code) {
    case 'not-allowed':
    case 'service-not-allowed':
      return t('dash.ai.voiceErrDenied')
    case 'no-speech':
      return t('dash.ai.voiceErrNoSpeech')
    case 'audio-capture':
      return t('dash.ai.voiceErrNoMic')
    case 'network':
      return t('dash.ai.voiceErrNetwork')
    default:
      return t('dash.ai.voiceErrGeneric')
  }
}

const samples = () => [t('dash.ai.voiceSample1'), t('dash.ai.voiceSample2'), t('dash.ai.voiceSample3')]

const BARS = 28

/** Tingkat suara mikrofon nyata (AnalyserNode) sebagai riwayat 28 titik untuk gelombang. Diam bila tak aktif atau tak ada izin. */
function useWaveform(active: boolean): number[] {
  const [levels, setLevels] = useState<number[]>(() => Array(BARS).fill(0))
  useEffect(() => {
    if (!active || !navigator.mediaDevices?.getUserMedia) return
    let stream: MediaStream | undefined
    let ctx: AudioContext | undefined
    let timer: number | undefined
    let cancelled = false
    navigator.mediaDevices
      .getUserMedia({ audio: true })
      .then((s) => {
        if (cancelled) return s.getTracks().forEach((t) => t.stop())
        stream = s
        ctx = new AudioContext()
        const analyser = ctx.createAnalyser()
        analyser.fftSize = 256
        ctx.createMediaStreamSource(s).connect(analyser)
        const data = new Uint8Array(analyser.fftSize)
        timer = window.setInterval(() => {
          analyser.getByteTimeDomainData(data)
          let sum = 0
          for (const v of data) sum += ((v - 128) / 128) ** 2
          const level = Math.min(1, Math.sqrt(sum / data.length) * 4)
          setLevels((prev) => [...prev.slice(1), level])
        }, 70)
      })
      .catch(() => undefined) // tanpa izin: gelombang diam, pengenalan suara tetap berjalan sendiri
    return () => {
      cancelled = true
      window.clearInterval(timer)
      stream?.getTracks().forEach((t) => t.stop())
      void ctx?.close()
    }
  }, [active])
  return active ? levels : Array(BARS).fill(0)
}

function Waveform({ active }: { active: boolean }) {
  const levels = useWaveform(active)
  return (
    <div className="flex h-12 flex-1 items-center gap-[3px]" role="img" aria-label={active ? t('dash.ai.voiceWaveListening') : t('dash.ai.voiceWaveIdle')}>
      {levels.map((l, i) => (
        <span key={i} className={cn('w-[3px] rounded-full transition-[height] duration-75', active ? 'bg-accent' : 'bg-line-strong')} style={{ height: `${Math.max(4, Math.round(l * 44))}px` }} />
      ))}
    </div>
  )
}

/**
 * "Ucapkan": bicara, atau ketik/pilih contoh; lalu AI mengurai ucapan menjadi barang yang harus diperiksa sebelum
 * ditambahkan. Bagian yang tidak dipahami ditampilkan apa adanya, tidak ditebak.
 * Menangkap suara: mode tiruan memakai pengenalan suara peramban (Web Speech API). Mode http, saat saklar suara menyala,
 * merekam dengan MediaRecorder, mengodekannya sebagai WAV 16 kHz mono, dan mengirimnya ke `ai.transcribe`; transkripnya masuk ke kolom teks untuk diperiksa. Bila
 * merekam atau mentranskripsi gagal, mikrofon jatuh ke pengenalan suara peramban, lalu ke mengetik.
 */
export function VoiceSheet({
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
  const { data: settings } = useSettings()
  const [supported] = useState(() => !!recognitionCtor())
  const [canRecord] = useState(canRecordAudio)
  const [recordFailed, setRecordFailed] = useState(false)
  const mode = pickCaptureMode({ http: api.kind === 'http', voiceOn: settings?.ai.voice ?? false, canRecord, canRecognize: supported, recordFailed })
  const [transcript, setTranscript] = useState('')
  const [listening, setListening] = useState(false)
  const [phase, setPhase] = useState<'idle' | 'starting' | 'recording' | 'transcribing'>('idle')
  const [micError, setMicError] = useState<string>() // kode galat pengenalan suara
  const [captureError, setCaptureError] = useState<unknown>() // galat merekam atau mentranskripsi
  const [result, setResult] = useState<{ parse: SpeechParse; rows: ReviewRow[] }>()
  const [target, setTarget] = useState(String(defaultShipment))
  const rec = useRef<Recognition | null>(null)
  const recording = useRef<Recording | null>(null)
  const attempt = useRef(0) // naik tiap sheet ditutup: hasil perekaman atau transkripsi yang terlambat dibuang

  const stopListening = () => {
    rec.current?.abort()
    rec.current = null
    setListening(false)
  }

  // sheet ditutup/komponen dilepas: matikan mikrofon
  useEffect(
    () => () => {
      rec.current?.abort()
      recording.current?.cancel()
    },
    [],
  )

  function failCapture(err: unknown) {
    setPhase('idle')
    setRecordFailed(true) // mikrofon beralih ke pengenalan suara peramban (atau mengetik) sampai sheet ditutup
    setCaptureError(err)
  }

  async function record() {
    if (phase !== 'idle') return // dobel ketuk saat izin mikrofon masih ditanyakan
    const mine = attempt.current
    setMicError(undefined)
    setCaptureError(undefined)
    setPhase('starting')
    try {
      const started = await startRecording(browserRecordingDeps(() => void finishRecording()))
      if (attempt.current !== mine) return started.cancel() // sheet ditutup selagi izin ditanyakan
      recording.current = started
      setPhase('recording')
    } catch (err) {
      if (attempt.current === mine) failCapture(err)
    }
  }

  async function finishRecording() {
    const current = recording.current
    if (!current) return
    const mine = attempt.current
    recording.current = null
    setPhase('transcribing')
    try {
      const text = await transcribeRecording(api.ai, await current.stop(), browserAudioDecoder)
      if (attempt.current !== mine) return
      setTranscript(text)
      setPhase('idle')
    } catch (err) {
      if (attempt.current === mine) failCapture(err)
    }
  }

  function listen() {
    const Ctor = recognitionCtor()
    if (!Ctor) return
    setMicError(undefined)
    const r = new Ctor()
    r.lang = speechLang()
    r.interimResults = true
    r.continuous = true
    r.onresult = (e) => {
      const text = Array.from(e.results, (x) => x[0].transcript).join(' ')
      setTranscript(text.trim())
    }
    r.onerror = (e) => setMicError(e.error)
    r.onend = () => setListening(false)
    rec.current = r
    setListening(true)
    try {
      r.start()
    } catch {
      setListening(false)
    }
  }

  const understand = useMutation({
    mutationFn: (text: string) => api.ai.parseSpeech(text),
    onSuccess: (parse) => setResult({ parse, rows: toRows(parse.items) }),
  })

  const matched = result
    ? result.parse.matches.reduce<Record<number, string[]>>((acc, m) => ({ ...acc, [m.itemIndex]: [...(acc[m.itemIndex] ?? []), m.text] }), {})
    : undefined
  const included = result ? result.rows.filter((r) => r.include && r.qty > 0) : []

  function close(o: boolean) {
    if (!o) {
      stopListening()
      attempt.current++
      recording.current?.cancel()
      recording.current = null
      setPhase('idle')
      setRecordFailed(false)
      setCaptureError(undefined)
      setResult(undefined)
      understand.reset()
    }
    onOpenChange(o)
  }

  const busy = phase !== 'idle'
  const active = mode === 'record' ? phase === 'recording' : listening
  const captureMessage = (err: unknown): string => {
    switch (captureErrorKind(err)) {
      case 'denied':
        return recognitionError('not-allowed')
      case 'noMic':
        return recognitionError('audio-capture')
      case 'api':
        return errorMessage(err)
      default:
        return t('dash.ai.voiceErrTranscribe')
    }
  }

  return (
    <Sheet
      open={open}
      onOpenChange={close}
      title={t('dash.ai.voiceTitle')}
      description={t('dash.ai.voiceDesc')}
      footer={
        result ? (
          <>
            <Button variant="secondary" iconLeft={<RotateCcw className="size-4" aria-hidden />} onClick={() => setResult(undefined)}>
              {t('dash.ai.voiceAgain')}
            </Button>
            <Button
              variant="primary"
              iconLeft={<Check className="size-4" aria-hidden />}
              aria-disabled={included.length === 0 || undefined}
              onClick={() => {
                if (included.length === 0) return
                onAdd(toItems(result.rows, 'suara'), Number(target))
                close(false)
              }}
            >
              {included.length > 0 ? t('dash.ai.addItems', { count: included.length }) : t('dash.ai.addItemsNone')}
            </Button>
          </>
        ) : (
          <>
            <Button variant="secondary" onClick={() => close(false)}>
              {t('dash.ai.manualInstead')}
            </Button>
            <Button
              variant="primary"
              loading={understand.isPending}
              aria-disabled={busy || undefined}
              onClick={() => {
                if (busy) return // masih merekam atau mengubah rekaman menjadi teks
                stopListening()
                understand.mutate(transcript)
              }}
            >
              {t('dash.ai.voiceUnderstand')}
            </Button>
          </>
        )
      }
    >
      <p role="status" className="sr-only">
        {phase === 'transcribing' ? t('dash.ai.voiceTranscribing') : understand.isPending ? t('dash.ai.voiceBusy') : result ? t('dash.ai.voiceDone', { count: result.rows.length }) : ''}
      </p>
      {!result ? (
        <div className="space-y-5">
          {mode !== 'type' ? (
            <div className="flex items-center gap-4 rounded-card bg-sunken p-3.5">
              <Button
                variant={active ? 'danger' : 'primary'}
                className="size-14 shrink-0 rounded-full px-0"
                aria-label={active ? t('dash.ai.voiceStop') : t('dash.ai.voiceStart')}
                loading={phase === 'starting' || phase === 'transcribing'}
                onClick={mode === 'record' ? (active ? () => void finishRecording() : () => void record()) : listening ? stopListening : listen}
              >
                {active ? <Square className="size-5 fill-current" aria-hidden /> : <Mic className="size-6" aria-hidden />}
              </Button>
              <div className="min-w-0 flex-1">
                <p className="text-body font-semibold" aria-live="polite">
                  {phase === 'transcribing'
                    ? t('dash.ai.voiceTranscribing')
                    : active
                      ? t('dash.ai.voiceListening')
                      : transcript
                        ? t('dash.ai.voiceFinished')
                        : t('dash.ai.voiceTapMic')}
                </p>
                <Waveform active={active} />
              </div>
            </div>
          ) : (
            <Notice tone="info" title={t('dash.ai.voiceUnsupportedTitle')}>
              {t('dash.ai.voiceUnsupported')}
            </Notice>
          )}

          {micError && <Notice tone="warning">{recognitionError(micError)}</Notice>}
          {captureError !== undefined && <Notice tone="warning">{captureMessage(captureError)}</Notice>}
          {understand.isError && <Notice tone="danger">{errorMessage(understand.error)}</Notice>}

          <Field label={t('dash.ai.voiceField')} hint={mode !== 'type' ? t('dash.ai.voiceFieldHint') : t('dash.ai.voiceFieldHintTyped')}>
            <Textarea value={transcript} onChange={(e) => setTranscript(e.target.value)} placeholder={t('dash.ai.voicePlaceholder')} rows={3} />
          </Field>

          <div>
            <p className="mb-2 text-small font-semibold">{t('dash.ai.voiceTryExample')}</p>
            <div className="flex flex-col items-start gap-2">
              {samples().map((s) => (
                <button key={s} type="button" onClick={() => setTranscript(s)} className="rounded-chip border border-line bg-surface px-3 py-1.5 text-left text-small transition-colors hover:border-line-strong hover:bg-sunken">
                  “{s}”
                </button>
              ))}
            </div>
          </div>
        </div>
      ) : (
        <div className="space-y-5">
          <blockquote className="rounded-card bg-sunken px-4 py-3 text-body">“{transcript}”</blockquote>
          {result.rows.length > 0 ? (
            <>
              <DetectedReview rows={result.rows} onChange={(rows) => setResult({ ...result, rows })} matched={matched} />
              {shipmentLabels.length > 1 && (
                <Field label={t('dash.ai.addTo')}>
                  <Select aria-label={t('dash.ai.addToShipment')} value={target} onValueChange={setTarget} options={shipmentLabels.map((l, i) => ({ value: String(i), label: l }))} />
                </Field>
              )}
            </>
          ) : (
            <Notice tone="warning" title={t('dash.ai.voiceNoneTitle')}>
              {t('dash.ai.voiceNoneBody')}
            </Notice>
          )}
          {result.parse.leftover && (
            <Notice tone="info" title={t('dash.ai.voiceLeftoverTitle')}>
              {t('dash.ai.voiceLeftoverBody', { text: result.parse.leftover })}
            </Notice>
          )}
        </div>
      )}
    </Sheet>
  )
}
