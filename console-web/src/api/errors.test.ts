import { describe, expect, it } from 'vitest'

import { ApiError, parseErrorResponse } from './errors'
import { errorEnvelopeSchema } from './schemas'

describe('parseErrorResponse', () => {
  it('preserves a structured agent error', async () => {
    const response = new Response(
      JSON.stringify({
        error: {
          code: 'runtime_unavailable',
          message: 'The local runtime is unavailable.',
          retryable: true,
        },
      }),
      { status: 503, headers: { 'Content-Type': 'application/json' } },
    )

    await expect(parseErrorResponse(response)).resolves.toEqual(
      new ApiError('runtime_unavailable', 'The local runtime is unavailable.', 503, true),
    )
  })

  it('normalizes the legacy string envelope during migration', async () => {
    const response = new Response(JSON.stringify({ error: 'model is still loaded' }), {
      status: 409,
      statusText: 'Conflict',
    })

    await expect(parseErrorResponse(response)).resolves.toEqual(
      new ApiError('http_409', 'model is still loaded', 409, false),
    )
  })

  it('falls back to status text without exposing an HTML response', async () => {
    const response = new Response('<html>proxy failure</html>', {
      status: 502,
      statusText: 'Bad Gateway',
    })

    await expect(parseErrorResponse(response)).resolves.toEqual(
      new ApiError('http_502', 'Bad Gateway', 502, true),
    )
  })
})

describe('errorEnvelopeSchema', () => {
  it('accepts the structured agent error contract', () => {
    expect(
      errorEnvelopeSchema.parse({
        error: {
          code: 'runtime_unavailable',
          message: 'The local runtime is unavailable.',
          retryable: true,
        },
      }),
    ).toEqual({
      error: {
        code: 'runtime_unavailable',
        message: 'The local runtime is unavailable.',
        retryable: true,
      },
    })
  })
})
