import { afterEach, describe, expect, it, vi } from 'vitest'
import * as z from 'zod'

import { getJSON, SESSION_UNAUTHORIZED_EVENT } from './client'

describe('API session expiry', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('notifies the session gate when a protected request returns 401', async () => {
    const dispatchEvent = vi.fn()
    vi.stubGlobal('window', { dispatchEvent })
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              code: 'unauthorized',
              message: 'browser session expired',
              retryable: false,
            },
          }),
          { status: 401, headers: { 'Content-Type': 'application/json' } },
        ),
      ),
    )

    await expect(getJSON('/api/models', z.object({ models: z.array(z.string()) }))).rejects.toThrow(
      'browser session expired',
    )
    expect(dispatchEvent).toHaveBeenCalledOnce()
    expect(dispatchEvent.mock.calls[0]?.[0]).toBeInstanceOf(Event)
    expect(dispatchEvent.mock.calls[0]?.[0].type).toBe(SESSION_UNAUTHORIZED_EVENT)
  })
})
