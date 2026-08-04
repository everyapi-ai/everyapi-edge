import { useEffect, useRef } from 'react'

import { useMutation, useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query'

import { del, getJSON, postJSON, postJSONResponse } from './client'
import {
  logListSchema,
  modelBenchmarkSchema,
  modelCapabilitiesSchema,
  modelListSchema,
  nodeProfileSchema,
  imageRuntimeSchema,
  overviewSchema,
  pullQueueSchema,
  requestListSchema,
  runtimeSchema,
  settlementListSchema,
  storageSchema,
  storageMigrationSchema,
  type EdgeRequest,
  type LogEntry,
  type Model,
  type ModelCapabilities,
  type NodeProfile,
  type ImageRuntime,
  type Overview,
  type PullQueue,
  type Runtime,
  type Settlement,
  type Storage,
  type StorageMigration,
} from './schemas'

// The agent holds this data in memory and it changes on every relayed request,
// so polling stays. React Query owns the cadence per endpoint instead of one
// global setInterval refetching all six at once.
const LIVE_REFETCH_MS = 5_000
// A running download reports byte progress; the model list itself changes only
// when a pull or delete finishes, which invalidates it explicitly.
const PULL_REFETCH_MS = 1_500
const IDLE_REFETCH_MS = 15_000

export const queryKeys = {
  overview: ['overview'] as const,
  nodeProfile: ['node-profile'] as const,
  models: ['models'] as const,
  modelCapabilities: (name: string) => ['models', 'capabilities', name] as const,
  requests: ['requests'] as const,
  logs: ['logs'] as const,
  settlements: ['settlements'] as const,
  pull: ['models', 'pull'] as const,
  runtime: ['runtime'] as const,
  imageRuntime: ['image-runtime'] as const,
  storage: ['storage'] as const,
  storageMigration: ['storage', 'migration'] as const,
}

export const useOverview = (): UseQueryResult<Overview> =>
  useQuery({
    queryKey: queryKeys.overview,
    queryFn: () => getJSON('/api/overview', overviewSchema),
    refetchInterval: LIVE_REFETCH_MS,
  })

export const useNodeProfile = (): UseQueryResult<NodeProfile> =>
  useQuery({
    queryKey: queryKeys.nodeProfile,
    queryFn: () => getJSON('/api/node', nodeProfileSchema),
    staleTime: 60_000,
  })

export const useUpdateAgent = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => postJSON('/api/update', {}),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.overview })
    },
  })
}

export const useModels = (): UseQueryResult<Model[]> =>
  useQuery({
    queryKey: queryKeys.models,
    queryFn: async () => (await getJSON('/api/models', modelListSchema)).models,
    refetchInterval: IDLE_REFETCH_MS,
  })

export const useModelCapabilities = (name: string): UseQueryResult<ModelCapabilities> =>
  useQuery({
    queryKey: queryKeys.modelCapabilities(name),
    queryFn: () =>
      getJSON(`/api/models/capabilities?name=${encodeURIComponent(name)}`, modelCapabilitiesSchema),
    enabled: Boolean(name),
    staleTime: 60_000,
  })

export const useRuntime = (): UseQueryResult<Runtime> =>
  useQuery({
    queryKey: queryKeys.runtime,
    queryFn: () => getJSON('/api/runtime', runtimeSchema),
    refetchInterval: LIVE_REFETCH_MS,
  })

export const useImageRuntime = (): UseQueryResult<ImageRuntime> =>
  useQuery({
    queryKey: queryKeys.imageRuntime,
    queryFn: () => getJSON('/api/image-runtime', imageRuntimeSchema),
    refetchInterval: LIVE_REFETCH_MS,
  })

export const useSetImageRuntimeModel = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (model: string) =>
      postJSONResponse('/api/image-runtime/model', { model }, imageRuntimeSchema),
    onSuccess: (runtime) => {
      queryClient.setQueryData(queryKeys.imageRuntime, runtime)
    },
  })
}

