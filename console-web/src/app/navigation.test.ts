import { describe, expect, it } from 'vitest'

import type { AnyRoute } from '@tanstack/react-router'

import { ROUTES } from './routes'
import { NAVIGATION_ITEMS } from './navigation'

/** `RouteOptions` is a union whose id-addressed member declares no `path`, so reading it
 *  back off a path-addressed route needs an explicit narrow. */
const routePath = (route: AnyRoute) => (route.options as unknown as { path?: string }).path ?? ''

describe('control room navigation', () => {
  it('exposes machine settings in the maintenance section', () => {
    expect(NAVIGATION_ITEMS).toContainEqual(
      expect.objectContaining({ to: '/settings', labelKey: 'nav.settings' }),
    )
  })

  it('gives every registered route a navigation entry', () => {
    const navigable = new Set(NAVIGATION_ITEMS.map((item) => item.to))
    const orphaned = ROUTES.map(routePath).filter((path) => !navigable.has(path))
    expect(orphaned).toEqual([])
  })

  it('points every navigation entry at a registered route', () => {
    const registered = new Set(ROUTES.map(routePath))
    const dangling = NAVIGATION_ITEMS.map((item) => item.to).filter((to) => !registered.has(to))
    expect(dangling).toEqual([])
  })
})
