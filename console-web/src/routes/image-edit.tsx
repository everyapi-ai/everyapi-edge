import { useEffect, useRef, useState } from 'react'

import { createRoute } from '@tanstack/react-router'
import { ImageUp } from 'lucide-react'

import { useImageRuntime } from '@/api/queries'
import { parseErrorResponse } from '@/api/errors'
import { Button, PageHeader, Panel } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'

import { rootRoute } from './root'

const ImageEditPage = () => {
  const { t } = useTranslation()
  const runtime = useImageRuntime()
  const [image, setImage] = useState<File | null>(null)
  const [sourcePreview, setSourcePreview] = useState('')
  const sourcePreviewRef = useRef('')
  const editControllerRef = useRef<AbortController | null>(null)
  const [prompt, setPrompt] = useState('')
  const [result, setResult] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const model = runtime.data?.models[0] ?? ''

  useEffect(
    () => () => {
      if (sourcePreviewRef.current) URL.revokeObjectURL(sourcePreviewRef.current)
      editControllerRef.current?.abort()
    },
    [],
  )

  const selectSourceImage = (file: File | undefined) => {
    setImage(file ?? null)
    setResult('')
    setError('')
    if (sourcePreviewRef.current) URL.revokeObjectURL(sourcePreviewRef.current)
    const nextPreview = file ? URL.createObjectURL(file) : ''
    sourcePreviewRef.current = nextPreview
    setSourcePreview(nextPreview)
  }

  const submit = async () => {
    if (!image || !prompt.trim() || !model) return
    const controller = new AbortController()
    editControllerRef.current = controller
    setPending(true)
    setError('')
    setResult('')
    const form = new FormData()
    form.set('image', image)
    form.set('prompt', prompt.trim())
    form.set('model', model)
    try {
      const response = await fetch('/api/image/edit', {
        method: 'POST',
        body: form,
        signal: controller.signal,
      })
      if (!response.ok) throw await parseErrorResponse(response)
      const payload = (await response.json()) as { b64_json?: string }
      if (!payload.b64_json) throw new Error(t('imageEdit.error'))
      setResult(`data:image/png;base64,${payload.b64_json}`)
    } catch (reason) {
      if (!controller.signal.aborted)
        setError(reason instanceof Error ? reason.message : t('imageEdit.error'))
    } finally {
      if (editControllerRef.current === controller) editControllerRef.current = null
      setPending(false)
    }
  }

  const stop = () => editControllerRef.current?.abort()

  const ready = runtime.data?.status === 'ready'
  const runtimeError =
    runtime.data?.error === 'A CUDA-capable GPU is required for image editing.'
      ? t('imageEdit.gpuRequired')
      : runtime.data?.error || t('imageEdit.unavailable')
  return (
    <div className='flex flex-col gap-5'>
      <PageHeader title={t('imageEdit.title')} description={t('imageEdit.description')} />
      <Panel title={t('imageEdit.title')}>
        {!ready ? (
          <p data-image-runtime-error className='mb-4 text-sm text-muted'>
            {runtimeError}
          </p>
        ) : null}
        <div className='grid gap-4 lg:grid-cols-2'>
          <div className='flex flex-col gap-3'>
            <label className='text-sm font-medium text-ink-2' htmlFor='source-image'>
              {t('imageEdit.source')}
            </label>
            <label
              data-source-file-picker
              htmlFor='source-image'
              className='flex cursor-pointer items-center gap-3 rounded-md border border-dashed border-line-2 bg-surface-1 px-3 py-2.5 text-sm transition-colors hover:border-accent/60 hover:bg-surface-2'
            >
              <span className='grid size-8 shrink-0 place-items-center rounded-sm bg-accent/14 text-accent'>
                <ImageUp className='size-4' aria-hidden='true' />
              </span>
              <span className='min-w-0'>
                <span className='block font-medium text-ink'>{t('imageEdit.chooseSource')}</span>
                <span
                  data-source-file-name
                  className='mt-0.5 block truncate font-mono text-xs text-muted'
                >
                  {image?.name || t('imageEdit.noSource')}
                </span>
              </span>
            </label>
            <input
              id='source-image'
              aria-label={t('imageEdit.source')}
              type='file'
              accept='image/*'
              onChange={(event) => selectSourceImage(event.target.files?.[0])}
              className='sr-only'
            />
            {sourcePreview && image ? (
              <figure className='rounded-md border border-line bg-surface-1 p-2.5'>
                <img
                  data-image-source-preview
                  src={sourcePreview}
                  alt={image.name}
                  className='max-h-56 w-full rounded-sm object-contain'
                />
                <figcaption className='mt-2 truncate font-mono text-xs text-muted'>
                  {image.name}
                </figcaption>
              </figure>
            ) : null}
            <p className='text-xs text-muted'>
              {t('imageEdit.activeModel')}:{' '}
              <span data-image-editor-model className='font-mono text-ink'>
                {model}
              </span>
            </p>
            <label className='text-sm font-medium text-ink-2' htmlFor='image-instruction'>
              {t('imageEdit.instruction')}
            </label>
            <textarea
              id='image-instruction'
              aria-label={t('imageEdit.instruction')}
              value={prompt}
              onChange={(event) => setPrompt(event.target.value)}
              className='min-h-28 rounded-md border border-line-2 bg-surface-1 p-3 text-sm text-ink outline-none focus:border-accent'
            />
            {pending ? (
              <Button
                type='button'
                variant='ghost'
                data-stop-image-edit
                onClick={stop}
                className='gap-2'
              >
                <ImageUp className='size-4' />
                {t('imageEdit.stop')}
              </Button>
            ) : (
              <Button
                type='button'
                disabled={!ready || !image || !prompt.trim()}
                onClick={() => void submit()}
                className='gap-2'
              >
                <ImageUp className='size-4' />
                {t('imageEdit.submit')}
              </Button>
            )}
            {error ? (
              <p role='alert' className='text-sm text-danger'>
                {error}
              </p>
            ) : null}
          </div>
          <div className='rounded-md border border-dashed border-line p-3'>
            {result ? (
              <div>
                <img
                  src={result}
                  alt={t('imageEdit.result')}
                  className='max-h-[520px] w-full object-contain'
                />
                <a
                  href={result}
                  download='everyapi-image-edit.png'
                  className='mt-3 inline-flex text-sm font-medium text-accent hover:text-ink'
                >
                  {t('imageEdit.download')}
                </a>
              </div>
            ) : (
              <p className='text-sm text-muted'>{t('imageEdit.result')}</p>
            )}
          </div>
        </div>
      </Panel>
    </div>
  )
}

export const imageEditRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/image-edit',
  component: ImageEditPage,
})
