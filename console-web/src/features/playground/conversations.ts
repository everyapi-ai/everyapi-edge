export type ChatMessage = {
  role: 'user' | 'assistant'
  content: string
  attachment?: { name: string; dataURL: string; base64: string }
  usage?: { prompt_tokens: number; completion_tokens: number }
}

export type SavedConversation = {
  id: string
  title: string
  model: string
  system: string
  temperature: number
  messages: ChatMessage[]
}

export type SavedHistory = {
  activeID: string
  conversations: SavedConversation[]
}

export const conversationStorageKey = 'everyapi.edge.playground.v1'
export const maxSavedConversations = 30

export const newConversationID = () =>
  globalThis.crypto?.randomUUID?.() ??
  `conversation-${Date.now()}-${Math.random().toString(36).slice(2)}`

export const titleFromMessage = (content: string, fallback: string) => {
  const normalized = content.replace(/\s+/g, ' ').trim()
  if (!normalized) return fallback
  return normalized.length > 48 ? `${normalized.slice(0, 47)}…` : normalized
}

export const normalizeSavedHistory = (raw: string | null): SavedHistory => {
  try {
    if (!raw) return { activeID: '', conversations: [] }
    const value: unknown = JSON.parse(raw)
    if (
      !value ||
      typeof value !== 'object' ||
      !Array.isArray((value as { conversations?: unknown }).conversations)
    )
      return { activeID: '', conversations: [] }
    const conversations = (value as { conversations: unknown[] }).conversations
      .flatMap((entry): SavedConversation[] => {
        if (!entry || typeof entry !== 'object') return []
        const candidate = entry as Record<string, unknown>
        if (
          typeof candidate.id !== 'string' ||
          typeof candidate.title !== 'string' ||
          typeof candidate.model !== 'string'
        )
          return []
        const messages = Array.isArray(candidate.messages)
          ? candidate.messages.flatMap((message): ChatMessage[] => {
              if (!message || typeof message !== 'object') return []
              const saved = message as Record<string, unknown>
              if (
                (saved.role !== 'user' && saved.role !== 'assistant') ||
                typeof saved.content !== 'string'
              )
                return []
              const usage =
                saved.usage &&
                typeof saved.usage === 'object' &&
                typeof (saved.usage as Record<string, unknown>).prompt_tokens === 'number' &&
                typeof (saved.usage as Record<string, unknown>).completion_tokens === 'number'
                  ? {
                      prompt_tokens: (saved.usage as Record<string, number>).prompt_tokens,
                      completion_tokens: (saved.usage as Record<string, number>).completion_tokens,
                    }
                  : undefined
              return [{ role: saved.role as ChatMessage['role'], content: saved.content, usage }]
            })
          : []
        return [
          {
            id: candidate.id,
            title: candidate.title,
            model: candidate.model,
            system: typeof candidate.system === 'string' ? candidate.system : '',
            temperature:
              typeof candidate.temperature === 'number' &&
              candidate.temperature >= 0 &&
              candidate.temperature <= 2
                ? candidate.temperature
                : 0.7,
            messages,
          },
        ]
      })
      .slice(0, maxSavedConversations)
    const requestedActiveID =
      typeof (value as { active_id?: unknown }).active_id === 'string'
        ? (value as { active_id: string }).active_id
        : ''
    return {
      activeID: conversations.some((conversation) => conversation.id === requestedActiveID)
        ? requestedActiveID
        : (conversations[0]?.id ?? ''),
      conversations,
    }
  } catch {
    return { activeID: '', conversations: [] }
  }
}

export const serializeHistory = (history: SavedHistory) =>
  JSON.stringify({
    version: 1,
    active_id: history.activeID,
    conversations: history.conversations.map(({ messages, ...conversation }) => ({
      ...conversation,
      messages: messages.map(({ attachment: _attachment, ...message }) => message),
    })),
  })
