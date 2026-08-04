import { createRoute } from '@tanstack/react-router'

import { ModelsPage } from '@/features/models/ModelsPage'

import { rootRoute } from './root'

export const modelsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/models',
  component: ModelsPage,
})
