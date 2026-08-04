import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'

import {
  ArrowUp,
  Bot,
  Download,
  ImagePlus,
  Plus,
  RotateCcw,
  Square,
  Trash2,
  UserRound,
  X,
} from 'lucide-react'

import { postJSONStream } from '@/api/client'
import { useModelCapabilities, useModels } from '@/api/queries'
import { playgroundStreamEventSchema } from '@/api/schemas'
import { Button, PageHeader, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'

type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
  attachment?: { name: string; dataURL: string; base64: string }
  usage?: { prompt_tokens: number; completion_tokens: number }
}

type SavedConversation = {
  id: string
  title: string
  model: string
  system: string
  temperature: number
  messages: ChatMessage[]
}

type SavedHistory = {
  activeID: string
  conversations: SavedConversation[]
}

const conversationStorageKey = 'everyapi.edge.playground.v1'
const maxSavedConversations = 30

const newConversationID = () =>
  globalThis.crypto?.randomUUID?.() ??
  `conversation-${Date.now()}-${Math.random().toString(36).slice(2)}`

const titleFromMessage = (content: string, fallback: string) => {
  const normalized = content.replace(/\s+/g, ' ').trim()
  if (!normalized) return fallback
  return normalized.length > 48 ? `${normalized.slice(0, 47)}…` : normalized
}

const readSavedHistory = (): SavedHistory => {
  try {
    const raw = window.localStorage.getItem(conversationStorageKey)
    if (!raw) return { activeID: '', conversations: [] }
    const value: unknown = JSON.parse(raw)
    if (
      !value ||
      typeof value !== 'object' ||
      !Array.isArray((value as { conversations?: unknown }).conversations)
    )
      return { activeID: '', conversations: [] }
    const conversations = (value as { conversations: unknown[] }).conversations
      .flatMap((entry): SavedConversation[] => {
        if (!entry || typeof entry !== 'object') return []
        const candidate = entry as Record<string, unknown>
        if (
          typeof candidate.id !== 'string' ||
          typeof candidate.title !== 'string' ||
          typeof candidate.model !== 'string'
        )
          return []
        const messages = Array.isArray(candidate.messages)
          ? candidate.messages.flatMap((message): ChatMessage[] => {
              if (!message || typeof message !== 'object') return []
              const saved = message as Record<string, unknown>
              if (
                (saved.role !== 'user' && saved.role !== 'assistant') ||
                typeof saved.content !== 'string'
              )
                return []
              const usage =
                saved.usage &&
                typeof saved.usage === 'object' &&
                typeof (saved.usage as Record<string, unknown>).prompt_tokens === 'number' &&
                typeof (saved.usage as Record<string, unknown>).completion_tokens === 'number'
                  ? {
                      prompt_tokens: (saved.usage as Record<string, number>).prompt_tokens,
                      completion_tokens: (saved.usage as Record<string, number>).completion_tokens,
                    }
                  : undefined
              return [{ role: saved.role as ChatMessage['role'], content: saved.content, usage }]
            })
          : []
        return [
          {
            id: candidate.id,
            title: candidate.title,
            model: candidate.model,
            system: typeof candidate.system === 'string' ? candidate.system : '',
            temperature:
              typeof candidate.temperature === 'number' &&
              candidate.temperature >= 0 &&
              candidate.temperature <= 2
                ? candidate.temperature
                : 0.7,
            messages,
          },
        ]
      })
      .slice(0, maxSavedConversations)
    const requestedActiveID =
      typeof (value as { active_id?: unknown }).active_id === 'string'
        ? (value as { active_id: string }).active_id
        : ''
    return {
      activeID: conversations.some((conversation) => conversation.id === requestedActiveID)
        ? requestedActiveID
        : (conversations[0]?.id ?? ''),
      conversations,
    }
  } catch {
    return { activeID: '', conversations: [] }
  }
}

