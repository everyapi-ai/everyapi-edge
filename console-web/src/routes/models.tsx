import { useMemo, useState } from 'react'

import { createRoute, useNavigate } from '@tanstack/react-router'
import { Download, ImageUp, MessageSquareText, Trash2, ZapOff } from 'lucide-react'

import {
  useDeleteModel,
  useCancelPull,
  useImageRuntime,
  useModels,
  useOverview,
  usePullQueue,
  useRefreshModelsOnPullCompletion,
  useRuntime,
  useSetImageRuntimeModel,
  useStartPull,
  useUnloadRuntimeModel,
} from '@/api/queries'
import { Button, PageHeader, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatGigabytes } from '@/lib/format'

import { rootRoute } from './root'

/** Conservative picks sized to leave headroom for the KV cache: a model that
 *  exactly fills VRAM starts swapping the moment context grows. */
const recommendationsFor = (vramGB: number): string[] => {
  if (vramGB >= 32) return ['qwen3:32b', 'gemma3:27b']
  if (vramGB >= 16) return ['qwen3:14b', 'gemma3:12b']
  if (vramGB >= 8) return ['qwen3:8b', 'llama3.1:8b']
  return ['qwen2.5:3b', 'llama3.2:3b']
}

type ModelType = 'chat' | 'reasoning' | 'code' | 'vision' | 'embedding' | 'image'

type CatalogModel = { name: string; provider: string; type: ModelType; minimumGB: number; runtime?: 'ollama' | 'diffusers' }

const candidateBytes = (candidate: CatalogModel) => candidate.minimumGB * (1024 ** 3)

const isImageEditor = (candidate: CatalogModel) =>
  candidate.runtime === 'diffusers' && /^Qwen\/Qwen-Image-Edit(?:-|$)/.test(candidate.name)

