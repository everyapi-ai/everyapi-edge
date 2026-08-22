import type { EdgeRequest } from '@/api/schemas'
import { useTranslation } from '@/i18n/useTranslation'
import { formatCount, formatTime } from '@/lib/format'

export const TrafficCards = ({ requests }: { requests: EdgeRequest[] }) => {
  const { t, locale } = useTranslation()

  return (
    <div className='flex flex-col gap-3 md:hidden'>
      {requests.map((request) => (
        <article
          key={request.id}
          data-traffic-card={request.id}
          className='rounded-lg border border-line bg-surface-1 p-4'
        >
          <div className='flex items-start justify-between gap-3'>
            <div className='min-w-0'>
              <p className='break-words text-sm font-semibold text-ink'>{request.consumer}</p>
              <p className='mt-1 break-all font-mono text-xs text-muted'>{request.path}</p>
            </div>
            {request.error ? (
              <span className='shrink-0 text-xs font-medium text-danger'>
                {t('traffic.resultFailure')}
              </span>
            ) : (
              <span className='shrink-0 text-xs font-medium text-good'>{t('traffic.ok')}</span>
            )}
          </div>
          <dl className='mt-4 grid grid-cols-2 gap-3 text-xs'>
            <div>
              <dt className='text-faint'>{t('traffic.columnModel')}</dt>
              <dd className='mt-1 break-words text-ink-2'>{request.model}</dd>
            </div>
            <div>
              <dt className='text-faint'>{t('traffic.columnCompleted')}</dt>
              <dd className='mt-1 text-ink-2'>{formatTime(request.completed_at, locale)}</dd>
            </div>
            <div>
              <dt className='text-faint'>{t('traffic.columnUsage')}</dt>
              <dd className='mt-1 text-ink-2'>
                {formatCount(request.prompt_tokens, locale)} +{' '}
                {formatCount(request.completion_tokens, locale)}
              </dd>
            </div>
            <div>
              <dt className='text-faint'>{t('traffic.columnDuration')}</dt>
              <dd className='mt-1 text-ink-2'>{formatCount(request.duration_ms, locale)}ms</dd>
            </div>
          </dl>
          {request.error ? (
            <p className='mt-3 break-words border-t border-line pt-3 text-xs text-danger'>
              {request.error}
            </p>
          ) : null}
        </article>
      ))}
    </div>
  )
}
