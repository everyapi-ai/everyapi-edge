import { ChevronDown, RefreshCw, ShieldCheck } from 'lucide-react'

import type { NodeProfile, Overview, Settlement, UpdateSettings } from '@/api/schemas'
import { Button, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatTime, formatUSDMicros } from '@/lib/format'

type NodeDetailsProps = {
  profile?: NodeProfile
  overview?: Overview
  settlements?: Settlement[]
  settlementsPending: boolean
  settlementsError: boolean
  settlementsErrorDetail?: unknown
  updatePending: boolean
  updateError?: string
  updateStateLabel: string
  automaticUpdateSettings?: UpdateSettings
  automaticUpdatePending: boolean
  automaticUpdateError?: string
  profileHardware: string
  onRetrySettlements: () => void
  onUpdate: () => void
  onAutomaticUpdateChange: (enabled: boolean) => void
}

export const NodeDetails = ({
  profile,
  overview,
  settlements,
  settlementsPending,
  settlementsError,
  settlementsErrorDetail,
  updatePending,
  updateError,
  updateStateLabel,
  automaticUpdateSettings,
  automaticUpdatePending,
  automaticUpdateError,
  profileHardware,
  onRetrySettlements,
  onUpdate,
  onAutomaticUpdateChange,
}: NodeDetailsProps) => {
  const { t, locale } = useTranslation()

  return (
    <details data-node-details className='group rounded-xl border border-line bg-surface-0'>
      <summary className='flex cursor-pointer list-none items-center justify-between gap-3 px-5 py-4 text-sm font-semibold text-ink marker:hidden'>
        <span>{t('overview.nodeDetails')}</span>
        <ChevronDown
          className='size-4 shrink-0 text-faint transition-transform group-open:rotate-180'
          aria-hidden='true'
        />
      </summary>
      <div className='grid grid-cols-1 gap-4 border-t border-line p-4 xl:grid-cols-2'>
        <Panel title={t('nodeProfile.title')}>
          <dl data-node-profile className='grid grid-cols-[auto_1fr] gap-x-5 gap-y-3 text-sm'>
            <dt className='text-muted'>{t('nodeProfile.name')}</dt>
            <dd className='font-medium text-ink'>{profile?.name || t('common.unknown')}</dd>
            <dt className='text-muted'>{t('nodeProfile.hardware')}</dt>
            <dd className='text-ink'>{profileHardware}</dd>
            <dt className='text-muted'>{t('nodeProfile.platform')}</dt>
            <dd className='font-mono text-xs text-ink'>
              {profile?.platform || t('common.unknown')}
            </dd>
            <dt className='text-muted'>{t('nodeProfile.agent')}</dt>
            <dd className='font-mono text-xs text-ink'>
              {profile?.agent_version || t('common.unknown')}
            </dd>
            <dt className='text-muted'>{t('nodeProfile.location')}</dt>
            <dd className='text-ink'>{profile?.country_iso2 || t('common.unknown')}</dd>
          </dl>
        </Panel>
        <Panel title={t('update.title')}>
          <dl className='grid grid-cols-[auto_1fr] gap-x-5 gap-y-3 text-sm'>
            <dt className='text-muted'>{t('update.currentVersion')}</dt>
            <dd className='text-right font-mono text-ink'>
              v{overview?.agent_version || t('common.unknown')}
            </dd>
            {overview?.update_state ? (
              <>
                <dt className='text-muted'>{t('update.status')}</dt>
                <dd className='text-right text-ink'>{updateStateLabel}</dd>
              </>
            ) : null}
          </dl>
          {overview?.update_error || updateError ? (
            <p role='alert' className='mt-3 text-sm text-danger'>
              {overview?.update_error || updateError}
            </p>
          ) : null}
          <Button
            type='button'
            onClick={onUpdate}
            disabled={
              updatePending ||
              ['checking', 'downloading', 'restarting'].includes(overview?.update_state ?? '')
            }
            className='mt-4 inline-flex items-center gap-2'
          >
            <RefreshCw className='size-3.5' aria-hidden='true' />
            {t('update.action')}
          </Button>
          <div className='mt-4 border-t border-line pt-4'>
            <label className='flex items-start gap-3 text-sm text-ink'>
              <input
                type='checkbox'
                checked={automaticUpdateSettings?.auto_update ?? false}
                disabled={automaticUpdatePending || !automaticUpdateSettings}
                onChange={(event) => onAutomaticUpdateChange(event.target.checked)}
                className='mt-0.5 size-4 accent-accent'
              />
              <span>
                <span className='block font-medium'>{t('update.autoLabel')}</span>
                <span className='mt-1 block text-xs leading-5 text-muted'>
                  {t('update.autoDescription', {
                    hours: automaticUpdateSettings?.check_interval_hours ?? 24,
                  })}
                </span>
              </span>
            </label>
            {automaticUpdateError ? (
              <p role='alert' className='mt-2 text-sm text-danger'>
                {automaticUpdateError}
              </p>
            ) : null}
          </div>
        </Panel>
        <Panel title={t('settlement.title')}>
          <p className='rounded-lg border border-warn/22 bg-warn/10 p-3 text-sm leading-5 text-ink-2'>
            {t('settlement.notice')}
          </p>
          <QueryState
            isPending={settlementsPending}
            isError={settlementsError}
            error={settlementsErrorDetail}
            isEmpty={settlements?.length === 0}
            emptyMessage={t('settlement.waiting')}
            onRetry={onRetrySettlements}
          >
            <p className='mt-4 text-xs font-medium text-muted'>{t('settlement.recent')}</p>
            <ul className='mt-2 flex flex-col gap-2 text-sm'>
              {(settlements ?? []).slice(0, 5).map((receipt) => (
                <li key={receipt.request_id} className='flex justify-between gap-3'>
                  <span className='font-medium text-good'>
                    {formatUSDMicros(receipt.seller_amount_micros)}
                  </span>
                  <span className='text-faint'>{formatTime(receipt.settled_at, locale)}</span>
                </li>
              ))}
            </ul>
          </QueryState>
        </Panel>
        <Panel title={t('privacy.title')}>
          <p className='flex gap-2.5 text-sm leading-6 text-muted'>
            <ShieldCheck className='mt-0.5 size-4 shrink-0 text-accent' aria-hidden='true' />
            {t('privacy.body')}
          </p>
        </Panel>
      </div>
    </details>
  )
}
