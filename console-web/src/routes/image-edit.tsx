import { useState } from 'react'

import { createRoute } from '@tanstack/react-router'
import { ImageUp } from 'lucide-react'

import { useImageRuntime } from '@/api/queries'
import { Button, PageHeader, Panel } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'

import { rootRoute } from './root'

const ImageEditPage = () => {
  const { t } = useTranslation()
  const runtime = useImageRuntime()
  const [image, setImage] = useState<File | null>(null)
  const [prompt, setPrompt] = useState('')
  const [result, setResult] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const model = runtime.data?.models[0] ?? ''

  const submit = async () => {
    if (!image || !prompt.trim() || !model) return
    setPending(true); setError(''); setResult('')
    const form = new FormData()
    form.set('image', image); form.set('prompt', prompt.trim()); form.set('model', model)
    try {
      const response = await fetch('/api/image/edit', { method: 'POST', body: form })
      const payload = await response.json() as { b64_json?: string; error?: string }
      if (!response.ok || !payload.b64_json) throw new Error(payload.error || t('imageEdit.error'))
      setResult(`data:image/png;base64,${payload.b64_json}`)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : t('imageEdit.error'))
    } finally { setPending(false) }
  }

  const ready = runtime.data?.status === 'ready'
  return <div className='flex flex-col gap-5'>
    <PageHeader title={t('imageEdit.title')} description={t('imageEdit.description')} />
    <Panel title={t('imageEdit.title')}>
      {!ready ? <p className='text-sm text-muted'>{runtime.data?.error || t('imageEdit.unavailable')}</p> : <div className='grid gap-4 lg:grid-cols-2'>
        <div className='flex flex-col gap-3'>
          <label className='text-sm font-medium text-ink-2' htmlFor='source-image'>{t('imageEdit.source')}</label>
          <input id='source-image' aria-label={t('imageEdit.source')} type='file' accept='image/*' onChange={(event) => setImage(event.target.files?.[0] ?? null)} className='block w-full text-sm text-muted file:mr-3 file:rounded-sm file:border-0 file:bg-surface-2 file:px-3 file:py-2 file:text-ink' />
          <label className='text-sm font-medium text-ink-2' htmlFor='image-instruction'>{t('imageEdit.instruction')}</label>
          <textarea id='image-instruction' aria-label={t('imageEdit.instruction')} value={prompt} onChange={(event) => setPrompt(event.target.value)} className='min-h-28 rounded-md border border-line-2 bg-surface-1 p-3 text-sm text-ink outline-none focus:border-accent' />
          <Button type='button' disabled={!image || !prompt.trim() || pending} onClick={() => void submit()} className='gap-2'><ImageUp className='size-4' />{pending ? t('imageEdit.editing') : t('imageEdit.submit')}</Button>
          {error ? <p role='alert' className='text-sm text-danger'>{error}</p> : null}
        </div>
        <div className='rounded-md border border-dashed border-line p-3'>{result ? <img src={result} alt={t('imageEdit.result')} className='max-h-[520px] w-full object-contain' /> : <p className='text-sm text-muted'>{t('imageEdit.result')}</p>}</div>
      </div>}
    </Panel>
  </div>
}

export const imageEditRoute = createRoute({ getParentRoute: () => rootRoute, path: '/image-edit', component: ImageEditPage })
