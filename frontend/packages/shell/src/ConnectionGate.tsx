import { ErrorState, Logo } from '@loong/ui'
import { REAL_BACKEND, useApiStatus } from '@loong/data'
import { appOrigin } from '@loong/domain'
import { renderMsg, t, type SharedMessages } from '@loong/i18n'
import { richTranslator } from '@loong/i18n/react'
import type { ReactNode } from 'react'

const tr = richTranslator<SharedMessages>()

/**
 * Menjaga aplikasi bila backend tidak bisa dihubungi: backend tiruan (api.<domain>) saat pengembangan, atau API asli di
 * build http (teksnya untuk pengguna, tanpa petunjuk developer). Selama masih menghubungkan, aplikasi tetap dirender
 * (tampil sebagai skeleton) dan hanya muncul pita tipis agar tidak berkedip.
 */
export function ConnectionGate({ children }: { children: ReactNode }) {
  const { status, detail } = useApiStatus()

  if (status === 'gagal') {
    return (
      <main className="mx-auto flex min-h-screen max-w-md flex-col items-center justify-center gap-6 p-6 text-center">
        <Logo />
        <ErrorState
          title={t(REAL_BACKEND ? 'shell.connection.failedTitleLive' : 'shell.connection.failedTitle')}
          message={detail ? renderMsg(detail) : undefined}
          onRetry={() => location.reload()}
          compact
        />
        <p className="text-small text-muted">
          {REAL_BACKEND
            ? t('shell.connection.failedHelpLive')
            : tr('shell.connection.failedHelp', {
                origin: appOrigin('api'),
                url: (c) => <code className="rounded-chip bg-sunken px-1.5 py-0.5 font-mono">{c}</code>,
                cmd: (c) => <code className="rounded-chip bg-sunken px-1.5 py-0.5 font-mono">{c}</code>,
              })}
        </p>
      </main>
    )
  }

  return (
    <>
      {status === 'menghubungkan' && (
        <div
          role="status"
          className="fixed inset-x-0 top-0 z-[95] h-0.5 overflow-hidden bg-transparent opacity-0 [animation:fade-up_200ms_ease-out_500ms_forwards]"
        >
          <span className="sr-only">{t(REAL_BACKEND ? 'shell.connection.connectingLive' : 'shell.connection.connecting')}</span>
          <span className="block h-full w-1/3 animate-[skeleton-shimmer_1.1s_ease-in-out_infinite] bg-accent" />
        </div>
      )}
      {children}
    </>
  )
}
