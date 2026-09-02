import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  RouterProvider,
  createBrowserHistory,
  createRouter,
  type AnyRoute,
} from '@tanstack/react-router'

import { ROUTES } from '@/app/routes'
import { rootRoute } from '@/routes/root'

import './styles.css'

// The Go handler's catch-all document route makes real paths safe to reload.
// Browser history therefore keeps each local console page shareable and
// bookmarkable, including LAN URLs such as http://edge-host:8421/models.
const history = createBrowserHistory()

const routeTree: AnyRoute = rootRoute.addChildren(ROUTES)

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
  </StrictMode>,
)
