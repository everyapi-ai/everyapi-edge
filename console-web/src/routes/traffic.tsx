import { createRoute } from '@tanstack/react-router'

import { TrafficPage } from '@/features/observability/TrafficPage'

import { rootRoute } from './root'

export const trafficRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/traffic',
  component: TrafficPage,
})
