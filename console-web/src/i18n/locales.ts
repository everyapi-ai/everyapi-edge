// The control room ships to suppliers worldwide through the public
// everyapi-ai/everyapi-edge mirror, so its copy cannot be hardcoded in one
// language. Two locales are carried inline rather than lazy-loaded: the whole
// console is one embedded file, so there is no second request to defer to.

export const LOCALES = ['en', 'zh'] as const
export type Locale = (typeof LOCALES)[number]
export const DEFAULT_LOCALE: Locale = 'en'

export const isSupportedLocale = (value: string): value is Locale =>
  (LOCALES as readonly string[]).includes(value)

export const LOCALE_LABELS: Record<Locale, string> = {
  en: 'English',
  zh: '中文',
}

const en = {
  'unlock.eyebrow': 'EveryAPI / Local only',
  'unlock.title': 'Edge Control Room',
  'unlock.description':
    'Local model management for this machine. Enter the console token the installer printed; it is kept in this browser session only.',
  'unlock.tokenLabel': 'Local console token',
  'unlock.submit': 'Enter control room',
  'unlock.tokenRequired': 'Enter the console token to continue.',

  'nav.overview': 'Overview',
  'nav.models': 'Models',
  'nav.traffic': 'Traffic',
  'nav.logs': 'Logs',
  'nav.lock': 'Lock console',
  'nav.language': 'Language',

  'header.eyebrow': 'EveryAPI / Edge Control Room',
  'header.title': 'Machine status at a glance',
  'header.online': 'Local console online',
  'header.offline': 'Connection failed',
  'header.connecting': 'Connecting to the local console',
  'header.updated': 'Updated {time}',

  'stat.active': 'Active requests',
  'stat.activeHint': 'Currently handled by this node',
  'stat.vram': 'Loaded VRAM',
  'stat.vramHint': 'From models Ollama has resident',
  'stat.completed': 'Completed requests',
  'stat.completedHint': 'During this agent run',
  'stat.tokens': 'Generated tokens',
  'stat.tokensHint': 'Successful inferences only',
  'stat.earnings': 'Settled earnings',
  'stat.earningsHint': 'Latest 200 gateway receipts',
  'stat.earningsPending': 'Awaiting sync',

  'models.title': 'Model library',
  'models.recommendationsLoading': 'Choosing suggestions for this machine…',
  'models.noVram':
    'No usable VRAM detected. Set EVERYAPI_VRAM_GB in the installer configuration before choosing a model.',
  'models.recommendations': 'About {vram} GB available for models. Conservative picks:',
  'models.download': 'Download {name}',
  'models.nameLabel': 'Ollama model name',
  'models.namePlaceholder': 'for example qwen3:8b',
  'models.pull': 'Download model',
  'models.pullStarted': 'Started downloading {name}',
  'models.columnModel': 'Model',
  'models.columnSize': 'Size',
  'models.columnDetails': 'Parameters / quantization',
  'models.remove': 'Remove',
  'models.removeConfirm': 'Remove {name}?',
  'models.empty': 'No local models yet. Enter a model name and start a download.',
  'models.invalidName': 'Enter a valid Ollama model name.',

  'settlement.title': 'How settlement works',
  'settlement.notice':
    'Earnings come only from node receipts the gateway has committed; they are never estimated from tokens. The amount is what this node earned, not the seller account withdrawable balance.',
  'settlement.waiting': 'Waiting for the first settled receipt…',
  'settlement.recent': 'Recently settled:',
  'privacy.title': 'Privacy boundary',
  'privacy.body':
    'The control room stores no prompts, responses, Authorization headers or real user identities. The traffic table shows safe request metadata only.',

  'traffic.title': 'Recent traffic',
  'traffic.empty': 'No requests have passed through this agent yet.',
  'traffic.columnCompleted': 'Completed',
  'traffic.columnConsumer': 'Customer',
  'traffic.columnModel': 'Model',
  'traffic.columnPath': 'Endpoint',
  'traffic.columnUsage': 'Usage',
  'traffic.columnDuration': 'Duration',
  'traffic.columnResult': 'Result',
  'traffic.ok': 'OK',

  'logs.title': 'Agent logs',
  'logs.empty': 'Waiting for logs…',

  'state.loading': 'Loading…',
  'state.error': 'Could not load this panel.',
  'state.retry': 'Retry',
  'common.unknown': '—',
} satisfies Record<string, string>

