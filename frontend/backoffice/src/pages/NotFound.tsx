import { Button, EmptyState } from '@loong/ui'
import { Compass } from 'lucide-react'
import { Link } from 'react-router'
import { t } from '../i18n'

export function NotFound() {
  return (
    <div className="mx-auto flex min-h-[60vh] max-w-md items-center">
      <EmptyState
        icon={<Compass />}
        title={t('bo.app.notFoundTitle')}
        description={t('bo.app.notFoundDescription')}
        action={
          <Button variant="primary" asChild>
            <Link to="/">{t('bo.app.notFoundBack')}</Link>
          </Button>
        }
      />
    </div>
  )
}
