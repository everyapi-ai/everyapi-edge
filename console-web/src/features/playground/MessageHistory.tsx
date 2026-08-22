import { Bot, UserRound } from 'lucide-react'

import { Button } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'

import type { ChatMessage } from './conversations'

export const MessageHistory = ({
  messages,
  isStreaming,
  onRegenerate,
}: {
  messages: ChatMessage[]
  isStreaming: boolean
  onRegenerate: (index: number) => void
}) => {
  const { t } = useTranslation()
  return (
    <div
      data-playground-transcript
      className='flex flex-1 flex-col gap-5 overflow-y-auto px-4 py-6 sm:px-7'
    >
      {messages.length === 0 ? (
        <div className='m-auto max-w-sm text-center'>
          <span className='mx-auto grid size-10 place-items-center rounded-xl bg-accent/12 text-accent'>
            <Bot className='size-5' aria-hidden='true' />
          </span>
          <h2 className='mt-4 text-base font-semibold text-ink'>{t('playground.empty')}</h2>
          <p className='mt-2 text-sm leading-6 text-muted'>{t('playground.emptyHint')}</p>
        </div>
      ) : (
        messages.map((message, index) => {
          const user = message.role === 'user'
          const pending = !user && isStreaming && index === messages.length - 1
          return (
            <article
              key={`${message.role}-${index}`}
              className={`flex gap-3 ${user ? 'flex-row-reverse' : ''}`}
            >
              <span
                className={`grid size-7 shrink-0 place-items-center rounded-md ${user ? 'bg-surface-2 text-ink-2' : 'bg-accent/12 text-accent'}`}
              >
                {user ? (
                  <UserRound className='size-3.5' aria-hidden='true' />
                ) : (
                  <Bot className='size-3.5' aria-hidden='true' />
                )}
              </span>
              <div className={`max-w-[82%] ${user ? 'text-right' : ''}`}>
                <p className='mb-1 text-xs font-medium text-faint'>
                  {user ? t('playground.you') : t('playground.assistant')}
                </p>
                <div
                  className={`whitespace-pre-wrap rounded-xl px-3.5 py-3 text-sm leading-6 ${user ? 'bg-accent text-accent-ink' : 'border border-line bg-surface-1 text-ink'}`}
                >
                  {message.attachment ? (
                    <img
                      src={message.attachment.dataURL}
                      alt={message.attachment.name}
                      className='mb-2 max-h-64 max-w-full rounded-md object-contain'
                    />
                  ) : null}
                  {message.content || (pending ? t('playground.sending') : '')}
                </div>
                {!user && !pending ? (
                  <Button
                    type='button'
                    variant='ghost'
                    onClick={() => onRegenerate(index)}
                    className='mt-2 px-2 py-1 text-xs'
                  >
                    {t('playground.regenerate')}
                  </Button>
                ) : null}
                {message.usage ? (
                  <p className='mt-1 text-xs text-faint'>
                    {t('playground.usage', {
                      prompt: message.usage.prompt_tokens,
                      completion: message.usage.completion_tokens,
                    })}
                  </p>
                ) : null}
              </div>
            </article>
          )
        })
      )}
    </div>
  )
}
