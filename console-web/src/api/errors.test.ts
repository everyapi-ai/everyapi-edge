import { describe, expect, it } from 'vitest'

import { ApiError, describeError, parseErrorResponse } from './errors'
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
  it('keeps the blockers from a conflicting migration plan', async () => {
    const response = new Response(
      JSON.stringify({
        source: { path: '/models', accessible: true },
        destination: { path: '/models/new', accessible: true },
        ready: false,
        blockers: [
          'the destination must not be inside the source directory',
          '  the destination directory is not empty  ',
          '',
        ],
      }),
      { status: 409, headers: { 'Content-Type': 'application/json' } },
    )

    await expect(parseErrorResponse(response)).resolves.toEqual(
      new ApiError(
        'conflict',
        'the destination must not be inside the source directory; the destination directory is not empty',
        409,
      ),
    )
  })
})

describe('describeError', () => {
  /** The real `t` would only prove the message table is wired; this asserts which
   *  key is chosen and whether the server detail survives. */
  const t = ((key: string) => key) as never

  it('localizes a known agent code and hides the generic 5xx copy', () => {
    const described = describeError(
      new ApiError('runtime_unavailable', 'The local runtime is unavailable.', 503),
      t,
    )
    expect(described).toEqual({ headline: 'error.runtime_unavailable', detail: undefined })
  })

  it('keeps the specific server detail on a client error', () => {
    const described = describeError(
      new ApiError('invalid_request', 'model "qwen3:14b" is still loaded', 400),
      t,
    )
    expect(described).toEqual({
      headline: 'error.invalid_request',
      detail: 'model "qwen3:14b" is still loaded',
    })
  })

  it('falls back to the raw message for a status with no localized message', () => {
    expect(describeError(new ApiError('http_418', 'I am a teapot', 418), t)).toEqual({
      headline: 'I am a teapot',
      detail: undefined,
    })
  })

  it('localizes an envelope-less response through its status', () => {
    // A reverse proxy answering 404 gives only `statusText`, which must not become the operator's whole explanation.
    expect(describeError(new ApiError('http_404', 'Not Found', 404), t)).toEqual({
      headline: 'error.not_found',
      detail: 'Not Found',
    })
  })

  it('localizes an envelope-less 500 the way the agent codes it', () => {
    // The agent's own default arm is `internal_error`, so a proxy or a panicking handler answering 500 with no envelope must not leave "Internal Server Error" as the operator's whole explanation.
    expect(describeError(new ApiError('http_500', 'Internal Server Error', 500), t)).toEqual({
      headline: 'error.internal_error',
      detail: undefined,
    })
  })

  it('reports a fetch failure as an unreachable agent', () => {
    expect(describeError(new TypeError('Failed to fetch'), t)).toEqual({
      headline: 'error.network',
    })
  })

  it('demotes a console-side failure to detail instead of headlining its dump', () => {
    // A schema mismatch throws a ZodError whose message is the whole issue list; as a headline it fills the panel with JSON.
    const schemaMismatch = new Error(
      '[{"expected":"enum","code":"invalid_value","path":["capabilities",0,"id"]}]',
    )
    expect(describeError(schemaMismatch, t)).toEqual({
      headline: 'error.internal_error',
      detail: '[{"expected":"enum","code":"invalid_value","path":["capabilities",0,"id"]}]',
    })
  })

  it('falls back to the panel message when there is no error', () => {
    expect(describeError(undefined, t)).toEqual({ headline: 'state.error' })
  })
})
