import { createRoute } from '@tanstack/react-router'
import { ShieldCheck } from 'lucide-react'

import { useOverview, useSettlements } from '@/api/queries'
import { Panel, QueryState, StatCard } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatCount, formatGigabytes, formatTime, formatUSDMicros } from '@/lib/format'

import { rootRoute } from './root'

const OverviewPage = () => {
  const { t, locale } = useTranslation()
  const overview = useOverview()
  const settlements = useSettlements()

  const stats = overview.data
  const placeholder = t('common.unknown')

  return (
    <div className='flex flex-col gap-3'>
      <QueryState
        isPending={overview.isPending}
        isError={overview.isError}
        onRetry={() => void overview.refetch()}
      >
        <section className='grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-5'>
          <StatCard
            label={t('stat.active')}
            value={stats ? formatCount(stats.active_requests, locale) : placeholder}
            hint={t('stat.activeHint')}
          />
          <StatCard
            label={t('stat.vram')}
            value={stats ? formatGigabytes(stats.loaded_vram_bytes) : placeholder}
            hint={t('stat.vramHint')}
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

      <div className='grid grid-cols-1 gap-3 lg:grid-cols-2'>
        <Panel title={t('settlement.title')}>
          <p className='border-l-[3px] border-amber bg-[#2c2419] p-3 text-xs text-[#d7bf9d]'>
            {t('settlement.notice')}
          </p>
          <QueryState
            isPending={settlements.isPending}
            isError={settlements.isError}
            isEmpty={settlements.data?.length === 0}
            emptyMessage={t('settlement.waiting')}
            onRetry={() => void settlements.refetch()}
          >
            <p className='mt-3 text-xs text-muted'>{t('settlement.recent')}</p>
            <ul className='mt-2 flex flex-col gap-1.5 text-xs'>
              {(settlements.data ?? []).slice(0, 5).map((receipt) => (
                <li key={receipt.request_id} className='flex justify-between gap-3'>
                  <span className='text-lime'>
                    {formatUSDMicros(receipt.seller_amount_micros)}
                  </span>
                  <span className='text-muted'>{formatTime(receipt.settled_at, locale)}</span>
                </li>
              ))}
            </ul>
          </QueryState>
        </Panel>

        <Panel title={t('privacy.title')}>
          <p className='flex gap-2 text-xs text-muted'>
            <ShieldCheck className='mt-0.5 size-4 shrink-0 text-lime' aria-hidden='true' />
            {t('privacy.body')}
          </p>
        </Panel>
      </div>
    </div>
  )
}

export const overviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: OverviewPage,
})
