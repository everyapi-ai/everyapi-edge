import { useMemo, useState } from 'react'

import { useRequests } from '@/api/queries'
import { Input, PageHeader, Panel, QueryState } from '@/components/primitives'
import { TrafficCards } from '@/features/observability/TrafficCards'
import { TrafficTable } from '@/features/observability/TrafficTable'
import { useTranslation } from '@/i18n/useTranslation'

export const TrafficPage = () => {
  const { t } = useTranslation()
  const requests = useRequests()
  const [modelFilter, setModelFilter] = useState('')
  const [resultFilter, setResultFilter] = useState<'ok' | 'error' | ''>('')
  const [capabilityFilter, setCapabilityFilter] = useState('')
  const [search, setSearch] = useState('')
  const models = useMemo(
    () =>
      [...new Set((requests.data ?? []).map((request) => request.model))].sort((left, right) =>
        left.localeCompare(right),
      ),
    [requests.data],
  )
  const capabilities = useMemo(
    () =>
      [
        ...new Set((requests.data ?? []).map((request) => request.capability).filter(Boolean)),
      ].sort(),
    [requests.data],
  )
  const filteredRequests = useMemo(() => {
    const query = search.trim().toLocaleLowerCase()
    return (requests.data ?? []).filter(
      (request) =>
        (!modelFilter || request.model === modelFilter) &&
        (!capabilityFilter || request.capability === capabilityFilter) &&
        (!resultFilter || (resultFilter === 'error' ? Boolean(request.error) : !request.error)) &&
        (!query ||
          `${request.consumer} ${request.model} ${request.path} ${request.error}`
            .toLocaleLowerCase()
            .includes(query)),
    )
  }, [capabilityFilter, modelFilter, requests.data, resultFilter, search])

  return (
    <div className='flex flex-col gap-5'>
      <PageHeader title={t('traffic.title')} description={t('traffic.description')} />
      <Panel title={t('traffic.title')}>
        <QueryState
          isPending={requests.isPending}
          isError={requests.isError}
          error={requests.error}
          isEmpty={requests.data?.length === 0}
          emptyMessage={t('traffic.empty')}
          onRetry={() => void requests.refetch()}
        >
          <div data-traffic-filters className='mb-4 grid gap-3 lg:grid-cols-4'>
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
                htmlFor='traffic-capability'
                className='mb-1.5 block text-xs font-medium text-ink-2'
              >
                {t('traffic.filterCapability')}
              </label>
              <select
                id='traffic-capability'
                value={capabilityFilter}
                onChange={(event) => setCapabilityFilter(event.target.value)}
                className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 font-mono text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'
              >
                <option value=''>{t('traffic.allCapabilities')}</option>
                {capabilities.map((capability) => (
                  <option key={capability}>{capability}</option>
                ))}
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
          <TrafficCards requests={filteredRequests} />
          <TrafficTable requests={filteredRequests} />
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
