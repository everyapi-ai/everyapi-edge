import * as z from 'zod'

// Mirrors the JSON the Go handler emits. Every field here has a counterpart in
// clients/edge/internal/console/{server,store}.go — when a struct tag changes
// there, change it here in the same commit. Parsing rather than casting means a
// drift between the two shows up as one visible error in the UI instead of
// `undefined` silently rendering as an empty cell.

// Go marshals a zero `time.Time` as "0001-01-01T00:00:00Z" for fields without
// omitempty, and omits them entirely with it. Both mean "not set" to the UI.
const timestamp = z
  .string()
  .optional()
  .transform((value) => {
    if (!value) return null
    const parsed = new Date(value)
    if (Number.isNaN(parsed.getTime()) || parsed.getUTCFullYear() <= 1) return null
    return parsed
  })

export const overviewSchema = z.object({
  active_requests: z.number(),
  completed_requests: z.number(),
  failed_requests: z.number(),
  prompt_tokens: z.number(),
  completion_tokens: z.number(),
  loaded_vram_bytes: z.number(),
  vram_total_gb: z.number(),
  settled_earnings_micros: z.number(),
  settled_earnings_available: z.boolean(),
})

export const modelSchema = z.object({
  name: z.string(),
  size: z.number(),
  modified_at: z.string().optional(),
  details: z
    .object({
      parameter_size: z.string().optional(),
      quantization_level: z.string().optional(),
    })
    .optional(),
})

// `models` is a Go slice, so an empty library marshals as JSON null, not [].
export const modelListSchema = z.object({
  models: z.array(modelSchema).nullish().transform((models) => models ?? []),
})

export const requestSchema = z.object({
  id: z.string(),
  model: z.string(),
  path: z.string(),
  consumer: z.string(),
  started_at: timestamp,
  completed_at: timestamp,
  duration_ms: z.number().optional().default(0),
  prompt_tokens: z.number().optional().default(0),
  completion_tokens: z.number().optional().default(0),
  error: z.string().optional().default(''),
})

export const logEntrySchema = z.object({
  at: timestamp,
  level: z.string(),
  message: z.string(),
})

export const settlementSchema = z.object({
  request_id: z.string(),
  seller_amount_micros: z.number(),
  settled_at: timestamp,
})

export const pullJobSchema = z.object({
  name: z.string(),
  status: z.string(),
  completed: z.number().optional().default(0),
  total: z.number().optional().default(0),
  error: z.string().optional().default(''),
  done: z.boolean(),
})

// GET /api/models/pull returns JSON null when no download has ever run in this
// process; the handler serialises a nil *pullJob rather than 404ing.
export const pullJobResponseSchema = pullJobSchema.nullable()

const nullableList = <T extends z.ZodTypeAny>(item: T) =>
  z
    .array(item)
    .nullish()
    .transform((items) => items ?? [])

export const requestListSchema = nullableList(requestSchema)
export const logListSchema = nullableList(logEntrySchema)
export const settlementListSchema = nullableList(settlementSchema)

// The handler's error envelope: writeError marshals {"error": "..."}.
export const errorEnvelopeSchema = z.object({ error: z.string() })

export type Overview = z.infer<typeof overviewSchema>
export type Model = z.infer<typeof modelSchema>
export type EdgeRequest = z.infer<typeof requestSchema>
export type LogEntry = z.infer<typeof logEntrySchema>
export type Settlement = z.infer<typeof settlementSchema>
export type PullJob = z.infer<typeof pullJobSchema>