export const PlaygroundPage = ({ initialModel = '' }: { initialModel?: string }) => {
  const { t } = useTranslation()
  const models = useModels()
  const [history, setHistory] = useState<SavedHistory>(readSavedHistory)
  const initialConversation =
    history.conversations.find((conversation) => conversation.id === history.activeID) ??
    history.conversations[0]
  const [model, setModel] = useState(initialConversation?.model ?? initialModel)
  const [system, setSystem] = useState(initialConversation?.system ?? '')
  const [temperature, setTemperature] = useState(initialConversation?.temperature ?? 0.7)
  const [draft, setDraft] = useState('')
  const [attachment, setAttachment] = useState<ChatMessage['attachment']>()
  const [messages, setMessages] = useState<ChatMessage[]>(initialConversation?.messages ?? [])
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
      : [
          supportsImages ? t('playground.modelMultimodal') : t('playground.modelText'),
          capabilities.data?.capabilities.includes('tools') ? t('playground.modelTools') : '',
        ]
          .filter(Boolean)
          .join(' · ')

  useEffect(() => {
    if (models.data && model && !availableModels.some((candidate) => candidate.name === model))
      setModel(availableModels[0]?.name ?? '')
  }, [availableModels, model, models.data])

  useEffect(() => {
    if (!history.activeID && availableModels.length) {
      const conversation: SavedConversation = {
        id: newConversationID(),
        title: t('playground.newConversation'),
        model: model || availableModels[0].name,
        system,
        temperature,
        messages,
      }
      setHistory({ activeID: conversation.id, conversations: [conversation] })
    }
  }, [availableModels, history.activeID, messages, model, system, t, temperature])

  useEffect(() => {
    if (!history.activeID) return
    setHistory((current) => ({
      ...current,
      conversations: current.conversations.map((conversation) =>
        conversation.id === current.activeID
          ? {
              ...conversation,
              title: messages.some((message) => message.role === 'user')
                ? conversation.title
                : t('playground.newConversation'),
              model,
              system,
              temperature,
              messages,
            }
          : conversation,
      ),
    }))
  }, [history.activeID, messages, model, system, t, temperature])

  useEffect(() => {
    if (!history.conversations.length) return
    const persisted = {
      version: 1,
      active_id: history.activeID,
      conversations: history.conversations.map(
        ({ messages: conversationMessages, ...conversation }) => ({
          ...conversation,
          // Keep images out of browser storage; a local chat history should not
          // silently turn into a second multi-gigabyte image library.
          messages: conversationMessages.map(({ attachment: _attachment, ...message }) => message),
        }),
      ),
    }
    try {
      window.localStorage.setItem(conversationStorageKey, JSON.stringify(persisted))
    } catch {
      // Browser storage is optional; never block the local chat when it is full.
    }
  }, [history])

  const createConversation = () => {
    if (isStreaming) return
    const conversation: SavedConversation = {
      id: newConversationID(),
      title: t('playground.newConversation'),
      model: selectedModel,
      system: '',
      temperature: 0.7,
      messages: [],
    }
    setHistory((current) => ({
      activeID: conversation.id,
      conversations: [conversation, ...current.conversations].slice(0, maxSavedConversations),
    }))
    setModel(conversation.model)
    setSystem('')
    setTemperature(0.7)
    setMessages([])
    setDraft('')
    setAttachment(undefined)
    setError('')
  }

  const selectConversation = (conversation: SavedConversation) => {
    if (isStreaming) return
    setHistory((current) => ({ ...current, activeID: conversation.id }))
    setModel(conversation.model)
    setSystem(conversation.system)
    setTemperature(conversation.temperature)
    setMessages(conversation.messages)
    setDraft('')
    setAttachment(undefined)
    setError('')
  }

  const deleteConversation = (id: string) => {
    if (isStreaming) return
    const remaining = history.conversations.filter((conversation) => conversation.id !== id)
    if (!remaining.length) {
      const conversation: SavedConversation = {
        id: newConversationID(),
        title: t('playground.newConversation'),
        model: selectedModel,
        system: '',
        temperature: 0.7,
        messages: [],
      }
      setHistory({ activeID: conversation.id, conversations: [conversation] })
      setSystem('')
      setTemperature(0.7)
      setMessages([])
      setDraft('')
      setAttachment(undefined)
      setError('')
      return
    }
    const next =
      history.activeID === id
        ? remaining[0]
        : (remaining.find((conversation) => conversation.id === history.activeID) ?? remaining[0])
    setHistory({ activeID: next.id, conversations: remaining })
    if (history.activeID === id) selectConversation(next)
  }

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
          messages: conversation.map(({ role, content, attachment }) => ({
            role,
            content,
            images: attachment ? [attachment.base64] : [],
          })),
          system,
          temperature,
          stream: true,
        },
        playgroundStreamEventSchema,
        (event) => {
          if (event.type === 'error') throw new Error(event.error || t('playground.responseError'))
          if (event.type === 'delta') {
            setMessages((current) =>
              current.map((message, index) =>
                index === current.length - 1
                  ? { ...message, content: message.content + event.content }
                  : message,
              ),
            )
          }
          if (event.type === 'done') {
            setMessages((current) =>
              current.map((message, index) =>
                index === current.length - 1
                  ? {
                      ...message,
                      usage: event.usage
                        ? {
                            prompt_tokens: event.usage.prompt_tokens,
                            completion_tokens: event.usage.completion_tokens,
                          }
                        : undefined,
                    }
                  : message,
              ),
            )
          }
        },
        controller.signal,
      )
    } catch (cause) {
      setMessages((current) =>
        current.filter((_, index) => index !== current.length - 1 || current[index].content),
      )
      if (!controller.signal.aborted)
        setError(cause instanceof Error ? cause.message : t('playground.responseError'))
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
    setHistory((current) => ({
      ...current,
      conversations: current.conversations.map((conversation) =>
        conversation.id === current.activeID &&
        !conversation.messages.some((message) => message.role === 'user')
          ? { ...conversation, title: titleFromMessage(content, conversation.title) }
          : conversation,
      ),
    }))
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

  const exportConversation = () => {
    const sections = [
      `# ${history.conversations.find((conversation) => conversation.id === history.activeID)?.title || t('playground.title')}`,
      `Model: ${selectedModel}`,
      system ? `System: ${system}` : '',
      '',
      ...messages.flatMap((message) => [
        `## ${message.role === 'user' ? t('playground.you') : t('playground.assistant')}`,
        message.content,
        '',
      ]),
    ].filter(Boolean)
    const url = URL.createObjectURL(
      new Blob([sections.join('\n')], { type: 'text/markdown;charset=utf-8' }),
    )
    const link = document.createElement('a')
    link.href = url
    link.download = 'everyapi-local-conversation.md'
    link.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 0)
  }

  const canSend = Boolean(
    selectedModel &&
    (draft.trim() || attachment) &&
    !isStreaming &&
    (!attachment || supportsImages),
  )
  const transcript = useMemo(() => messages, [messages])

  return (
    <div className='flex min-h-[calc(100svh-8.5rem)] flex-col gap-5'>
      <PageHeader
        title={t('playground.title')}
        description={t('playground.description')}
        actions={
          transcript.length ? (
            <div className='flex items-center gap-2'>
              <Button
                type='button'
                variant='ghost'
                disabled={isStreaming}
                onClick={exportConversation}
                className='inline-flex items-center gap-2'
              >
                <Download className='size-3.5' aria-hidden='true' />
                {t('playground.export')}
              </Button>
              <Button
                type='button'
                variant='ghost'
                disabled={isStreaming}
                onClick={() => {
                  setMessages([])
                  setError('')
                }}
                className='inline-flex items-center gap-2'
              >
                <RotateCcw className='size-3.5' aria-hidden='true' />
                {t('playground.clear')}
              </Button>
            </div>
          ) : null
        }
      />
      <QueryState
        isPending={models.isPending}
        isError={models.isError}
        onRetry={() => void models.refetch()}
      >
        {availableModels.length === 0 ? (
          <div className='rounded-xl border border-line bg-surface-0 px-5 py-12 text-center text-sm text-muted'>
            {t('playground.noModels')}
          </div>
        ) : (
          <section className='flex min-h-[580px] flex-1 flex-col overflow-hidden rounded-xl border border-line bg-surface-0 shadow-[0_16px_40px_-30px_rgba(0,0,0,0.9)]'>
            <div
              data-playground-history
              className='border-line flex flex-col gap-2 border-b bg-surface-1 px-4 py-3 sm:flex-row sm:items-center'
            >
              <Button
                type='button'
                variant='ghost'
                disabled={isStreaming}
                onClick={createConversation}
                className='inline-flex shrink-0 items-center justify-center gap-1.5 px-2.5 py-1.5 text-xs'
              >
                <Plus className='size-3.5' aria-hidden='true' />
                {t('playground.newConversation')}
              </Button>
              <p className='shrink-0 text-xs font-medium text-faint'>{t('playground.history')}</p>
              <div className='flex min-w-0 gap-1 overflow-x-auto pb-0.5'>
                {history.conversations.map((conversation) => (
                  <div
                    key={conversation.id}
                    className={`group flex shrink-0 items-center rounded-md ${conversation.id === history.activeID ? 'bg-surface-2' : 'hover:bg-surface-2'}`}
                  >
                    <button
                      type='button'
                      data-playground-session
                      onClick={() => selectConversation(conversation)}
                      className='max-w-48 truncate px-2.5 py-1.5 text-left text-xs text-ink-2'
                    >
                      {conversation.title}
                    </button>
                    <button
                      type='button'
                      aria-label={t('playground.deleteConversation', { title: conversation.title })}
                      disabled={isStreaming}
                      onClick={() => deleteConversation(conversation.id)}
                      className='mr-1 grid size-5 place-items-center rounded-sm text-faint hover:bg-danger/18 hover:text-danger disabled:cursor-not-allowed'
                    >
                      <Trash2 className='size-3' aria-hidden='true' />
                    </button>
                  </div>
                ))}
              </div>
              <p className='text-xs text-faint sm:ml-auto'>{t('playground.historyLocal')}</p>
            </div>
            <div className='border-line grid gap-3 border-b bg-surface-1 px-4 py-3 lg:grid-cols-[minmax(170px,0.7fr)_minmax(260px,1.5fr)_minmax(190px,0.6fr)] lg:items-end'>
              <label className='block text-xs font-medium text-muted'>
                <span className='flex items-center justify-between gap-2'>
                  <span>{t('playground.model')}</span>
                  <span data-model-capability className='font-normal text-accent'>
                    {capabilityLabel}
                  </span>
                </span>
                <select
                  value={selectedModel}
                  onChange={(event) => setModel(event.target.value)}
                  disabled={isStreaming}
                  className='mt-1.5 block w-full rounded-md border border-line-2 bg-surface-0 px-2.5 py-1.5 font-mono text-sm text-ink outline-none focus:border-accent focus:ring-2 focus:ring-accent/20'
                >
                  {availableModels.map((candidate) => (
                    <option key={candidate.name} value={candidate.name}>
                      {candidate.name}
                    </option>
                  ))}
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
                <span className='flex items-center justify-between'>
                  <span>{t('playground.temperature')}</span>
                  <output>{temperature.toFixed(1)}</output>
                </span>
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
            <div
              data-playground-transcript
              className='flex flex-1 flex-col gap-5 overflow-y-auto px-4 py-6 sm:px-7'
            >
              {transcript.length === 0 ? (
                <div className='m-auto max-w-sm text-center'>
                  <span className='mx-auto grid size-10 place-items-center rounded-xl bg-accent/12 text-accent'>
                    <Bot className='size-5' aria-hidden='true' />
                  </span>
                  <h2 className='mt-4 text-base font-semibold text-ink'>{t('playground.empty')}</h2>
                  <p className='mt-2 text-sm leading-6 text-muted'>{t('playground.emptyHint')}</p>
                </div>
              ) : (
                transcript.map((message, index) => {
                  const user = message.role === 'user'
                  const pending = !user && isStreaming && index === transcript.length - 1
                  return (
                    <article
                      key={`${message.role}-${index}`}
                      className={`flex gap-3 ${user ? 'flex-row-reverse' : ''}`}
                    >
                      <span
                        className={`grid size-7 shrink-0 place-items-center rounded-md ${user ? 'bg-surface-2 text-ink-2' : 'bg-accent/12 text-accent'}`}
                      >
                        {user ? (
                          <UserRound className='size-3.5' aria-hidden='true' />
                        ) : (
                          <Bot className='size-3.5' aria-hidden='true' />
                        )}
                      </span>
                      <div className={`max-w-[82%] ${user ? 'text-right' : ''}`}>
                        <p className='mb-1 text-xs font-medium text-faint'>
                          {user ? t('playground.you') : t('playground.assistant')}
                        </p>
                        <div
                          className={`whitespace-pre-wrap rounded-xl px-3.5 py-3 text-sm leading-6 ${user ? 'bg-accent text-accent-ink' : 'border border-line bg-surface-1 text-ink'}`}
                        >
                          {message.attachment ? (
                            <img
                              src={message.attachment.dataURL}
                              alt={message.attachment.name}
                              className='mb-2 max-h-64 max-w-full rounded-md object-contain'
                            />
                          ) : null}
                          {message.content || (pending ? t('playground.sending') : '')}
                        </div>
                        {!user && !pending ? (
                          <Button
                            type='button'
                            variant='ghost'
                            onClick={() => regenerate(index)}
                            className='mt-2 px-2 py-1 text-xs'
                          >
                            {t('playground.regenerate')}
                          </Button>
                        ) : null}
                        {message.usage ? (
                          <p className='mt-1 text-xs text-faint'>
                            {t('playground.usage', {
                              prompt: message.usage.prompt_tokens,
                              completion: message.usage.completion_tokens,
                            })}
                          </p>
                        ) : null}
                      </div>
                    </article>
                  )
                })
              )}
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
                        <button
                          type='button'
                          aria-label={t('playground.removeImage')}
                          disabled={isStreaming}
                          onClick={() => setAttachment(undefined)}
                          className='text-muted hover:text-ink disabled:cursor-not-allowed'
                        >
                          <X className='size-3.5' aria-hidden='true' />
                        </button>
                      </span>
                    ) : (
                      <label
                        className={`inline-flex items-center gap-1.5 text-xs text-muted ${attachmentDisabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:text-ink'}`}
                      >
                        <ImagePlus className='size-3.5 text-accent' aria-hidden='true' />
                        <span>
                          {capabilities.isPending
                            ? t('playground.imageSupportChecking')
                            : t('playground.attachImage')}
                        </span>
                        <input
                          aria-label={t('playground.attachImage')}
                          type='file'
                          accept='image/*'
                          disabled={attachmentDisabled}
                          onChange={(event) => attachImage(event.target.files?.[0])}
                          className='sr-only'
                        />
                      </label>
                    )}
                  </div>
                  {isStreaming ? (
                    <Button
                      type='button'
                      variant='ghost'
                      data-stop-generation
                      onClick={stop}
                      className='inline-flex items-center gap-1.5'
                    >
                      <Square className='size-3.5' aria-hidden='true' />
                      {t('playground.stop')}
                    </Button>
                  ) : (
                    <Button
                      type='submit'
                      disabled={!canSend}
                      className='inline-flex items-center gap-1.5'
                    >
                      <ArrowUp className='size-3.5' aria-hidden='true' />
                      {t('playground.send')}
                    </Button>
                  )}
                </div>
              </div>
              {error ? (
                <p role='alert' className='mt-2 text-xs text-danger'>
                  {error}
                </p>
              ) : null}
              {!attachment && !capabilities.isPending && !supportsImages ? (
                <p className='mt-2 text-xs text-muted'>{t('playground.imageUnsupported')}</p>
              ) : null}
            </form>
          </section>
        )}
      </QueryState>
    </div>
  )
}
