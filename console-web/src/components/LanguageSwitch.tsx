import { Languages } from 'lucide-react'

import { LOCALES, LOCALE_LABELS, isSupportedLocale } from '@/i18n/locales'
import { useTranslation } from '@/i18n/useTranslation'
import { useSessionStore } from '@/stores/session'

export const LanguageSwitch = () => {
  const { t, locale } = useTranslation()
  const setLocale = useSessionStore((state) => state.setLocale)

  return (
    <div className='flex items-center gap-1.5'>
      <Languages className='size-4 text-muted' aria-hidden='true' />
      <label htmlFor='console-locale' className='sr-only'>
        {t('nav.language')}
      </label>
      <select
        id='console-locale'
        value={locale}
        onChange={(event) => {
          const next = event.target.value
          if (isSupportedLocale(next)) setLocale(next)
        }}
        className='cursor-pointer rounded-md border border-line-2 bg-surface-1 px-2 py-1.5 text-xs text-ink outline-none transition-colors hover:border-accent/55 focus:border-accent'
      >
        {LOCALES.map((value) => (
          <option key={value} value={value}>
            {LOCALE_LABELS[value]}
          </option>
        ))}
      </select>
    </div>
  )
}