export type MessageKey = keyof typeof en

const zh: Record<MessageKey, string> = {
  'unlock.eyebrow': 'EveryAPI / 仅限本机',
  'unlock.title': 'Edge 控制室',
  'unlock.description':
    '本机模型管理入口。请输入安装器输出的 console token；令牌只保存在当前浏览器会话中。',
  'unlock.tokenLabel': '本地控制台令牌',
  'unlock.submit': '进入控制室',
  'unlock.tokenRequired': '请输入控制台令牌后继续。',

  'nav.overview': '总览',
  'nav.models': '模型',
  'nav.traffic': '流量',
  'nav.logs': '日志',
  'nav.lock': '锁定控制台',
  'nav.language': '语言',

  'header.eyebrow': 'EveryAPI / Edge 控制室',
  'header.title': '机器状态一眼看清',
  'header.online': '本地控制台在线',
  'header.offline': '连接失败',
  'header.connecting': '正在连接本地控制台',
  'header.updated': '更新于 {time}',

  'stat.active': '当前并发',
  'stat.activeHint': '正在由此节点处理',
  'stat.vram': '已加载显存',
  'stat.vramHint': '来自 Ollama 当前驻留的模型',
  'stat.completed': '已完成请求',
  'stat.completedHint': '本次 agent 运行期间',
  'stat.tokens': '已生成 Token',
  'stat.tokensHint': '只统计成功完成的推理',
  'stat.earnings': '已结算收益',
  'stat.earningsHint': '最近 200 条网关结算收据',
  'stat.earningsPending': '待同步',

  'models.title': '模型库',
  'models.recommendationsLoading': '正在按机器显存选择建议…',
  'models.noVram': '未检测到可用显存。请先在安装配置中设置 EVERYAPI_VRAM_GB，再选择模型。',
  'models.recommendations': '检测到约 {vram} GB 可供模型使用。保守建议：',
  'models.download': '下载 {name}',
  'models.nameLabel': 'Ollama 模型名称',
  'models.namePlaceholder': '例如 qwen3:8b',
  'models.pull': '下载模型',
  'models.pullStarted': '已开始下载 {name}',
  'models.columnModel': '模型',
  'models.columnSize': '大小',
  'models.columnDetails': '参数 / 量化',
  'models.remove': '移除',
  'models.removeConfirm': '移除 {name}？',
  'models.empty': '还没有本地模型。输入模型名后开始下载。',
  'models.invalidName': '请输入合法的 Ollama 模型名称。',

  'settlement.title': '结算说明',
  'settlement.notice':
    '收益仅来自网关已完成入账的节点收据，不会按 token 估算。金额是本节点所得，不等同于卖家账户的全部可提现余额。',
  'settlement.waiting': '等待首笔已结算收据…',
  'settlement.recent': '最近已结算：',
  'privacy.title': '隐私边界',
  'privacy.body':
    '控制室不会保存 prompt、响应、Authorization 或用户真实身份。流量表只显示安全的请求元数据。',

  'traffic.title': '最近流量',
  'traffic.empty': '尚无经过此 agent 的请求。',
  'traffic.columnCompleted': '完成时间',
  'traffic.columnConsumer': '客户',
  'traffic.columnModel': '模型',
  'traffic.columnPath': '接口',
  'traffic.columnUsage': '用量',
  'traffic.columnDuration': '耗时',
  'traffic.columnResult': '结果',
  'traffic.ok': '正常',

  'logs.title': 'Agent 日志',
  'logs.empty': '等待日志…',

  'state.loading': '加载中…',
  'state.error': '此面板加载失败。',
  'state.retry': '重试',
  'common.unknown': '—',
}

export const MESSAGES: Record<Locale, Record<MessageKey, string>> = { en, zh }
