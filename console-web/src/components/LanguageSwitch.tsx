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
        className='cursor-pointer rounded-[2px] border border-panel-edge bg-[#0d120e] px-2 py-1 text-xs text-ink'
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
