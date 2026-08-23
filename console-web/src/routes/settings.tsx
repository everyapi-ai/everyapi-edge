import { useEffect, useState } from 'react'

import { createRoute } from '@tanstack/react-router'

import {
  useResourceSettings,
  useRotatePairingToken,
  useSaveResourcePolicy,
  useSaveUpdateSettings,
  useSetDrain,
  useUpdateSettings,
} from '@/api/queries'
import type { ResourcePolicy } from '@/api/schemas'
import { Button, Input, PageHeader, Panel, QueryState } from '@/components/primitives'
import { useTranslation, type Translate } from '@/i18n/useTranslation'

import { rootRoute } from './root'

const runtimeKeys = ['text', 'image', 'speech', 'video', 'render', 'rerank'] as const
type RuntimeKey = (typeof runtimeKeys)[number]

const runtimeLabel = (t: Translate, runtime: RuntimeKey) =>
  t(
    (
      {
        text: 'settings.runtimeText',
        image: 'settings.runtimeImage',
        speech: 'settings.runtimeSpeech',
        video: 'settings.runtimeVideo',
        render: 'settings.runtimeRender',
        rerank: 'settings.runtimeRerank',
      } as const
    )[runtime],
  )

const SettingsPage = () => {
  const { t } = useTranslation()
  const settings = useResourceSettings()
  const save = useSaveResourcePolicy()
  const drain = useSetDrain()
  const updates = useUpdateSettings()
  const saveUpdates = useSaveUpdateSettings()
  const rotatePairingToken = useRotatePairingToken()
  const [policy, setPolicy] = useState<ResourcePolicy | null>(null)
  const [autoUpdate, setAutoUpdate] = useState(false)
  const [maintenanceStart, setMaintenanceStart] = useState('00:00')
  const [maintenanceEnd, setMaintenanceEnd] = useState('00:00')

  useEffect(() => {
    if (settings.data) setPolicy(settings.data.resource_policy)
  }, [settings.data])

  useEffect(() => {
    if (!updates.data) return
    setAutoUpdate(updates.data.auto_update)
    setMaintenanceStart(updates.data.maintenance_start)
    setMaintenanceEnd(updates.data.maintenance_end)
  }, [updates.data])

  const updatePolicy = (
    runtime: RuntimeKey,
    field: 'max_concurrent' | 'reserve_vram_mb',
    value: string,
  ) => {
    const parsed = Number.parseInt(value, 10)
    setPolicy((current) =>
      current
        ? {
            ...current,
            [runtime]: { ...current[runtime], [field]: Number.isNaN(parsed) ? 0 : parsed },
          }
        : current,
    )
  }

  const policyInput = (runtime: RuntimeKey, field: 'max_concurrent' | 'reserve_vram_mb') => {
    const isConcurrency = field === 'max_concurrent'
    const fieldLabel = t(isConcurrency ? 'settings.maxConcurrent' : 'settings.reserveVRAM')
    return (
      <Input
        type='number'
        min={isConcurrency ? 1 : 0}
        max={isConcurrency ? 64 : undefined}
        step={isConcurrency ? undefined : 256}
        required
        value={policy?.[runtime][field] ?? 0}
        onChange={(event) => updatePolicy(runtime, field, event.target.value)}
        aria-label={`${runtimeLabel(t, runtime)} ${fieldLabel}`}
      />
    )
  }

  const isDrained = settings.data?.drain_state !== 'serving'
  const busy = save.isPending || drain.isPending

  return (
    <div className='flex flex-col gap-5'>
      <PageHeader title={t('settings.title')} description={t('settings.description')} />
      <QueryState
        isPending={settings.isPending}
        isError={settings.isError}
        onRetry={() => void settings.refetch()}
      >
        <div className='grid gap-4 xl:grid-cols-[minmax(0,1.4fr)_minmax(280px,0.6fr)]'>
          <Panel title={t('settings.resources')} className='min-w-0'>
            <p className='mb-4 text-sm leading-6 text-muted'>{t('settings.resourcesHint')}</p>
            {policy ? (
              <form
                onSubmit={(event) => {
                  event.preventDefault()
                  save.mutate(policy)
                }}
              >
                <div className='overflow-x-auto'>
                  <table
                    className='block w-full text-sm md:table md:min-w-[560px] md:border-collapse'
                    aria-label={t('settings.resources')}
                  >
                    <thead className='hidden md:table-header-group'>
                      <tr>
                        <th className='border-b border-line px-3 py-2 text-left text-xs font-medium text-faint'>
                          {t('settings.runtime')}
                        </th>
                        <th className='border-b border-line px-3 py-2 text-left text-xs font-medium text-faint'>
                          {t('settings.maxConcurrent')}
                        </th>
                        <th className='border-b border-line px-3 py-2 text-left text-xs font-medium text-faint'>
                          {t('settings.reserveVRAM')}
                        </th>
                      </tr>
                    </thead>
                    <tbody className='grid gap-3 md:table-row-group'>
                      {runtimeKeys.map((runtime) => (
                        <tr
                          key={runtime}
                          data-resource-policy-card={runtime}
                          className='grid gap-3 rounded-lg border border-line bg-surface-1 p-4 sm:grid-cols-2 md:table-row md:border-0 md:bg-transparent md:p-0'
                        >
                          <th className='block text-left font-medium text-ink sm:col-span-2 md:table-cell md:border-b md:border-line md:px-3 md:py-3'>
                            {runtimeLabel(t, runtime)}
                          </th>
                          <td className='block md:table-cell md:border-b md:border-line md:px-3 md:py-3'>
                            <label className='block text-xs font-medium text-muted md:contents'>
                              <span className='mb-2 block md:hidden'>
                                {t('settings.maxConcurrent')}
                              </span>
                              {policyInput(runtime, 'max_concurrent')}
                            </label>
                          </td>
                          <td className='block md:table-cell md:border-b md:border-line md:px-3 md:py-3'>
                            <label className='block text-xs font-medium text-muted md:contents'>
                              <span className='mb-2 block md:hidden'>
                                {t('settings.reserveVRAM')}
                              </span>
                              {policyInput(runtime, 'reserve_vram_mb')}
                            </label>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
                <div className='mt-4 flex flex-wrap items-center gap-3'>
                  <Button type='submit' disabled={busy}>
                    {save.isPending ? t('settings.saving') : t('settings.save')}
                  </Button>
                  <span className='text-xs text-muted'>{t('settings.saveHint')}</span>
                </div>
                {save.isError ? (
                  <p className='mt-3 text-sm text-danger' role='alert'>
                    {save.error.message}
                  </p>
                ) : null}
              </form>
            ) : null}
          </Panel>

          <Panel title={t('settings.drainTitle')}>
            <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-3 text-sm'>
              <dt className='text-muted'>{t('settings.drainState')}</dt>
              <dd className='font-medium text-ink'>
                {t(`settings.state.${settings.data?.drain_state ?? 'serving'}`)}
              </dd>
              <dt className='text-muted'>{t('settings.activeRequests')}</dt>
              <dd className='font-mono text-ink'>{settings.data?.active_requests ?? 0}</dd>
            </dl>
            <p className='mt-4 text-sm leading-6 text-muted'>{t('settings.drainHint')}</p>
            <Button
              type='button'
              variant={isDrained ? 'primary' : 'danger'}
              disabled={busy}
              onClick={() => drain.mutate(!isDrained)}
              className='mt-4'
            >
              {isDrained ? t('settings.resume') : t('settings.drain')}
            </Button>
            {drain.isError ? (
              <p className='mt-3 text-sm text-danger' role='alert'>
                {drain.error.message}
              </p>
            ) : null}
          </Panel>
        </div>
      </QueryState>

      <QueryState
        isPending={updates.isPending}
        isError={updates.isError}
        onRetry={() => void updates.refetch()}
      >
        <Panel title={t('settings.updateTitle')}>
          <form
            className='grid gap-5 lg:grid-cols-[minmax(260px,0.8fr)_minmax(320px,1.2fr)]'
            onSubmit={(event) => {
              event.preventDefault()
              saveUpdates.mutate({
                auto_update: autoUpdate,
                maintenance_start: maintenanceStart,
                maintenance_end: maintenanceEnd,
              })
            }}
          >
            <div>
              <label className='flex items-start gap-3 text-sm text-ink'>
                <input
                  type='checkbox'
                  checked={autoUpdate}
                  onChange={(event) => setAutoUpdate(event.target.checked)}
                  className='mt-1 h-4 w-4 accent-brand'
                />
                <span>
                  <span className='block font-medium'>{t('settings.autoUpdate')}</span>
                  <span className='mt-1 block leading-6 text-muted'>
                    {t('settings.autoUpdateHint')}
                  </span>
                </span>
              </label>
              <div className='mt-4 grid grid-cols-2 gap-3'>
                <label className='text-sm text-muted'>
                  {t('settings.maintenanceStart')}
                  <Input
                    className='mt-2'
                    type='time'
                    required
                    value={maintenanceStart}
                    onChange={(event) => setMaintenanceStart(event.target.value)}
                  />
                </label>
                <label className='text-sm text-muted'>
                  {t('settings.maintenanceEnd')}
                  <Input
                    className='mt-2'
                    type='time'
                    required
                    value={maintenanceEnd}
                    onChange={(event) => setMaintenanceEnd(event.target.value)}
                  />
                </label>
              </div>
              <p className='mt-3 text-xs leading-5 text-muted'>{t('settings.maintenanceHint')}</p>
              <Button className='mt-4' type='submit' disabled={saveUpdates.isPending}>
                {saveUpdates.isPending ? t('settings.updateSaving') : t('settings.updateSave')}
              </Button>
              {saveUpdates.isError ? (
                <p className='mt-3 text-sm text-danger' role='alert'>
                  {saveUpdates.error.message}
                </p>
              ) : null}
            </div>
            <div>
              <dl className='grid grid-cols-[auto_1fr] gap-x-4 gap-y-2 text-sm'>
                <dt className='text-muted'>{t('settings.installedVersion')}</dt>
                <dd className='font-mono text-ink'>{updates.data?.installed_version || '—'}</dd>
                <dt className='text-muted'>{t('settings.latestVersion')}</dt>
                <dd className='font-mono text-ink'>{updates.data?.latest_version || '—'}</dd>
                <dt className='text-muted'>{t('settings.lastCheck')}</dt>
                <dd className='text-ink'>
                  {formatUpdateTime(updates.data?.last_check_at_unix_ms)}
                </dd>
                <dt className='text-muted'>{t('settings.nextCheck')}</dt>
                <dd className='text-ink'>
                  {formatUpdateTime(updates.data?.next_check_at_unix_ms)}
                </dd>
              </dl>
              {updates.data?.rollback_reason ? (
                <p className='mt-4 rounded-md border border-danger/30 bg-danger/5 px-3 py-2 text-sm text-danger'>
                  {t('settings.rollbackReason')}: {updates.data.rollback_reason}
                </p>
              ) : null}
              <h3 className='mt-5 text-sm font-medium text-ink'>{t('settings.updateHistory')}</h3>
              <ul className='mt-2 space-y-2 text-sm'>
                {updates.data?.history.length ? (
                  updates.data.history
                    .slice()
                    .reverse()
                    .map((entry, index) => (
                      <li
                        key={`${entry.checked_at_unix_ms}-${entry.state}-${index}`}
                        className='flex flex-wrap justify-between gap-2 border-b border-line pb-2'
                      >
                        <span className='text-ink'>
                          {updateStatusLabel(t, entry.state)}
                          {entry.version ? ` · ${entry.version}` : ''}
                        </span>
                        <span className='text-xs text-muted'>
                          {formatUpdateTime(entry.checked_at_unix_ms)}
                        </span>
                      </li>
                    ))
                ) : (
                  <li className='text-muted'>{t('settings.noUpdateHistory')}</li>
                )}
              </ul>
            </div>
          </form>
        </Panel>
      </QueryState>

      <Panel title={t('settings.pairingTitle')}>
        <p className='text-sm leading-6 text-muted'>{t('settings.pairingHint')}</p>
        <Button
          type='button'
          variant='danger'
          disabled={rotatePairingToken.isPending}
          onClick={() => {
            if (window.confirm(t('settings.pairingConfirm'))) rotatePairingToken.mutate()
          }}
          className='mt-4'
        >
          {rotatePairingToken.isPending
            ? t('settings.pairingRotating')
            : t('settings.pairingRotate')}
        </Button>
        {rotatePairingToken.data ? (
          <div className='mt-4 rounded-md border border-line bg-panel-2 p-3'>
            <p className='text-xs text-muted'>{t('settings.pairingNewToken')}</p>
            <code className='mt-2 block break-all select-all font-mono text-sm text-ink'>
              {rotatePairingToken.data.token}
            </code>
            <p className='mt-2 text-xs text-warning'>{t('settings.pairingSignedOut')}</p>
          </div>
        ) : null}
        {rotatePairingToken.isError ? (
          <p className='mt-3 text-sm text-danger' role='alert'>
            {rotatePairingToken.error.message}
          </p>
        ) : null}
      </Panel>
    </div>
  )
}

const formatUpdateTime = (unixMs?: number) => (unixMs ? new Date(unixMs).toLocaleString() : '—')

const updateStatusLabel = (t: Translate, state: string) => {
  switch (state) {
    case 'checking':
      return t('update.state.checking')
    case 'downloading':
      return t('update.state.downloading')
    case 'staged':
      return t('update.state.staged')
    case 'restarting':
      return t('update.state.restarting')
    case 'current':
      return t('update.state.current')
    case 'failed':
      return t('update.state.failed')
    case 'rolled_back':
      return t('update.state.rolled_back')
    default:
      return state
  }
}

export const settingsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/settings',
  component: SettingsPage,
})
