import { useEffect, useMemo, useRef, useState, type FormEvent } from 'react'

import { Download, RotateCcw } from 'lucide-react'

import { postJSONStream } from '@/api/client'
import { useModelCapabilities, useModels } from '@/api/queries'
import { playgroundStreamEventSchema } from '@/api/schemas'
import { Button, PageHeader, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { ChatComposer } from './ChatComposer'
import {
  conversationStorageKey,
  maxSavedConversations,
  newConversationID,
  normalizeSavedHistory,
  serializeHistory,
  titleFromMessage,
  type ChatMessage,
  type SavedConversation,
  type SavedHistory,
} from './conversations'
import { ConversationRail } from './ConversationRail'
import { MessageHistory } from './MessageHistory'

const readSavedHistory = (): SavedHistory =>
  normalizeSavedHistory(window.localStorage.getItem(conversationStorageKey))

export const ChatPlayground = ({ initialModel = '' }: { initialModel?: string }) => {
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
    try {
      window.localStorage.setItem(conversationStorageKey, serializeHistory(history))
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
        error={models.error}
        onRetry={() => void models.refetch()}
      >
        {availableModels.length === 0 ? (
          <div className='rounded-xl border border-line bg-surface-0 px-5 py-12 text-center text-sm text-muted'>
            {t('playground.noModels')}
          </div>
        ) : (
          <section className='flex min-h-[580px] flex-1 flex-col overflow-hidden rounded-xl border border-line bg-surface-0 shadow-[0_16px_40px_-30px_rgba(0,0,0,0.9)]'>
            <ConversationRail
              conversations={history.conversations}
              activeID={history.activeID}
              disabled={isStreaming}
              onCreate={createConversation}
              onSelect={selectConversation}
              onDelete={deleteConversation}
            />
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
            <MessageHistory
              messages={transcript}
              isStreaming={isStreaming}
              onRegenerate={regenerate}
            />
            <ChatComposer
              draft={draft}
              attachment={attachment}
              isStreaming={isStreaming}
              attachmentDisabled={attachmentDisabled}
              capabilitiesPending={capabilities.isPending}
              supportsImages={supportsImages}
              canSend={canSend}
              error={error}
              onDraft={setDraft}
              onAttachment={setAttachment}
              onAttach={attachImage}
              onStop={stop}
              onSubmit={send}
            />
          </section>
        )}
      </QueryState>
    </div>
  )
}
