import type { Locale } from '@/i18n/locales'

export const formatCount = (value: number, locale: Locale): string =>
  new Intl.NumberFormat(locale).format(value)

/** Ollama reports sizes in bytes with decimal-GB labels, so divide by 1e9 to
 *  match what `ollama list` prints rather than using 2^30. */
export const formatGigabytes = (bytes: number): string => `${(bytes / 1e9).toFixed(1)} GB`

/** Receipts carry USD micros; six decimals keeps sub-cent settlements visible
 *  instead of rounding a real payout to $0.00. */
export const formatUSDMicros = (micros: number): string => `$${(micros / 1e6).toFixed(6)}`

export const formatTime = (value: Date | null, locale: Locale): string =>
  value ? new Intl.DateTimeFormat(locale, { timeStyle: 'medium' }).format(value) : '—'
