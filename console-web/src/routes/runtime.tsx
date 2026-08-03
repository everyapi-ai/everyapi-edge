import { createRoute } from '@tanstack/react-router'
import { useMutation } from '@tanstack/react-query'
import { ZapOff } from 'lucide-react'

import { postJSON } from '@/api/client'
import { useRuntime } from '@/api/queries'
import { Button, PageHeader, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatCount, formatGigabytes, formatTime } from '@/lib/format'

import { rootRoute } from './root'

const RuntimePage = () => {
  const { t, locale } = useTranslation()
  const runtime = useRuntime()
  const unload = useMutation({
    mutationFn: (model: string) => postJSON('/api/runtime/unload', { model }),
    onSuccess: () => void runtime.refetch(),
  })

  const release = (model: string) => {
    if (window.confirm(t('runtime.unloadConfirm', { model }))) unload.mutate(model)
  }

  return (
    <div className='flex flex-col gap-5'>
      <PageHeader title={t('runtime.title')} description={t('runtime.description')} />
      <QueryState isPending={runtime.isPending} isError={runtime.isError} onRetry={() => void runtime.refetch()}>
        <div className='grid gap-4 lg:grid-cols-[minmax(0,0.72fr)_minmax(0,1.28fr)]'>
          <Panel title={t('runtime.connection')}>
            <dl className='grid grid-cols-[auto_1fr] gap-x-5 gap-y-3 text-sm'>
              <dt className='text-muted'>{t('runtime.version')}</dt>
              <dd className='font-mono text-ink'>{runtime.data?.version ?? t('common.unknown')}</dd>
              <dt className='text-muted'>{t('runtime.loadedCount')}</dt>
              <dd className='text-ink'>{formatCount(runtime.data?.models.length ?? 0, locale)}</dd>
            </dl>
          </Panel>

          <Panel title={t('runtime.loadedModels')}>
            {runtime.data?.models.length ? (
              <div className='overflow-x-auto'>
                <table className='w-full min-w-[560px] border-collapse text-sm'>
                  <thead>
                    <tr>
                      {[t('runtime.model'), t('runtime.vram'), t('runtime.context'), t('runtime.expires')].map((heading) => (
                        <th key={heading} scope='col' className='border-b border-line px-3 py-2.5 text-left text-xs font-medium text-faint'>
                          {heading}
                        </th>
                      ))}
                      <th scope='col' className='border-b border-line px-3 py-2.5' />
                    </tr>
                  </thead>
                  <tbody>
                    {runtime.data.models.map((model) => (
                      <tr key={model.name}>
                        <td className='border-b border-line px-3 py-3 font-medium text-ink'>{model.name}</td>
                        <td className='border-b border-line px-3 py-3 text-ink-2'>{formatGigabytes(model.size_vram)}</td>
                        <td className='border-b border-line px-3 py-3 text-ink-2'>
                          {model.context_length ? formatCount(model.context_length, locale) : t('common.unknown')}
                        </td>
                        <td className='border-b border-line px-3 py-3 text-ink-2'>{formatTime(model.expires_at, locale)}</td>
                        <td className='border-b border-line px-3 py-3 text-right'>
                          <Button type='button' variant='ghost' disabled={unload.isPending} onClick={() => release(model.name)} className='inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs'>
                            <ZapOff className='size-3.5' aria-hidden='true' />
                            {t('runtime.unload')}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : <p className='text-sm text-muted'>{t('runtime.empty')}</p>}
          </Panel>
        </div>
      </QueryState>
    </div>
  )
}

export const runtimeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/runtime',
  component: RuntimePage,
})
