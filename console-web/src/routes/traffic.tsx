import { createRoute } from '@tanstack/react-router'

import { useRequests } from '@/api/queries'
import { Panel, QueryState } from '@/components/primitives'
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
    <Panel title={t('traffic.title')}>
      <QueryState
        isPending={requests.isPending}
        isError={requests.isError}
        isEmpty={requests.data?.length === 0}
        emptyMessage={t('traffic.empty')}
        onRetry={() => void requests.refetch()}
      >
        <div className='overflow-x-auto'>
          <table className='w-full border-collapse text-xs'>
            <thead>
              <tr>
                {columns.map((heading) => (
                  <th
                    key={heading}
                    scope='col'
                    className='border-b border-panel-edge px-1.5 py-2.5 text-left text-[10px] font-normal tracking-[0.12em] text-muted uppercase'
                  >
                    {heading}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {(requests.data ?? []).map((request) => (
                <tr key={request.id}>
                  <td className='border-b border-panel-edge px-1.5 py-2.5 whitespace-nowrap'>
                    {formatTime(request.completed_at, locale)}
                  </td>
                  <td className='max-w-[210px] truncate border-b border-panel-edge px-1.5 py-2.5'>
                    {request.consumer}
                  </td>
                  <td className='max-w-[210px] truncate border-b border-panel-edge px-1.5 py-2.5'>
                    {request.model}
                  </td>
                  <td className='max-w-[210px] truncate border-b border-panel-edge px-1.5 py-2.5 text-muted'>
                    {request.path}
                  </td>
                  <td className='border-b border-panel-edge px-1.5 py-2.5 whitespace-nowrap'>
                    {formatCount(request.prompt_tokens, locale)} +{' '}
                    {formatCount(request.completion_tokens, locale)}
                  </td>
                  <td className='border-b border-panel-edge px-1.5 py-2.5 whitespace-nowrap'>
                    {formatCount(request.duration_ms, locale)}ms
                  </td>
                  <td className='max-w-[210px] truncate border-b border-panel-edge px-1.5 py-2.5'>
                    {request.error ? (
                      <span className='text-danger'>{request.error}</span>
                    ) : (
                      <span className='text-lime'>{t('traffic.ok')}</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </QueryState>
    </Panel>
  )
}

export const trafficRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/traffic',
  component: TrafficPage,
})
