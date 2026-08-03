import { createRoute } from '@tanstack/react-router'
import { useMemo, useState } from 'react'

import { useLogs } from '@/api/queries'
import { Input, PageHeader, Panel, QueryState } from '@/components/primitives'
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
  const [levelFilter, setLevelFilter] = useState('')
  const [search, setSearch] = useState('')
  const levels = useMemo(
    () => [...new Set((logs.data ?? []).map((entry) => entry.level.toLowerCase()))].sort((left, right) => left.localeCompare(right)),
    [logs.data],
  )
  const filteredLogs = useMemo(() => {
    const query = search.trim().toLocaleLowerCase()
    return (logs.data ?? []).filter((entry) =>
      (!levelFilter || entry.level.toLowerCase() === levelFilter) &&
      (!query || `${entry.level} ${entry.message}`.toLocaleLowerCase().includes(query)),
    )
  }, [levelFilter, logs.data, search])

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
        <div data-log-filters className='mb-4 grid gap-3 sm:grid-cols-[minmax(0,0.55fr)_minmax(0,1.45fr)]'>
          <div>
            <label htmlFor='log-level' className='mb-1.5 block text-xs font-medium text-ink-2'>{t('logs.filterLevel')}</label>
            <select id='log-level' aria-label={t('logs.filterLevel')} value={levelFilter} onChange={(event) => setLevelFilter(event.target.value)} className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'>
              <option value=''>{t('logs.allLevels')}</option>
              {levels.map((level) => <option key={level} value={level}>{level}</option>)}
            </select>
          </div>
          <div>
            <label htmlFor='log-search' className='mb-1.5 block text-xs font-medium text-ink-2'>{t('logs.search')}</label>
            <Input id='log-search' aria-label={t('logs.search')} value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t('logs.searchPlaceholder')} />
          </div>
        </div>
        <p data-log-count className='mb-3 text-xs text-faint'>{t('logs.matching', { count: filteredLogs.length })}</p>
        <ol className='max-h-[60vh] overflow-auto rounded-lg border border-line bg-surface-1 px-4'>
          {filteredLogs.map((entry, index) => (
            <li
              key={`${entry.at?.toISOString() ?? index}-${index}`}
              data-log-entry
              className='border-b border-line py-3 text-sm break-words text-ink-2 last:border-b-0'
            >
              <time className='mr-2 font-mono text-xs text-faint'>{formatTime(entry.at, locale)}</time>
              <b className={levelColor(entry.level)}>{entry.level}</b> {entry.message}
            </li>
          ))}
        </ol>
        {filteredLogs.length === 0 ? <p data-log-empty className='py-5 text-sm text-muted'>{t('logs.noMatches')}</p> : null}
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
