import type { Capability } from '@/api/schemas'
import type { MessageKey } from '@/i18n/locales'
import { useTranslation, type Translate } from '@/i18n/useTranslation'

const RUNTIME_ORDER = ['text', 'image', 'speech', 'video', 'render', 'rerank'] as const

type RuntimeKind = (typeof RUNTIME_ORDER)[number]

const RUNTIME_LABEL_KEYS: Record<RuntimeKind, MessageKey> = {
  text: 'settings.runtimeText',
  image: 'settings.runtimeImage',
  speech: 'settings.runtimeSpeech',
  video: 'settings.runtimeVideo',
  render: 'settings.runtimeRender',
  rerank: 'settings.runtimeRerank',
}

/** Degraded and unavailable both mean "not earning", but only degraded implies the runtime answered at all, so they keep distinct tones. */
const STATUS_TONE: Record<Capability['status'], string> = {
  ready: 'border-good/30 bg-good/10 text-good',
  warming: 'border-accent/30 bg-accent/10 text-accent',
  degraded: 'border-warn/30 bg-warn/10 text-warn',
  unavailable: 'border-line-2 bg-surface-2 text-muted',
  unsupported: 'border-line-2 bg-surface-2 text-muted',
}

const statusLabel = (t: Translate, status: Capability['status']) =>
  t(`capability.status.${status}` as MessageKey)

const capabilityLabel = (t: Translate, id: Capability['id']) => t(`capability.${id}` as MessageKey)

/** The agent reports every capability of all six runtimes, but until now only the playground read five of them, so an operator whose speech or video runtime was down had nowhere to see it. */
export const CapabilityMatrix = ({ capabilities }: { capabilities: Capability[] }) => {
  const { t } = useTranslation()

  if (!capabilities.length)
    return <p className='text-sm text-muted'>{t('runtime.capabilityEmpty')}</p>

  const grouped = RUNTIME_ORDER.map((runtime) => ({
    runtime,
    items: capabilities.filter((capability) => capability.runtime === runtime),
  })).filter((group) => group.items.length > 0)

  const ready = capabilities.filter((capability) => capability.status === 'ready').length

  return (
    <div className='flex flex-col gap-4'>
      <div>
        <p className='text-sm leading-6 text-muted'>{t('runtime.capabilitiesHint')}</p>
        <p className='mt-1 text-xs font-medium text-faint' data-capability-summary>
          {t('runtime.capabilityReady', { ready, total: capabilities.length })}
        </p>
      </div>
      {grouped.map((group) => (
        <section key={group.runtime} data-capability-runtime={group.runtime}>
          <h3 className='text-xs font-medium tracking-[0.08em] text-faint uppercase'>
            {t(RUNTIME_LABEL_KEYS[group.runtime])}
          </h3>
          <ul className='mt-2 grid gap-2 sm:grid-cols-2'>
            {group.items.map((capability, position) => (
              <li
                // The agent collapses each capability to one entry, but a duplicate must degrade to a repeated row rather than a silently dropped one.
                key={`${capability.id}-${position}`}
                data-capability={capability.id}
                data-capability-status={capability.status}
                className='min-w-0 rounded-lg border border-line bg-surface-1 px-3 py-2.5'
              >
                <div className='flex flex-wrap items-center justify-between gap-2'>
                  <span className='min-w-0 truncate text-sm font-medium text-ink'>
                    {capabilityLabel(t, capability.id)}
                  </span>
                  <span
                    className={`shrink-0 rounded-full border px-2 py-0.5 text-xs font-medium ${STATUS_TONE[capability.status]}`}
                  >
                    {statusLabel(t, capability.status)}
                  </span>
                </div>
                <p className='mt-1 font-mono text-[11px] break-all text-faint'>{capability.id}</p>
                {capability.models.length ? (
                  <p className='mt-1.5 text-xs break-words text-ink-2'>
                    <span className='text-faint'>{t('runtime.capabilityModels')}: </span>
                    {capability.models.join(', ')}
                  </p>
                ) : null}
                {capability.reason ? (
                  <p className='mt-1.5 text-xs break-words text-muted'>{capability.reason}</p>
                ) : null}
              </li>
            ))}
          </ul>
        </section>
      ))}
    </div>
  )
}
