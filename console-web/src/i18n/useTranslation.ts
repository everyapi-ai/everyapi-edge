import { useCallback } from 'react'

import { useSessionStore } from '@/stores/session'

import { DEFAULT_LOCALE, MESSAGES, type Locale, type MessageKey } from './locales'

export type Translate = (key: MessageKey, values?: Record<string, string | number>) => string

/** Minimal message lookup with `{name}` interpolation. i18next would be the
 *  house choice for a multi-page app, but this console is a single embedded
 *  document with two locales and no lazy loading — the library would be pure
 *  bundle weight for the same output. */
export const useTranslation = (): { t: Translate; locale: Locale } => {
  const locale = useSessionStore((state) => state.locale)
  const t = useCallback<Translate>(
    (key, values) => {
      const template = MESSAGES[locale]?.[key] ?? MESSAGES[DEFAULT_LOCALE][key]
      if (!values) return template
      return template.replace(/\{(\w+)\}/g, (match, name: string) =>
        name in values ? String(values[name]) : match
      )
    },
    [locale]
  )
  return { t, locale }
}
