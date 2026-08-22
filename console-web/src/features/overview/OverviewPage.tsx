import { useNavigate } from '@tanstack/react-router'
import { Boxes, HardDrive, MessageSquareText } from 'lucide-react'

import {
  useModels,
  useNodeProfile,
  useOverview,
  useRuntime,
  useSettlements,
  useStorage,
  useUpdateAgent,
} from '@/api/queries'
import { Button, PageHeader, Panel, QueryState, StatCard } from '@/components/primitives'
import { CapacityRail } from '@/components/ui/CapacityRail'
import { NodeDetails } from '@/features/overview/NodeDetails'
import { useTranslation } from '@/i18n/useTranslation'
import { formatCount, formatGigabytes, formatTime, formatUSDMicros } from '@/lib/format'

export const OverviewPage = () => {
  const { t, locale } = useTranslation()
  const overview = useOverview()
  const nodeProfile = useNodeProfile()
  const settlements = useSettlements()
  const models = useModels()
  const runtime = useRuntime()
  const storage = useStorage()
  const update = useUpdateAgent()
  const navigate = useNavigate()

  const stats = overview.data
  const profile = nodeProfile.data
  const profileHardware =
    [profile?.gpu_model, profile?.vram_total_gb ? `${profile.vram_total_gb} GB` : '']
      .filter(Boolean)
      .join(' · ') || t('common.unknown')
  const placeholder = t('common.unknown')
  const gatewayStatus =
    stats?.gateway_state === 'online'
      ? t('gateway.connected')
      : stats?.gateway_state === 'preview'
        ? t('gateway.preview')
        : stats?.gateway_state === 'offline'
          ? t('gateway.disconnected')
          : t('gateway.connecting')
  const runtimeCount = runtime.data?.models.length
  const modelCount = models.data?.length
  const modelDirectoryMismatch = Boolean(
    storage.data?.accessible && storage.data.used_bytes === 0 && modelCount,
  )
  const scheduledReconnect =
    stats?.gateway_state === 'offline' &&
    Boolean(stats.gateway_reconnect_attempt && stats.gateway_next_reconnect_at)
  const updateStateLabel = stats?.update_state
    ? ((
        {
          checking: t('update.state.checking'),
          downloading: t('update.state.downloading'),
          restarting: t('update.state.restarting'),
          current: t('update.state.current'),
          failed: t('update.state.failed'),
        } as Record<string, string>
      )[stats.update_state] ?? stats.update_state)
    : ''
  const updateAgent = () => {
    if (window.confirm(t('update.confirm'))) update.mutate()
  }
  const readiness = [
    {
      label: t('overview.readinessGateway'),
      value: gatewayStatus,
      tone:
        stats?.gateway_state === 'online'
          ? 'text-good'
          : stats?.gateway_state === 'offline'
            ? 'text-danger'
            : stats?.gateway_state === 'preview'
              ? 'text-accent'
              : 'text-warn',
    },
    {
      label: t('overview.readinessRuntime'),
      value: runtime.isError
        ? t('overview.unavailable')
        : runtimeCount === undefined
          ? t('common.unknown')
          : t('overview.loadedModels', { count: formatCount(runtimeCount, locale) }),
      tone: runtime.isError ? 'text-danger' : 'text-ink',
    },
    {
      label: t('overview.readinessModels'),
      value: models.isError
        ? t('overview.unavailable')
        : modelCount === undefined
          ? t('common.unknown')
          : t('overview.installedModels', { count: formatCount(modelCount, locale) }),
      tone: models.isError ? 'text-danger' : 'text-ink',
    },
    {
      label: t('overview.readinessStorage'),
      value: modelDirectoryMismatch
        ? t('overview.storageExternal')
        : storage.data?.accessible
          ? t('overview.storageUsage', { size: formatGigabytes(storage.data.used_bytes) })
          : storage.isError || storage.data
            ? t('overview.unavailable')
            : t('common.unknown'),
      tone: modelDirectoryMismatch
        ? 'text-warn'
        : storage.data?.accessible
          ? 'text-ink'
          : storage.isError || storage.data
            ? 'text-danger'
            : 'text-ink',
    },
  ]

  return (
    <div data-command-center className='flex flex-col gap-5'>
      <PageHeader title={t('header.title')} description={t('overview.description')} />
      <CapacityRail
        loadedBytes={stats?.loaded_vram_bytes ?? 0}
        reservedBytes={stats?.reserved_vram_bytes ?? 0}
        totalGB={stats?.vram_total_gb ?? 0}
      />
      <QueryState
        isPending={overview.isPending}
        isError={overview.isError}
        onRetry={() => void overview.refetch()}
      >
        <section className='grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4'>
          <StatCard
            label={t('stat.active')}
            value={stats ? formatCount(stats.active_requests, locale) : placeholder}
            hint={t('stat.activeHint')}
          />
          <StatCard
            label={t('stat.completed')}
            value={stats ? formatCount(stats.completed_requests, locale) : placeholder}
            hint={t('stat.completedHint')}
          />
          <StatCard
            label={t('stat.tokens')}
            value={stats ? formatCount(stats.completion_tokens, locale) : placeholder}
            hint={t('stat.tokensHint')}
          />
          <StatCard
            label={t('stat.earnings')}
            value={
              stats?.settled_earnings_available
                ? formatUSDMicros(stats.settled_earnings_micros)
                : t('stat.earningsPending')
            }
            hint={t('stat.earningsHint')}
          />
        </section>
      </QueryState>

      <div className='grid grid-cols-1 items-start gap-4 xl:grid-cols-2'>
        <Panel title={t('gateway.title')}>
          <p
            className={`text-sm font-medium ${stats?.gateway_state === 'online' ? 'text-good' : stats?.gateway_state === 'offline' ? 'text-danger' : stats?.gateway_state === 'preview' ? 'text-accent' : 'text-warn'}`}
          >
            {gatewayStatus}
          </p>
          <dl className='mt-4 grid grid-cols-[auto_1fr] gap-x-5 gap-y-3 text-sm'>
            <dt className='text-muted'>{t('gateway.lastConnected')}</dt>
            <dd className='text-ink'>
              {stats?.gateway_last_connected_at
                ? formatTime(stats.gateway_last_connected_at, locale)
                : t('common.unknown')}
            </dd>
            <dt className='text-muted'>{t('gateway.roundTrip')}</dt>
            <dd className='text-ink'>
              {stats?.gateway_round_trip_ms
                ? `${stats.gateway_round_trip_ms} ms`
                : t('common.unknown')}
            </dd>
            {scheduledReconnect ? (
              <div data-gateway-reconnect className='contents'>
                <dt className='text-muted'>{t('gateway.nextRetry')}</dt>
                <dd className='text-ink'>
                  {formatTime(stats!.gateway_next_reconnect_at!, locale)} ·{' '}
                  {t('gateway.reconnectAttempt', { attempt: stats!.gateway_reconnect_attempt })}
                </dd>
              </div>
            ) : null}
          </dl>
          {stats?.gateway_last_error ? (
            <p className='mt-3 text-xs leading-5 text-muted'>{stats.gateway_last_error}</p>
          ) : null}
        </Panel>
        <Panel title={t('overview.readiness')}>
          <dl
            data-node-readiness
            className='grid grid-cols-[minmax(0,1fr)_auto] gap-x-5 gap-y-3 text-sm'
          >
            {readiness.map((item) => (
              <div key={item.label} className='contents'>
                <dt className='text-muted'>{item.label}</dt>
                <dd className={`text-right font-medium ${item.tone}`}>{item.value}</dd>
              </div>
            ))}
          </dl>
          <div data-overview-actions className='mt-5 border-t border-line pt-4'>
            <p className='text-xs font-medium text-muted'>{t('overview.actionsHint')}</p>
            <div className='mt-3 grid gap-2 sm:grid-cols-3'>
              <Button
                type='button'
                variant='ghost'
                onClick={() => void navigate({ to: '/models' })}
                className='inline-flex items-center justify-center gap-2'
              >
                <Boxes className='size-3.5' aria-hidden='true' />
                {t('overview.openModels')}
              </Button>
              <Button
                type='button'
                variant='ghost'
                onClick={() => void navigate({ to: '/playground' })}
                className='inline-flex items-center justify-center gap-2'
              >
                <MessageSquareText className='size-3.5' aria-hidden='true' />
                {t('overview.openPlayground')}
              </Button>
              <Button
                type='button'
                variant='ghost'
                onClick={() => void navigate({ to: '/storage' })}
                className='inline-flex items-center justify-center gap-2'
              >
                <HardDrive className='size-3.5' aria-hidden='true' />
                {t('overview.openStorage')}
              </Button>
            </div>
          </div>
        </Panel>
      </div>
      <NodeDetails
        profile={profile}
        overview={stats}
        settlements={settlements.data}
        settlementsPending={settlements.isPending}
        settlementsError={settlements.isError}
        updatePending={update.isPending}
        updateError={update.error?.message}
        updateStateLabel={updateStateLabel}
        profileHardware={profileHardware}
        onRetrySettlements={() => void settlements.refetch()}
        onUpdate={updateAgent}
      />
    </div>
  )
}
