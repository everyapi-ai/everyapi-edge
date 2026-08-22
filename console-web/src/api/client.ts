import type * as z from 'zod'

import { parseErrorResponse } from './errors'

export const SESSION_UNAUTHORIZED_EVENT = 'everyapi:session-unauthorized'

export const apiFetch = async (path: string, init: RequestInit = {}): Promise<Response> => {
  const hasFormData = typeof FormData !== 'undefined' && init.body instanceof FormData
  const response = await fetch(path, {
    ...init,
    headers: {
      ...(init.body === undefined || hasFormData ? {} : { 'Content-Type': 'application/json' }),
      ...init.headers,
    },
  })
  if (!response.ok) {
    if (response.status === 401 && typeof window !== 'undefined') {
      window.dispatchEvent(new Event(SESSION_UNAUTHORIZED_EVENT))
    }
    throw await parseErrorResponse(response)
  }
  return response
}

/** GET + schema-validate. A schema mismatch surfaces as a query error rather
 *  than a half-rendered panel. */
export const getJSON = async <T extends z.ZodTypeAny>(
  path: string,
  schema: T,
): Promise<z.infer<T>> => {
  const response = await apiFetch(path)
  return schema.parse(await response.json()) as z.infer<T>
}

export const postJSON = async (path: string, body: unknown): Promise<void> => {
  await apiFetch(path, { method: 'POST', body: JSON.stringify(body) })
}

export const postJSONResponse = async <T extends z.ZodTypeAny>(
  path: string,
  body: unknown,
  schema: T,
): Promise<z.infer<T>> => {
  const response = await apiFetch(path, { method: 'POST', body: JSON.stringify(body) })
  return schema.parse(await response.json()) as z.infer<T>
}

export const putJSONResponse = async <T extends z.ZodTypeAny>(
  path: string,
  body: unknown,
  schema: T,
): Promise<z.infer<T>> => {
  const response = await apiFetch(path, { method: 'PUT', body: JSON.stringify(body) })
  return schema.parse(await response.json()) as z.infer<T>
}

/** Consume the local agent's small SSE envelope. It intentionally keeps the
 * parser here rather than introducing a streaming dependency into the one-file
 * control room bundle. */
export const postJSONStream = async <T extends z.ZodTypeAny>(
  path: string,
  body: unknown,
  schema: T,
  onEvent: (event: z.infer<T>) => void,
  signal?: AbortSignal,
): Promise<void> => {
  const response = await apiFetch(path, { method: 'POST', body: JSON.stringify(body), signal })
  if (!response.body) throw new Error('the agent returned an empty chat stream')

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  let buffer = ''
  const consume = (frame: string) => {
    const data = frame
      .split('\n')
      .find((line) => line.startsWith('data:'))
      ?.slice('data:'.length)
      .trim()
    if (data) onEvent(schema.parse(JSON.parse(data)) as z.infer<T>)
  }
  try {
    for (;;) {
      const { done, value } = await reader.read()
      buffer += decoder.decode(value, { stream: !done })
      let boundary = buffer.indexOf('\n\n')
      while (boundary >= 0) {
        consume(buffer.slice(0, boundary))
        buffer = buffer.slice(boundary + 2)
        boundary = buffer.indexOf('\n\n')
      }
      if (done) break
    }
    if (buffer.trim()) consume(buffer)
  } finally {
    reader.releaseLock()
  }
}

export const del = async (path: string): Promise<void> => {
  await apiFetch(path, { method: 'DELETE' })
}
