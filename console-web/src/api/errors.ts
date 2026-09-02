import type { MessageKey } from '@/i18n/locales'
import type { Translate } from '@/i18n/useTranslation'

const fallbackCode = (status: number) => `http_${status}`

/** Mirrors `errorCode` in `internal/console/http.go` so a response carrying no envelope of its own still resolves to the code the agent would have used. */
const errorCodeForStatus = (status: number) => {
  switch (status) {
    case 400:
      return 'invalid_request'
    case 401:
      return 'unauthorized'
    case 403:
      return 'forbidden'
    case 404:
      return 'not_found'
    case 409:
      return 'conflict'
    case 422:
      return 'unsupported_operation'
    case 501:
      return 'not_supported'
    case 502:
    case 504:
      return 'runtime_error'
    case 503:
      return 'runtime_unavailable'
    // The agent's own default arm is `internal_error`, and a 500 is the one status a reverse proxy or a panicking handler produces without an envelope, so mirroring it here is what keeps "Internal Server Error" out of the operator's only explanation.
    case 500:
      return 'internal_error'
    default:
      return fallbackCode(status)
  }
}

const retryableStatus = (status: number) => status >= 500 || status === 408 || status === 429

export class ApiError extends Error {
  readonly code: string
  readonly status: number
  readonly retryable: boolean

  constructor(code: string, message: string, status: number, retryable = retryableStatus(status)) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.status = status
    this.retryable = retryable
  }
}

type StructuredError = {
  code?: unknown
  message?: unknown
  retryable?: unknown
}

export const parseErrorResponse = async (response: Response): Promise<ApiError> => {
  const body = await response.text()
  try {
    const parsed: unknown = JSON.parse(body)
    // POST /api/storage/migrate answers a blocked migration with 409 and a MigrationPlan body rather than the error envelope, so this runs before the envelope check: its blockers are the only statement of why the copy cannot start.
    if (
      parsed &&
      typeof parsed === 'object' &&
      Array.isArray((parsed as { blockers?: unknown }).blockers)
    ) {
      const reasons = (parsed as { blockers: unknown[] }).blockers
        .filter(
          (blocker): blocker is string => typeof blocker === 'string' && blocker.trim() !== '',
        )
        .map((blocker) => blocker.trim())
      if (reasons.length) {
        return new ApiError(
          errorCodeForStatus(response.status),
          reasons.join('; '),
          response.status,
        )
      }
    }
    if (parsed && typeof parsed === 'object' && 'error' in parsed) {
      const error = (parsed as { error: unknown }).error
      if (typeof error === 'string' && error.trim()) {
        return new ApiError(fallbackCode(response.status), error.trim(), response.status)
      }
      if (error && typeof error === 'object') {
        const details = error as StructuredError
        if (typeof details.message === 'string' && details.message.trim()) {
          return new ApiError(
            typeof details.code === 'string' && details.code
              ? details.code
              : fallbackCode(response.status),
            details.message.trim(),
            response.status,
            typeof details.retryable === 'boolean'
              ? details.retryable
              : retryableStatus(response.status),
          )
        }
      }
    }
  } catch {
    // A reverse proxy can return HTML. Never surface markup as an operator error.
  }

  const contentType = response.headers.get('Content-Type') ?? ''
  const plainText = contentType.startsWith('text/plain') && !body.trimStart().startsWith('<')
  const message = plainText && body.trim() ? body.trim().slice(0, 1_024) : response.statusText
  return new ApiError(fallbackCode(response.status), message || 'Request failed', response.status)
}

/** The agent codes its failures (`internal/console/http.go`) so the console can
 *  say what went wrong in the operator's language instead of one generic
 *  sentence. Anything unrecognised falls back to the raw server message. */
const MESSAGE_KEYS: Record<string, MessageKey> = {
  invalid_request: 'error.invalid_request',
  unauthorized: 'error.unauthorized',
  forbidden: 'error.forbidden',
  not_found: 'error.not_found',
  conflict: 'error.conflict',
  unsupported_operation: 'error.unsupported_operation',
  not_supported: 'error.not_supported',
  runtime_error: 'error.runtime_error',
  runtime_unavailable: 'error.runtime_unavailable',
  internal_error: 'error.internal_error',
}

export type DescribedError = { headline: string; detail?: string }

/** Below 500 the agent returns the underlying error verbatim, and that detail is
 *  the only thing that tells an operator which model or path was rejected — keep
 *  it. At 500 and above the message is already a fixed generic sentence, so the
 *  localized headline says the same thing and the English copy is noise. */
export const describeError = (error: unknown, t: Translate): DescribedError => {
  if (error instanceof ApiError) {
    // An unmapped code still resolves through the status, so a bare `statusText` such as "Not Found" never becomes the operator's only, English, explanation.
    const key = MESSAGE_KEYS[error.code] ?? MESSAGE_KEYS[errorCodeForStatus(error.status)]
    const detail = error.status < 500 && error.message.trim() ? error.message.trim() : undefined
    if (key) return { headline: t(key), detail }
    return { headline: error.message.trim() || t('state.error'), detail: undefined }
  }
  if (error instanceof TypeError) return { headline: t('error.network') }
  // Anything else that reaches here came from the console, not from the agent: a schema mismatch (a ZodError carries the whole issue list as its message), an aborted request, a bug. The text is worth keeping for diagnosis, but as detail — a several-hundred-character JSON dump is not a headline.
  if (error instanceof Error && error.message.trim())
    return { headline: t('error.internal_error'), detail: error.message.trim() }
  return { headline: t('state.error') }
}
