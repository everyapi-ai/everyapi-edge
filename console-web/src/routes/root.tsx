import { Link, Outlet, createRootRoute, useRouterState } from '@tanstack/react-router'
import { Activity, Boxes, Cpu, HardDrive, ImageUp, Menu, MessageSquareText, ScrollText, ServerCog, SlidersHorizontal, X } from 'lucide-react'
import { useState } from 'react'

import { LanguageSwitch } from '@/components/LanguageSwitch'
import { useOverview } from '@/api/queries'
import { useTranslation } from '@/i18n/useTranslation'
import { formatTime } from '@/lib/format'
import type { MessageKey } from '@/i18n/locales'

const NAV: { to: string; labelKey: MessageKey; icon: typeof Activity }[] = [
  { to: '/', labelKey: 'nav.overview', icon: SlidersHorizontal },
  { to: '/runtime', labelKey: 'nav.runtime', icon: Cpu },
  { to: '/models', labelKey: 'nav.models', icon: Boxes },
  { to: '/playground', labelKey: 'nav.playground', icon: MessageSquareText },
  { to: '/image-edit', labelKey: 'nav.imageEdit', icon: ImageUp },
  { to: '/storage', labelKey: 'nav.storage', icon: HardDrive },
  { to: '/traffic', labelKey: 'nav.traffic', icon: Activity },
  { to: '/logs', labelKey: 'nav.logs', icon: ScrollText },
]

const ConnectionState = () => {
  const { t, locale } = useTranslation()
  const overview = useOverview()
  const gateway = overview.data
  const scheduledReconnect = gateway?.gateway_state === 'offline' && Boolean(gateway.gateway_reconnect_attempt && gateway.gateway_next_reconnect_at)
  const label = overview.isError
    ? t('header.localUnavailable')
    : overview.isPending
      ? t('header.gatewayConnecting')
      : gateway?.gateway_state === 'online'
        ? t('header.gatewayOnline')
        : gateway?.gateway_state === 'preview'
          ? t('header.localPreview')
        : gateway?.gateway_state === 'offline'
          ? t('header.gatewayOffline')
          : t('header.gatewayConnecting')
  const dotColor = overview.isError || gateway?.gateway_state === 'offline' ? 'bg-danger' : overview.isPending || gateway?.gateway_state === 'connecting' ? 'bg-warn' : gateway?.gateway_state === 'preview' ? 'bg-accent' : 'bg-good'

  return (
    <div className='text-xs text-ink-dim'>
      <p className='flex items-center gap-1.5 font-medium'>
        <span className={`inline-block size-1.5 rounded-full ${dotColor}`} aria-hidden='true' />
        <span aria-live='polite'>{label}</span>
      </p>
      {scheduledReconnect ? <p data-sidebar-gateway-reconnect className='mt-1 text-ink-faint'>{t('gateway.reconnectAttempt', { attempt: gateway!.gateway_reconnect_attempt })} · {t('gateway.nextRetry')}: {formatTime(gateway!.gateway_next_reconnect_at!, locale)}</p> : null}
      {gateway?.gateway_state === 'offline' && gateway.gateway_last_error ? <p title={gateway.gateway_last_error} className='mt-1 truncate text-ink-faint'>{gateway.gateway_last_error}</p> : overview.dataUpdatedAt ? <p className='mt-1 text-ink-faint'>{t('header.updated', { time: formatTime(new Date(overview.dataUpdatedAt), locale) })}</p> : null}
    </div>
  )
}

const Navigation = ({ onNavigate }: { onNavigate?: () => void }) => {
  const { t } = useTranslation()
  const currentPath = useRouterState({ select: (state) => state.location.pathname })

  return (
    <nav aria-label={t('header.eyebrow')} className='flex flex-1 flex-col overflow-y-auto px-2 py-1.5'>
      <p className='px-2.5 pb-1.5 pt-3.5 text-[10.5px] font-semibold tracking-[0.15em] text-ink-faint uppercase'>
        {t('header.title')}
      </p>
      {NAV.map(({ to, labelKey, icon: Icon }) => {
        const active = currentPath === to
        return (
          <Link
            key={to}
            to={to}
            onClick={onNavigate}
            aria-current={active ? 'page' : undefined}
            className={`relative flex items-center gap-2.5 rounded-sm px-2.5 py-2 text-base font-medium transition-colors ${
              active ? 'bg-surface-2 text-ink' : 'text-ink-2 hover:bg-surface-2 hover:text-ink'
            }`}
          >
            <Icon className={`size-[14px] shrink-0 ${active ? 'text-accent' : 'text-ink-dim'}`} aria-hidden='true' />
            <span className='flex-1 truncate'>{t(labelKey)}</span>
          </Link>
        )
      })}
    </nav>
  )
}

