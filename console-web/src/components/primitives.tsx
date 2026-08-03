import { forwardRef, type InputHTMLAttributes, type ReactNode } from 'react'

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
  <section className={`rounded-xl border border-line bg-surface-0 p-5 shadow-[0_16px_40px_-30px_rgba(0,0,0,0.9)] ${className}`}>
    {title ? <h2 className='mb-4 text-base font-semibold tracking-[-0.01em] text-ink'>{title}</h2> : null}
    {children}
  </section>
)

export const PageHeader = ({ title, description, actions }: { title: string; description: string; actions?: ReactNode }) => (
  <div className='flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between'>
    <div className='max-w-2xl'>
      <h1 className='text-2xl font-semibold tracking-[-0.025em] text-ink sm:text-[28px]'>{title}</h1>
      <p className='mt-1.5 text-sm leading-6 text-muted'>{description}</p>
    </div>
    {actions ? <div className='shrink-0'>{actions}</div> : null}
  </div>
)

export const StatCard = ({ label, value, hint }: { label: string; value: string; hint: string }) => (
  <article className='relative overflow-hidden rounded-xl border border-line bg-surface-0 p-4 shadow-[0_14px_32px_-28px_rgba(0,0,0,0.9)]'>
    <div className='text-xs font-medium text-muted'>{label}</div>
    <div className='mt-2.5 text-[28px] leading-none font-semibold tracking-[-0.03em] text-ink'>{value}</div>
    <div className='mt-3 text-xs leading-5 text-faint'>{hint}</div>
  </article>
)

export const Button = ({
  children,
  variant = 'primary',
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: 'primary' | 'ghost' | 'danger' }) => {
  const styles: Record<'primary' | 'ghost' | 'danger', string> = {
    primary:
      'border border-accent bg-accent px-3.5 py-2 text-sm font-medium text-accent-ink shadow-[0_8px_18px_-12px_color-mix(in_oklab,var(--color-accent)_90%,transparent)] hover:brightness-110 disabled:cursor-not-allowed disabled:opacity-50',
    ghost:
      'border border-line-2 bg-surface-1 px-3 py-2 text-sm font-medium text-ink-2 hover:border-accent/55 hover:bg-surface-2 hover:text-ink disabled:cursor-not-allowed disabled:opacity-50',
    danger:
      'border border-danger/35 bg-danger/10 px-3 py-2 text-sm font-medium text-danger hover:bg-danger/18 disabled:cursor-not-allowed disabled:opacity-50',
  }
  return (
    <button
      {...props}
      className={`cursor-pointer rounded-[9px] transition-[background-color,border-color,color,transform] duration-150 active:translate-y-px ${styles[variant]} ${props.className ?? ''}`}
    >
      {children}
    </button>
  )
}

/** Shared single-line field shell, matching the Dashboard input treatment. */
export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(
  ({ className = '', ...props }, ref) => (
    <input
      ref={ref}
      className={`w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 font-mono text-sm text-ink outline-none transition-colors placeholder:text-faint focus:border-accent focus:ring-2 focus:ring-accent/20 ${className}`}
      {...props}
    />
  )
)
Input.displayName = 'Input'

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
      <p className='flex items-center gap-2 py-8 text-sm text-muted' role='status' aria-live='polite'>
        <Loader2 className='size-4 animate-spin text-accent' aria-hidden='true' />
        {t('state.loading')}
      </p>
    )
  }

  if (isError) {
    return (
      <div className='flex flex-wrap items-center gap-3 rounded-lg border border-danger/25 bg-danger/8 px-3.5 py-3 text-sm' role='alert'>
        <span className='flex items-center gap-2 text-danger'>
          <TriangleAlert className='size-4' aria-hidden='true' />
          {t('state.error')}
        </span>
        {onRetry ? <Button variant='ghost' type='button' onClick={onRetry}>{t('state.retry')}</Button> : null}
      </div>
    )
  }

  if (isEmpty) return <p className='py-8 text-sm text-muted'>{emptyMessage}</p>

  return <>{children}</>
}
