import { createRoute } from '@tanstack/react-router'
import { ZapOff } from 'lucide-react'

import { useOverview, useRuntime, useUnloadAllRuntimeModels, useUnloadRuntimeModel } from '@/api/queries'
import { Button, PageHeader, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatCount, formatTime, formatVRAMGigabytes } from '@/lib/format'

import { rootRoute } from './root'

const RuntimePage = () => {
  const { t, locale } = useTranslation()
  const overview = useOverview()
  const runtime = useRuntime()
  const unload = useUnloadRuntimeModel()
  const unloadAll = useUnloadAllRuntimeModels()

  const release = (model: string) => {
    if (window.confirm(t('runtime.unloadConfirm', { model }))) unload.mutate(model)
  }

  const releaseAll = () => {
    const count = runtime.data?.models.length ?? 0
    if (count && window.confirm(t('runtime.unloadAllConfirm', { count: formatCount(count, locale) }))) unloadAll.mutate()
  }

  const totalMemoryBytes = (overview.data?.vram_total_gb ?? 0) * (1024 ** 3)
  const loadedMemoryBytes = overview.data?.loaded_vram_bytes ?? 0
  const reservedMemoryBytes = overview.data?.reserved_vram_bytes ?? 0
  const availableMemoryBytes = overview.data?.available_vram_bytes ?? 0
  const loadedPercent = totalMemoryBytes ? Math.min(100, Math.round((loadedMemoryBytes / totalMemoryBytes) * 100)) : 0
  const reservedPercent = totalMemoryBytes ? Math.min(100 - loadedPercent, Math.round((reservedMemoryBytes / totalMemoryBytes) * 100)) : 0

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
            {runtime.data?.models.length ? (
              <Button
                type='button'
                variant='ghost'
                disabled={unload.isPending || unloadAll.isPending}
                onClick={releaseAll}
                data-unload-all-models
                className='mt-4 inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs'
              >
                <ZapOff className='size-3.5' aria-hidden='true' />
                {t('runtime.unloadAll')}
              </Button>
            ) : null}
            {overview.data?.vram_total_gb ? (
              <section data-runtime-memory-budget className='mt-5 border-t border-line pt-4'>
                <h3 className='text-sm font-medium text-ink'>{t('runtime.memoryBudget')}</h3>
                <dl className='mt-3 grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-4'>
                  <div>
                    <dt className='text-faint'>{t('runtime.memoryTotal')}</dt>
                    <dd className='mt-1 font-medium text-ink'>{formatVRAMGigabytes(totalMemoryBytes)}</dd>
                  </div>
                  <div>
                    <dt className='text-faint'>{t('runtime.memoryUsed')}</dt>
                    <dd className='mt-1 font-medium text-accent'>{formatVRAMGigabytes(loadedMemoryBytes)}</dd>
                  </div>
                  <div>
                    <dt className='text-faint'>{t('runtime.memoryReserved')}</dt>
                    <dd className='mt-1 font-medium text-warn'>{formatVRAMGigabytes(reservedMemoryBytes)}</dd>
                  </div>
                  <div>
                    <dt className='text-faint'>{t('runtime.memoryAvailable')}</dt>
                    <dd className='mt-1 font-medium text-good'>{formatVRAMGigabytes(availableMemoryBytes)}</dd>
                  </div>
                </dl>
                <div
                  role='progressbar'
                  aria-label={t('runtime.memoryBudget')}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={loadedPercent}
                  className='mt-3 flex h-2 overflow-hidden rounded-full bg-surface-2'
                >
                  <span className='bg-accent transition-[width] duration-300' style={{ width: `${loadedPercent}%` }} />
                  <span className='bg-warn/80 transition-[width] duration-300' style={{ width: `${reservedPercent}%` }} />
                </div>
                <p className='mt-2 text-xs leading-5 text-muted'>{t('runtime.memoryHint')}</p>
              </section>
            ) : null}
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
                        <td className='border-b border-line px-3 py-3 text-ink-2'>{formatVRAMGigabytes(model.size_vram)}</td>
                        <td className='border-b border-line px-3 py-3 text-ink-2'>
                          {model.context_length ? formatCount(model.context_length, locale) : t('common.unknown')}
                        </td>
                        <td className='border-b border-line px-3 py-3 text-ink-2'>{formatTime(model.expires_at, locale)}</td>
                        <td className='border-b border-line px-3 py-3 text-right'>
                          <Button type='button' variant='ghost' disabled={unload.isPending || unloadAll.isPending} onClick={() => release(model.name)} className='inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs'>
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