const Sidebar = ({ onNavigate }: { onNavigate?: () => void }) => {
  const { t } = useTranslation()

  return (
    <aside className='border-line bg-canvas sticky top-0 flex h-svh w-[232px] flex-col border-r'>
      <div className='flex items-center gap-2.5 px-4 pb-3.5 pt-4'>
        <span className='grid size-7 place-items-center rounded-sm bg-accent/15 text-accent'>
          <ServerCog className='size-4' aria-hidden='true' />
        </span>
        <span className='text-md font-semibold tracking-[-0.01em] text-ink'>
          everyapi<span className='text-accent'>.</span>
        </span>
      </div>
      <div className='px-3 pb-2'>
        <div className='border-line bg-surface-1 flex h-7 items-center gap-2 rounded-sm border px-2.5 text-xs text-ink-dim'>
          <ServerCog className='size-[13px] shrink-0' aria-hidden='true' />
          <span className='truncate'>{t('header.eyebrow')}</span>
        </div>
      </div>
      <Navigation onNavigate={onNavigate} />
      <div className='border-line border-t p-3'>
        <ConnectionState />
      </div>
    </aside>
  )
}

const Shell = () => {
  const { t } = useTranslation()
  const [mobileNav, setMobileNav] = useState(false)
  const currentPath = useRouterState({ select: (state) => state.location.pathname })
  const activePage = NAV.find((item) => item.to === currentPath)?.labelKey ?? 'nav.overview'

  return (
    <div className='grid min-h-svh grid-cols-1 md:grid-cols-[232px_1fr]'>
      <div className='hidden md:block'>
        <Sidebar />
      </div>

      {mobileNav ? (
        <>
          <button type='button' aria-label={t('nav.close')} onClick={() => setMobileNav(false)} className='fixed inset-0 z-[55] bg-black/55 backdrop-blur-[2px] md:hidden' />
          <div role='dialog' aria-modal='true' aria-label={t('header.eyebrow')} className='fixed left-0 top-0 z-[60] h-svh w-[264px] shadow-[0_30px_60px_rgba(0,0,0,0.55)] md:hidden'>
            <button type='button' aria-label={t('nav.close')} onClick={() => setMobileNav(false)} className='absolute right-3 top-3 z-10 grid size-7 place-items-center text-ink-2 hover:text-ink'>
              <X className='size-4' aria-hidden='true' />
            </button>
            <Sidebar onNavigate={() => setMobileNav(false)} />
          </div>
        </>
      ) : null}

      <div className='min-w-0 flex flex-col'>
        <header className='border-line bg-[color-mix(in_oklab,var(--color-canvas)_80%,transparent)] sticky top-0 z-[5] flex h-12 items-center gap-3 border-b px-3 backdrop-blur-[8px] md:px-5'>
          <button type='button' aria-label={t('nav.open')} onClick={() => setMobileNav(true)} className='text-ink-2 hover:text-ink grid size-7 shrink-0 place-items-center md:hidden'>
            <Menu className='size-4' aria-hidden='true' />
          </button>
          <div className='flex min-w-0 flex-1 items-center gap-2 text-base md:hidden'>
            <span className='truncate font-medium'>{t(activePage)}</span>
          </div>
          <div className='text-ink-faint hidden min-w-0 items-center gap-2 font-mono text-xs tracking-[0.02em] md:flex'>
            <span>everyapi</span>
            <span className='text-ink-faint/60'>/</span>
            <span className='text-ink-dim truncate'>{t('header.eyebrow')}</span>
          </div>
          <span className='hidden flex-1 md:block' />
          <div className='flex items-center gap-1.5'>
            <LanguageSwitch />
          </div>
        </header>
        <main className='min-w-0 flex-1'>
          <div className='w-full px-3 pb-24 pt-5 md:pt-9'>
            <Outlet />
          </div>
        </main>
      </div>
    </div>
  )
}

export const rootRoute = createRootRoute({ component: Shell })
