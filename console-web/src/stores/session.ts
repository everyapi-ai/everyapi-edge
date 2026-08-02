import { create } from 'zustand'
import { persist, createJSONStorage } from 'zustand/middleware'

import { DEFAULT_LOCALE, isSupportedLocale, type Locale } from '@/i18n/locales'

interface SessionState {
  /** The console token from the installer. Held in sessionStorage only: it
   *  grants model download/removal on this machine, so it must not outlive the
   *  browser session the supplier opened it in. */
  token: string
  locale: Locale
  unlock: (token: string) => void
  lock: () => void
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
      token: '',
      locale: detectLocale(),
      unlock: (token: string) => set({ token: token.trim() }),
      lock: () => set({ token: '' }),
      setLocale: (locale: Locale) => set({ locale }),
    }),
    {
      name: 'everyapi-edge-console',
      storage: createJSONStorage(() => sessionStorage),
      version: 1,
    }
  )
)
