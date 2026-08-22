import { Gauge, Info, MessageSquareText, Trash2, ZapOff } from 'lucide-react'

import { Button } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatGigabytes } from '@/lib/format'

import type { InstalledModelsPresentationProps } from './InstalledModelsPanel'

export const InstalledModelCards = ({
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
    <div className='flex flex-col gap-3'>
      {models.map((model) => {
        const loaded = loadedModels.has(model.name)
        return (
          <article
            key={model.name}
            data-installed-model-card={model.name}
            className='rounded-lg border border-line bg-surface-1 p-4'
          >
            <div className='flex items-start justify-between gap-3'>
              <div className='min-w-0'>
                <p className='break-words font-mono text-sm font-semibold text-ink'>{model.name}</p>
                <p className='mt-1 text-xs text-muted'>
                  {providerFor(model.name)} · {typeFor(model.name)}
                </p>
              </div>
              <span
                data-model-residency
                className={`shrink-0 rounded-full px-2 py-0.5 text-xs font-medium ${loaded ? 'bg-accent/14 text-accent' : 'bg-surface-2 text-ink-dim'}`}
              >
                {loaded ? t('models.loaded') : t('models.installed')}
              </span>
            </div>
            <dl className='mt-4 grid grid-cols-2 gap-3 text-xs'>
              <div>
                <dt className='text-faint'>{t('models.columnSize')}</dt>
                <dd className='mt-1 text-ink-2'>{formatGigabytes(model.size)}</dd>
              </div>
              <div>
                <dt className='text-faint'>{t('models.columnDetails')}</dt>
                <dd className='mt-1 text-ink-2'>
                  {model.details?.parameter_size ?? t('common.unknown')} /{' '}
                  {model.details?.quantization_level ?? t('common.unknown')}
                </dd>
              </div>
            </dl>
            <div className='mt-4 grid grid-cols-4 gap-2'>
              <Button
                type='button'
                variant='ghost'
                aria-label={t('models.inspectCapabilities')}
                title={t('models.inspectCapabilities')}
                onClick={() => onInspect(model.name)}
                className='inline-flex min-h-10 items-center justify-center p-0'
              >
                <Info className='size-4' aria-hidden='true' />
              </Button>
              <Button
                type='button'
                variant='ghost'
                aria-label={t('models.openPlayground')}
                title={t('models.openPlayground')}
                onClick={() => onOpen(model.name)}
                className='inline-flex min-h-10 items-center justify-center p-0'
              >
                <MessageSquareText className='size-4' aria-hidden='true' />
              </Button>
              {!isImage(model.name) ? (
                <Button
                  type='button'
                  variant='ghost'
                  aria-label={t('models.benchmark')}
                  title={activeRequests > 0 ? t('models.benchmarkBusy') : t('models.benchmark')}
                  disabled={benchmarkPending || activeRequests > 0}
                  onClick={() => onBenchmark(model.name)}
                  className='inline-flex min-h-10 items-center justify-center p-0'
                >
                  <Gauge className='size-4' aria-hidden='true' />
                </Button>
              ) : (
                <span />
              )}
              {loaded ? (
                <Button
                  type='button'
                  variant='ghost'
                  aria-label={t('runtime.unload')}
                  title={t('runtime.unload')}
                  disabled={unloadPending}
                  onClick={() => onUnload(model.name)}
                  className='inline-flex min-h-10 items-center justify-center p-0'
                >
                  <ZapOff className='size-4' aria-hidden='true' />
                </Button>
              ) : (
                <Button
                  type='button'
                  variant='danger'
                  aria-label={t('models.remove')}
                  title={t('models.remove')}
                  disabled={deletePending}
                  onClick={() => onRemove(model.name)}
                  className='inline-flex min-h-10 items-center justify-center p-0'
                >
                  <Trash2 className='size-4' aria-hidden='true' />
                </Button>
              )}
            </div>
            {loaded ? (
              <Button
                type='button'
                variant='danger'
                aria-label={t('models.remove')}
                title={t('models.unloadBeforeRemove')}
                disabled
                className='mt-2 inline-flex w-full items-center justify-center gap-2'
              >
                <Trash2 className='size-3.5' aria-hidden='true' />
                {t('models.remove')}
              </Button>
            ) : null}
          </article>
        )
      })}
    </div>
  )
}
