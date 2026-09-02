import { create } from 'zustand'
import { persist, createJSONStorage } from 'zustand/middleware'

import { DEFAULT_LOCALE, isSupportedLocale, type Locale } from '@/i18n/locales'

interface SessionState {
  locale: Locale
  setLocale: (locale: Locale) => void
}

/** `navigator` is a host value, not ours. A browser always fills these in, but the store is created at module load, so any environment that leaves `languages` and `language` unset — a test runner, a server-side import — would take the whole console down on an entry nobody validated. */
const detectLocale = (): Locale => {
  const host = typeof navigator === 'undefined' ? undefined : navigator
  const candidates = host?.languages?.length ? host.languages : [host?.language]
  for (const candidate of candidates) {
    if (typeof candidate !== 'string') continue
    const base = candidate.toLowerCase().split('-')[0]
    if (isSupportedLocale(base)) return base
  }
  return DEFAULT_LOCALE
}

export const useSessionStore = create<SessionState>()(
  persist(
    (set) => ({
      locale: detectLocale(),
      setLocale: (locale: Locale) => set({ locale }),
    }),
    {
      name: 'everyapi-edge-console',
      storage: createJSONStorage(() => sessionStorage),
      version: 1,
    },
  ),
)
