import { describe, expect, it } from 'vitest'

import { updateSettingsSchema } from './schemas'

describe('update settings contract', () => {
  it('parses the maintenance window, observations, rollback, and bounded history', () => {
    const parsed = updateSettingsSchema.parse({
      auto_update: true,
      check_interval_hours: 24,
      maintenance_start: '23:30',
      maintenance_end: '02:00',
      last_check_at_unix_ms: 1_700_000_000_000,
      next_check_at_unix_ms: 1_700_086_400_000,
      installed_version: '1.2.9',
      latest_version: '1.3.0',
      rollback_reason: 'candidate did not reconnect',
      history: [{ state: 'rolled_back', version: '1.3.0' }],
    })

    expect(parsed.maintenance_start).toBe('23:30')
    expect(parsed.rollback_reason).toBe('candidate did not reconnect')
    expect(parsed.history[0]?.state).toBe('rolled_back')
  })

  it('rejects malformed maintenance times', () => {
    expect(() =>
      updateSettingsSchema.parse({
        auto_update: true,
        check_interval_hours: 24,
        maintenance_start: '24:00',
        maintenance_end: '02:00',
      }),
    ).toThrow()
  })
})
