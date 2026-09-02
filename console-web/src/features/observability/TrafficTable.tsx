import type { EdgeRequest } from '@/api/schemas'
import { useTranslation } from '@/i18n/useTranslation'
import { formatCount, formatTime } from '@/lib/format'

export const TrafficTable = ({ requests }: { requests: EdgeRequest[] }) => {
  const { t, locale } = useTranslation()
  const columns = [
    t('traffic.columnCompleted'),
    t('traffic.columnConsumer'),
    t('traffic.columnModel'),
    t('traffic.columnPath'),
    t('traffic.columnCapability'),
    t('traffic.columnUsage'),
    t('traffic.columnTTFT'),
    t('traffic.columnDuration'),
    t('traffic.columnResult'),
  ]

  return (
    <div
      role='region'
      aria-label={t('traffic.tableLabel')}
      tabIndex={0}
      className='hidden overflow-x-auto rounded-sm focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none md:block'
    >
      <table className='w-full min-w-[960px] border-collapse text-sm'>
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
          {requests.map((request) => (
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
              <td className='max-w-[180px] truncate border-b border-line px-3 py-3 font-mono text-xs text-accent'>
                {request.capability || '—'}
              </td>
              <td className='border-b border-line px-3 py-3 whitespace-nowrap text-ink-2'>
                {formatCount(request.prompt_tokens, locale)} +{' '}
                {formatCount(request.completion_tokens, locale)}
              </td>
              <td className='border-b border-line px-3 py-3 whitespace-nowrap text-ink-2'>
                {request.ttft_ms ? `${formatCount(request.ttft_ms, locale)}ms` : '—'}
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
  )
}
