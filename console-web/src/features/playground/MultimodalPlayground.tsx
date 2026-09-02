import { useEffect, useMemo, useRef, useState } from 'react'

import { AudioLines, Image, MessageSquareText, Sigma } from 'lucide-react'

import { apiFetch } from '@/api/client'
import { useCapabilities } from '@/api/queries'
import type { Capability } from '@/api/schemas'
import { Button, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'

import { ChatPlayground } from './PlaygroundPage'
import { ImageEditPlayground } from './ImageEditPlayground'

type PlaygroundMode = 'chat' | 'image' | 'speech' | 'embedding'

const tabs: Array<{ mode: PlaygroundMode; capability: Capability['id']; icon: typeof Image }> = [
  { mode: 'chat', capability: 'text.chat', icon: MessageSquareText },
  { mode: 'image', capability: 'image.generate', icon: Image },
  { mode: 'speech', capability: 'audio.tts', icon: AudioLines },
  { mode: 'embedding', capability: 'text.embedding', icon: Sigma },
]

export const MultimodalPlayground = ({ initialModel = '' }: { initialModel?: string }) => {
  const { t } = useTranslation()
  const capabilities = useCapabilities()
  const [mode, setMode] = useState<PlaygroundMode>('chat')
  const byID = useMemo(
    () => new Map((capabilities.data ?? []).map((capability) => [capability.id, capability])),
    [capabilities.data],
  )

  return (
    <div className='flex flex-col gap-4'>
      <nav
        aria-label={t('playground.mode')}
        className='flex flex-wrap gap-2 rounded-lg border border-line bg-surface-0 p-2'
      >
        {tabs.map((tab) => {
          const capability = byID.get(tab.capability)
          const unavailable = capability?.status !== 'ready'
          const Icon = tab.icon
          return (
            <button
              key={tab.mode}
              type='button'
              onClick={() => setMode(tab.mode)}
              className={`inline-flex items-center gap-2 rounded-md px-3 py-2 text-sm font-medium transition-colors ${mode === tab.mode ? 'bg-accent text-white' : 'text-muted hover:bg-surface-2 hover:text-ink'}`}
            >
              <Icon className='size-4' aria-hidden='true' />
              {t(`playground.mode.${tab.mode}`)}
              {unavailable ? (
                <span
                  className='size-1.5 rounded-full bg-warn'
                  aria-label={capability?.status ?? 'unavailable'}
                />
              ) : null}
            </button>
          )
        })}
      </nav>
      {mode === 'chat' ? <ChatPlayground initialModel={initialModel} /> : null}
      <QueryState
        isPending={capabilities.isPending}
        isError={capabilities.isError}
        error={capabilities.error}
        onRetry={() => void capabilities.refetch()}
      >
        {mode === 'image' ? (
          <ImagePlayground generate={byID.get('image.generate')} edit={byID.get('image.edit')} />
        ) : null}
        {mode === 'speech' ? <SpeechPlayground capability={byID.get('audio.tts')} /> : null}
        {mode === 'embedding' ? (
          <EmbeddingPlayground capability={byID.get('text.embedding')} />
        ) : null}
      </QueryState>
    </div>
  )
}

const CapabilityNotice = ({ capability }: { capability?: Capability }) => {
  const { t } = useTranslation()
  if (capability?.status === 'ready') return null
  return (
    <p className='rounded-md border border-warn/30 bg-warn/10 px-3 py-2 text-sm text-warn'>
      {capability?.reason || t('playground.capabilityUnavailable')}
    </p>
  )
}

const ImagePlayground = ({ generate, edit }: { generate?: Capability; edit?: Capability }) => {
  const { t } = useTranslation()
  const [operation, setOperation] = useState<'generate' | 'edit'>('generate')
  return (
    <div className='flex flex-col gap-4'>
      <div className='flex gap-2'>
        {(['generate', 'edit'] as const).map((value) => (
          <Button
            key={value}
            type='button'
            variant={operation === value ? 'primary' : 'ghost'}
            onClick={() => setOperation(value)}
          >
            {t(`playground.image.${value}`)}
          </Button>
        ))}
      </div>
      {operation === 'generate' ? (
        <ImageGenerationPlayground capability={generate} />
      ) : (
        <ImageEditPlayground capability={edit} embedded />
      )}
    </div>
  )
}

const ImageGenerationPlayground = ({ capability }: { capability?: Capability }) => {
  const { t } = useTranslation()
  const model = capability?.models[0] ?? ''
  const [prompt, setPrompt] = useState('')
  const [result, setResult] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const controller = useRef<AbortController | null>(null)
  useEffect(() => () => controller.current?.abort(), [])

  const submit = async () => {
    if (!model || !prompt.trim()) return
    const next = new AbortController()
    controller.current = next
    setPending(true)
    setError('')
    setResult('')
    try {
      const response = await apiFetch('/api/playground/image', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model, prompt: prompt.trim(), size: '1024x1024' }),
        signal: next.signal,
      })
      const payload = (await response.json()) as {
        b64_json?: string
        data?: Array<{ b64_json?: string }>
      }
      const encoded = payload.b64_json ?? payload.data?.[0]?.b64_json
      if (!encoded) throw new Error(t('playground.image.error'))
      setResult(`data:image/png;base64,${encoded}`)
    } catch (cause) {
      if (!next.signal.aborted)
        setError(cause instanceof Error ? cause.message : t('playground.image.error'))
    } finally {
      if (controller.current === next) controller.current = null
      setPending(false)
    }
  }

  return (
    <Panel title={t('playground.mode.image')}>
      <CapabilityNotice capability={capability} />
      <div className='mt-4 grid gap-4 lg:grid-cols-2'>
        <div className='flex flex-col gap-3'>
          <p className='font-mono text-xs text-muted'>{model || '—'}</p>
          <textarea
            value={prompt}
            onChange={(event) => setPrompt(event.target.value)}
            placeholder={t('playground.image.prompt')}
            className='min-h-32 rounded-md border border-line-2 bg-surface-1 p-3 text-sm outline-none focus:border-accent'
          />
          <Button
            type='button'
            disabled={pending || capability?.status !== 'ready' || !prompt.trim()}
            onClick={() => void submit()}
          >
            {pending ? t('playground.running') : t('playground.image.generate')}
          </Button>
          {error ? (
            <p role='alert' className='text-sm text-danger'>
              {error}
            </p>
          ) : null}
        </div>
        <div className='grid min-h-64 place-items-center rounded-md border border-dashed border-line p-3'>
          {result ? (
            <img
              src={result}
              alt={t('playground.image.result')}
              className='max-h-[520px] w-full object-contain'
            />
          ) : (
            <p className='text-sm text-muted'>{t('playground.image.result')}</p>
          )}
        </div>
      </div>
    </Panel>
  )
}

