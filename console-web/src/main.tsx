import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createHashHistory,
  createRouter,
  type AnyRoute,
} from '@tanstack/react-router'

import { logsRoute } from '@/routes/logs'
import { modelsRoute } from '@/routes/models'
import { overviewRoute } from '@/routes/overview'
import { playgroundRoute } from '@/routes/playground'
import { imageEditRoute } from '@/routes/image-edit'
import { runtimeRoute } from '@/routes/runtime'
import { storageRoute } from '@/routes/storage'
import { rootRoute } from '@/routes/root'
import { trafficRoute } from '@/routes/traffic'

import './styles.css'

// Hash history, not browser history: the Go handler serves the embedded document
// from `/` via a catch-all mux pattern, so a real path like /models would work
// on navigation but a manual reload would too — while a hash keeps the whole
// route space off the server entirely. That leaves internal/console/server.go's
// routing untouched: exactly one HTML route and the /api/ tree.
const history = createHashHistory()

const routeTree: AnyRoute = rootRoute.addChildren([
  overviewRoute,
  runtimeRoute,
  storageRoute,
  modelsRoute,
  playgroundRoute,
  imageEditRoute,
  trafficRoute,
  logsRoute,
])

const router = createRouter({ routeTree, history, defaultPreload: false })

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router
  }
}

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // The agent is on loopback, so a failed request means it is genuinely
      // down — retrying hides that behind a delay.
      retry: false,
      refetchOnWindowFocus: false,
      staleTime: 2_000,
    },
    mutations: { retry: false },
  },
})

const App = () => <RouterProvider router={router} />

const container = document.getElementById('root')
if (!container) throw new Error('missing #root container')

createRoot(container).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <App />
    </QueryClientProvider>
  </StrictMode>
)
