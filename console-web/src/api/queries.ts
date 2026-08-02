import { useEffect, useRef } from 'react'

import { useMutation, useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query'

import { del, getJSON, postJSON } from './client'
import {
  logListSchema,
  modelListSchema,
  overviewSchema,
  pullJobResponseSchema,
  requestListSchema,
  settlementListSchema,
  type EdgeRequest,
  type LogEntry,
  type Model,
  type Overview,
  type PullJob,
  type Settlement,
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
  models: ['models'] as const,
  requests: ['requests'] as const,
  logs: ['logs'] as const,
  settlements: ['settlements'] as const,
  pull: ['models', 'pull'] as const,
}

export const useOverview = (): UseQueryResult<Overview> =>
  useQuery({
    queryKey: queryKeys.overview,
    queryFn: () => getJSON('/api/overview', overviewSchema),
    refetchInterval: LIVE_REFETCH_MS,
  })

export const useModels = (): UseQueryResult<Model[]> =>
  useQuery({
    queryKey: queryKeys.models,
    queryFn: async () => (await getJSON('/api/models', modelListSchema)).models,
    refetchInterval: IDLE_REFETCH_MS,
  })

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

export const usePullJob = (): UseQueryResult<PullJob | null> =>
  useQuery({
    queryKey: queryKeys.pull,
    queryFn: () => getJSON('/api/models/pull', pullJobResponseSchema),
    // Poll fast while a download is running, then back off. A finished job
    // stays readable so the supplier can see how the last pull ended.
    refetchInterval: (query) =>
      query.state.data && !query.state.data.done ? PULL_REFETCH_MS : IDLE_REFETCH_MS,
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

export const useDeleteModel = () => {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => del(`/api/models?name=${encodeURIComponent(name)}`),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: queryKeys.models })
    },
  })
}

/** A finished download changes what the library holds. Refreshing on that edge
 *  keeps the list poll slow instead of hammering /api/tags every second. */
export const useRefreshModelsOnPullCompletion = (job: PullJob | null | undefined) => {
  const queryClient = useQueryClient()
  const done = job?.done ?? false
  const name = job?.name ?? ''
  const previous = useRef<string | null>(null)
  useEffect(() => {
    const marker = done ? name : null
    if (marker && previous.current !== marker) {
      void queryClient.invalidateQueries({ queryKey: queryKeys.models })
    }
    previous.current = marker
  }, [done, name, queryClient])
}
