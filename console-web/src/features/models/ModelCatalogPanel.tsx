import type { ReactNode } from 'react'

import { Panel } from '@/components/primitives'
import { useTranslation } from '@/i18n/useTranslation'

export type ModelType = 'chat' | 'reasoning' | 'code' | 'vision' | 'embedding' | 'image'

export type CatalogModel = {
  name: string
  provider: string
  type: ModelType
  minimumGB: number
  runtime?: 'ollama' | 'diffusers'
}

export const candidateBytes = (candidate: CatalogModel) => candidate.minimumGB * 1024 ** 3

const IMAGE_EDITOR_MODELS = new Set([
  'Qwen/Qwen-Image-Edit',
  'Qwen/Qwen-Image-Edit-2509',
  'Qwen/Qwen-Image-Edit-2511',
])

export const isImageEditor = (candidate: CatalogModel) =>
  candidate.runtime === 'diffusers' && IMAGE_EDITOR_MODELS.has(candidate.name)

// Curated from Ollama's public library. Keep explicit runnable tags here so a
// supplier never downloads an ambiguous family alias that resolves to a huge
// default model.
export const MODEL_CATALOG: CatalogModel[] = [
  { name: 'qwen2.5:0.5b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 2 },
  { name: 'qwen2.5:1.5b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 3 },
  { name: 'qwen2.5:3b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 4 },
  { name: 'qwen2.5:7b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 8 },
  { name: 'qwen2.5:14b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 14 },
  { name: 'qwen2.5:32b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 24 },
  { name: 'qwen2.5:72b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 56 },
  { name: 'qwen3:0.6b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 2 },
  { name: 'qwen3:1.7b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 3 },
  { name: 'qwen3:4b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 6 },
  { name: 'qwen3:8b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 8 },
  { name: 'qwen3:14b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 14 },
  { name: 'qwen3:32b', provider: 'Alibaba / Qwen', type: 'chat', minimumGB: 24 },
  { name: 'qwen2.5-coder:1.5b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 3 },
  { name: 'qwen2.5-coder:7b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 8 },
  { name: 'qwen2.5-coder:14b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 14 },
  { name: 'qwen2.5-coder:32b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 24 },
  { name: 'qwen3-coder:30b', provider: 'Alibaba / Qwen', type: 'code', minimumGB: 24 },
  { name: 'qwen3-vl:4b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 6 },
  { name: 'qwen3-vl:8b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 10 },
  { name: 'qwen3-vl:32b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 28 },
  { name: 'qwen2.5vl:3b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 6 },
  { name: 'qwen2.5vl:7b', provider: 'Alibaba / Qwen', type: 'vision', minimumGB: 10 },
  { name: 'qwen3-embedding:0.6b', provider: 'Alibaba / Qwen', type: 'embedding', minimumGB: 2 },
  { name: 'qwen3-embedding:4b', provider: 'Alibaba / Qwen', type: 'embedding', minimumGB: 4 },
  { name: 'qwen3-embedding:8b', provider: 'Alibaba / Qwen', type: 'embedding', minimumGB: 8 },
  {
    name: 'Qwen/Qwen-Image',
    provider: 'Alibaba / Qwen',
    type: 'image',
    minimumGB: 48,
    runtime: 'diffusers',
  },
  {
    name: 'Qwen/Qwen-Image-2512',
    provider: 'Alibaba / Qwen',
    type: 'image',
    minimumGB: 48,
    runtime: 'diffusers',
  },
  {
    name: 'Qwen/Qwen-Image-Edit',
    provider: 'Alibaba / Qwen',
    type: 'image',
    minimumGB: 48,
    runtime: 'diffusers',
  },
  {
    name: 'Qwen/Qwen-Image-Edit-2509',
    provider: 'Alibaba / Qwen',
    type: 'image',
    minimumGB: 48,
    runtime: 'diffusers',
  },
  {
    name: 'Qwen/Qwen-Image-Edit-2511',
    provider: 'Alibaba / Qwen',
    type: 'image',
    minimumGB: 48,
    runtime: 'diffusers',
  },
  {
    name: 'Qwen/Qwen-Image-Layered',
    provider: 'Alibaba / Qwen',
    type: 'image',
    minimumGB: 48,
    runtime: 'diffusers',
  },
  { name: 'llama3.2:1b', provider: 'Meta', type: 'chat', minimumGB: 2 },
  { name: 'llama3.2:3b', provider: 'Meta', type: 'chat', minimumGB: 4 },
  { name: 'llama3.1:8b', provider: 'Meta', type: 'chat', minimumGB: 8 },
  { name: 'llama3.1:70b', provider: 'Meta', type: 'chat', minimumGB: 56 },
  { name: 'llama3.3:70b', provider: 'Meta', type: 'chat', minimumGB: 56 },
  { name: 'llama3.2-vision:11b', provider: 'Meta', type: 'vision', minimumGB: 12 },
  { name: 'llama3.2-vision:90b', provider: 'Meta', type: 'vision', minimumGB: 72 },
  { name: 'gemma2:2b', provider: 'Google', type: 'chat', minimumGB: 3 },
  { name: 'gemma2:9b', provider: 'Google', type: 'chat', minimumGB: 10 },
  { name: 'gemma2:27b', provider: 'Google', type: 'chat', minimumGB: 22 },
  { name: 'gemma3:1b', provider: 'Google', type: 'vision', minimumGB: 2 },
  { name: 'gemma3:4b', provider: 'Google', type: 'vision', minimumGB: 6 },
  { name: 'gemma3:12b', provider: 'Google', type: 'vision', minimumGB: 12 },
  { name: 'gemma3:27b', provider: 'Google', type: 'vision', minimumGB: 22 },
  { name: 'codegemma:2b', provider: 'Google', type: 'code', minimumGB: 3 },
  { name: 'codegemma:7b', provider: 'Google', type: 'code', minimumGB: 8 },
  { name: 'deepseek-r1:1.5b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 3 },
  { name: 'deepseek-r1:7b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 8 },
  { name: 'deepseek-r1:8b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 8 },
  { name: 'deepseek-r1:14b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 14 },
  { name: 'deepseek-r1:32b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 24 },
  { name: 'deepseek-r1:70b', provider: 'DeepSeek', type: 'reasoning', minimumGB: 56 },
  { name: 'deepseek-coder-v2:16b', provider: 'DeepSeek', type: 'code', minimumGB: 14 },
  { name: 'deepseek-coder-v2:236b', provider: 'DeepSeek', type: 'code', minimumGB: 160 },
  { name: 'mistral:7b', provider: 'Mistral AI', type: 'chat', minimumGB: 8 },
  { name: 'mistral-nemo:12b', provider: 'Mistral AI', type: 'chat', minimumGB: 12 },
  { name: 'mistral-small:24b', provider: 'Mistral AI', type: 'chat', minimumGB: 20 },
  { name: 'ministral-3:3b', provider: 'Mistral AI', type: 'chat', minimumGB: 4 },
  { name: 'ministral-3:8b', provider: 'Mistral AI', type: 'chat', minimumGB: 8 },
  { name: 'ministral-3:14b', provider: 'Mistral AI', type: 'chat', minimumGB: 14 },
  { name: 'codestral:22b', provider: 'Mistral AI', type: 'code', minimumGB: 18 },
  { name: 'mixtral:8x7b', provider: 'Mistral AI', type: 'chat', minimumGB: 40 },
  { name: 'phi3:mini', provider: 'Microsoft', type: 'chat', minimumGB: 4 },
  { name: 'phi3:medium', provider: 'Microsoft', type: 'chat', minimumGB: 14 },
  { name: 'phi4:14b', provider: 'Microsoft', type: 'reasoning', minimumGB: 14 },
  { name: 'phi4-mini:3.8b', provider: 'Microsoft', type: 'chat', minimumGB: 4 },
  { name: 'gpt-oss:20b', provider: 'OpenAI', type: 'reasoning', minimumGB: 18 },
  { name: 'gpt-oss:120b', provider: 'OpenAI', type: 'reasoning', minimumGB: 96 },
  { name: 'nomic-embed-text', provider: 'Nomic AI', type: 'embedding', minimumGB: 2 },
  { name: 'nomic-embed-text:v1.5', provider: 'Nomic AI', type: 'embedding', minimumGB: 2 },
  { name: 'bge-m3', provider: 'BAAI', type: 'embedding', minimumGB: 4 },
  { name: 'mxbai-embed-large', provider: 'Mixedbread AI', type: 'embedding', minimumGB: 2 },
  { name: 'all-minilm', provider: 'Sentence Transformers', type: 'embedding', minimumGB: 2 },
  { name: 'snowflake-arctic-embed2', provider: 'Snowflake', type: 'embedding', minimumGB: 2 },
  { name: 'llava:7b', provider: 'LLaVA', type: 'vision', minimumGB: 8 },
  { name: 'llava:13b', provider: 'LLaVA', type: 'vision', minimumGB: 14 },
  { name: 'minicpm-v:8b', provider: 'OpenBMB', type: 'vision', minimumGB: 10 },
  { name: 'starcoder2:7b', provider: 'Hugging Face / BigCode', type: 'code', minimumGB: 8 },
  { name: 'starcoder2:15b', provider: 'Hugging Face / BigCode', type: 'code', minimumGB: 14 },
  { name: 'granite3.1-dense:8b', provider: 'IBM', type: 'chat', minimumGB: 8 },
  { name: 'granite-code:8b', provider: 'IBM', type: 'code', minimumGB: 8 },
]

const catalogModelFor = (name: string) => MODEL_CATALOG.find((candidate) => candidate.name === name)

export const providerFor = (name: string) => catalogModelFor(name)?.provider ?? 'Local'

export const modelTypeFor = (name: string): ModelType => {
  const catalogModel = catalogModelFor(name)
  if (catalogModel) return catalogModel.type

  const normalized = name.toLowerCase()
  if (/(embed|bge)/.test(normalized)) return 'embedding'
  if (/(vision|vl|llava|minicpm-v)/.test(normalized)) return 'vision'
  if (/(coder|code|codestral|starcoder)/.test(normalized)) return 'code'
  if (/(deepseek-r1|reasoning|qwq|phi4|gpt-oss)/.test(normalized)) return 'reasoning'
  return 'chat'
}

export const typeKey: Record<
  ModelType,
  | 'models.typeChat'
  | 'models.typeReasoning'
  | 'models.typeCode'
  | 'models.typeVision'
  | 'models.typeEmbedding'
  | 'models.typeImage'
> = {
  chat: 'models.typeChat',
  reasoning: 'models.typeReasoning',
  code: 'models.typeCode',
  vision: 'models.typeVision',
  embedding: 'models.typeEmbedding',
  image: 'models.typeImage',
}

export const ModelCatalogPanel = ({ children }: { children: ReactNode }) => {
  const { t } = useTranslation()
  return <Panel title={t('models.pull')}>{children}</Panel>
}
