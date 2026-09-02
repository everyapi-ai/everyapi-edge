import { logsRoute } from '@/routes/logs'
import { imageEditRoute } from '@/routes/image-edit'
import { modelsRoute } from '@/routes/models'
import { overviewRoute } from '@/routes/overview'
import { playgroundRoute } from '@/routes/playground'
import { runtimeRoute } from '@/routes/runtime'
import { settingsRoute } from '@/routes/settings'
import { storageRoute } from '@/routes/storage'
import { trafficRoute } from '@/routes/traffic'

/** The single list of pages the console serves. main.tsx builds the router from it and navigation.test.ts checks it against the navigation rail, so a page added here without a rail entry fails the suite instead of shipping unreachable. */
export const ROUTES = [
  overviewRoute,
  runtimeRoute,
  storageRoute,
  settingsRoute,
  modelsRoute,
  playgroundRoute,
  imageEditRoute,
  trafficRoute,
  logsRoute,
]
