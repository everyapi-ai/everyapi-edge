import { useTranslation } from '@/i18n/useTranslation'
import { formatVRAMGigabytes } from '@/lib/format'

type CapacityRailProps = {
  loadedBytes: number
  reservedBytes: number
  totalGB: number
}

export const CapacityRail = ({ loadedBytes, reservedBytes, totalGB }: CapacityRailProps) => {
  const { t } = useTranslation()
  const totalBytes = totalGB * 1024 ** 3
  const loadedPercent = totalBytes ? Math.min(100, (loadedBytes / totalBytes) * 100) : 0
  const reservedPercent = totalBytes
    ? Math.min(100 - loadedPercent, (reservedBytes / totalBytes) * 100)
    : 0

  return (
    <section
      data-capacity-rail
      className='grid gap-5 border border-line bg-surface-0 px-4 py-4 sm:grid-cols-[minmax(0,1fr)_auto] sm:px-5'
    >
      <div className='min-w-0'>
        <p className='font-mono text-[9px] font-semibold uppercase tracking-[0.2em] text-accent'>
          {t('capacity.kicker')}
        </p>
        <div className='mt-1 flex flex-wrap items-baseline gap-x-3 gap-y-1'>
          <h2 className='font-display text-xl font-semibold tracking-tight text-ink'>
            {t('capacity.title')}
          </h2>
          <p className='font-mono text-[10px] uppercase tracking-[0.12em] text-ink-faint'>
            {t('capacity.loaded')} {formatVRAMGigabytes(loadedBytes)} / {totalGB || '—'} GB
          </p>
        </div>
        <div
          role='progressbar'
          aria-label={t('capacity.title')}
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(loadedPercent)}
          className='mt-4 flex h-2 overflow-hidden bg-surface-3'
        >
          <span className='h-full bg-accent' style={{ width: `${loadedPercent}%` }} />
          <span className='h-full bg-warn/80' style={{ width: `${reservedPercent}%` }} />
        </div>
      </div>
      <dl className='grid grid-cols-3 gap-5 sm:min-w-[310px] sm:border-l sm:border-line sm:pl-5'>
        <div>
          <dt className='font-mono text-[9px] uppercase tracking-[0.14em] text-ink-faint'>
            {t('capacity.loaded')}
          </dt>
          <dd className='mt-1 font-mono text-sm text-accent'>{formatVRAMGigabytes(loadedBytes)}</dd>
        </div>
        <div>
          <dt className='font-mono text-[9px] uppercase tracking-[0.14em] text-ink-faint'>
            {t('capacity.reserved')}
          </dt>
          <dd className='mt-1 font-mono text-sm text-warn'>{formatVRAMGigabytes(reservedBytes)}</dd>
        </div>
        <div>
          <dt className='font-mono text-[9px] uppercase tracking-[0.14em] text-ink-faint'>
            {t('capacity.total')}
          </dt>
          <dd className='mt-1 font-mono text-sm text-ink'>{totalGB || '—'} GB</dd>
        </div>
      </dl>
    </section>
  )
}
