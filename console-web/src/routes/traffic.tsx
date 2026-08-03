import { createRoute } from '@tanstack/react-router'

import { useRequests } from '@/api/queries'
import { PageHeader, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatCount, formatTime } from '@/lib/format'

import { rootRoute } from './root'

const TrafficPage = () => {
  const { t, locale } = useTranslation()
  const requests = useRequests()

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
        <div className='overflow-x-auto'>
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
              {(requests.data ?? []).map((request) => (
                <tr key={request.id}>
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
      </QueryState>
      </Panel>
    </div>
  )
}

export const trafficRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/traffic',
  component: TrafficPage,
})
