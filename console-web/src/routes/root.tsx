import { Link, Outlet, createRootRoute, useRouterState } from '@tanstack/react-router'
import { Activity, Boxes, LockKeyhole, ScrollText, SlidersHorizontal } from 'lucide-react'

import { LanguageSwitch } from '@/components/LanguageSwitch'
import { Button } from '@/components/primitives'
import { useOverview } from '@/api/queries'
import { useTranslation } from '@/i18n/useTranslation'
import { formatTime } from '@/lib/format'
import { useSessionStore } from '@/stores/session'
import type { MessageKey } from '@/i18n/locales'

const NAV: { to: string; labelKey: MessageKey; icon: typeof Activity }[] = [
  { to: '/', labelKey: 'nav.overview', icon: SlidersHorizontal },
  { to: '/models', labelKey: 'nav.models', icon: Boxes },
  { to: '/traffic', labelKey: 'nav.traffic', icon: Activity },
  { to: '/logs', labelKey: 'nav.logs', icon: ScrollText },
]

const ConnectionState = () => {
  const { t, locale } = useTranslation()
  // Overview is the cheapest endpoint that hits Ollama, so its query state
  // doubles as the console's own liveness indicator.
  const overview = useOverview()
  const label = overview.isError
    ? t('header.offline')
    : overview.isPending
      ? t('header.connecting')
      : t('header.online')
  const dotColor = overview.isError ? 'bg-danger' : overview.isPending ? 'bg-amber' : 'bg-lime'

  return (
    <div className='text-xs text-muted'>
      <p className='flex items-center gap-2'>
        <span className={`inline-block size-2 rounded-full ${dotColor}`} aria-hidden='true' />
        <span aria-live='polite'>{label}</span>
      </p>
      {overview.dataUpdatedAt ? (
        <p className='mt-1'>
          {t('header.updated', { time: formatTime(new Date(overview.dataUpdatedAt), locale) })}
        </p>
      ) : null}
    </div>
  )
}

const Shell = () => {
  const { t } = useTranslation()
  const lock = useSessionStore((state) => state.lock)
  const currentPath = useRouterState({ select: (state) => state.location.pathname })

  return (
    <div className='mx-auto max-w-[1440px] p-4 sm:p-7'>
      <header className='mb-[22px] flex flex-col gap-3 border-b border-panel-edge pb-[22px] md:flex-row md:items-end md:justify-between'>
        <div>
          <p className='text-[11px] tracking-[0.17em] text-lime uppercase'>{t('header.eyebrow')}</p>
          <h1 className='mt-1.5 max-w-[14ch] font-display text-[clamp(32px,6vw,68px)] leading-[0.85] font-semibold tracking-[0.015em] uppercase'>
            {t('header.title')}
          </h1>
        </div>
        <div className='flex items-center gap-4'>
          <ConnectionState />
          <LanguageSwitch />
          <Button variant='ghost' type='button' onClick={lock} className='flex items-center gap-1.5'>
            <LockKeyhole className='size-3.5' aria-hidden='true' />
            {t('nav.lock')}
          </Button>
        </div>
      </header>

      <nav aria-label={t('header.eyebrow')} className='mb-3 flex flex-wrap gap-2'>
        {NAV.map(({ to, labelKey, icon: Icon }) => {
          const active = currentPath === to
          return (
            <Link
              key={to}
              to={to}
              aria-current={active ? 'page' : undefined}
              className={`flex items-center gap-2 rounded-[2px] border px-3 py-2 text-xs tracking-[0.08em] uppercase transition-colors ${
                active
                  ? 'border-lime bg-[#b8f2551a] text-lime'
                  : 'border-panel-edge text-muted hover:text-ink'
              }`}
            >
              <Icon className='size-3.5' aria-hidden='true' />
              {t(labelKey)}
            </Link>
          )
        })}
      </nav>

      <Outlet />
    </div>
  )
}

export const rootRoute = createRootRoute({ component: Shell })
