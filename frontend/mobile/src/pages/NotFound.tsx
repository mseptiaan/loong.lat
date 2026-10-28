import { Button, EmptyState } from '@loong/ui'
import { Compass } from 'lucide-react'
import { Link } from 'react-router'
import { t } from '../i18n'

export function NotFound() {
  return (
    <main className="mx-auto flex min-h-dvh max-w-[28rem] items-center px-6">
      <EmptyState
        icon={<Compass />}
        title={t('drv.app.notFoundTitle')}
        description={t('drv.app.notFoundHint')}
        action={
          <Button variant="primary" size="lg" asChild>
            <Link to="/">{t('drv.app.backHome')}</Link>
          </Button>
        }
      />
    </main>
  )
}
