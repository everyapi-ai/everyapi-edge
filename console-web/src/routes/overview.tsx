import { createRoute } from '@tanstack/react-router'

import { OverviewPage } from '@/features/overview/OverviewPage'

import { rootRoute } from './root'

export const overviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: OverviewPage,
})
