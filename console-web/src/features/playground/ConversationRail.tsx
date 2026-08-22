import { Plus, Trash2 } from 'lucide-react'

import { Button } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'

import type { SavedConversation } from './conversations'

export const ConversationRail = ({
  conversations,
  activeID,
  disabled,
  onCreate,
  onSelect,
  onDelete,
}: {
  conversations: SavedConversation[]
  activeID: string
  disabled: boolean
  onCreate: () => void
  onSelect: (conversation: SavedConversation) => void
  onDelete: (id: string) => void
}) => {
  const { t } = useTranslation()
  return (
    <div
      data-playground-history
      className='border-line flex flex-col gap-2 border-b bg-surface-1 px-4 py-3 sm:flex-row sm:items-center'
    >
      <Button
        type='button'
        variant='ghost'
        disabled={disabled}
        onClick={onCreate}
        className='inline-flex shrink-0 items-center justify-center gap-1.5 px-2.5 py-1.5 text-xs'
      >
        <Plus className='size-3.5' aria-hidden='true' />
        {t('playground.newConversation')}
      </Button>
      <p className='shrink-0 text-xs font-medium text-faint'>{t('playground.history')}</p>
      <div className='flex min-w-0 gap-1 overflow-x-auto pb-0.5'>
        {conversations.map((conversation) => (
          <div
            key={conversation.id}
            className={`group flex shrink-0 items-center rounded-md ${conversation.id === activeID ? 'bg-surface-2' : 'hover:bg-surface-2'}`}
          >
            <button
              type='button'
              data-playground-session
              onClick={() => onSelect(conversation)}
              className='max-w-48 truncate px-2.5 py-1.5 text-left text-xs text-ink-2'
            >
              {conversation.title}
            </button>
            <button
              type='button'
              aria-label={t('playground.deleteConversation', { title: conversation.title })}
              disabled={disabled}
              onClick={() => onDelete(conversation.id)}
              className='mr-1 grid size-5 place-items-center rounded-sm text-faint hover:bg-danger/18 hover:text-danger disabled:cursor-not-allowed'
            >
              <Trash2 className='size-3' aria-hidden='true' />
            </button>
          </div>
        ))}
      </div>
      <p className='text-xs text-faint sm:ml-auto'>{t('playground.historyLocal')}</p>
    </div>
  )
}
