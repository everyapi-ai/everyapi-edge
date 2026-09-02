import { useMemo, useState } from 'react'

import { useNavigate } from '@tanstack/react-router'
import { Download, ImageUp, MessageSquareText } from 'lucide-react'

import {
  useDeleteModel,
  useBenchmarkModel,
  useCancelPull,
  useImageRuntime,
  useModelCapabilities,
  useModels,
  useOverview,
  usePullQueue,
  useRefreshModelsOnPullCompletion,
  useRuntime,
  useSetImageRuntimeModel,
  useStartPull,
  useUnloadRuntimeModel,
} from '@/api/queries'
import { Button, Input, PageHeader, Panel, QueryState } from '@/components/primitives'
import { InstalledModelsPanel } from '@/features/models/InstalledModelsPanel'
import {
  candidateBytes,
  isImageEditor,
  MODEL_CATALOG,
  ModelCatalogPanel,
  modelTypeFor,
  providerFor,
  typeKey,
  type CatalogModel,
  type ModelType,
} from '@/features/models/ModelCatalogPanel'
import { ModelResultPanels } from '@/features/models/ModelResultPanels'
import { useTranslation } from '@/i18n/useTranslation'

/** Conservative picks sized to leave headroom for the KV cache: a model that
 *  exactly fills VRAM starts swapping the moment context grows. */
const recommendationsFor = (vramGB: number): string[] => {
  if (vramGB >= 32) return ['qwen3:32b', 'gemma3:27b']
  if (vramGB >= 16) return ['qwen3:14b', 'gemma3:12b']
  if (vramGB >= 8) return ['qwen3:8b', 'llama3.1:8b']
  return ['qwen2.5:3b', 'llama3.2:3b']
}

const formatTransferRate = (bytesPerSecond: number) => {
  if (bytesPerSecond >= 1024 ** 3) return `${(bytesPerSecond / 1024 ** 3).toFixed(1)} GB/s`
  return `${Math.max(1, Math.round(bytesPerSecond / 1024 ** 2))} MB/s`
}

const formatRemaining = (seconds: number) => {
  const wholeSeconds = Math.max(0, Math.ceil(seconds))
  if (wholeSeconds >= 3600)
    return `${Math.floor(wholeSeconds / 3600)}h ${Math.ceil((wholeSeconds % 3600) / 60)}m`
  if (wholeSeconds >= 60) return `${Math.floor(wholeSeconds / 60)}m ${wholeSeconds % 60}s`
  return `${wholeSeconds}s`
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

  const percent =
    job.total > 0 ? Math.min(100, (job.completed / job.total) * 100) : job.done ? 100 : 8

  return (
    <div className='mt-3'>
      <div className='flex items-center justify-between gap-3 text-xs text-muted'>
        <p>
          {job.name}: {job.status}
          {job.error ? ` — ${job.error}` : ''}
        </p>
        {active ? (
          <Button
            type='button'
            variant='ghost'
            data-cancel-download={active.name}
            disabled={cancelPull.isPending}
            onClick={() => cancelPull.mutate(active.name)}
            className='shrink-0 px-2 py-1 text-xs'
          >
            {t('models.cancelDownload', { name: active.name })}
          </Button>
        ) : null}
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
      {active && (job.rate_bytes_per_second > 0 || job.seconds_remaining > 0) ? (
        <div className='mt-2 flex flex-wrap gap-x-3 gap-y-1 text-xs text-faint'>
          {job.rate_bytes_per_second > 0 ? (
            <span data-download-speed>{formatTransferRate(job.rate_bytes_per_second)}</span>
          ) : null}
          {job.seconds_remaining > 0 ? (
            <span data-download-eta>
              {t('models.downloadRemaining', { time: formatRemaining(job.seconds_remaining) })}
            </span>
          ) : null}
        </div>
      ) : null}
      {queued.length ? (
        <ul data-download-queue className='mt-3 flex flex-col gap-1.5'>
          {queued.map((item) => (
            <li
              key={item.name}
              className='flex items-center justify-between gap-3 rounded-md border border-line bg-surface-1 px-2.5 py-2 text-xs text-muted'
            >
              <span className='truncate'>{item.name}</span>
              <Button
                type='button'
                variant='ghost'
                data-cancel-download={item.name}
                disabled={cancelPull.isPending}
                onClick={() => cancelPull.mutate(item.name)}
                className='shrink-0 px-2 py-1 text-xs'
              >
                {t('models.cancelDownload', { name: item.name })}
              </Button>
            </li>
          ))}
        </ul>
      ) : null}
      {cancelPull.isError ? (
        <p role='alert' className='mt-2 text-xs text-danger'>
          {cancelPull.error.message}
        </p>
      ) : null}
    </div>
  )
}

