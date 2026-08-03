import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'

import { createRoute } from '@tanstack/react-router'
import { ArrowUp, Bot, ImagePlus, RotateCcw, Square, UserRound, X } from 'lucide-react'

import { postJSONStream } from '@/api/client'
import { useModelCapabilities, useModels } from '@/api/queries'
import { playgroundStreamEventSchema } from '@/api/schemas'
import { Button, PageHeader, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'

import { rootRoute } from './root'

type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
  attachment?: { name: string; dataURL: string; base64: string }
  usage?: { prompt_tokens: number; completion_tokens: number }
}

const PlaygroundPage = () => {
  const { t } = useTranslation()
  const models = useModels()
  const search = playgroundRoute.useSearch()
  const [model, setModel] = useState(search.model)
  const [system, setSystem] = useState('')
  const [temperature, setTemperature] = useState(0.7)
  const [draft, setDraft] = useState('')
  const [attachment, setAttachment] = useState<ChatMessage['attachment']>()
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [isStreaming, setIsStreaming] = useState(false)
  const [error, setError] = useState('')
  const streamAbort = useRef<AbortController | null>(null)
  const availableModels = models.data ?? []
  const selectedModel = model || availableModels[0]?.name || ''
  const capabilities = useModelCapabilities(selectedModel)
  const supportsImages = capabilities.data?.capabilities.includes('vision') ?? false
  const attachmentDisabled = isStreaming || capabilities.isPending || !supportsImages
  const capabilityLabel = capabilities.isPending
    ? t('playground.imageSupportChecking')
    : capabilities.isError
      ? t('playground.modelCapabilityUnavailable')
      : [supportsImages ? t('playground.modelMultimodal') : t('playground.modelText'), capabilities.data?.capabilities.includes('tools') ? t('playground.modelTools') : ''].filter(Boolean).join(' · ')

  useEffect(() => {
    if (model && !availableModels.some((candidate) => candidate.name === model)) setModel('')
  }, [availableModels, model])

  const runChat = async (conversation: ChatMessage[]) => {
    const controller = new AbortController()
    streamAbort.current = controller
    setError('')
    setIsStreaming(true)
    setMessages([...conversation, { role: 'assistant', content: '' }])
    try {
      await postJSONStream(
        '/api/playground/chat',
        {
          model: selectedModel,
          messages: conversation.map(({ role, content, attachment }) => ({ role, content, images: attachment ? [attachment.base64] : [] })),
          system,
          temperature,
          stream: true,
        },
        playgroundStreamEventSchema,
        (event) => {
          if (event.type === 'error') throw new Error(event.error || t('playground.responseError'))
          if (event.type === 'delta') {
            setMessages((current) => current.map((message, index) => (
              index === current.length - 1 ? { ...message, content: message.content + event.content } : message
            )))
          }
          if (event.type === 'done') {
            setMessages((current) => current.map((message, index) => (
              index === current.length - 1
                ? { ...message, usage: event.usage ? { prompt_tokens: event.usage.prompt_tokens, completion_tokens: event.usage.completion_tokens } : undefined }
                : message
            )))
          }
        },
        controller.signal,
      )
    } catch (cause) {
      setMessages((current) => current.filter((_, index) => index !== current.length - 1 || current[index].content))
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t('playground.responseError'))
    } finally {
      if (streamAbort.current === controller) streamAbort.current = null
      setIsStreaming(false)
    }
  }

  const stop = () => streamAbort.current?.abort()

  const send = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const content = draft.trim()
    if ((!content && !attachment) || !selectedModel || isStreaming) return
    setDraft('')
    setAttachment(undefined)
    void runChat([...messages, { role: 'user', content, attachment }])
  }

  const attachImage = (file: File | undefined) => {
    if (!file) return
    if (file.size > 4 * 1024 * 1024) {
      setError(t('playground.imageTooLarge'))
      return
    }
    const reader = new FileReader()
    reader.onerror = () => setError(t('playground.imageReadError'))
    reader.onload = () => {
      const dataURL = typeof reader.result === 'string' ? reader.result : ''
      const base64 = dataURL.split(',', 2)[1]
      if (!base64) {
        setError(t('playground.imageReadError'))
        return
      }
      setError('')
      setAttachment({ name: file.name, dataURL, base64 })
    }
    reader.readAsDataURL(file)
  }

  const regenerate = (assistantIndex: number) => {
    if (isStreaming) return
    const conversation = messages.slice(0, assistantIndex)
    if (conversation.at(-1)?.role === 'user') void runChat(conversation)
  }

  const canSend = Boolean(selectedModel && (draft.trim() || attachment) && !isStreaming && (!attachment || supportsImages))
  const transcript = useMemo(() => messages, [messages])

  return (
    <div className='flex min-h-[calc(100svh-8.5rem)] flex-col gap-5'>
      <PageHeader
        title={t('playground.title')}
        description={t('playground.description')}
        actions={transcript.length ? (
          <Button type='button' variant='ghost' disabled={isStreaming} onClick={() => { setMessages([]); setError('') }} className='inline-flex items-center gap-2'>
            <RotateCcw className='size-3.5' aria-hidden='true' />
            {t('playground.clear')}
          </Button>
        ) : null}
      />
      <QueryState isPending={models.isPending} isError={models.isError} onRetry={() => void models.refetch()}>
        {availableModels.length === 0 ? (
          <div className='rounded-xl border border-line bg-surface-0 px-5 py-12 text-center text-sm text-muted'>{t('playground.noModels')}</div>
        ) : (
          <section className='flex min-h-[580px] flex-1 flex-col overflow-hidden rounded-xl border border-line bg-surface-0 shadow-[0_16px_40px_-30px_rgba(0,0,0,0.9)]'>
            <div className='border-line grid gap-3 border-b bg-surface-1 px-4 py-3 lg:grid-cols-[minmax(170px,0.7fr)_minmax(260px,1.5fr)_minmax(190px,0.6fr)] lg:items-end'>
              <label className='block text-xs font-medium text-muted'>
                <span className='flex items-center justify-between gap-2'>
                  <span>{t('playground.model')}</span>
                  <span data-model-capability className='font-normal text-accent'>{capabilityLabel}</span>
                </span>
                <select
                  value={selectedModel}
                  onChange={(event) => setModel(event.target.value)}
                  disabled={isStreaming}
                  className='mt-1.5 block w-full rounded-md border border-line-2 bg-surface-0 px-2.5 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent focus:ring-2 focus:ring-accent/20'
                >
                  {availableModels.map((candidate) => <option key={candidate.name} value={candidate.name}>{candidate.name}</option>)}
                </select>
              </label>
              <label className='block text-xs font-medium text-muted'>
                {t('playground.system')}
                <input
                  aria-label={t('playground.system')}
                  value={system}
                  onChange={(event) => setSystem(event.target.value)}
                  disabled={isStreaming}
                  placeholder={t('playground.systemHint')}
                  className='mt-1.5 block w-full rounded-md border border-line-2 bg-surface-0 px-2.5 py-1.5 text-sm text-ink outline-none placeholder:text-faint focus:border-accent focus:ring-2 focus:ring-accent/20'
                />
              </label>
              <label className='block text-xs font-medium text-muted'>
                <span className='flex items-center justify-between'><span>{t('playground.temperature')}</span><output>{temperature.toFixed(1)}</output></span>
                <input
                  aria-label={t('playground.temperature')}
                  type='range'
                  min='0'
                  max='2'
                  step='0.1'
                  value={temperature}
                  disabled={isStreaming}
                  onChange={(event) => setTemperature(Number(event.target.value))}
                  className='accent-accent mt-2 block w-full disabled:cursor-not-allowed'
                />
              </label>
            </div>
            <div className='flex flex-1 flex-col gap-5 overflow-y-auto px-4 py-6 sm:px-7'>
              {transcript.length === 0 ? (
                <div className='m-auto max-w-sm text-center'>
                  <span className='mx-auto grid size-10 place-items-center rounded-xl bg-accent/12 text-accent'><Bot className='size-5' aria-hidden='true' /></span>
                  <h2 className='mt-4 text-base font-semibold text-ink'>{t('playground.empty')}</h2>
                  <p className='mt-2 text-sm leading-6 text-muted'>{t('playground.emptyHint')}</p>
                </div>
              ) : transcript.map((message, index) => {
                const user = message.role === 'user'
                const pending = !user && isStreaming && index === transcript.length - 1
                return (
                  <article key={`${message.role}-${index}`} className={`flex gap-3 ${user ? 'flex-row-reverse' : ''}`}>
                    <span className={`grid size-7 shrink-0 place-items-center rounded-md ${user ? 'bg-surface-2 text-ink-2' : 'bg-accent/12 text-accent'}`}>
                      {user ? <UserRound className='size-3.5' aria-hidden='true' /> : <Bot className='size-3.5' aria-hidden='true' />}
                    </span>
                    <div className={`max-w-[82%] ${user ? 'text-right' : ''}`}>
                      <p className='mb-1 text-xs font-medium text-faint'>{user ? t('playground.you') : t('playground.assistant')}</p>
                      <div className={`whitespace-pre-wrap rounded-xl px-3.5 py-3 text-sm leading-6 ${user ? 'bg-accent text-accent-ink' : 'border border-line bg-surface-1 text-ink'}`}>
                        {message.attachment ? <img src={message.attachment.dataURL} alt={message.attachment.name} className='mb-2 max-h-64 max-w-full rounded-md object-contain' /> : null}
                        {message.content || (pending ? t('playground.sending') : '')}
                      </div>
                      {!user && !pending ? <Button type='button' variant='ghost' onClick={() => regenerate(index)} className='mt-2 px-2 py-1 text-xs'>{t('playground.regenerate')}</Button> : null}
                      {message.usage ? <p className='mt-1 text-xs text-faint'>{t('playground.usage', { prompt: message.usage.prompt_tokens, completion: message.usage.completion_tokens })}</p> : null}
                    </div>
                  </article>
                )
              })}
            </div>
            <form onSubmit={send} className='border-line border-t bg-surface-1 p-3 sm:p-4'>
              <div className='rounded-lg border border-line-2 bg-surface-0 p-2 focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/20'>
                <textarea
                  name='playground-message'
                  value={draft}
                  onChange={(event) => setDraft(event.target.value)}
                  placeholder={t('playground.placeholder')}
                  rows={3}
                  disabled={isStreaming}
                  className='block w-full resize-none bg-transparent px-2 py-1 text-sm leading-6 text-ink outline-none placeholder:text-faint disabled:cursor-not-allowed'
                />
                <div className='flex items-center justify-between gap-3 pt-1'>
                  <div className='min-w-0'>
                    {attachment ? (
                      <span className='flex items-center gap-2 text-xs text-muted'>
                        <ImagePlus className='size-3.5 shrink-0 text-accent' aria-hidden='true' />
                        <span className='truncate'>{attachment.name}</span>
                        <button type='button' aria-label={t('playground.removeImage')} disabled={isStreaming} onClick={() => setAttachment(undefined)} className='text-muted hover:text-ink disabled:cursor-not-allowed'><X className='size-3.5' aria-hidden='true' /></button>
                      </span>
                    ) : (
                      <label className={`inline-flex items-center gap-1.5 text-xs text-muted ${attachmentDisabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:text-ink'}`}>
                        <ImagePlus className='size-3.5 text-accent' aria-hidden='true' />
                        <span>{capabilities.isPending ? t('playground.imageSupportChecking') : t('playground.attachImage')}</span>
                        <input aria-label={t('playground.attachImage')} type='file' accept='image/*' disabled={attachmentDisabled} onChange={(event) => attachImage(event.target.files?.[0])} className='sr-only' />
                      </label>
                    )}
                  </div>
                  {isStreaming ? (
                    <Button type='button' variant='ghost' data-stop-generation onClick={stop} className='inline-flex items-center gap-1.5'>
                      <Square className='size-3.5' aria-hidden='true' />
                      {t('playground.stop')}
                    </Button>
                  ) : (
                    <Button type='submit' disabled={!canSend} className='inline-flex items-center gap-1.5'>
                      <ArrowUp className='size-3.5' aria-hidden='true' />
                      {t('playground.send')}
                    </Button>
                  )}
                </div>
              </div>
              {error ? <p role='alert' className='mt-2 text-xs text-danger'>{error}</p> : null}
              {!attachment && !capabilities.isPending && !supportsImages ? <p className='mt-2 text-xs text-muted'>{t('playground.imageUnsupported')}</p> : null}
            </form>
          </section>
        )}
      </QueryState>
    </div>
  )
}

export const playgroundRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/playground',
  validateSearch: (search: Record<string, unknown>) => ({
    model: typeof search.model === 'string' ? search.model : '',
  }),
  component: PlaygroundPage,
})
