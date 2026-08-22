import { Gauge, Info, MessageSquareText, Trash2, ZapOff } from 'lucide-react'

import { Button } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatGigabytes } from '@/lib/format'

import type { InstalledModelsPresentationProps } from './InstalledModelsPanel'

export const InstalledModelTable = ({
  models,
  loadedModels,
  activeRequests,
  benchmarkPending,
  unloadPending,
  deletePending,
  providerFor,
  typeFor,
  isImage,
  onInspect,
  onOpen,
  onBenchmark,
  onUnload,
  onRemove,
}: InstalledModelsPresentationProps) => {
  const { t } = useTranslation()

  return (
    <div
      role='region'
      aria-label={t('models.tableLabel')}
      tabIndex={0}
      className='max-w-full overflow-x-auto rounded-sm focus-visible:ring-2 focus-visible:ring-accent/50 focus-visible:outline-none'
    >
      <table className='w-full min-w-[800px] border-collapse text-sm'>
        <thead>
          <tr>
            {[
              t('models.columnProvider'),
              t('models.columnModel'),
              t('models.columnStatus'),
              t('models.columnType'),
              t('models.columnSize'),
              t('models.columnDetails'),
            ].map((heading) => (
              <th
                key={heading}
                scope='col'
                className='border-b border-line px-3 py-2.5 text-left text-xs font-medium whitespace-nowrap text-faint'
              >
                {heading}
              </th>
            ))}
            <th scope='col' className='border-b border-line px-3 py-2.5' />
          </tr>
        </thead>
        <tbody>
          {models.map((model) => {
            const loaded = loadedModels.has(model.name)
            return (
              <tr key={model.name} data-installed-model={model.name}>
                <td
                  data-model-provider
                  className='border-b border-line px-3 py-3 whitespace-nowrap text-ink-2'
                >
                  {providerFor(model.name)}
                </td>
                <td className='border-b border-line px-3 py-3 font-medium whitespace-nowrap text-ink'>
                  {model.name}
                </td>
                <td className='border-b border-line px-3 py-3 whitespace-nowrap text-muted'>
                  <span
                    data-model-residency
                    className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${loaded ? 'bg-accent/14 text-accent' : 'bg-surface-2 text-ink-dim'}`}
                  >
                    {loaded ? t('models.loaded') : t('models.installed')}
                  </span>
                </td>
                <td
                  data-model-kind
                  className='border-b border-line px-3 py-3 whitespace-nowrap text-muted'
                >
                  {typeFor(model.name)}
                </td>
                <td className='border-b border-line px-3 py-3 whitespace-nowrap text-ink-2'>
                  {formatGigabytes(model.size)}
                </td>
                <td className='border-b border-line px-3 py-3 whitespace-nowrap text-muted'>
                  {model.details?.parameter_size ?? t('common.unknown')} /{' '}
                  {model.details?.quantization_level ?? t('common.unknown')}
                </td>
                <td className='border-b border-line px-3 py-3 text-right'>
                  <div className='flex justify-end gap-2'>
                    <Button
                      type='button'
                      variant='ghost'
                      aria-label={t('models.inspectCapabilities')}
                      title={t('models.inspectCapabilities')}
                      onClick={() => onInspect(model.name)}
                      className='inline-flex size-9 items-center justify-center p-0'
                    >
                      <Info className='size-3.5' aria-hidden='true' />
                    </Button>
                    <Button
                      type='button'
                      variant='ghost'
                      aria-label={t('models.openPlayground')}
                      title={t('models.openPlayground')}
                      onClick={() => onOpen(model.name)}
                      className='inline-flex size-9 items-center justify-center p-0'
                    >
                      <MessageSquareText className='size-3.5' aria-hidden='true' />
                    </Button>
                    {!isImage(model.name) ? (
                      <Button
                        type='button'
                        variant='ghost'
                        aria-label={t('models.benchmark')}
                        title={
                          activeRequests > 0 ? t('models.benchmarkBusy') : t('models.benchmark')
                        }
                        disabled={benchmarkPending || activeRequests > 0}
                        onClick={() => onBenchmark(model.name)}
                        className='inline-flex size-9 items-center justify-center p-0'
                      >
                        <Gauge className='size-3.5' aria-hidden='true' />
                      </Button>
                    ) : null}
                    {loaded ? (
                      <Button
                        type='button'
                        variant='ghost'
                        aria-label={t('runtime.unload')}
                        title={t('runtime.unload')}
                        disabled={unloadPending}
                        onClick={() => onUnload(model.name)}
                        className='inline-flex size-9 items-center justify-center p-0'
                      >
                        <ZapOff className='size-3.5' aria-hidden='true' />
                      </Button>
                    ) : null}
                    <Button
                      type='button'
                      variant='danger'
                      aria-label={t('models.remove')}
                      title={loaded ? t('models.unloadBeforeRemove') : t('models.remove')}
                      disabled={deletePending || loaded}
                      onClick={() => onRemove(model.name)}
                      className='inline-flex size-9 items-center justify-center p-0'
                    >
                      <Trash2 className='size-3.5' aria-hidden='true' />
                    </Button>
                  </div>
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
