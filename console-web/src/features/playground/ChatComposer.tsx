import type { FormEvent } from 'react'

import { ArrowUp, ImagePlus, Square, X } from 'lucide-react'

import { Button } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'

import type { ChatMessage } from './conversations'

export const ChatComposer = ({
  draft,
  attachment,
  isStreaming,
  attachmentDisabled,
  capabilitiesPending,
  supportsImages,
  canSend,
  error,
  onDraft,
  onAttachment,
  onAttach,
  onStop,
  onSubmit,
}: {
  draft: string
  attachment?: ChatMessage['attachment']
  isStreaming: boolean
  attachmentDisabled: boolean
  capabilitiesPending: boolean
  supportsImages: boolean
  canSend: boolean
  error: string
  onDraft: (value: string) => void
  onAttachment: (value: ChatMessage['attachment'] | undefined) => void
  onAttach: (file: File | undefined) => void
  onStop: () => void
  onSubmit: (event: FormEvent<HTMLFormElement>) => void
}) => {
  const { t } = useTranslation()
  return (
    <form onSubmit={onSubmit} className='border-line border-t bg-surface-1 p-3 sm:p-4'>
      <div className='rounded-lg border border-line-2 bg-surface-0 p-2 focus-within:border-accent focus-within:ring-2 focus-within:ring-accent/20'>
        <textarea
          name='playground-message'
          value={draft}
          onChange={(event) => onDraft(event.target.value)}
          placeholder={t('playground.placeholder')}
          rows={3}
          disabled={isStreaming}
          className='block w-full resize-none bg-transparent px-2 py-1 text-sm leading-6 text-ink outline-none placeholder:text-faint disabled:cursor-not-allowed'
        />
        <div className='flex items-center justify-between gap-3 pt-1'>
          <div className='min-w-0'>
            {attachment ? (
              <span className='flex items-center gap-2 text-xs text-muted'>
                <ImagePlus className='size-3.5 shrink-0 text-accent' aria-hidden='true' />
                <span className='truncate'>{attachment.name}</span>
                <button
                  type='button'
                  aria-label={t('playground.removeImage')}
                  disabled={isStreaming}
                  onClick={() => onAttachment(undefined)}
                  className='text-muted hover:text-ink disabled:cursor-not-allowed'
                >
                  <X className='size-3.5' aria-hidden='true' />
                </button>
              </span>
            ) : (
              <label
                className={`inline-flex items-center gap-1.5 text-xs text-muted ${attachmentDisabled ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:text-ink'}`}
              >
                <ImagePlus className='size-3.5 text-accent' aria-hidden='true' />
                <span>
                  {capabilitiesPending
                    ? t('playground.imageSupportChecking')
                    : t('playground.attachImage')}
                </span>
                <input
                  aria-label={t('playground.attachImage')}
                  type='file'
                  accept='image/*'
                  disabled={attachmentDisabled}
                  onChange={(event) => onAttach(event.target.files?.[0])}
                  className='sr-only'
                />
              </label>
            )}
          </div>
          {isStreaming ? (
            <Button
              type='button'
              variant='ghost'
              data-stop-generation
              onClick={onStop}
              className='inline-flex items-center gap-1.5'
            >
              <Square className='size-3.5' aria-hidden='true' />
              {t('playground.stop')}
            </Button>
          ) : (
            <Button type='submit' disabled={!canSend} className='inline-flex items-center gap-1.5'>
              <ArrowUp className='size-3.5' aria-hidden='true' />
              {t('playground.send')}
            </Button>
          )}
        </div>
      </div>
      {error ? (
        <p role='alert' className='mt-2 text-xs text-danger'>
          {error}
        </p>
      ) : null}
      {!attachment && !capabilitiesPending && !supportsImages ? (
        <p className='mt-2 text-xs text-muted'>{t('playground.imageUnsupported')}</p>
      ) : null}
    </form>
  )
}
