import { useMemo, useState } from 'react'

import { useRequests } from '@/api/queries'
import { Input, PageHeader, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatCount, formatTime } from '@/lib/format'

export const TrafficPage = () => {
  const { t, locale } = useTranslation()
  const requests = useRequests()
  const [modelFilter, setModelFilter] = useState('')
  const [resultFilter, setResultFilter] = useState<'ok' | 'error' | ''>('')
  const [search, setSearch] = useState('')
  const models = useMemo(
    () =>
      [...new Set((requests.data ?? []).map((request) => request.model))].sort((left, right) =>
        left.localeCompare(right),
      ),
    [requests.data],
  )
  const filteredRequests = useMemo(() => {
    const query = search.trim().toLocaleLowerCase()
    return (requests.data ?? []).filter(
      (request) =>
        (!modelFilter || request.model === modelFilter) &&
        (!resultFilter || (resultFilter === 'error' ? Boolean(request.error) : !request.error)) &&
        (!query ||
          `${request.consumer} ${request.model} ${request.path} ${request.error}`
            .toLocaleLowerCase()
            .includes(query)),
    )
  }, [modelFilter, requests.data, resultFilter, search])

  const columns = [
    t('traffic.columnCompleted'),
    t('traffic.columnConsumer'),
    t('traffic.columnModel'),
    t('traffic.columnPath'),
    t('traffic.columnUsage'),
    t('traffic.columnDuration'),
    t('traffic.columnResult'),
  ]

  return (
    <div className='flex flex-col gap-5'>
      <PageHeader title={t('traffic.title')} description={t('traffic.description')} />
      <Panel title={t('traffic.title')}>
        <QueryState
          isPending={requests.isPending}
          isError={requests.isError}
          isEmpty={requests.data?.length === 0}
          emptyMessage={t('traffic.empty')}
          onRetry={() => void requests.refetch()}
        >
          <div
            data-traffic-filters
            className='mb-4 grid gap-3 lg:grid-cols-[minmax(0,0.8fr)_minmax(0,0.8fr)_minmax(0,1.4fr)]'
          >
            <div>
              <label
                htmlFor='traffic-model'
                className='mb-1.5 block text-xs font-medium text-ink-2'
              >
                {t('traffic.filterModel')}
              </label>
              <select
                id='traffic-model'
                aria-label={t('traffic.filterModel')}
                value={modelFilter}
                onChange={(event) => setModelFilter(event.target.value)}
                className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 font-mono text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'
              >
                <option value=''>{t('traffic.allModels')}</option>
                {models.map((model) => (
                  <option key={model} value={model}>
                    {model}
                  </option>
                ))}
              </select>
            </div>
            <div>
              <label
                htmlFor='traffic-result'
                className='mb-1.5 block text-xs font-medium text-ink-2'
              >
                {t('traffic.filterResult')}
              </label>
              <select
                id='traffic-result'
                aria-label={t('traffic.filterResult')}
                value={resultFilter}
                onChange={(event) => setResultFilter(event.target.value as 'ok' | 'error' | '')}
                className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'
              >
                <option value=''>{t('traffic.allResults')}</option>
                <option value='ok'>{t('traffic.resultSuccess')}</option>
                <option value='error'>{t('traffic.resultFailure')}</option>
              </select>
            </div>
            <div>
              <label
                htmlFor='traffic-search'
                className='mb-1.5 block text-xs font-medium text-ink-2'
              >
                {t('traffic.search')}
              </label>
              <Input
                id='traffic-search'
                aria-label={t('traffic.search')}
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                placeholder={t('traffic.searchPlaceholder')}
              />
            </div>
          </div>
          <p data-traffic-count className='mb-3 text-xs text-faint'>
            {t('traffic.matching', { count: filteredRequests.length })}
          </p>
          <p className='mb-2 text-xs text-faint xl:hidden'>{t('traffic.tableScrollHint')}</p>
          <div
            role='region'
            aria-label={t('traffic.tableLabel')}
            tabIndex={0}
            className='overflow-x-auto rounded-sm focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none'
          >
            <table className='w-full min-w-[860px] border-collapse text-sm'>
              <thead>
                <tr>
                  {columns.map((heading) => (
                    <th
                      key={heading}
                      scope='col'
                      className='border-b border-line px-3 py-2.5 text-left text-xs font-medium text-faint'
                    >
                      {heading}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {filteredRequests.map((request) => (
                  <tr key={request.id} data-traffic-row>
                    <td className='border-b border-line px-3 py-3 whitespace-nowrap text-ink-2'>
                      {formatTime(request.completed_at, locale)}
                    </td>
                    <td className='max-w-[210px] truncate border-b border-line px-3 py-3 font-medium text-ink'>
                      {request.consumer}
                    </td>
                    <td className='max-w-[210px] truncate border-b border-line px-3 py-3 text-ink-2'>
                      {request.model}
                    </td>
                    <td className='max-w-[210px] truncate border-b border-line px-3 py-3 font-mono text-xs text-muted'>
                      {request.path}
                    </td>
                    <td className='border-b border-line px-3 py-3 whitespace-nowrap text-ink-2'>
                      {formatCount(request.prompt_tokens, locale)} +{' '}
                      {formatCount(request.completion_tokens, locale)}
                    </td>
                    <td className='border-b border-line px-3 py-3 whitespace-nowrap text-ink-2'>
                      {formatCount(request.duration_ms, locale)}ms
                    </td>
                    <td className='max-w-[210px] truncate border-b border-line px-3 py-3'>
                      {request.error ? (
                        <span className='text-danger'>{request.error}</span>
                      ) : (
                        <span className='font-medium text-good'>{t('traffic.ok')}</span>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          {filteredRequests.length === 0 ? (
            <p data-traffic-empty className='py-5 text-sm text-muted'>
              {t('traffic.noMatches')}
            </p>
          ) : null}
        </QueryState>
      </Panel>
    </div>
  )
}
