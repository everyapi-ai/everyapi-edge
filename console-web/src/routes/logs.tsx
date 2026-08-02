import { createRoute } from '@tanstack/react-router'

import { useLogs } from '@/api/queries'
import { Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatTime } from '@/lib/format'

import { rootRoute } from './root'

const levelColor = (level: string): string => {
  const normalized = level.toLowerCase()
  if (normalized === 'error' || normalized === 'fatal') return 'text-danger'
  if (normalized === 'warn' || normalized === 'warning') return 'text-amber'
  return 'text-lime'
}

const LogsPage = () => {
  const { t, locale } = useTranslation()
  const logs = useLogs()

  return (
    <Panel title={t('logs.title')}>
      <QueryState
        isPending={logs.isPending}
        isError={logs.isError}
        isEmpty={logs.data?.length === 0}
        emptyMessage={t('logs.empty')}
        onRetry={() => void logs.refetch()}
      >
        <ol className='max-h-[60vh] overflow-auto border border-panel-edge bg-[#0d120e] px-3'>
          {(logs.data ?? []).map((entry, index) => (
            <li
              key={`${entry.at?.toISOString() ?? index}-${index}`}
              className='border-b border-[#202a23] py-2.5 text-xs break-words text-[#cbd1c8] last:border-b-0'
            >
              <time className='mr-2 text-[#687269]'>{formatTime(entry.at, locale)}</time>
              <b className={levelColor(entry.level)}>[{entry.level}]</b> {entry.message}
            </li>
          ))}
        </ol>
      </QueryState>
    </Panel>
  )
}

export const logsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/logs',
  component: LogsPage,
})
