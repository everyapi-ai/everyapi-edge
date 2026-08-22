import { createRoute } from '@tanstack/react-router'

import { ImageEditPlayground } from '@/features/playground/ImageEditPlayground'

import { rootRoute } from './root'

const ImageEditPage = () => <ImageEditPlayground />

export const imageEditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/image-edit',
  component: ImageEditPage,
})
