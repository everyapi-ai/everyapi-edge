import { createRoute } from '@tanstack/react-router'

import { StoragePage } from '@/features/storage/StoragePage'

import { rootRoute } from './root'

export const storageRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/storage',
  component: StoragePage,
})
