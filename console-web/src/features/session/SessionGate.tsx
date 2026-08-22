import { type FormEvent, type ReactNode, useEffect, useState } from 'react'

import { useQueryClient } from '@tanstack/react-query'
import { KeyRound, Loader2, ShieldCheck } from 'lucide-react'

import { SESSION_UNAUTHORIZED_EVENT } from '@/api/client'
import { queryKeys, usePairSession, useSession } from '@/api/queries'
import type { Session } from '@/api/schemas'
import { Button, Input } from '@/components/primitives'
import { LanguageSwitch } from '@/components/LanguageSwitch'
import { useTranslation } from '@/i18n/useTranslation'

export const SessionGate = ({ children }: { children: ReactNode }) => {
  const { t } = useTranslation()
  const session = useSession()
  const pair = usePairSession()
  const queryClient = useQueryClient()
  const [token, setToken] = useState('')

  useEffect(() => {
    const expireSession = () => {
      queryClient.removeQueries({
        predicate: (query) => query.queryKey[0] !== queryKeys.session[0],
      })
      queryClient.setQueryData(queryKeys.session, {
        authenticated: false,
        pairing_required: true,
      } satisfies Session)
    }
    window.addEventListener(SESSION_UNAUTHORIZED_EVENT, expireSession)
    return () => window.removeEventListener(SESSION_UNAUTHORIZED_EVENT, expireSession)
  }, [queryClient])

  if (session.isPending) {
    return (
      <div className='grid min-h-svh place-items-center bg-canvas text-muted' role='status'>
        <span className='flex items-center gap-2 text-sm'>
          <Loader2 className='size-4 animate-spin text-accent' aria-hidden='true' />
          {t('session.checking')}
        </span>
      </div>
    )
  }

  if (session.isError) {
    return (
      <div className='grid min-h-svh place-items-center bg-canvas px-4'>
        <div className='w-full max-w-md rounded-xl border border-danger/25 bg-surface-0 p-6 text-center'>
          <p className='text-sm text-danger' role='alert'>
            {t('session.unavailable')}
          </p>
          <Button
            type='button'
            variant='ghost'
            className='mt-4'
            onClick={() => void session.refetch()}
          >
            {t('state.retry')}
          </Button>
        </div>
      </div>
    )
  }

  if (session.data.authenticated) return <>{children}</>

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (token.trim()) {
      pair.mutate(token, {
        onSuccess: () => {
          setToken('')
          pair.reset()
        },
      })
    }
  }

  return (
    <main className='relative grid min-h-svh place-items-center overflow-hidden bg-canvas px-4 py-12'>
      <div className='absolute right-4 top-4'>
        <LanguageSwitch />
      </div>
      <section
        className='w-full max-w-md rounded-2xl border border-line bg-surface-0 p-6 shadow-[0_30px_80px_-40px_rgba(0,0,0,0.9)] sm:p-8'
        aria-labelledby='pairing-title'
      >
        <div className='grid size-11 place-items-center rounded-xl border border-accent/35 bg-accent/10 text-accent'>
          <ShieldCheck className='size-5' aria-hidden='true' />
        </div>
        <p className='mt-5 font-mono text-[10px] uppercase tracking-[0.14em] text-faint'>
          {t('header.eyebrow')}
        </p>
        <h1 id='pairing-title' className='mt-2 text-2xl font-semibold tracking-[-0.025em] text-ink'>
          {t('session.title')}
        </h1>
        <p className='mt-2 text-sm leading-6 text-muted'>{t('session.description')}</p>
        <form className='mt-6' onSubmit={submit}>
          <label htmlFor='pairing-token' className='text-sm font-medium text-ink-2'>
            {t('session.token')}
          </label>
          <div className='relative mt-2'>
            <KeyRound
              className='pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-faint'
              aria-hidden='true'
            />
            <Input
              id='pairing-token'
              type='password'
              autoComplete='current-password'
              autoFocus
              value={token}
              onChange={(event) => setToken(event.target.value)}
              className='pl-9'
            />
          </div>
          {pair.isError ? (
            <p className='mt-3 text-sm text-danger' role='alert'>
              {pair.error.message}
            </p>
          ) : null}
          <Button type='submit' className='mt-5 w-full' disabled={!token.trim() || pair.isPending}>
            {pair.isPending ? t('session.pairing') : t('session.submit')}
          </Button>
        </form>
        <p className='mt-5 text-xs leading-5 text-faint'>{t('session.hint')}</p>
      </section>
    </main>
  )
}
