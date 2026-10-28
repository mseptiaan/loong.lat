import { Button, EmptyState } from '@loong/ui'
import { Compass } from 'lucide-react'
import { Link } from 'react-router'
import { t } from '../i18n'

export function NotFound() {
  return (
    <main className="mx-auto flex min-h-[70vh] max-w-md items-center">
      <EmptyState
        icon={<Compass />}
        title={t('dash.app.notFoundTitle')}
        description={t('dash.app.notFoundHint')}
        action={
          <Button variant="primary" asChild>
            <Link to="/">{t('dash.app.notFoundBack')}</Link>
          </Button>
        }
      />
    </main>
  )
}
