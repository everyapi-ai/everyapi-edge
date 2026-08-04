import { Link, useRouterState } from '@tanstack/react-router'
import { ServerCog } from 'lucide-react'

import { NAVIGATION_GROUPS } from '@/app/navigation'
import { useTranslation } from '@/i18n/useTranslation'

import { SystemStatusRail } from './SystemStatus'

const Navigation = ({ onNavigate }: { onNavigate?: () => void }) => {
  const { t } = useTranslation()
  const currentPath = useRouterState({ select: (state) => state.location.pathname })

  return (
    <nav
      aria-label={t('header.eyebrow')}
      className='flex flex-1 flex-col overflow-y-auto px-3 py-2'
    >
      {NAVIGATION_GROUPS.map((group) => (
        <section key={group.labelKey} data-navigation-group className='pt-3 first:pt-1'>
          <p
            data-navigation-group-label
            className='px-2 pb-1.5 font-mono text-[9px] font-semibold uppercase tracking-[0.2em] text-ink-faint'
          >
            {t(group.labelKey)}
          </p>
          <div className='flex flex-col gap-0.5'>
            {group.items.map(({ to, labelKey, icon: Icon }) => {
              const active = currentPath === to
              return (
                <Link
                  key={to}
                  to={to}
                  onClick={onNavigate}
                  aria-current={active ? 'page' : undefined}
                  className={`group relative flex items-center gap-3 border-l px-3 py-2 text-sm font-medium transition-colors ${
                    active
                      ? 'border-accent bg-accent/8 text-ink'
                      : 'border-transparent text-ink-2 hover:border-line-2 hover:bg-surface-1 hover:text-ink'
                  }`}
                >
                  <Icon
                    className={`size-3.5 shrink-0 ${active ? 'text-accent' : 'text-ink-faint group-hover:text-ink-2'}`}
                    aria-hidden='true'
                  />
                  <span className='flex-1 truncate'>{t(labelKey)}</span>
                  {active ? <span className='font-mono text-[8px] text-accent'>LIVE</span> : null}
                </Link>
              )
            })}
          </div>
        </section>
      ))}
    </nav>
  )
}

export const NavigationRail = ({ onNavigate }: { onNavigate?: () => void }) => {
  const { t } = useTranslation()

  return (
    <aside className='border-line bg-canvas sticky top-0 flex h-svh w-[252px] flex-col border-r'>
      <div className='border-line border-b px-4 py-4'>
        <div className='flex items-center gap-3'>
          <span className='grid size-8 place-items-center border border-accent/45 bg-accent/10 text-accent'>
            <ServerCog className='size-4' aria-hidden='true' />
          </span>
          <div className='min-w-0'>
            <p className='font-display text-base font-bold uppercase tracking-[0.08em] text-ink'>
              EveryAPI
            </p>
            <p className='truncate font-mono text-[9px] uppercase tracking-[0.16em] text-ink-faint'>
              {t('header.eyebrow')} / 02
            </p>
          </div>
        </div>
      </div>
      <Navigation onNavigate={onNavigate} />
      <SystemStatusRail />
    </aside>
  )
}
