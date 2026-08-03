import type { Locale } from '@/i18n/locales'

export const formatCount = (value: number, locale: Locale): string =>
  new Intl.NumberFormat(locale).format(value)

/** Ollama reports sizes in bytes with decimal-GB labels, so divide by 1e9 to
 *  match what `ollama list` prints rather than using 2^30. */
export const formatGigabytes = (bytes: number): string => `${(bytes / 1e9).toFixed(1)} GB`

/** GPU memory budgets are reported by the agent in GiB-based bytes. Keep the
 * displayed total and its loaded/reserved/available segments on that same
 * scale; using decimal disk units here makes a configured 48 GB look like
 * 51.5 GB and breaks the budget users use to choose models. */
export const formatVRAMGigabytes = (bytes: number): string => `${(bytes / (1024 ** 3)).toFixed(1)} GB`

/** Receipts carry USD micros; six decimals keeps sub-cent settlements visible
 *  instead of rounding a real payout to $0.00. */
export const formatUSDMicros = (micros: number): string => `$${(micros / 1e6).toFixed(6)}`

export const formatTime = (value: Date | null, locale: Locale): string =>
  value ? new Intl.DateTimeFormat(locale, { timeStyle: 'medium' }).format(value) : '—'
