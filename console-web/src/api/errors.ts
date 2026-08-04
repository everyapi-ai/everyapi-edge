const fallbackCode = (status: number) => `http_${status}`

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
