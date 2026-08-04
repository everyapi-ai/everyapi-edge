import { create } from 'zustand'
import { persist, createJSONStorage } from 'zustand/middleware'

import { DEFAULT_LOCALE, isSupportedLocale, type Locale } from '@/i18n/locales'

interface SessionState {
  locale: Locale
  setLocale: (locale: Locale) => void
}

const detectLocale = (): Locale => {
  for (const candidate of navigator.languages ?? [navigator.language]) {
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
