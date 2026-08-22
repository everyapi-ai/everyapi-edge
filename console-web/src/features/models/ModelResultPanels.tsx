import type { ModelBenchmark, ModelCapabilities } from '@/api/schemas'
import { useTranslation } from '@/i18n/useTranslation'

export const ModelResultPanels = ({
  benchmark,
  inspectedModel,
  capabilities,
  capabilitiesPending,
  capabilitiesError,
  capabilityLabel,
}: {
  benchmark?: ModelBenchmark
  inspectedModel: string
  capabilities?: ModelCapabilities
  capabilitiesPending: boolean
  capabilitiesError?: string
  capabilityLabel: (capability: string) => string
}) => {
  const { t } = useTranslation()
  return (
    <>
      {benchmark ? (
        <section
          data-model-benchmark
          className='mt-4 rounded-md border border-good/25 bg-good/8 p-3'
        >
          <h3 className='text-sm font-medium text-good'>{t('models.benchmarkTitle')}</h3>
          <dl className='mt-3 grid grid-cols-2 gap-x-4 gap-y-3 text-xs sm:grid-cols-4'>
            <div>
              <dt className='text-faint'>{t('models.columnModel')}</dt>
              <dd className='mt-1 font-mono text-ink'>{benchmark.model}</dd>
            </div>
            <div>
              <dt className='text-faint'>{t('models.benchmarkRate')}</dt>
              <dd className='mt-1 font-medium text-good'>
                {benchmark.tokens_per_second.toFixed(1)} {t('models.benchmarkRateUnit')}
              </dd>
            </div>
            <div>
              <dt className='text-faint'>{t('models.benchmarkTokens')}</dt>
              <dd className='mt-1 font-medium text-ink'>{benchmark.eval_count}</dd>
            </div>
            <div>
              <dt className='text-faint'>{t('models.benchmarkDuration')}</dt>
              <dd className='mt-1 font-medium text-ink'>
                {(benchmark.total_duration_ns / 1e9).toFixed(1)} s
              </dd>
            </div>
          </dl>
          <p className='mt-3 text-xs leading-5 text-muted'>{t('models.benchmarkHint')}</p>
        </section>
      ) : null}
      {inspectedModel ? (
        <section
          data-model-capabilities
          className='mt-4 rounded-md border border-line bg-surface-1 p-3'
        >
          <h3 className='text-sm font-medium text-ink'>{t('models.capabilitiesTitle')}</h3>
          <p className='mt-1 font-mono text-xs text-muted'>{inspectedModel}</p>
          {capabilitiesPending ? (
            <p className='mt-3 text-sm text-muted'>{t('models.capabilitiesLoading')}</p>
          ) : null}
          {capabilitiesError ? (
            <p role='alert' className='mt-3 text-sm text-danger'>
              {capabilitiesError}
            </p>
          ) : null}
          {capabilities ? (
            capabilities.capabilities.length ? (
              <div className='mt-3 flex flex-wrap gap-2'>
                {capabilities.capabilities.map((capability) => (
                  <span
                    key={capability}
                    className='rounded-full bg-accent/14 px-2.5 py-1 text-xs font-medium text-accent'
                  >
                    {capabilityLabel(capability)}
                  </span>
                ))}
              </div>
            ) : (
              <p className='mt-3 text-sm text-muted'>{t('models.capabilitiesEmpty')}</p>
            )
          ) : null}
        </section>
      ) : null}
    </>
  )
}
