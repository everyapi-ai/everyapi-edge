import { createRoute } from '@tanstack/react-router'

import { LogsPage } from '@/features/observability/LogsPage'

import { rootRoute } from './root'

export const logsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/logs',
  component: LogsPage,
})
