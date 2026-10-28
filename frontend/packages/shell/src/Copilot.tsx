import { useApi } from '@loong/data'
import type { CopilotAction, CopilotScope } from '@loong/domain'
import { t } from '@loong/i18n'
import { Button, Card, IconButton, Input, cn, toast } from '@loong/ui'
import { Copy, RotateCcw, SendHorizontal, Sparkles, Square, ThumbsDown, ThumbsUp } from 'lucide-react'
import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { AiAnswer } from './AiAnswer'
import { initialStream, useStreamRunner, type StreamState } from './aiStream'

interface Message {
  id: number
  question: string
  state: StreamState
  feedback?: 'baik' | 'kurang'
  applied: string[]
}

/** Kunci katalog (bukan teks): alasan dirender saat tampil sehingga mengikuti bahasa. */
const FEEDBACK_REASONS = ['shell.copilot.reasonNumbers', 'shell.copilot.reasonOffTopic', 'shell.copilot.reasonTooLong', 'shell.copilot.reasonOther'] as const

/** Teks bersih untuk disalin: tanpa penanda tebal/kode markdown, tabel tetap terbaca. */
const plain = (md: string) => md.replace(/\*\*/g, '').replace(/`/g, '')

/**
 * Copilot: tanya jawab tentang satu plan dengan jawaban yang dihitung dari datanya. Mengikuti kontrak antarmuka AI:
 * kerangka pesan langsung muncul, jejak alat terlihat, teks mengalir dengan caret, selalu ada tombol berhenti,
 * pengguliran otomatis berhenti begitu pengguna menggulir ke atas, dan jawaban bisa disalin, diulang, dan dinilai.
 * Tindakan yang diusulkan hanya berjalan bila pengguna menekannya (`onAction`).
 */
export function Copilot({
  scope,
  starters,
  onAction,
  subtitle = t('shell.copilot.subtitle'),
  placeholder = t('shell.copilot.placeholder'),
  className,
  listClassName,
}: {
  scope: CopilotScope
  starters: string[]
  onAction?: (action: CopilotAction) => Promise<void>
  subtitle?: string
  placeholder?: string
  className?: string
  listClassName?: string
}) {
  const api = useApi()
  const { run, stop } = useStreamRunner()
  const [messages, setMessages] = useState<Message[]>([])
  const [draft, setDraft] = useState('')
  const [running, setRunning] = useState(false)
  const counter = useRef(0)
  const list = useRef<HTMLDivElement>(null)
  const stick = useRef(true)

  const patch = useCallback((id: number, fn: (m: Message) => Message) => setMessages((all) => all.map((m) => (m.id === id ? fn(m) : m))), [])

  const start = useCallback(
    async (question: string, existing?: number) => {
      const id = existing ?? ++counter.current
      stick.current = true
      setRunning(true)
      if (existing === undefined) setMessages((all) => [...all, { id, question, state: initialStream, applied: [] }])
      else patch(id, (m) => ({ ...m, state: initialStream, feedback: undefined }))
      await run(
        () => api.ai.copilot(scope, question),
        (fn) => patch(id, (m) => ({ ...m, state: fn(m.state) })),
      )
      setRunning(false)
    },
    [api, scope, run, patch],
  )

  function submit(e?: FormEvent) {
    e?.preventDefault()
    const q = draft.trim()
    if (!q) return
    setDraft('')
    void start(q)
  }

  function halt() {
    stop()
    setRunning(false)
    setMessages((all) => all.map((m) => (m.state.phase === 'thinking' || m.state.phase === 'streaming' ? { ...m, state: { ...m.state, phase: 'stopped' } } : m)))
  }

  // ikuti ujung jawaban, tetapi berhenti begitu pengguna menggulir ke atas
  useEffect(() => {
    const el = list.current
    if (el && stick.current) el.scrollTo({ top: el.scrollHeight })
  }, [messages])

  return (
    <Card className={cn('flex min-h-0 flex-col overflow-hidden', className)}>
      <div className="flex items-center gap-2.5 border-b border-line px-card py-3">
        <span className="grid size-7 place-items-center rounded-input bg-accent-tint text-accent" aria-hidden>
          <Sparkles className="size-4" />
        </span>
        <div className="min-w-0">
          <h2 className="text-title leading-tight">Copilot</h2>
          <p className="truncate text-small text-muted">{subtitle}</p>
        </div>
      </div>

      <div
        ref={list}
        onScroll={(e) => {
          const el = e.currentTarget
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 64
        }}
        className={cn('min-h-0 flex-1 space-y-5 overflow-y-auto px-card py-4', listClassName)}
      >
        {messages.length === 0 && (
          <div className="space-y-3">
            <p className="text-body text-muted">{t('shell.copilot.intro')}</p>
            <div className="flex flex-wrap gap-2">
              {starters.map((s) => (
                <button
                  key={s}
                  type="button"
                  onClick={() => void start(s)}
                  className="rounded-chip border border-line-input bg-surface px-3 py-1.5 text-left text-small font-medium transition-colors hover:border-subtle hover:bg-sunken"
                >
                  {s}
                </button>
              ))}
            </div>
          </div>
        )}

        {messages.map((m, idx) => {
          const last = idx === messages.length - 1
          const finished = m.state.phase === 'done' || m.state.phase === 'stopped'
          return (
            <div key={m.id} className="space-y-2.5">
              <p className="ml-auto w-fit max-w-[90%] rounded-card rounded-br-[4px] bg-accent-tint px-3 py-2 text-body">{m.question}</p>
              <AiAnswer
                state={m.state}
                applied={m.applied}
                onRetry={() => void start(m.question, m.id)}
                onAction={
                  onAction
                    ? async (a) => {
                        await onAction(a)
                        patch(m.id, (x) => ({ ...x, applied: [...x.applied, a.id] }))
                      }
                    : undefined
                }
              />
              {finished && m.state.text && (
                <div className={cn('flex flex-wrap items-center gap-1', !last && 'opacity-0 transition-opacity focus-within:opacity-100 hover:opacity-100')}>
                  <IconButton
                    label={t('shell.copilot.copy')}
                    icon={<Copy className="size-4" />}
                    onClick={() => {
                      void navigator.clipboard?.writeText(plain(m.state.text))
                      toast.success(t('shell.copilot.copied'))
                    }}
                  />
                  <IconButton label={t('shell.copilot.regenerate')} icon={<RotateCcw className="size-4" />} onClick={() => void start(m.question, m.id)} />
                  <IconButton
                    label={t('shell.copilot.helpful')}
                    icon={<ThumbsUp className={cn('size-4', m.feedback === 'baik' && 'fill-current text-accent')} />}
                    aria-pressed={m.feedback === 'baik'}
                    onClick={() => {
                      patch(m.id, (x) => ({ ...x, feedback: x.feedback === 'baik' ? undefined : 'baik' }))
                      if (m.feedback !== 'baik') toast.success(t('shell.copilot.thanksHelpful'))
                    }}
                  />
                  <IconButton
                    label={t('shell.copilot.notHelpful')}
                    icon={<ThumbsDown className={cn('size-4', m.feedback === 'kurang' && 'fill-current text-accent')} />}
                    aria-pressed={m.feedback === 'kurang'}
                    onClick={() => patch(m.id, (x) => ({ ...x, feedback: x.feedback === 'kurang' ? undefined : 'kurang' }))}
                  />
                  {m.state.requestId && <span className="ml-1 text-caption text-subtle">{m.state.requestId}</span>}
                </div>
              )}
              {m.feedback === 'kurang' && (
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="text-small text-muted">{t('shell.copilot.whatWasMissing')}</span>
                  {FEEDBACK_REASONS.map((reason) => (
                    <button
                      key={reason}
                      type="button"
                      onClick={() => {
                        patch(m.id, (x) => ({ ...x, feedback: undefined }))
                        toast.success(t('shell.copilot.thanksFeedback'))
                      }}
                      className="rounded-chip border border-line-input px-2.5 py-1 text-small hover:bg-sunken"
                    >
                      {t(reason)}
                    </button>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>

      <form onSubmit={submit} className="flex items-center gap-2 border-t border-line p-3">
        <Input value={draft} onChange={(e) => setDraft(e.target.value)} placeholder={placeholder} aria-label={t('shell.copilot.inputLabel')} autoComplete="off" />
        {running ? (
          <Button key="berhenti" type="button" variant="secondary" iconLeft={<Square className="size-3.5 fill-current" aria-hidden />} onClick={halt}>
            {t('shell.copilot.stop')}
          </Button>
        ) : (
          <Button key="kirim" type="submit" variant="primary" iconLeft={<SendHorizontal className="size-4" aria-hidden />}>
            {t('shell.copilot.send')}
          </Button>
        )}
      </form>
    </Card>
  )
}