// Curated from Ollama's public library. Keep explicit runnable tags here so a
// supplier never downloads an ambiguous family alias that resolves to a huge
// default model.
const MODEL_CATALOG: CatalogModel[] = [
  { name: 'qwen2.5:0.5b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 2 },
  { name: 'qwen2.5:1.5b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 3 },
  { name: 'qwen2.5:3b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 4 },
  { name: 'qwen2.5:7b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 8 },
  { name: 'qwen2.5:14b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 14 },
  { name: 'qwen2.5:32b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 24 },
  { name: 'qwen2.5:72b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 56 },
  { name: 'qwen3:0.6b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 2 },
  { name: 'qwen3:1.7b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 3 },
  { name: 'qwen3:4b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 6 },
  { name: 'qwen3:8b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 8 },
  { name: 'qwen3:14b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 14 },
  { name: 'qwen3:32b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 24 },
  { name: 'qwen2.5-coder:1.5b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 3 },
  { name: 'qwen2.5-coder:7b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 8 },
  { name: 'qwen2.5-coder:14b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 14 },
  { name: 'qwen2.5-coder:32b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 24 },
  { name: 'qwen3-coder:30b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 24 },
  { name: 'qwen3-vl:4b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 6 },
  { name: 'qwen3-vl:8b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 10 },
  { name: 'qwen3-vl:32b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 28 },
  { name: 'qwen2.5vl:3b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 6 },
  { name: 'qwen2.5vl:7b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 10 },
  { name: 'qwen3-embedding:0.6b', provider: 'Alibaba / Qwen', type: 'embedding', minimumGB: 2 },
  { name: 'qwen3-embedding:4b', provider: 'Alibaba / Qwen', type: 'embedding', minimumGB: 4 },
  { name: 'qwen3-embedding:8b', provider: 'Alibaba / Qwen', type: 'embedding', minimumGB: 8 },
  { name: 'Qwen/Qwen-Image', provider: 'Alibaba / Qwen', type: 'image', minimumGB: 48, runtime: 'diffusers' },
  { name: 'Qwen/Qwen-Image-2512', provider: 'Alibaba / Qwen', type: 'image', minimumGB: 48, runtime: 'diffusers' },
  { name: 'Qwen/Qwen-Image-Edit', provider: 'Alibaba / Qwen', type: 'image', minimumGB: 48, runtime: 'diffusers' },
  { name: 'Qwen/Qwen-Image-Edit-2509', provider: 'Alibaba / Qwen', type: 'image', minimumGB: 48, runtime: 'diffusers' },
  { name: 'Qwen/Qwen-Image-Edit-2511', provider: 'Alibaba / Qwen', type: 'image', minimumGB: 48, runtime: 'diffusers' },
  { name: 'Qwen/Qwen-Image-Layered', provider: 'Alibaba / Qwen', type: 'image', minimumGB: 48, runtime: 'diffusers' },
  { name: 'llama3.2:1b', provider: 'Meta', type: 'chat', minimumGB: 2 },
  { name: 'llama3.2:3b', provider: 'Meta', type: 'chat', minimumGB: 4 },
  { name: 'llama3.1:8b', provider: 'Meta', type: 'chat', minimumGB: 8 },
  { name: 'llama3.1:70b', provider: 'Meta', type: 'chat', minimumGB: 56 },
  { name: 'llama3.3:70b', provider: 'Meta', type: 'chat', minimumGB: 56 },
  { name: 'llama3.2-vision:11b', provider: 'Meta', type: 'vision', minimumGB: 12 },
  { name: 'llama3.2-vision:90b', provider: 'Meta', type: 'vision', minimumGB: 72 },
  { name: 'gemma2:2b', provider: 'Google', type: 'chat', minimumGB: 3 },
  { name: 'gemma2:9b', provider: 'Google', type: 'chat', minimumGB: 10 },
  { name: 'gemma2:27b', provider: 'Google', type: 'chat', minimumGB: 22 },
  { name: 'gemma3:1b', provider: 'Google', type: 'vision', minimumGB: 2 },
  { name: 'gemma3:4b', provider: 'Google', type: 'vision', minimumGB: 6 },
  { name: 'gemma3:12b', provider: 'Google', type: 'vision', minimumGB: 12 },
  { name: 'gemma3:27b', provider: 'Google', type: 'vision', minimumGB: 22 },
  { name: 'codegemma:2b', provider: 'Google', type: 'code', minimumGB: 3 },
  { name: 'codegemma:7b', provider: 'Google', type: 'code', minimumGB: 8 },
  { name: 'deepseek-r1:1.5b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 3 },
  { name: 'deepseek-r1:7b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 8 },
  { name: 'deepseek-r1:8b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 8 },
  { name: 'deepseek-r1:14b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 14 },
  { name: 'deepseek-r1:32b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 24 },
  { name: 'deepseek-r1:70b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 56 },
  { name: 'deepseek-coder-v2:16b', provider: 'DeepSeek', type: 'code', minimumGB: 14 },
  { name: 'deepseek-coder-v2:236b', provider: 'DeepSeek', type: 'code', minimumGB: 160 },
  { name: 'mistral:7b', provider: 'Mistral AI', type: 'chat', minimumGB: 8 },
  { name: 'mistral-nemo:12b', provider: 'Mistral AI', type: 'chat', minimumGB: 12 },
  { name: 'mistral-small:24b', provider: 'Mistral AI', type: 'chat', minimumGB: 20 },
  { name: 'ministral-3:3b', provider: 'Mistral AI', type: 'chat', minimumGB: 4 },
  { name: 'ministral-3:8b', provider: 'Mistral AI', type: 'chat', minimumGB: 8 },
  { name: 'ministral-3:14b', provider: 'Mistral AI', type: 'chat', minimumGB: 14 },
  { name: 'codestral:22b', provider: 'Mistral AI', type: 'code', minimumGB: 18 },
  { name: 'mixtral:8x7b', provider: 'Mistral AI', type: 'chat', minimumGB: 40 },
  { name: 'phi3:mini', provider: 'Microsoft', type: 'chat', minimumGB: 4 },
  { name: 'phi3:medium', provider: 'Microsoft', type: 'chat', minimumGB: 14 },
  { name: 'phi4:14b', provider: 'Microsoft', type: 'reasoning', minimumGB: 14 },
  { name: 'phi4-mini:3.8b', provider: 'Microsoft', type: 'chat', minimumGB: 4 },
  { name: 'gpt-oss:20b', provider: 'OpenAI', type: 'reasoning', minimumGB: 18 },
  { name: 'gpt-oss:120b', provider: 'OpenAI', type: 'reasoning', minimumGB: 96 },
  { name: 'nomic-embed-text', provider: 'Nomic AI', type: 'embedding', minimumGB: 2 },
  { name: 'nomic-embed-text:v1.5', provider: 'Nomic AI', type: 'embedding', minimumGB: 2 },
  { name: 'bge-m3', provider: 'BAAI', type: 'embedding', minimumGB: 4 },
  { name: 'mxbai-embed-large', provider: 'Mixedbread AI', type: 'embedding', minimumGB: 2 },
  { name: 'all-minilm', provider: 'Sentence Transformers', type: 'embedding', minimumGB: 2 },
  { name: 'snowflake-arctic-embed2', provider: 'Snowflake', type: 'embedding', minimumGB: 2 },
  { name: 'llava:7b', provider: 'LLaVA', type: 'vision', minimumGB: 8 },
  { name: 'llava:13b', provider: 'LLaVA', type: 'vision', minimumGB: 14 },
  { name: 'minicpm-v:8b', provider: 'OpenBMB', type: 'vision', minimumGB: 10 },
  { name: 'starcoder2:7b', provider: 'Hugging Face / BigCode', type: 'code', minimumGB: 8 },
  { name: 'starcoder2:15b', provider: 'Hugging Face / BigCode', type: 'code', minimumGB: 14 },
  { name: 'granite3.1-dense:8b', provider: 'IBM', type: 'chat', minimumGB: 8 },
  { name: 'granite-code:8b', provider: 'IBM', type: 'code', minimumGB: 8 },
]

const catalogModelFor = (name: string) => MODEL_CATALOG.find((candidate) => candidate.name === name)

const providerFor = (name: string) => catalogModelFor(name)?.provider ?? 'Local'

const modelTypeFor = (name: string): ModelType => {
  const catalogModel = catalogModelFor(name)
  if (catalogModel) return catalogModel.type

  const normalized = name.toLowerCase()
  if (/(embed|bge)/.test(normalized)) return 'embedding'
  if (/(vision|vl|llava|minicpm-v)/.test(normalized)) return 'vision'
  if (/(coder|code|codestral|starcoder)/.test(normalized)) return 'code'
  if (/(deepseek-r1|reasoning|qwq|phi4|gpt-oss)/.test(normalized)) return 'reasoning'
  return 'chat'
}

const typeKey: Record<ModelType, 'models.typeChat' | 'models.typeReasoning' | 'models.typeCode' | 'models.typeVision' | 'models.typeEmbedding' | 'models.typeImage'> = {
  chat: 'models.typeChat', reasoning: 'models.typeReasoning', code: 'models.typeCode', vision: 'models.typeVision', embedding: 'models.typeEmbedding', image: 'models.typeImage',
}

const PullProgress = () => {
  const { t } = useTranslation()
  const pull = usePullQueue()
  const cancelPull = useCancelPull()
  useRefreshModelsOnPullCompletion(pull.data)

  const active = pull.data?.active
  const queued = pull.data?.queued ?? []
  const job = active ?? pull.data?.latest
  if (!job) return null

  const percent = job.total > 0 ? Math.min(100, (job.completed / job.total) * 100) : job.done ? 100 : 8

  return (
    <div className='mt-3'>
      <div className='flex items-center justify-between gap-3 text-xs text-muted'>
        <p>{job.name}: {job.status}{job.error ? ` — ${job.error}` : ''}</p>
        {active ? <Button type='button' variant='ghost' data-cancel-download={active.name} disabled={cancelPull.isPending} onClick={() => cancelPull.mutate(active.name)} className='shrink-0 px-2 py-1 text-xs'>{t('models.cancelDownload', { name: active.name })}</Button> : null}
      </div>
      <div
        role='progressbar'
        aria-label={job.name}
        aria-valuenow={Math.round(percent)}
        aria-valuemin={0}
        aria-valuemax={100}
        className='mt-2 h-1.5 overflow-hidden rounded-full bg-surface-2'
      >
        <span
          className={`block h-full rounded-full transition-[width] duration-300 ${job.error ? 'bg-danger' : 'bg-accent'}`}
          style={{ width: `${percent}%` }}
        />
      </div>
      {queued.length ? (
        <ul data-download-queue className='mt-3 flex flex-col gap-1.5'>
          {queued.map((item) => (
            <li key={item.name} className='flex items-center justify-between gap-3 rounded-md border border-line bg-surface-1 px-2.5 py-2 text-xs text-muted'>
              <span className='truncate'>{item.name}</span>
              <Button type='button' variant='ghost' data-cancel-download={item.name} disabled={cancelPull.isPending} onClick={() => cancelPull.mutate(item.name)} className='shrink-0 px-2 py-1 text-xs'>{t('models.cancelDownload', { name: item.name })}</Button>
            </li>
          ))}
        </ul>
      ) : null}
      {cancelPull.isError ? <p role='alert' className='mt-2 text-xs text-danger'>{cancelPull.error.message}</p> : null}
    </div>
  )
}

const ModelsPage = () => {
  const { t } = useTranslation()
  const models = useModels()
  const overview = useOverview()
  const runtime = useRuntime()
  const imageRuntime = useImageRuntime()
  const setImageRuntimeModel = useSetImageRuntimeModel()
  const startPull = useStartPull()
  const deleteModel = useDeleteModel()
  const unloadRuntimeModel = useUnloadRuntimeModel()
  const [selectedProvider, setSelectedProvider] = useState('')
  const [selectedType, setSelectedType] = useState<ModelType | ''>('')
  const [selectedModel, setSelectedModel] = useState('')
  const [formError, setFormError] = useState('')
  const navigate = useNavigate()
  const installedModels = useMemo(() => new Set(models.data?.map((model) => model.name) ?? []), [models.data])

  const totalMemoryGB = overview.data?.vram_total_gb ?? 0
  const loadedMemoryBytes = overview.data?.loaded_vram_bytes ?? 0
  const reservedMemoryBytes = overview.data?.reserved_vram_bytes ?? 0
  const availableMemoryBytes = overview.data?.available_vram_bytes ?? 0
  const availableMemoryGB = Math.floor(availableMemoryBytes / (1024 ** 3))
  const loadedModels = useMemo(() => new Set(runtime.data?.models.map((model) => model.name) ?? []), [runtime.data])
  const hasCapacityBudget = totalMemoryGB > 0
  const suggestions = useMemo(() => recommendationsFor(availableMemoryGB), [availableMemoryGB])
  const catalog = useMemo(() => {
    const recommended = new Set(suggestions)
    return [...MODEL_CATALOG].sort((left, right) => Number(recommended.has(right.name)) - Number(recommended.has(left.name)))
  }, [suggestions])
  const providerGroups = useMemo(() => {
    const groups = new Map<string, CatalogModel[]>()
    for (const candidate of catalog) groups.set(candidate.provider, [...(groups.get(candidate.provider) ?? []), candidate])
    return [...groups.entries()]
  }, [catalog])
  const providerToPull = selectedProvider || providerGroups[0]?.[0] || ''
  const providerModels = providerGroups.find(([provider]) => provider === providerToPull)?.[1] ?? []
  const availableTypes = [...new Set(providerModels.map((candidate) => candidate.type))]
  const typeToPull = selectedType || availableTypes[0] || ''
  const typedModels = providerModels
    .filter((candidate) => candidate.type === typeToPull)
    .sort((left, right) => Number(isImageEditor(right)) - Number(isImageEditor(left)))
  const modelToPull = selectedModel || typedModels[0]?.name || ''
  const chosenModel = typedModels.find((candidate) => candidate.name === modelToPull) ?? typedModels[0]
  const selectedInstalled = Boolean(chosenModel && installedModels.has(chosenModel.name))
  const cannotRun = (candidate: CatalogModel) =>
    (candidate.runtime === 'diffusers' && imageRuntime.data?.status !== 'ready') ||
    (hasCapacityBudget && candidateBytes(candidate) > availableMemoryBytes)

  const pull = (candidate: string) => {
    setFormError('')
    startPull.mutate(candidate, { onError: (error: Error) => setFormError(error.message) })
  }

  const selectImageModel = (candidate: string) => {
    setFormError('')
    setImageRuntimeModel.mutate(candidate, { onError: (error: Error) => setFormError(error.message) })
  }

  const remove = (candidate: string) => {
    if (!window.confirm(t('models.removeConfirm', { name: candidate }))) return
    deleteModel.mutate(candidate, { onError: (error: Error) => setFormError(error.message) })
  }

  const unload = (candidate: string) => {
    if (!window.confirm(t('runtime.unloadConfirm', { model: candidate }))) return
    setFormError('')
    unloadRuntimeModel.mutate(candidate, { onError: (error: Error) => setFormError(error.message) })
  }

  const openPlayground = (candidate: string) => void navigate({ to: '/playground', search: { model: candidate } })

  return (
    <div className='flex flex-col gap-5'>
      <PageHeader title={t('models.title')} description={t('models.description')} />
      <div className='grid gap-4 xl:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]'>
        <Panel title={t('models.pull')}>
          {overview.isPending ? (
            <p className='text-sm text-muted'>{t('models.recommendationsLoading')}</p>
          ) : totalMemoryGB > 0 ? (
            <div className='text-sm leading-6 text-muted'>
              {t('models.recommendations', { vram: availableMemoryGB })}
              <p data-model-capacity className='mt-1 text-xs text-faint'>{t('models.memoryBudget', { total: totalMemoryGB, loaded: Math.ceil(loadedMemoryBytes / (1024 ** 3)), reserved: Math.ceil(reservedMemoryBytes / (1024 ** 3)), available: availableMemoryGB })}</p>
              <div className='mt-3 flex flex-wrap gap-2'>
                {suggestions.map((suggestion) => (
                  <Button key={suggestion} type='button' variant='ghost' disabled={startPull.isPending} onClick={() => installedModels.has(suggestion) ? openPlayground(suggestion) : pull(suggestion)}>
                    {installedModels.has(suggestion) ? t('models.openInstalled', { name: suggestion }) : t('models.download', { name: suggestion })}
                  </Button>
                ))}
              </div>
            </div>
          ) : <p className='text-sm leading-6 text-muted'>{t('models.noVram')}</p>}

          <div className='mt-5'>
            <div className='grid gap-2 sm:grid-cols-[minmax(0,1fr)_5.5rem]'>
              <div>
                <label htmlFor='model-provider' className='mb-2 block text-sm font-medium text-ink-2'>{t('models.provider')}</label>
                <select id='model-provider' aria-label={t('models.provider')} value={providerToPull} onChange={(event) => { setSelectedProvider(event.target.value); setSelectedType(''); setSelectedModel('') }} className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'>
                  {providerGroups.map(([provider]) => <option key={provider} value={provider}>{provider}</option>)}
                </select>
              </div>
              <div>
                <label htmlFor='model-type' className='mb-2 block text-sm font-medium text-ink-2'>{t('models.type')}</label>
                <select id='model-type' aria-label={t('models.type')} value={typeToPull} onChange={(event) => { setSelectedType(event.target.value as ModelType); setSelectedModel('') }} className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'>
                  {availableTypes.map((type) => <option key={type} value={type}>{t(typeKey[type])}</option>)}
                </select>
              </div>
              <div className='sm:col-span-2'>
                <label htmlFor='model-catalog' className='mb-2 block text-sm font-medium text-ink-2'>{t('models.model')}</label>
                <select id='model-catalog' aria-label={t('models.model')} value={modelToPull} onChange={(event) => setSelectedModel(event.target.value)} className='w-full min-w-0 rounded-md border border-line-2 bg-surface-1 px-3 py-2 font-mono text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'>
                  {typedModels.map((candidate) => <option key={candidate.name} value={candidate.name} disabled={cannotRun(candidate)}>{candidate.name} · {t(typeKey[candidate.type])} · {t('models.requiresMemory', { memory: candidate.minimumGB })}{installedModels.has(candidate.name) ? ` · ${t('models.installed')}` : ''}</option>)}
                </select>
              </div>
            </div>
            <div className='mt-2 flex flex-col gap-2 sm:flex-row'>
              {selectedInstalled ? (
                <Button type='button' data-model-catalog-action onClick={() => openPlayground(modelToPull)} className='flex items-center justify-center gap-2'>
                  <MessageSquareText className='size-4' aria-hidden='true' />
                  {t('models.openPlayground')}
                </Button>
              ) : chosenModel?.runtime === 'diffusers' ? (
                <Button type='button' data-select-image-model disabled={setImageRuntimeModel.isPending || !isImageEditor(chosenModel) || cannotRun(chosenModel)} onClick={() => selectImageModel(modelToPull)} className='flex items-center justify-center gap-2'>
                  <ImageUp className='size-4' aria-hidden='true' />
                  {setImageRuntimeModel.isPending ? t('models.settingImageEditor') : t('models.useImageEditor')}
                </Button>
              ) : (
                <Button type='button' data-model-catalog-action disabled={startPull.isPending || !chosenModel || cannotRun(chosenModel)} onClick={() => pull(modelToPull)} className='flex items-center justify-center gap-2'>
                  <Download className='size-4' aria-hidden='true' />
                  {t('models.pull')}
                </Button>
              )}
            </div>
            {chosenModel ? <p data-model-type className={cannotRun(chosenModel) ? 'mt-2 text-xs text-danger' : 'mt-2 text-xs text-muted'}>{chosenModel.provider} · {t(typeKey[chosenModel.type])} · {t('models.requiresMemory', { memory: chosenModel.minimumGB })}{chosenModel.runtime === 'diffusers' && !isImageEditor(chosenModel) ? ` · ${t('models.imageEditorUnsupported')}` : ''}{cannotRun(chosenModel) ? ` · ${t('models.insufficientMemory', { memory: Math.ceil((candidateBytes(chosenModel) - availableMemoryBytes) / (1024 ** 3)) })}` : ''}</p> : null}
            <p className='mt-2 text-xs leading-5 text-muted'>{t('models.catalogHint')}</p>
          </div>
          {formError ? <p role='alert' className='mt-2 text-xs text-danger'>{formError}</p> : null}
          <PullProgress />
        </Panel>

        <Panel title={t('models.title')}>
          <QueryState isPending={models.isPending} isError={models.isError} isEmpty={models.data?.length === 0} emptyMessage={t('models.empty')} onRetry={() => void models.refetch()}>
            <div className='overflow-x-auto'>
              <table className='w-full min-w-[520px] border-collapse text-sm'>
              <thead>
                <tr>
                  {[
                    t('models.columnProvider'),
                    t('models.columnModel'),
                    t('models.columnStatus'),
                    t('models.columnType'),
                    t('models.columnSize'),
                    t('models.columnDetails'),
                  ].map((heading) => (
                    <th
                      key={heading}
                      scope='col'
                      className='border-b border-line px-3 py-2.5 text-left text-xs font-medium text-faint'
                    >
                      {heading}
                    </th>
                  ))}
                  <th scope='col' className='border-b border-line px-3 py-2.5' />
                </tr>
              </thead>
              <tbody>
                {(models.data ?? []).map((model) => (
                  <tr key={model.name} data-installed-model={model.name}>
                    <td data-model-provider className='border-b border-line px-3 py-3 text-ink-2'>{providerFor(model.name)}</td>
                    <td className='border-b border-line px-3 py-3 font-medium text-ink'>{model.name}</td>
                    <td className='border-b border-line px-3 py-3 text-muted'>
                      <span data-model-residency className={`inline-flex rounded-full px-2 py-0.5 text-xs font-medium ${loadedModels.has(model.name) ? 'bg-accent/14 text-accent' : 'bg-surface-2 text-ink-dim'}`}>
                        {loadedModels.has(model.name) ? t('models.loaded') : t('models.installed')}
                      </span>
                    </td>
                    <td data-model-kind className='border-b border-line px-3 py-3 text-muted'>{t(typeKey[modelTypeFor(model.name)])}</td>
                    <td className='border-b border-line px-3 py-3 text-ink-2'>
                      {formatGigabytes(model.size)}
                    </td>
                    <td className='border-b border-line px-3 py-3 text-muted'>
                      {model.details?.parameter_size ?? t('common.unknown')} /{' '}
                      {model.details?.quantization_level ?? t('common.unknown')}
                    </td>
                    <td className='border-b border-line px-3 py-3 text-right'>
                      <div className='flex justify-end gap-2'>
                      <Button
                        type='button'
                        variant='ghost'
                        aria-label={t('models.openPlayground')}
                        title={t('models.openPlayground')}
                        onClick={() => openPlayground(model.name)}
                        className='inline-flex size-9 items-center justify-center p-0'
                      >
                        <MessageSquareText className='size-3.5' aria-hidden='true' />
                      </Button>
                      {loadedModels.has(model.name) ? (
                        <Button
                          type='button'
                          variant='ghost'
                          aria-label={t('runtime.unload')}
                          title={t('runtime.unload')}
                          disabled={unloadRuntimeModel.isPending}
                          onClick={() => unload(model.name)}
                          className='inline-flex size-9 items-center justify-center p-0'
                        >
                          <ZapOff className='size-3.5' aria-hidden='true' />
                        </Button>
                      ) : null}
                      <Button
                        type='button'
                        variant='danger'
                        aria-label={t('models.remove')}
                        title={loadedModels.has(model.name) ? t('models.unloadBeforeRemove') : t('models.remove')}
                        disabled={deleteModel.isPending || loadedModels.has(model.name)}
                        onClick={() => remove(model.name)}
                        className='inline-flex size-9 items-center justify-center p-0'
                      >
                        <Trash2 className='size-3.5' aria-hidden='true' />
                      </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          </QueryState>
        </Panel>
      </div>
    </div>
  )
}

export const modelsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/models',
  component: ModelsPage,
})
