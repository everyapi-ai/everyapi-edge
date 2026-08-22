import { describe, expect, it } from 'vitest'

import { normalizeSavedHistory, serializeHistory, titleFromMessage } from './conversations'

describe('playground conversation persistence', () => {
  it('normalizes whitespace and truncates conversation titles', () => {
    expect(titleFromMessage('  hello\n  local   model  ', 'New conversation')).toBe(
      'hello local model',
    )
    expect(titleFromMessage('x'.repeat(60), 'New conversation')).toBe(`${'x'.repeat(47)}…`)
    expect(titleFromMessage('   ', 'New conversation')).toBe('New conversation')
  })

  it('drops malformed conversations and selects an existing active conversation', () => {
    const history = normalizeSavedHistory(
      JSON.stringify({
        active_id: 'missing',
        conversations: [
          {
            id: 'valid',
            title: 'Saved',
            model: 'qwen3:8b',
            temperature: 9,
            messages: [
              { role: 'user', content: 'hello' },
              { role: 'system', content: 'drop me' },
            ],
          },
          { id: 3, title: 'Invalid', model: 'qwen3:8b' },
        ],
      }),
    )
    expect(history.activeID).toBe('valid')
    expect(history.conversations).toHaveLength(1)
    expect(history.conversations[0]?.temperature).toBe(0.7)
    expect(history.conversations[0]?.messages).toEqual([
      { role: 'user', content: 'hello', usage: undefined },
    ])
  })

  it('never persists image attachments', () => {
    const serialized = serializeHistory({
      activeID: 'one',
      conversations: [
        {
          id: 'one',
          title: 'Saved',
          model: 'qwen3:8b',
          system: '',
          temperature: 0.7,
          messages: [
            {
              role: 'user',
              content: 'hello',
              attachment: {
                name: 'large.png',
                dataURL: 'data:image/png;base64,eA==',
                base64: 'eA==',
              },
            },
          ],
        },
      ],
    })
    expect(serialized).not.toContain('large.png')
    expect(JSON.parse(serialized).active_id).toBe('one')
  })
})
