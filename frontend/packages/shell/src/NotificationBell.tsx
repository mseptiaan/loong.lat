import { useApi, useMutation, useNotifications, useQueryClient, qk } from '@loong/data'
import { formatAgo } from '@loong/domain'
import { renderMsg, t } from '@loong/i18n'
import { Button, EmptyState, IconButton, Popover, PopoverContent, PopoverTrigger, Skeleton, cn } from '@loong/ui'
import { Bell, BellOff } from 'lucide-react'
import { useState } from 'react'

/** Lonceng notifikasi bersama: titik penanda belum dibaca, daftar terbaru, tandai semua terbaca. */
export function NotificationBell({ onOpenPlan }: { onOpenPlan?: (planId: string) => void }) {
  const api = useApi()
  const qc = useQueryClient()
  const { data, isLoading } = useNotifications()
  const [open, setOpen] = useState(false)
  const unread = data?.filter((n) => !n.read).length ?? 0
  const markAll = useMutation({
    mutationFn: () => api.notifications.markRead(),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.notifications }),
  })

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <span className="relative inline-flex">
        <PopoverTrigger asChild>
          <IconButton label={unread ? t('shell.notifications.unreadLabel', { count: unread }) : t('shell.notifications.title')} icon={<Bell className="size-5" />} />
        </PopoverTrigger>
        {unread > 0 && <span aria-hidden className="pointer-events-none absolute right-1.5 top-1.5 size-2 rounded-full border-2 border-surface bg-accent" />}
      </span>
      <PopoverContent className="w-[min(24rem,calc(100vw-1.5rem))] p-0">
        <div className="flex items-center justify-between border-b border-line px-4 py-3">
          <h2 className="text-title">{t('shell.notifications.title')}</h2>
          {unread > 0 && (
            <Button variant="link" className="text-small" onClick={() => markAll.mutate()} loading={markAll.isPending}>
              {t('shell.notifications.markAllRead')}
            </Button>
          )}
        </div>
        <div className="max-h-[min(26rem,60vh)] overflow-y-auto">
          {isLoading ? (
            <div className="space-y-3 p-4">
              {[0, 1, 2].map((i) => (
                <div key={i} className="space-y-1.5">
                  <Skeleton className="h-4 w-2/3" />
                  <Skeleton className="h-3.5 w-full" />
                </div>
              ))}
            </div>
          ) : !data?.length ? (
            <EmptyState compact icon={<BellOff />} title={t('shell.notifications.emptyTitle')} description={t('shell.notifications.emptyHint')} />
          ) : (
            <ul>
              {data.map((n) => (
                <li key={n.id} className="border-b border-line last:border-0">
                  <button
                    type="button"
                    onClick={() => {
                      if (n.planId && onOpenPlan) {
                        setOpen(false)
                        onOpenPlan(n.planId)
                      }
                    }}
                    className={cn('flex w-full gap-3 px-4 py-3 text-left transition-colors hover:bg-sunken', !n.planId && 'cursor-default')}
                  >
                    <span aria-hidden className={cn('mt-1.5 size-2 shrink-0 rounded-full', n.read ? 'bg-transparent' : 'bg-accent')} />
                    <span className="min-w-0 flex-1">
                      <span className="flex items-baseline justify-between gap-3">
                        <span className={cn('text-body', !n.read && 'font-semibold')}>{renderMsg(n.title)}</span>
                        <span className="shrink-0 text-caption text-subtle">{formatAgo(n.at)}</span>
                      </span>
                      <span className="mt-0.5 block text-small text-muted">{renderMsg(n.body)}</span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </PopoverContent>
    </Popover>
  )
}
