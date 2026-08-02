import { useState, type FormEvent } from 'react'

import { KeyRound } from 'lucide-react'

import { useTranslation } from '@/i18n/useTranslation'
import { useSessionStore } from '@/stores/session'

import { Button } from './primitives'
import { LanguageSwitch } from './LanguageSwitch'

/** Token entry. Deliberately a plain controlled input rather than
 *  react-hook-form + zod: one required field with one rule does not need a form
 *  library, and the real validation is the agent's constant-time compare. */
export const UnlockScreen = () => {
  const { t } = useTranslation()
  const unlock = useSessionStore((state) => state.unlock)
  const [value, setValue] = useState('')
  const [error, setError] = useState('')

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const token = value.trim()
    if (!token) {
      setError(t('unlock.tokenRequired'))
      return
    }
    setError('')
    unlock(token)
  }

  return (
    <main className='mx-auto max-w-[580px] px-4 py-[12vh]'>
      <section className='rounded-sm border border-panel-edge bg-gradient-to-br from-[#1b221d] to-[#141914] p-7'>
        <div className='flex items-start justify-between gap-4'>
          <p className='text-[11px] tracking-[0.17em] text-lime uppercase'>{t('unlock.eyebrow')}</p>
          <LanguageSwitch />
        </div>
        <h1 className='mt-2 font-display text-[42px] leading-[0.9] font-semibold uppercase'>
          {t('unlock.title')}
        </h1>
        <p className='mt-3 text-muted'>{t('unlock.description')}</p>
        <form onSubmit={submit} className='mt-5 flex flex-wrap items-start gap-2'>
          <div className='min-w-0 flex-1'>
            <label htmlFor='console-token' className='sr-only'>
              {t('unlock.tokenLabel')}
            </label>
            <div className='flex items-center gap-2 rounded-[2px] border border-[#405043] bg-[#0d120e] px-3 focus-within:border-lime'>
              <KeyRound className='size-4 shrink-0 text-muted' aria-hidden='true' />
              <input
                id='console-token'
                type='password'
                autoComplete='off'
                autoFocus
                value={value}
                onChange={(event) => setValue(event.target.value)}
                placeholder={t('unlock.tokenLabel')}
                aria-invalid={error ? true : undefined}
                aria-describedby={error ? 'console-token-error' : undefined}
                className='w-full bg-transparent py-[11px] text-ink outline-none placeholder:text-muted'
              />
            </div>
            {error ? (
              <p id='console-token-error' role='alert' className='mt-2 text-xs text-danger'>
                {error}
              </p>
            ) : null}
          </div>
          <Button type='submit'>{t('unlock.submit')}</Button>
        </form>
      </section>
    </main>
  )
}