const SpeechPlayground = ({ capability }: { capability?: Capability }) => {
  const { t } = useTranslation()
  const model = capability?.models[0] ?? ''
  const formats = capability?.limits.formats.length ? capability.limits.formats : ['mp3', 'wav']
  const voices = capability?.limits.voices ?? []
  const languages = capability?.limits.languages ?? []
  const [input, setInput] = useState('')
  const [language, setLanguage] = useState('')
  const [voice, setVoice] = useState('')
  const [format, setFormat] = useState(formats[0] ?? 'mp3')
  const [audio, setAudio] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const controller = useRef<AbortController | null>(null)
  useEffect(() => () => controller.current?.abort(), [])
  useEffect(() => {
    if (!languages.includes(language)) setLanguage(languages[0] ?? '')
  }, [language, languages])
  const languageVoices = voices.filter((candidate) => candidate.startsWith(language))
  useEffect(() => {
    if (!languageVoices.includes(voice)) setVoice(languageVoices[0] ?? voices[0] ?? '')
  }, [languageVoices, voice, voices])
  useEffect(
    () => () => {
      if (audio) URL.revokeObjectURL(audio)
    },
    [audio],
  )

  const submit = async () => {
    const next = new AbortController()
    controller.current = next
    setPending(true)
    setError('')
    try {
      const response = await apiFetch('/api/playground/speech', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model, input: input.trim(), voice, response_format: format }),
        signal: next.signal,
      })
      const audioURL = URL.createObjectURL(await response.blob())
      setAudio((current) => {
        if (current) URL.revokeObjectURL(current)
        return audioURL
      })
    } catch (cause) {
      if (!next.signal.aborted)
        setError(cause instanceof Error ? cause.message : t('playground.speech.error'))
    } finally {
      if (controller.current === next) controller.current = null
      setPending(false)
    }
  }

  return (
    <Panel title={t('playground.mode.speech')}>
      <CapabilityNotice capability={capability} />
      <div className='mt-4 flex max-w-3xl flex-col gap-3'>
        <p className='font-mono text-xs text-muted'>{model || '—'}</p>
        <textarea
          value={input}
          onChange={(event) => setInput(event.target.value)}
          placeholder={t('playground.speech.input')}
          className='min-h-32 rounded-md border border-line-2 bg-surface-1 p-3 text-sm outline-none focus:border-accent'
        />
        <div className='grid gap-3 sm:grid-cols-3'>
          <select
            value={language}
            onChange={(event) => setLanguage(event.target.value)}
            aria-label={t('playground.speech.language')}
            className='rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm'
          >
            {languages.map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
          <select
            value={voice}
            onChange={(event) => setVoice(event.target.value)}
            aria-label={t('playground.speech.voice')}
            className='rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm'
          >
            {languageVoices.map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
          <select
            value={format}
            onChange={(event) => setFormat(event.target.value)}
            className='rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm'
          >
            {formats.map((value) => (
              <option key={value}>{value}</option>
            ))}
          </select>
        </div>
        <Button
          type='button'
          disabled={pending || capability?.status !== 'ready' || !input.trim() || !voice.trim()}
          onClick={() => void submit()}
        >
          {pending ? t('playground.running') : t('playground.speech.submit')}
        </Button>
        {audio ? <audio controls src={audio} className='w-full' /> : null}
        {error ? (
          <p role='alert' className='text-sm text-danger'>
            {error}
          </p>
        ) : null}
      </div>
    </Panel>
  )
}

const EmbeddingPlayground = ({ capability }: { capability?: Capability }) => {
  const { t } = useTranslation()
  const model = capability?.models[0] ?? ''
  const [input, setInput] = useState('')
  const [values, setValues] = useState<number[]>([])
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const controller = useRef<AbortController | null>(null)
  useEffect(() => () => controller.current?.abort(), [])

  const submit = async () => {
    const next = new AbortController()
    controller.current = next
    setPending(true)
    setError('')
    try {
      const response = await apiFetch('/api/playground/embedding', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ model, input: input.trim() }),
        signal: next.signal,
      })
      const payload = (await response.json()) as { data?: Array<{ embedding?: number[] }> }
      const embedding = payload.data?.[0]?.embedding
      if (!embedding) throw new Error(t('playground.embedding.error'))
      setValues(embedding)
    } catch (cause) {
      if (!next.signal.aborted)
        setError(cause instanceof Error ? cause.message : t('playground.embedding.error'))
    } finally {
      if (controller.current === next) controller.current = null
      setPending(false)
    }
  }

  return (
    <Panel title={t('playground.mode.embedding')}>
      <CapabilityNotice capability={capability} />
      <div className='mt-4 flex max-w-3xl flex-col gap-3'>
        <p className='font-mono text-xs text-muted'>{model || '—'}</p>
        <textarea
          value={input}
          onChange={(event) => setInput(event.target.value)}
          placeholder={t('playground.embedding.input')}
          className='min-h-32 rounded-md border border-line-2 bg-surface-1 p-3 text-sm outline-none focus:border-accent'
        />
        <Button
          type='button'
          disabled={pending || capability?.status !== 'ready' || !input.trim()}
          onClick={() => void submit()}
        >
          {pending ? t('playground.running') : t('playground.embedding.submit')}
        </Button>
        {values.length ? (
          <pre className='overflow-auto rounded-md border border-line bg-surface-1 p-3 text-xs text-ink'>
            {t('playground.embedding.dimensions', { count: values.length })}
            {'\n'}[{values.slice(0, 32).join(', ')}
            {values.length > 32 ? ', …' : ''}]
          </pre>
        ) : null}
        {error ? (
          <p role='alert' className='text-sm text-danger'>
            {error}
          </p>
        ) : null}
      </div>
    </Panel>
  )
}
