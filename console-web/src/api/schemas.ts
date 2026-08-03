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
  reserved_vram_bytes: z.number(),
  available_vram_bytes: z.number(),
  settled_earnings_micros: z.number(),
  settled_earnings_available: z.boolean(),
  gateway_state: z.enum(['connecting', 'online', 'offline', 'preview']).default('connecting'),
  gateway_last_connected_at: timestamp,
  gateway_last_error: z.string().optional().default(''),
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

export const modelCapabilitiesSchema = z.object({
  model: z.string(),
  capabilities: z.array(z.string()).nullish().transform((capabilities) => capabilities ?? []),
})

export const runtimeModelSchema = z.object({
  name: z.string(),
  size_vram: z.number(),
  context_length: z.number().optional().default(0),
  expires_at: timestamp,
})

export const runtimeSchema = z.object({
  version: z.string(),
  models: z.array(runtimeModelSchema).nullish().transform((models) => models ?? []),
})

export const imageRuntimeSchema = z.object({
  status: z.string(),
  models: z.array(z.string()).nullish().transform((models) => models ?? []),
  error: z.string().optional().default(''),
})

export const storageSchema = z.object({
  path: z.string(),
  accessible: z.boolean(),
  used_bytes: z.number(),
  error: z.string().optional().default(''),
})

export const migrationPlanSchema = z.object({
  source: storageSchema,
  destination: storageSchema,
  ready: z.boolean(),
  blockers: z.array(z.string()),
})

export const storageMigrationSchema = z.object({
  source: z.string().default(''),
  destination: z.string().default(''),
  status: z.string(),
  completed: z.number().default(0),
  total: z.number().default(0),
  error: z.string().optional().default(''),
  done: z.boolean(),
})

export const storagePickerSchema = z.object({
  path: z.string(),
})

export const playgroundResponseSchema = z.object({
  model: z.string(),
  content: z.string(),
  usage: z.object({
    prompt_tokens: z.number(),
    completion_tokens: z.number(),
    total_tokens: z.number(),
  }),
})

export const playgroundStreamEventSchema = z.object({
  type: z.enum(['delta', 'done', 'error']),
  content: z.string().optional().default(''),
  model: z.string().optional().default(''),
  usage: z.object({
    prompt_tokens: z.number(),
    completion_tokens: z.number(),
    total_tokens: z.number(),
  }).optional(),
  error: z.string().optional().default(''),
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

export const pullQueueSchema = z.object({
  active: pullJobSchema.nullable(),
  queued: z.array(pullJobSchema).nullish().transform((jobs) => jobs ?? []),
  latest: pullJobSchema.nullable(),
})

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
export type ImageRuntime = z.infer<typeof imageRuntimeSchema>
export type Model = z.infer<typeof modelSchema>
export type ModelCapabilities = z.infer<typeof modelCapabilitiesSchema>
export type Runtime = z.infer<typeof runtimeSchema>
export type RuntimeModel = z.infer<typeof runtimeModelSchema>
export type Storage = z.infer<typeof storageSchema>
export type MigrationPlan = z.infer<typeof migrationPlanSchema>
export type StorageMigration = z.infer<typeof storageMigrationSchema>
export type PlaygroundResponse = z.infer<typeof playgroundResponseSchema>
export type PlaygroundStreamEvent = z.infer<typeof playgroundStreamEventSchema>
export type EdgeRequest = z.infer<typeof requestSchema>
export type LogEntry = z.infer<typeof logEntrySchema>
export type Settlement = z.infer<typeof settlementSchema>
export type PullJob = z.infer<typeof pullJobSchema>
export type PullQueue = z.infer<typeof pullQueueSchema>
