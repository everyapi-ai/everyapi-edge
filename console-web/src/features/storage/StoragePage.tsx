import { useState, type FormEvent } from 'react'

import { useMutation } from '@tanstack/react-query'

import { useModels, useStartStorageMigration, useStorage, useStorageMigration } from '@/api/queries'
import { postJSONResponse } from '@/api/client'
import { migrationPlanSchema, storagePickerSchema } from '@/api/schemas'
import { Button, PageHeader, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatGigabytes } from '@/lib/format'

export const StoragePage = () => {
  const { t } = useTranslation()
  const storage = useStorage()
  const models = useModels()
  const migration = useStorageMigration()
  const startMigration = useStartStorageMigration()
  const [source, setSource] = useState('')
  const [destination, setDestination] = useState('')
  const plan = useMutation({
    mutationFn: ({ source, destination }: { source: string; destination: string }) =>
      postJSONResponse('/api/storage/plan', { source, destination }, migrationPlanSchema),
  })
  const picker = useMutation({
    mutationFn: () => postJSONResponse('/api/storage/pick', {}, storagePickerSchema),
  })

  const prepare = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (destination.trim()) plan.mutate({ source: source.trim(), destination: destination.trim() })
  }

  const copyModels = () => {
    if (!destination.trim()) return
    startMigration.mutate({ source: source.trim(), destination: destination.trim() })
  }

  const chooseFolder = (field: 'source' | 'destination') => {
    picker.mutate(undefined, {
      onSuccess: ({ path }) => {
        if (field === 'source') {
          setSource(path)
          setDestination((current) => current || storage.data?.path || '')
        } else setDestination(path)
        plan.reset()
      },
    })
  }

  const job = migration.data
  const copyPercent = job?.total
    ? Math.min(100, Math.round((job.completed / job.total) * 100))
    : job?.done
      ? 100
      : 0
  const modelsOutsideEdgeStorage = Boolean(
    storage.data?.accessible && storage.data.used_bytes === 0 && models.data?.length,
  )
  const diskUsedPercent = storage.data?.total_bytes
    ? Math.min(
        100,
        Math.round(
          ((storage.data.total_bytes - storage.data.available_bytes) / storage.data.total_bytes) *
            100,
        ),
      )
    : 0
  const selectedSource = source || storage.data?.path || ''

  return (
    <div className='flex flex-col gap-5'>
      <PageHeader title={t('storage.title')} description={t('storage.description')} />
      <QueryState
        isPending={storage.isPending}
        isError={storage.isError}
        onRetry={() => void storage.refetch()}
      >
        <div className='grid gap-4 lg:grid-cols-2'>
          <Panel title={t('storage.location')}>
            <p className='break-all rounded-md border border-line bg-surface-1 px-3 py-2 font-mono text-sm text-ink'>
              {storage.data?.path || t('common.unknown')}
            </p>
            <dl className='mt-4 grid grid-cols-[auto_1fr] gap-x-5 gap-y-3 text-sm'>
              <dt className='text-muted'>{t('storage.used')}</dt>
              <dd className='text-ink'>
                {storage.data?.accessible
                  ? formatGigabytes(storage.data.used_bytes)
                  : t('common.unknown')}
              </dd>
            </dl>
            {storage.data?.accessible && storage.data.total_bytes > 0 ? (
              <section data-storage-capacity className='mt-5 border-t border-line pt-4'>
                <h3 className='text-sm font-medium text-ink'>{t('storage.capacity')}</h3>
                <dl className='mt-3 grid grid-cols-2 gap-x-4 gap-y-3 text-xs'>
                  <div>
                    <dt className='text-faint'>{t('storage.total')}</dt>
                    <dd className='mt-1 font-medium text-ink'>
                      {formatGigabytes(storage.data.total_bytes)}
                    </dd>
                  </div>
                  <div>
                    <dt className='text-faint'>{t('storage.available')}</dt>
                    <dd className='mt-1 font-medium text-good'>
                      {formatGigabytes(storage.data.available_bytes)}
                    </dd>
                  </div>
                </dl>
                <div
                  role='progressbar'
                  aria-label={t('storage.capacity')}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  aria-valuenow={diskUsedPercent}
                  className='mt-3 h-2 overflow-hidden rounded-full bg-surface-2'
                >
                  <span
                    className='block h-full rounded-full bg-accent transition-[width] duration-300'
                    style={{ width: `${diskUsedPercent}%` }}
                  />
                </div>
                <p className='mt-2 text-xs leading-5 text-muted'>{t('storage.capacityHint')}</p>
              </section>
            ) : null}
            {!storage.data?.accessible ? (
              <p className='mt-3 text-sm text-warn'>
                {storage.data?.error || t('storage.unavailable')}
              </p>
            ) : null}
            {modelsOutsideEdgeStorage ? (
              <div
                data-external-models
                className='mt-4 rounded-md border border-warn/30 bg-warn/8 p-3'
              >
                <p className='text-sm font-medium text-warn'>{t('storage.externalModels')}</p>
                <p className='mt-1 text-xs leading-5 text-muted'>
                  {t('storage.externalModelsHint', { path: storage.data?.path ?? '' })}
                </p>
                <Button
                  type='button'
                  variant='ghost'
                  data-import-existing-models
                  disabled={picker.isPending}
                  onClick={() => chooseFolder('source')}
                  className='mt-3'
                >
                  {t('storage.importExisting')}
                </Button>
              </div>
            ) : null}
          </Panel>
          <Panel title={t('storage.migration')}>
            <form onSubmit={prepare}>
              <DirectoryChoice
                label={t('storage.source')}
                path={selectedSource}
                picker='source'
                action={
                  picker.isPending ? t('storage.choosingFolder') : t('storage.chooseSourceFolder')
                }
                description={t('storage.sourceHint')}
                disabled={picker.isPending}
                onChoose={() => chooseFolder('source')}
              />
              <DirectoryChoice
                label={t('storage.destination')}
                path={destination}
                picker='destination'
                action={
                  picker.isPending
                    ? t('storage.choosingFolder')
                    : t('storage.chooseDestinationFolder')
                }
                description={t('storage.destinationHint')}
                disabled={picker.isPending}
                onChoose={() => chooseFolder('destination')}
              />
              <Button
                type='submit'
                disabled={!destination.trim() || plan.isPending}
                className='mt-4'
              >
                {t('storage.prepare')}
              </Button>
            </form>
            {picker.isError ? (
              <p role='alert' className='mt-3 text-sm text-danger'>
                {picker.error.message}
              </p>
            ) : null}
            {plan.data ? (
              plan.data.ready ? (
                <div className='mt-3 rounded-md border border-good/25 bg-good/8 p-3'>
                  <p role='status' className='text-sm text-good'>
                    {t('storage.ready')}
                  </p>
                  <p className='mt-1 text-xs leading-5 text-muted'>{t('storage.copyNotice')}</p>
                  <Button
                    type='button'
                    data-start-storage-migration
                    disabled={startMigration.isPending || (job?.status === 'copying' && !job.done)}
                    onClick={copyModels}
                    className='mt-3'
                  >
                    {startMigration.isPending ? t('storage.copying') : t('storage.copy')}
                  </Button>
                </div>
              ) : (
                <div role='alert' className='mt-3 text-sm text-danger'>
                  <p>{t('storage.blocked')}</p>
                  <ul className='mt-1 list-disc pl-5'>
                    {plan.data.blockers.map((blocker) => (
                      <li key={blocker}>{blocker}</li>
                    ))}
                  </ul>
                </div>
              )
            ) : null}
            {plan.isError ? (
              <p role='alert' className='mt-3 text-sm text-danger'>
                {plan.error.message}
              </p>
            ) : null}
            {startMigration.isError ? (
              <p role='alert' className='mt-3 text-sm text-danger'>
                {startMigration.error.message}
              </p>
            ) : null}
            {job && job.status !== 'idle' ? (
              <div className='mt-4 border-t border-line pt-4'>
                <div className='flex items-center justify-between gap-3 text-xs text-muted'>
                  <span>
                    {job.done
                      ? job.error
                        ? t('storage.copyFailed')
                        : t('storage.copyComplete')
                      : t('storage.copying')}
                  </span>
                  <span>
                    {formatGigabytes(job.completed)} / {formatGigabytes(job.total)}
                  </span>
                </div>
                <div
                  role='progressbar'
                  aria-label={t('storage.copying')}
                  aria-valuenow={copyPercent}
                  aria-valuemin={0}
                  aria-valuemax={100}
                  className='mt-2 h-1.5 overflow-hidden rounded-full bg-surface-2'
                >
                  <span
                    className={`block h-full rounded-full transition-[width] duration-300 ${job.error ? 'bg-danger' : 'bg-accent'}`}
                    style={{ width: `${copyPercent}%` }}
                  />
                </div>
                {job.error ? (
                  <p role='alert' className='mt-2 text-sm text-danger'>
                    {job.error}
                  </p>
                ) : null}
                {job.done && !job.error ? (
                  <p className='mt-2 text-xs leading-5 text-muted'>
                    {t('storage.copyNext', { path: job.destination })}
                  </p>
                ) : null}
              </div>
            ) : null}
          </Panel>
        </div>
      </QueryState>
    </div>
  )
}

function DirectoryChoice({
  label,
  path,
  picker,
  action,
  description,
  disabled,
  onChoose,
}: {
  label: string
  path: string
  picker: 'source' | 'destination'
  action: string
  description: string
  disabled: boolean
  onChoose: () => void
}) {
  return (
    <section className='border-line border-b pb-4 first:pt-0 last:border-b-0'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <h3 className='text-sm font-medium text-ink-2'>{label}</h3>
        <Button
          type='button'
          variant='ghost'
          data-native-storage-picker={picker}
          disabled={disabled}
          onClick={onChoose}
        >
          {action}
        </Button>
      </div>
      <output
        data-storage-source={picker === 'source' ? true : undefined}
        data-storage-destination={picker === 'destination' ? true : undefined}
        className='mt-2 block min-h-10 break-all rounded-md border border-line bg-surface-1 px-3 py-2 font-mono text-xs leading-5 text-ink'
      >
        {path || '—'}
      </output>
      <p className='mt-2 text-xs leading-5 text-muted'>{description}</p>
    </section>
  )
}
