import { useMemo, useState, type FormEvent } from 'react'

import { createRoute } from '@tanstack/react-router'
import { Download, Trash2 } from 'lucide-react'
import * as z from 'zod'

import {
  useDeleteModel,
  useModels,
  useOverview,
  usePullJob,
  useRefreshModelsOnPullCompletion,
  useStartPull,
} from '@/api/queries'
import { Button, Panel, QueryState } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'
import { formatGigabytes } from '@/lib/format'

import { rootRoute } from './root'

// Mirrors validModelName in clients/edge/internal/console/server.go. Client-side
// checking is a UX shortcut only — the agent re-validates before touching
// Ollama, and that check is the authoritative one.
const modelNameSchema = z
  .string()
  .trim()
  .regex(/^[A-Za-z0-9][A-Za-z0-9._:/-]{0,254}$/)

/** Conservative picks sized to leave headroom for the KV cache: a model that
 *  exactly fills VRAM starts swapping the moment context grows. */
const recommendationsFor = (vramGB: number): string[] => {
  if (vramGB >= 32) return ['qwen3:32b', 'gemma3:27b']
  if (vramGB >= 16) return ['qwen3:14b', 'gemma3:12b']
  if (vramGB >= 8) return ['qwen3:8b', 'llama3.1:8b']
  return ['qwen2.5:3b', 'llama3.2:3b']
}

const PullProgress = () => {
  const pull = usePullJob()
  useRefreshModelsOnPullCompletion(pull.data)

  const job = pull.data
  if (!job) return null

  const percent = job.total > 0 ? Math.min(100, (job.completed / job.total) * 100) : job.done ? 100 : 8

  return (
    <div className='mt-3'>
      <p className='text-xs text-muted'>
        {job.name}: {job.status}
        {job.error ? ` — ${job.error}` : ''}
      </p>
      <div
        role='progressbar'
        aria-label={job.name}
        aria-valuenow={Math.round(percent)}
        aria-valuemin={0}
        aria-valuemax={100}
        className='mt-2 h-[5px] bg-[#0d120e]'
      >
        <span
          className={`block h-full transition-[width] duration-300 ${job.error ? 'bg-danger' : 'bg-lime'}`}
          style={{ width: `${percent}%` }}
        />
      </div>
    </div>
  )
}

const ModelsPage = () => {
  const { t } = useTranslation()
  const models = useModels()
  const overview = useOverview()
  const startPull = useStartPull()
  const deleteModel = useDeleteModel()
  const [name, setName] = useState('')
  const [formError, setFormError] = useState('')

  const vramGB = overview.data?.vram_total_gb ?? 0
  const suggestions = useMemo(() => recommendationsFor(vramGB), [vramGB])

  const pull = (candidate: string) => {
    const parsed = modelNameSchema.safeParse(candidate)
    if (!parsed.success) {
      setFormError(t('models.invalidName'))
      return
    }
    setFormError('')
    startPull.mutate(parsed.data, {
      onSuccess: () => setName(''),
      onError: (error: Error) => setFormError(error.message),
    })
  }

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    pull(name)
  }

  const remove = (candidate: string) => {
    if (!window.confirm(t('models.removeConfirm', { name: candidate }))) return
    deleteModel.mutate(candidate, { onError: (error: Error) => setFormError(error.message) })
  }

  return (
    <Panel title={t('models.title')}>
      {overview.isPending ? (
        <p className='text-xs text-muted'>{t('models.recommendationsLoading')}</p>
      ) : vramGB > 0 ? (
        <div className='text-xs text-muted'>
          {t('models.recommendations', { vram: vramGB })}
          <div className='mt-2.5 flex flex-wrap gap-2'>
            {suggestions.map((suggestion) => (
              <Button
                key={suggestion}
                type='button'
                variant='ghost'
                disabled={startPull.isPending}
                onClick={() => pull(suggestion)}
              >
                {t('models.download', { name: suggestion })}
              </Button>
            ))}
          </div>
        </div>
      ) : (
        <p className='text-xs text-muted'>{t('models.noVram')}</p>
      )}

      <form onSubmit={submit} className='mt-3 flex flex-wrap items-center gap-2'>
        <label htmlFor='model-name' className='sr-only'>
          {t('models.nameLabel')}
        </label>
        <input
          id='model-name'
          value={name}
          onChange={(event) => setName(event.target.value)}
          placeholder={t('models.namePlaceholder')}
          aria-invalid={formError ? true : undefined}
          aria-describedby={formError ? 'model-name-error' : undefined}
          className='min-w-0 flex-1 rounded-[2px] border border-[#405043] bg-[#0d120e] px-3 py-[11px] text-ink outline-none focus:border-lime'
        />
        <Button type='submit' disabled={startPull.isPending} className='flex items-center gap-2'>
          <Download className='size-4' aria-hidden='true' />
          {t('models.pull')}
        </Button>
      </form>
      {formError ? (
        <p id='model-name-error' role='alert' className='mt-2 text-xs text-danger'>
          {formError}
        </p>
      ) : null}

      <PullProgress />

      <div className='mt-4'>
        <QueryState
          isPending={models.isPending}
          isError={models.isError}
          isEmpty={models.data?.length === 0}
          emptyMessage={t('models.empty')}
          onRetry={() => void models.refetch()}
        >
          <div className='overflow-x-auto'>
            <table className='w-full border-collapse text-xs'>
              <thead>
                <tr>
                  {[
                    t('models.columnModel'),
                    t('models.columnSize'),
                    t('models.columnDetails'),
                  ].map((heading) => (
                    <th
                      key={heading}
                      scope='col'
                      className='border-b border-panel-edge px-1.5 py-2.5 text-left text-[10px] font-normal tracking-[0.12em] text-muted uppercase'
                    >
                      {heading}
                    </th>
                  ))}
                  <th scope='col' className='border-b border-panel-edge px-1.5 py-2.5' />
                </tr>
              </thead>
              <tbody>
                {(models.data ?? []).map((model) => (
                  <tr key={model.name}>
                    <td className='border-b border-panel-edge px-1.5 py-2.5'>{model.name}</td>
                    <td className='border-b border-panel-edge px-1.5 py-2.5'>
                      {formatGigabytes(model.size)}
                    </td>
                    <td className='border-b border-panel-edge px-1.5 py-2.5 text-muted'>
                      {model.details?.parameter_size ?? t('common.unknown')} /{' '}
                      {model.details?.quantization_level ?? t('common.unknown')}
                    </td>
                    <td className='border-b border-panel-edge px-1.5 py-2.5 text-right'>
                      <Button
                        type='button'
                        variant='danger'
                        disabled={deleteModel.isPending}
                        onClick={() => remove(model.name)}
                        className='inline-flex items-center gap-1.5'
                      >
                        <Trash2 className='size-3.5' aria-hidden='true' />
                        {t('models.remove')}
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </QueryState>
      </div>
    </Panel>
  )
}

export const modelsRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/models',
  component: ModelsPage,
})
