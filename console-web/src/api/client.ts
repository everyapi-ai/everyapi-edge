import type * as z from 'zod'

import { useSessionStore } from '@/stores/session'

/** Raised when the agent rejects the console token, so callers can send the
 *  supplier back to the unlock screen instead of showing a generic toast. */
export class UnauthorizedError extends Error {
  constructor() {
    super('unauthorized')
    this.name = 'UnauthorizedError'
  }
}

const readErrorMessage = async (response: Response): Promise<string> => {
  const body = await response.text()
  try {
    const parsed: unknown = JSON.parse(body)
    if (parsed && typeof parsed === 'object' && 'error' in parsed) {
      const message = (parsed as { error: unknown }).error
      if (typeof message === 'string' && message) return message
    }
  } catch {
    // http.Error writes text/plain (the token middleware does), so fall through
    // to the raw body.
  }
  return body.trim() || response.statusText
}

const request = async (path: string, init: RequestInit = {}): Promise<Response> => {
  const token = useSessionStore.getState().token
  const response = await fetch(path, {
    ...init,
    headers: {
      ...(init.body === undefined ? {} : { 'Content-Type': 'application/json' }),
      ...init.headers,
      Authorization: `Bearer ${token}`,
    },
  })
  if (response.status === 401 || response.status === 503) {
    throw new UnauthorizedError()
  }
  if (!response.ok) {
    throw new Error(await readErrorMessage(response))
  }
  return response
}

/** GET + schema-validate. A schema mismatch surfaces as a query error rather
 *  than a half-rendered panel. */
export const getJSON = async <T extends z.ZodTypeAny>(
  path: string,
  schema: T
): Promise<z.infer<T>> => {
  const response = await request(path)
  return schema.parse(await response.json()) as z.infer<T>
}

export const postJSON = async (path: string, body: unknown): Promise<void> => {
  await request(path, { method: 'POST', body: JSON.stringify(body) })
}

export const del = async (path: string): Promise<void> => {
  await request(path, { method: 'DELETE' })
}
