import { describe, expect, it } from 'vitest'

import { NAVIGATION_ITEMS } from './navigation'

describe('control room navigation', () => {
  it('exposes machine settings in the maintenance section', () => {
    expect(NAVIGATION_ITEMS).toContainEqual(
      expect.objectContaining({ to: '/settings', labelKey: 'nav.settings' }),
    )
  })
})
