import { createRoute } from '@tanstack/react-router'

import { useLogs } from '@/api/queries'
import { PageHeader, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatTime } from '@/lib/format'

import { rootRoute } from './root'

const levelColor = (level: string): string => {
  const normalized = level.toLowerCase()
  if (normalized === 'error' || normalized === 'fatal') return 'text-danger'
  if (normalized === 'warn' || normalized === 'warning') return 'text-amber'
  return 'text-good'
}

const LogsPage = () => {
  const { t, locale } = useTranslation()
  const logs = useLogs()

  return (
    <div className='flex flex-col gap-5'>
      <PageHeader title={t('logs.title')} description={t('logs.description')} />
      <Panel title={t('logs.title')}>
      <QueryState
        isPending={logs.isPending}
        isError={logs.isError}
        isEmpty={logs.data?.length === 0}
        emptyMessage={t('logs.empty')}
        onRetry={() => void logs.refetch()}
      >
        <ol className='max-h-[60vh] overflow-auto rounded-lg border border-line bg-surface-1 px-4'>
          {(logs.data ?? []).map((entry, index) => (
            <li
              key={`${entry.at?.toISOString() ?? index}-${index}`}
              className='border-b border-line py-3 text-sm break-words text-ink-2 last:border-b-0'
            >
              <time className='mr-2 font-mono text-xs text-faint'>{formatTime(entry.at, locale)}</time>
              <b className={levelColor(entry.level)}>{entry.level}</b> {entry.message}
            </li>
          ))}
        </ol>
      </QueryState>
      </Panel>
    </div>
  )
}

export const logsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/logs',
  component: LogsPage,
})
