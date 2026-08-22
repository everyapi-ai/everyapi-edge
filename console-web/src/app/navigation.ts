import {
  Activity,
  Boxes,
  Cpu,
  HardDrive,
  MessageSquareText,
  ScrollText,
  SlidersHorizontal,
  type LucideIcon,
} from 'lucide-react'

import type { MessageKey } from '@/i18n/locales'

export type NavigationItem = {
  to: string
  labelKey: MessageKey
  icon: LucideIcon
}

export type NavigationGroup = {
  labelKey: MessageKey
  items: NavigationItem[]
}

export const NAVIGATION_GROUPS: NavigationGroup[] = [
  {
    labelKey: 'nav.groupOperate',
    items: [
      { to: '/', labelKey: 'nav.overview', icon: SlidersHorizontal },
      { to: '/runtime', labelKey: 'nav.runtime', icon: Cpu },
      { to: '/models', labelKey: 'nav.models', icon: Boxes },
      { to: '/playground', labelKey: 'nav.playground', icon: MessageSquareText },
    ],
  },
  {
    labelKey: 'nav.groupMaintain',
    items: [{ to: '/storage', labelKey: 'nav.storage', icon: HardDrive }],
  },
  {
    labelKey: 'nav.groupObserve',
    items: [
      { to: '/traffic', labelKey: 'nav.traffic', icon: Activity },
      { to: '/logs', labelKey: 'nav.logs', icon: ScrollText },
    ],
  },
]

export const NAVIGATION_ITEMS = NAVIGATION_GROUPS.flatMap((group) => group.items)
