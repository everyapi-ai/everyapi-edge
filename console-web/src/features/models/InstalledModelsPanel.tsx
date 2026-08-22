import type { Model } from '@/api/schemas'

import { InstalledModelCards } from './InstalledModelCards'
import { InstalledModelTable } from './InstalledModelTable'

export type InstalledModelsPresentationProps = {
  models: Model[]
  loadedModels: Set<string>
  activeRequests: number
  benchmarkPending: boolean
  unloadPending: boolean
  deletePending: boolean
  providerFor: (name: string) => string
  typeFor: (name: string) => string
  isImage: (name: string) => boolean
  onInspect: (name: string) => void
  onOpen: (name: string) => void
  onBenchmark: (name: string) => void
  onUnload: (name: string) => void
  onRemove: (name: string) => void
}

export const InstalledModelsPanel = (props: InstalledModelsPresentationProps) => (
  <>
    <div className='md:hidden'>
      <InstalledModelCards {...props} />
    </div>
    <div className='hidden md:block'>
      <InstalledModelTable {...props} />
    </div>
  </>
)
