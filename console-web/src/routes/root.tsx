import { createRootRoute } from '@tanstack/react-router'

import { AppShell } from '@/components/layout/AppShell'
import { SessionGate } from '@/features/session/SessionGate'

const Root = () => (
  <SessionGate>
    <AppShell />
  </SessionGate>
)

export const rootRoute = createRootRoute({ component: Root })