export const useStorage = (): UseQueryResult<Storage> =>
  useQuery({
    queryKey: queryKeys.storage,
    queryFn: () => getJSON('/api/storage', storageSchema),
    refetchInterval: IDLE_REFETCH_MS,
  })

export const useStorageMigration = (): UseQueryResult<StorageMigration> =>
  useQuery({
    queryKey: queryKeys.storageMigration,
    queryFn: () => getJSON('/api/storage/migrate', storageMigrationSchema),
    refetchInterval: (query) => {
      const job = query.state.data
      return job && job.status !== 'idle' && !job.done ? PULL_REFETCH_MS : false
    },
  })

export const useStartStorageMigration = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ source, destination }: { source: string; destination: string }) =>
      postJSONResponse('/api/storage/migrate', { source, destination }, storageMigrationSchema),
    onSuccess: (job) => {
      queryClient.setQueryData(queryKeys.storageMigration, job)
    },
  })
}

export const useRequests = (): UseQueryResult<EdgeRequest[]> =>
  useQuery({
    queryKey: queryKeys.requests,
    queryFn: () => getJSON('/api/requests', requestListSchema),
    refetchInterval: LIVE_REFETCH_MS,
  })

export const useLogs = (): UseQueryResult<LogEntry[]> =>
  useQuery({
    queryKey: queryKeys.logs,
    queryFn: () => getJSON('/api/logs', logListSchema),
    refetchInterval: LIVE_REFETCH_MS,
  })

export const useSettlements = (): UseQueryResult<Settlement[]> =>
  useQuery({
    queryKey: queryKeys.settlements,
    queryFn: () => getJSON('/api/settlements', settlementListSchema),
    refetchInterval: LIVE_REFETCH_MS,
  })

export const usePullQueue = (): UseQueryResult<PullQueue> =>
  useQuery({
    queryKey: queryKeys.pull,
    queryFn: () => getJSON('/api/models/pull', pullQueueSchema),
    // Poll fast while active work or queued downloads remain, then back off.
    refetchInterval: (query) =>
      query.state.data?.active || query.state.data?.queued.length
        ? PULL_REFETCH_MS
        : IDLE_REFETCH_MS,
  })

export const useStartPull = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => postJSON('/api/models/pull', { name }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.pull })
    },
  })
}

export const useCancelPull = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => del(`/api/models/pull?name=${encodeURIComponent(name)}`),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.pull })
    },
  })
}

export const useDeleteModel = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (name: string) =>
      del(`/api/models?name=${encodeURIComponent(name)}&confirm_unloaded=true`),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.models })
    },
  })
}

export const useBenchmarkModel = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: ({ model, releaseLoaded }: { model: string; releaseLoaded: boolean }) =>
      postJSONResponse(
        '/api/models/benchmark',
        { model, release_loaded: releaseLoaded },
        modelBenchmarkSchema,
      ),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.runtime }),
        queryClient.invalidateQueries({ queryKey: queryKeys.overview }),
      ])
    },
  })
}

export const useUnloadRuntimeModel = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (model: string) => postJSON('/api/runtime/unload', { model }),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.runtime }),
        queryClient.invalidateQueries({ queryKey: queryKeys.overview }),
      ])
    },
  })
}

export const useUnloadAllRuntimeModels = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: () => postJSON('/api/runtime/unload-all', {}),
    onSuccess: async () => {
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: queryKeys.runtime }),
        queryClient.invalidateQueries({ queryKey: queryKeys.overview }),
      ])
    },
  })
}

/** A finished download changes what the library holds. Refreshing on that edge
 *  keeps the list poll slow instead of hammering /api/tags every second. */
export const useRefreshModelsOnPullCompletion = (queue: PullQueue | undefined) => {
  const queryClient = useQueryClient()
  const done = queue?.latest?.done ?? false
  const name = queue?.latest?.name ?? ''
  const previous = useRef<string | null>(null)
  useEffect(() => {
    const marker = done ? name : null
    if (marker && previous.current !== marker) {
      void queryClient.invalidateQueries({ queryKey: queryKeys.models })
    }
    previous.current = marker
  }, [done, name, queryClient])
}
