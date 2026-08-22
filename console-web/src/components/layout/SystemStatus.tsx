import { Radio, TriangleAlert } from 'lucide-react'

import { useOverview } from '@/api/queries'
import { useTranslation } from '@/i18n/useTranslation'
import { formatTime, formatVRAMGigabytes } from '@/lib/format'

const stateTone = (state: string | undefined, failed: boolean) => {
  if (failed || state === 'offline') return 'bg-danger'
  if (!state || state === 'connecting') return 'bg-warn'
  if (state === 'preview') return 'bg-accent'
  return 'bg-good'
}

const gatewayLabel = (
  state: string | undefined,
  failed: boolean,
  pending: boolean,
  t: ReturnType<typeof useTranslation>['t'],
) => {
  if (failed) return t('header.localUnavailable')
  if (pending || !state || state === 'connecting') return t('header.gatewayConnecting')
  if (state === 'online') return t('header.gatewayOnline')
  if (state === 'preview') return t('header.localPreview')
  return t('header.gatewayOffline')
}

export const SystemStatusRail = () => {
  const { t, locale } = useTranslation()
  const overview = useOverview()
  const state = overview.data?.gateway_state
  const label = gatewayLabel(state, overview.isError, overview.isPending, t)
  const totalBytes = (overview.data?.vram_total_gb ?? 0) * 1024 ** 3
  const loadedBytes = overview.data?.loaded_vram_bytes ?? 0
  const usage = totalBytes > 0 ? Math.min(100, (loadedBytes / totalBytes) * 100) : 0
  const scheduledReconnect =
    state === 'offline' &&
    Boolean(overview.data?.gateway_reconnect_attempt && overview.data.gateway_next_reconnect_at)

  return (
    <section data-system-status-rail className='border-line border-t px-4 py-4'>
      <div className='flex items-center justify-between gap-3'>
        <p className='flex min-w-0 items-center gap-2 text-xs font-semibold tracking-[0.02em] text-ink'>
          <span className={`size-2 shrink-0 rounded-full ${stateTone(state, overview.isError)}`} />
          <span className='truncate' aria-live='polite'>
            {label}
          </span>
        </p>
        {overview.isError ? (
          <TriangleAlert className='size-3.5 text-danger' aria-hidden='true' />
        ) : (
          <Radio className='size-3.5 text-ink-faint' aria-hidden='true' />
        )}
      </div>
      <div className='mt-3 flex items-center justify-between font-mono text-[10px] uppercase tracking-[0.1em] text-ink-faint'>
        <span>VRAM</span>
        <span>
          {formatVRAMGigabytes(loadedBytes)} / {overview.data?.vram_total_gb || '—'} GB
        </span>
      </div>
      <div className='mt-1.5 h-1 overflow-hidden bg-surface-3'>
        <span
          className='block h-full bg-accent transition-[width]'
          style={{ width: `${usage}%` }}
        />
      </div>
      {scheduledReconnect ? (
        <p className='mt-2 font-mono text-[10px] leading-4 text-warn'>
          {t('gateway.reconnectAttempt', {
            attempt: overview.data!.gateway_reconnect_attempt,
          })}
          <br />
          {t('gateway.nextRetry')} · {formatTime(overview.data!.gateway_next_reconnect_at, locale)}
        </p>
      ) : null}
      {overview.dataUpdatedAt ? (
        <p className='mt-2 font-mono text-[10px] text-ink-faint'>
          {t('header.updated', { time: formatTime(new Date(overview.dataUpdatedAt), locale) })}
        </p>
      ) : null}
    </section>
  )
}

export const MobileSystemStatus = () => {
  const { t } = useTranslation()
  const overview = useOverview()
  const state = overview.data?.gateway_state
  const label = gatewayLabel(state, overview.isError, overview.isPending, t)

  return (
    <div data-mobile-system-status className='flex items-center gap-2 xl:hidden'>
      <span className={`size-1.5 rounded-full ${stateTone(state, overview.isError)}`} />
      <span className='font-mono text-[10px] uppercase tracking-[0.08em] text-ink-dim'>
        {label}
      </span>
    </div>
  )
}
