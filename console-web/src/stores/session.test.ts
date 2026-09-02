import { afterEach, describe, expect, it, vi } from 'vitest'

/** The console is served to browsers, but the store runs `detectLocale` at module load, so a host that exposes no language must yield the default locale rather than throw on import. */
const loadStore = async () => {
  vi.resetModules()
  return (await import('./session')).useSessionStore
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('session locale detection', () => {
  it('falls back to the default locale when the host exposes no language at all', async () => {
    vi.stubGlobal('navigator', {})
    const store = await loadStore()
    expect(store.getState().locale).toBe('en')
  })

  it('ignores a languages list holding non-strings', async () => {
    vi.stubGlobal('navigator', { languages: [undefined, null] })
    const store = await loadStore()
    expect(store.getState().locale).toBe('en')
  })

  it('prefers the first supported language the host offers', async () => {
    vi.stubGlobal('navigator', { languages: ['fr-FR', 'zh-CN', 'en-US'] })
    const store = await loadStore()
    expect(store.getState().locale).toBe('zh')
  })

  it('reads the single language when the host has no languages list', async () => {
    vi.stubGlobal('navigator', { language: 'zh-Hans-CN' })
    const store = await loadStore()
    expect(store.getState().locale).toBe('zh')
  })
})