export const ModelsPage = () => {
  const { t } = useTranslation()
  const models = useModels()
  const overview = useOverview()
  const runtime = useRuntime()
  const imageRuntime = useImageRuntime()
  const setImageRuntimeModel = useSetImageRuntimeModel()
  const startPull = useStartPull()
  const deleteModel = useDeleteModel()
  const benchmarkModel = useBenchmarkModel()
  const unloadRuntimeModel = useUnloadRuntimeModel()
  const [selectedProvider, setSelectedProvider] = useState('')
  const [selectedType, setSelectedType] = useState<ModelType | ''>('')
  const [selectedModel, setSelectedModel] = useState('')
  const [installedProviderFilter, setInstalledProviderFilter] = useState('')
  const [installedTypeFilter, setInstalledTypeFilter] = useState<ModelType | ''>('')
  const [installedSearch, setInstalledSearch] = useState('')
  const [inspectedModel, setInspectedModel] = useState('')
  const [formError, setFormError] = useState('')
  const navigate = useNavigate()
  const installedModels = useMemo(
    () => new Set(models.data?.map((model) => model.name) ?? []),
    [models.data],
  )
  const installedProviders = useMemo(
    () =>
      [...new Set((models.data ?? []).map((model) => providerFor(model.name)))].sort(
        (left, right) => left.localeCompare(right),
      ),
    [models.data],
  )
  const installedTypes = useMemo(
    () => [...new Set((models.data ?? []).map((model) => modelTypeFor(model.name)))],
    [models.data],
  )
  const filteredModels = useMemo(() => {
    const query = installedSearch.trim().toLocaleLowerCase()
    return (models.data ?? []).filter(
      (model) =>
        (!installedProviderFilter || providerFor(model.name) === installedProviderFilter) &&
        (!installedTypeFilter || modelTypeFor(model.name) === installedTypeFilter) &&
        (!query || model.name.toLocaleLowerCase().includes(query)),
    )
  }, [installedProviderFilter, installedSearch, installedTypeFilter, models.data])
  const inspectedCapabilities = useModelCapabilities(inspectedModel)

  const totalMemoryGB = overview.data?.vram_total_gb ?? 0
  const loadedMemoryBytes = overview.data?.loaded_vram_bytes ?? 0
  const reservedMemoryBytes = overview.data?.reserved_vram_bytes ?? 0
  const availableMemoryBytes = overview.data?.available_vram_bytes ?? 0
  const availableMemoryGB = Math.floor(availableMemoryBytes / 1024 ** 3)
  const loadedModels = useMemo(
    () => new Set(runtime.data?.models.map((model) => model.name) ?? []),
    [runtime.data],
  )
  const hasCapacityBudget = totalMemoryGB > 0
  const suggestions = useMemo(() => recommendationsFor(availableMemoryGB), [availableMemoryGB])
  const catalog = useMemo(() => {
    const recommended = new Set(suggestions)
    return [...MODEL_CATALOG].sort(
      (left, right) => Number(recommended.has(right.name)) - Number(recommended.has(left.name)),
    )
  }, [suggestions])
  const providerGroups = useMemo(() => {
    const groups = new Map<string, CatalogModel[]>()
    for (const candidate of catalog)
      groups.set(candidate.provider, [...(groups.get(candidate.provider) ?? []), candidate])
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
  const chosenModel =
    typedModels.find((candidate) => candidate.name === modelToPull) ?? typedModels[0]
  const selectedInstalled = Boolean(chosenModel && installedModels.has(chosenModel.name))
  const hasUnsupportedImageEditor = Boolean(
    chosenModel?.runtime === 'diffusers' && !isImageEditor(chosenModel),
  )
  const cannotRun = (candidate: CatalogModel) =>
    (candidate.runtime === 'diffusers' &&
      (!isImageEditor(candidate) || imageRuntime.data?.status !== 'ready')) ||
    (hasCapacityBudget && candidateBytes(candidate) > availableMemoryBytes)

  const pull = (candidate: string) => {
    setFormError('')
    startPull.mutate(candidate, { onError: (error: Error) => setFormError(error.message) })
  }

  const selectImageModel = (candidate: string) => {
    setFormError('')
    setImageRuntimeModel.mutate(candidate, {
      onError: (error: Error) => setFormError(error.message),
    })
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

  const benchmark = (candidate: string) => {
    if ((overview.data?.active_requests ?? 0) > 0 || modelTypeFor(candidate) === 'image') return
    const releaseLoaded = loadedModels.has(candidate)
    if (releaseLoaded && !window.confirm(t('models.benchmarkReleaseConfirm', { name: candidate })))
      return
    setFormError('')
    benchmarkModel.mutate(
      { model: candidate, releaseLoaded },
      { onError: (error: Error) => setFormError(error.message) },
    )
  }

  const openPlayground = (candidate: string) =>
    void navigate({ to: '/playground', search: { model: candidate } })

  const capabilityLabel = (capability: string) => {
    if (capability === 'completion') return t('models.capabilityText')
    if (capability === 'vision') return t('models.capabilityMultimodal')
    if (capability === 'tools') return t('models.capabilityTools')
    if (capability === 'embedding') return t('models.capabilityEmbedding')
    return capability
  }

  return (
    <div className='flex flex-col gap-5'>
      <PageHeader title={t('models.title')} description={t('models.description')} />
      <div className='grid gap-4 min-[1800px]:grid-cols-[minmax(0,0.8fr)_minmax(0,1.2fr)]'>
        <ModelCatalogPanel>
          {overview.isPending ? (
            <p className='text-sm text-muted'>{t('models.recommendationsLoading')}</p>
          ) : totalMemoryGB > 0 ? (
            <div className='text-sm leading-6 text-muted'>
              {t('models.recommendations', { vram: availableMemoryGB })}
              <p data-model-capacity className='mt-1 text-xs text-faint'>
                {t('models.memoryBudget', {
                  total: totalMemoryGB,
                  loaded: Math.ceil(loadedMemoryBytes / 1024 ** 3),
                  reserved: Math.ceil(reservedMemoryBytes / 1024 ** 3),
                  available: availableMemoryGB,
                })}
              </p>
              <div className='mt-3 flex flex-wrap gap-2'>
                {suggestions.map((suggestion) => (
                  <Button
                    key={suggestion}
                    type='button'
                    variant='ghost'
                    disabled={startPull.isPending}
                    onClick={() =>
                      installedModels.has(suggestion)
                        ? openPlayground(suggestion)
                        : pull(suggestion)
                    }
                  >
                    {installedModels.has(suggestion)
                      ? t('models.openInstalled', { name: suggestion })
                      : t('models.download', { name: suggestion })}
                  </Button>
                ))}
              </div>
            </div>
          ) : (
            <p className='text-sm leading-6 text-muted'>{t('models.noVram')}</p>
          )}

          <div className='mt-5'>
            <div className='grid gap-2 sm:grid-cols-[minmax(0,1fr)_5.5rem]'>
              <div>
                <label
                  htmlFor='model-provider'
                  className='mb-2 block text-sm font-medium text-ink-2'
                >
                  {t('models.provider')}
                </label>
                <select
                  id='model-provider'
                  aria-label={t('models.provider')}
                  value={providerToPull}
                  onChange={(event) => {
                    setSelectedProvider(event.target.value)
                    setSelectedType('')
                    setSelectedModel('')
                  }}
                  className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'
                >
                  {providerGroups.map(([provider]) => (
                    <option key={provider} value={provider}>
                      {provider}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label htmlFor='model-type' className='mb-2 block text-sm font-medium text-ink-2'>
                  {t('models.type')}
                </label>
                <select
                  id='model-type'
                  aria-label={t('models.type')}
                  value={typeToPull}
                  onChange={(event) => {
                    setSelectedType(event.target.value as ModelType)
                    setSelectedModel('')
                  }}
                  className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'
                >
                  {availableTypes.map((type) => (
                    <option key={type} value={type}>
                      {t(typeKey[type])}
                    </option>
                  ))}
                </select>
              </div>
              <div className='sm:col-span-2'>
                <label
                  htmlFor='model-catalog'
                  className='mb-2 block text-sm font-medium text-ink-2'
                >
                  {t('models.model')}
                </label>
                <select
                  id='model-catalog'
                  aria-label={t('models.model')}
                  value={modelToPull}
                  onChange={(event) => setSelectedModel(event.target.value)}
                  className='w-full min-w-0 rounded-md border border-line-2 bg-surface-1 px-3 py-2 font-mono text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'
                >
                  {typedModels.map((candidate) => (
                    <option
                      key={candidate.name}
                      value={candidate.name}
                      disabled={cannotRun(candidate)}
                    >
                      {candidate.name} · {t(typeKey[candidate.type])} ·{' '}
                      {t('models.requiresMemory', { memory: candidate.minimumGB })}
                      {installedModels.has(candidate.name) ? ` · ${t('models.installed')}` : ''}
                    </option>
                  ))}
                </select>
              </div>
            </div>
            <div className='mt-2 flex flex-col gap-2 sm:flex-row'>
              {selectedInstalled ? (
                <Button
                  type='button'
                  data-model-catalog-action
                  onClick={() => openPlayground(modelToPull)}
                  className='flex items-center justify-center gap-2'
                >
                  <MessageSquareText className='size-4' aria-hidden='true' />
                  {t('models.openPlayground')}
                </Button>
              ) : chosenModel?.runtime === 'diffusers' ? (
                <Button
                  type='button'
                  data-select-image-model
                  disabled={
                    setImageRuntimeModel.isPending ||
                    !isImageEditor(chosenModel) ||
                    cannotRun(chosenModel)
                  }
                  onClick={() => selectImageModel(modelToPull)}
                  className='flex items-center justify-center gap-2'
                >
                  <ImageUp className='size-4' aria-hidden='true' />
                  {setImageRuntimeModel.isPending
                    ? t('models.settingImageEditor')
                    : t('models.useImageEditor')}
                </Button>
              ) : (
                <Button
                  type='button'
                  data-model-catalog-action
                  disabled={startPull.isPending || !chosenModel || cannotRun(chosenModel)}
                  onClick={() => pull(modelToPull)}
                  className='flex items-center justify-center gap-2'
                >
                  <Download className='size-4' aria-hidden='true' />
                  {t('models.pull')}
                </Button>
              )}
            </div>
            {chosenModel ? (
              <p
                data-model-type
                className={
                  cannotRun(chosenModel) ? 'mt-2 text-xs text-danger' : 'mt-2 text-xs text-muted'
                }
              >
                {chosenModel.provider} · {t(typeKey[chosenModel.type])} ·{' '}
                {t('models.requiresMemory', { memory: chosenModel.minimumGB })}
                {hasUnsupportedImageEditor ? ` · ${t('models.imageEditorUnsupported')}` : ''}
                {!hasUnsupportedImageEditor &&
                hasCapacityBudget &&
                candidateBytes(chosenModel) > availableMemoryBytes
                  ? ` · ${t('models.insufficientMemory', { memory: Math.ceil((candidateBytes(chosenModel) - availableMemoryBytes) / 1024 ** 3) })}`
                  : ''}
              </p>
            ) : null}
            <p className='mt-2 text-xs leading-5 text-muted'>{t('models.catalogHint')}</p>
          </div>
          {formError ? (
            <p role='alert' className='mt-2 text-xs text-danger'>
              {formError}
            </p>
          ) : null}
          <PullProgress />
        </ModelCatalogPanel>

        <Panel title={t('models.title')} className='min-w-0'>
          <QueryState
            isPending={models.isPending}
            isError={models.isError}
            error={models.error}
            isEmpty={models.data?.length === 0}
            emptyMessage={t('models.empty')}
            onRetry={() => void models.refetch()}
          >
            <div data-installed-model-filters className='mb-4 grid gap-3 sm:grid-cols-3'>
              <div>
                <label
                  htmlFor='installed-model-provider'
                  className='mb-1.5 block text-xs font-medium text-ink-2'
                >
                  {t('models.filterProvider')}
                </label>
                <select
                  id='installed-model-provider'
                  aria-label={t('models.filterProvider')}
                  value={installedProviderFilter}
                  onChange={(event) => setInstalledProviderFilter(event.target.value)}
                  className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'
                >
                  <option value=''>{t('models.filterAllProviders')}</option>
                  {installedProviders.map((provider) => (
                    <option key={provider} value={provider}>
                      {provider}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label
                  htmlFor='installed-model-type'
                  className='mb-1.5 block text-xs font-medium text-ink-2'
                >
                  {t('models.filterType')}
                </label>
                <select
                  id='installed-model-type'
                  aria-label={t('models.filterType')}
                  value={installedTypeFilter}
                  onChange={(event) => setInstalledTypeFilter(event.target.value as ModelType | '')}
                  className='w-full rounded-md border border-line-2 bg-surface-1 px-3 py-2 text-sm text-ink outline-none transition-colors focus:border-accent focus:ring-2 focus:ring-accent/20'
                >
                  <option value=''>{t('models.filterAllTypes')}</option>
                  {installedTypes.map((type) => (
                    <option key={type} value={type}>
                      {t(typeKey[type])}
                    </option>
                  ))}
                </select>
              </div>
              <div>
                <label
                  htmlFor='installed-model-search'
                  className='mb-1.5 block text-xs font-medium text-ink-2'
                >
                  {t('models.searchInstalled')}
                </label>
                <Input
                  id='installed-model-search'
                  aria-label={t('models.searchInstalled')}
                  value={installedSearch}
                  onChange={(event) => setInstalledSearch(event.target.value)}
                  placeholder={t('models.searchInstalledPlaceholder')}
                />
              </div>
            </div>
            <p data-installed-model-count className='mb-3 text-xs text-faint'>
              {t('models.filterCount', { count: filteredModels.length })}
            </p>
            <InstalledModelsPanel
              models={filteredModels}
              loadedModels={loadedModels}
              activeRequests={overview.data?.active_requests ?? 0}
              benchmarkPending={benchmarkModel.isPending}
              unloadPending={unloadRuntimeModel.isPending}
              deletePending={deleteModel.isPending}
              providerFor={providerFor}
              typeFor={(name) => t(typeKey[modelTypeFor(name)])}
              isImage={(name) => modelTypeFor(name) === 'image'}
              onInspect={setInspectedModel}
              onOpen={openPlayground}
              onBenchmark={benchmark}
              onUnload={unload}
              onRemove={remove}
            />
            {filteredModels.length === 0 ? (
              <p data-installed-model-empty className='py-5 text-sm text-muted'>
                {t('models.noFilterMatches')}
              </p>
            ) : null}
            <ModelResultPanels
              benchmark={benchmarkModel.data}
              inspectedModel={inspectedModel}
              capabilities={inspectedCapabilities.data}
              capabilitiesPending={inspectedCapabilities.isPending}
              capabilitiesError={inspectedCapabilities.error?.message}
              capabilityLabel={capabilityLabel}
            />
          </QueryState>
        </Panel>
      </div>
    </div>
  )
}
