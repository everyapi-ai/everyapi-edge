import { createRoute } from '@tanstack/react-router'

import { PlaygroundPage } from '@/features/playground/PlaygroundPage'

import { rootRoute } from './root'

const PlaygroundRoutePage = () => {
  const search = playgroundRoute.useSearch()
  return <PlaygroundPage initialModel={search.model} />
}

export const playgroundRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/playground',
  validateSearch: (search: Record<string, unknown>) => ({
    model: typeof search.model === 'string' ? search.model : '',
  }),
  component: PlaygroundRoutePage,
})
