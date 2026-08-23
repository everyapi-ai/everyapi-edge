import { describe, expect, it } from 'vitest'

import { resourceSettingsSchema } from './schemas'

describe('resource settings contract', () => {
  it('parses every runtime policy and live drain status', () => {
    const parsed = resourceSettingsSchema.parse({
      resource_policy: {
        text: { max_concurrent: 4, reserve_vram_mb: 1024 },
        image: { max_concurrent: 1, reserve_vram_mb: 4096 },
        speech: { max_concurrent: 2, reserve_vram_mb: 0 },
        video: { max_concurrent: 1, reserve_vram_mb: 8192 },
        render: { max_concurrent: 1, reserve_vram_mb: 4096 },
        rerank: { max_concurrent: 2, reserve_vram_mb: 2048 },
      },
      drain_state: 'draining',
      active_requests: 2,
    })

    expect(parsed.resource_policy.video.reserve_vram_mb).toBe(8192)
    expect(parsed.drain_state).toBe('draining')
    expect(parsed.active_requests).toBe(2)
  })

  it('rejects unsafe concurrency values before they reach the agent', () => {
    const policy = {
      text: { max_concurrent: 0, reserve_vram_mb: 0 },
      image: { max_concurrent: 1, reserve_vram_mb: 0 },
      speech: { max_concurrent: 1, reserve_vram_mb: 0 },
      video: { max_concurrent: 1, reserve_vram_mb: 0 },
      render: { max_concurrent: 1, reserve_vram_mb: 0 },
      rerank: { max_concurrent: 1, reserve_vram_mb: 0 },
    }
    expect(() =>
      resourceSettingsSchema.parse({
        resource_policy: policy,
        drain_state: 'serving',
        active_requests: 0,
      }),
    ).toThrow()
  })

  it('normalizes omitted zero VRAM reserves from the protocol', () => {
    const runtime = { max_concurrent: 1 }
    const parsed = resourceSettingsSchema.parse({
      resource_policy: {
        text: runtime,
        image: runtime,
        speech: runtime,
        video: runtime,
        render: runtime,
        rerank: runtime,
      },
      drain_state: 'serving',
      active_requests: 0,
    })
    expect(parsed.resource_policy.text.reserve_vram_mb).toBe(0)
  })
})
