import { Outlet, useRouterState } from '@tanstack/react-router'
import { LogOut, Menu, X } from 'lucide-react'
import { useEffect, useRef, useState } from 'react'

import { NAVIGATION_ITEMS } from '@/app/navigation'
import { useLogout, useSession } from '@/api/queries'
import { LanguageSwitch } from '@/components/LanguageSwitch'
import { useTranslation } from '@/i18n/useTranslation'

import { NavigationRail } from './NavigationRail'
import { MobileSystemStatus } from './SystemStatus'

export const AppShell = () => {
  const { t } = useTranslation()
  const session = useSession()
  const logout = useLogout()
  const [mobileNav, setMobileNav] = useState(false)
  const mobileDialogRef = useRef<HTMLDivElement>(null)
  const mobileTriggerRef = useRef<HTMLButtonElement>(null)
  const currentPath = useRouterState({ select: (state) => state.location.pathname })
  const activePage =
    NAVIGATION_ITEMS.find((item) => item.to === currentPath)?.labelKey ?? 'nav.overview'

  useEffect(() => {
    if (!mobileNav) return
    const dialog = mobileDialogRef.current
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    dialog?.querySelector<HTMLElement>('[data-mobile-nav-close]')?.focus()

    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        setMobileNav(false)
        return
      }
      if (event.key !== 'Tab' || !dialog) return
      const focusable = Array.from(
        dialog.querySelectorAll<HTMLElement>(
          'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
        ),
      ).filter((element) => !element.hasAttribute('hidden'))
      if (focusable.length === 0) return
      const first = focusable[0]
      const last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('keydown', onKeyDown)
      document.body.style.overflow = previousOverflow
      mobileTriggerRef.current?.focus()
    }
  }, [mobileNav])

  return (
    <div className='grid min-h-svh grid-cols-1 xl:grid-cols-[252px_1fr]'>
      <div className='hidden xl:block'>
        <NavigationRail />
      </div>
      {mobileNav ? (
        <>
          <div
            aria-hidden='true'
            onClick={() => setMobileNav(false)}
            className='fixed inset-0 z-[55] bg-black/70 backdrop-blur-[2px] xl:hidden'
          />
          <div
            ref={mobileDialogRef}
            role='dialog'
            aria-modal='true'
            aria-label={t('header.eyebrow')}
            className='fixed left-0 top-0 z-[60] h-svh w-[280px] shadow-[0_30px_60px_rgba(0,0,0,0.65)] xl:hidden'
          >
            <button
              data-mobile-nav-close
              type='button'
              aria-label={t('nav.close')}
              onClick={() => setMobileNav(false)}
              className='absolute right-3 top-3 z-10 grid size-8 place-items-center text-ink-2 hover:text-ink'
            >
              <X className='size-4' aria-hidden='true' />
            </button>
            <NavigationRail onNavigate={() => setMobileNav(false)} />
          </div>
        </>
      ) : null}
      <div
        className='min-w-0 flex flex-col'
        aria-hidden={mobileNav || undefined}
        inert={mobileNav ? true : undefined}
      >
        <header className='border-line bg-canvas/88 sticky top-0 z-[5] flex h-12 items-center gap-3 border-b px-3 backdrop-blur-xl md:px-6'>
          <button
            ref={mobileTriggerRef}
            type='button'
            aria-label={t('nav.open')}
            onClick={() => setMobileNav(true)}
            className='grid size-7 shrink-0 place-items-center text-ink-2 hover:text-ink xl:hidden'
          >
            <Menu className='size-4' aria-hidden='true' />
          </button>
          <div className='flex min-w-0 flex-1 items-center gap-3 xl:hidden'>
            <span className='truncate text-sm font-semibold'>{t(activePage)}</span>
            <span className='h-3 w-px bg-line' />
            <MobileSystemStatus />
          </div>
          <div className='hidden min-w-0 items-center gap-2 font-mono text-[10px] uppercase tracking-[0.13em] text-ink-faint xl:flex'>
            <span>Edge / Local node</span>
            <span className='text-accent'>●</span>
            <span className='truncate text-ink-dim'>{t(activePage)}</span>
          </div>
          <span className='hidden flex-1 xl:block' />
          <LanguageSwitch />
          {session.data?.pairing_required ? (
            <button
              type='button'
              aria-label={t('session.logout')}
              title={t('session.logout')}
              disabled={logout.isPending}
              onClick={() => logout.mutate()}
              className='grid size-7 shrink-0 place-items-center text-ink-2 hover:text-ink disabled:opacity-50'
            >
              <LogOut className='size-4' aria-hidden='true' />
            </button>
          ) : null}
        </header>
        <main className='min-w-0 flex-1'>
          <div className='mx-auto w-full max-w-[1680px] px-3 pb-24 pt-6 md:px-7 md:pt-10'>
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}
