import type { ReactNode } from 'react'

import { Loader2, TriangleAlert } from 'lucide-react'

import { useTranslation } from '@/i18n/useTranslation'

export const Panel = ({
  title,
  children,
  className = '',
}: {
  title?: string
  children: ReactNode
  className?: string
}) => (
  <section
    className={`rounded-sm border border-panel-edge bg-gradient-to-br from-[#1b221d] to-[#141914] p-[18px] ${className}`}
  >
    {title ? (
      <h2 className='mb-4 font-display text-[23px] leading-none font-semibold tracking-[0.04em] uppercase'>
        {title}
      </h2>
    ) : null}
    {children}
  </section>
)

export const StatCard = ({ label, value, hint }: { label: string; value: string; hint: string }) => (
  <article className='flex min-h-[130px] flex-col rounded-sm border border-panel-edge bg-gradient-to-br from-[#1b221d] to-[#141914] p-[18px]'>
    <div className='text-[11px] tracking-[0.12em] text-muted uppercase'>{label}</div>
    <div className='mt-3 font-display text-[38px] leading-none font-semibold tracking-[0.03em]'>
      {value}
    </div>
    <div className='mt-auto pt-2 text-xs text-muted'>{hint}</div>
  </article>
)

export const Button = ({
  children,
  variant = 'primary',
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'ghost' | 'danger' }) => {
  const styles: Record<'primary' | 'ghost' | 'danger', string> = {
    primary:
      'bg-lime text-[#11160e] font-extrabold px-[15px] py-[11px] hover:brightness-110 disabled:opacity-50 disabled:cursor-not-allowed',
    ghost:
      'border border-[#4b6737] text-lime px-2 py-1.5 text-xs hover:bg-[#b8f2550f] disabled:opacity-50 disabled:cursor-not-allowed',
    danger:
      'border border-[#71403c] text-danger px-2 py-1.5 text-xs hover:bg-[#ff77700f] disabled:opacity-50 disabled:cursor-not-allowed',
  }
  return (
    <button
      {...props}
      className={`cursor-pointer rounded-[2px] transition-[filter,background-color] ${styles[variant]} ${props.className ?? ''}`}
    >
      {children}
    </button>
  )
}

/** Every data panel renders loading, empty and error rather than only the happy
 *  path — a supplier whose Ollama is down should see why, not a blank card. */
export const QueryState = ({
  isPending,
  isError,
  isEmpty,
  emptyMessage,
  onRetry,
  children,
}: {
  isPending: boolean
  isError: boolean
  isEmpty?: boolean
  emptyMessage?: string
  onRetry?: () => void
  children: ReactNode
}) => {
  const { t } = useTranslation()

  if (isPending) {
    return (
      <p className='flex items-center gap-2 py-5 text-muted' role='status' aria-live='polite'>
        <Loader2 className='size-4 animate-spin' aria-hidden='true' />
        {t('state.loading')}
      </p>
    )
  }

  if (isError) {
    return (
      <div className='flex flex-wrap items-center gap-3 py-5' role='alert'>
        <span className='flex items-center gap-2 text-danger'>
          <TriangleAlert className='size-4' aria-hidden='true' />
          {t('state.error')}
        </span>
        {onRetry ? (
          <Button variant='ghost' type='button' onClick={onRetry}>
            {t('state.retry')}
          </Button>
        ) : null}
      </div>
    )
  }

  if (isEmpty) {
    return <p className='py-5 text-muted'>{emptyMessage}</p>
  }

  return <>{children}</>
}
