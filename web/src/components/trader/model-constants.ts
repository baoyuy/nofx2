// Constants for AI model and provider configuration

export interface Claw402Model {
  id: string
  name: string
  provider: string
  desc: string
  descZh: string
  brand: string // key for getModelIcon / getModelColor
  priceIn: number // USD per 1M input tokens (upto pay-as-you-go)
  priceOut: number // USD per 1M output tokens
  isNew?: boolean
}

export interface AIProviderConfig {
  defaultModel: string
  apiUrl: string
  apiName: string
}

// Get friendly AI model display name
export function getModelDisplayName(modelId: string): string {
  switch (modelId.toLowerCase()) {
    case 'deepseek':
      return 'DeepSeek'
    case 'qwen':
      return 'Qwen'
    case 'claude':
      return 'Claude'
    default:
      return modelId.toUpperCase()
  }
}

// Extract name part after underscore
export function getShortName(fullName: string): string {
  const parts = fullName.split('_')
  return parts.length > 1 ? parts[parts.length - 1] : fullName
}

export const DEFAULT_CLAW402_MODEL = 'gpt-5.6'

// Models available through Claw402 (x402 USDC payment protocol)
// Must stay in sync with the claw402 catalog (GET /api/v1/catalog)
// Indicative retail USD per 1M tokens, including gateway markup. DeepSeek
// shows peak rates; GPT long-context multipliers and the per-request service
// fee are separate. The live gateway quote/verified receipt is authoritative.
export const CLAW402_MODELS: Claw402Model[] = [
  {
    id: 'gpt-6',
    name: 'GPT-6 Astra',
    provider: 'OpenAI',
    desc: 'Most capable · 1.05M context',
    descZh: '能力最强 · 1.05M 上下文',
    brand: 'openai',
    priceIn: 14.375,
    priceOut: 57.5,
    isNew: true,
  },
  {
    id: 'gpt-6.1-sol',
    name: 'GPT-6.1 Sol',
    provider: 'OpenAI',
    desc: 'Latest workhorse · 1.05M context',
    descZh: '最新主力 · 1.05M 上下文',
    brand: 'openai',
    priceIn: 2.875,
    priceOut: 11.5,
    isNew: true,
  },
  {
    id: 'gpt-6-sol',
    name: 'GPT-6 Sol',
    provider: 'OpenAI',
    desc: 'Previous workhorse · 1.05M context',
    descZh: '上一代主力 · 1.05M 上下文',
    brand: 'openai',
    priceIn: 2.875,
    priceOut: 11.5,
    isNew: true,
  },
  {
    id: 'gpt-6-luna',
    name: 'GPT-6 Luna',
    provider: 'OpenAI',
    desc: 'Fast and cost-efficient · 1.05M context',
    descZh: '快速且高性价比 · 1.05M 上下文',
    brand: 'openai',
    priceIn: 0.14375,
    priceOut: 0.575,
    isNew: true,
  },
  {
    id: 'gpt-5.6',
    name: 'GPT-5.6 Sol',
    provider: 'OpenAI',
    desc: 'Previous flagship',
    descZh: '上一代旗舰',
    brand: 'openai',
    priceIn: 5.75,
    priceOut: 34.5,
  },
  {
    id: 'gpt-5.6-terra',
    name: 'GPT-5.6 Terra',
    provider: 'OpenAI',
    desc: 'Balanced',
    descZh: '均衡之选',
    brand: 'openai',
    priceIn: 2.875,
    priceOut: 17.25,
  },
  {
    id: 'gpt-5.6-luna',
    name: 'GPT-5.6 Luna',
    provider: 'OpenAI',
    desc: 'Cost-efficient',
    descZh: '高性价比',
    brand: 'openai',
    priceIn: 1.15,
    priceOut: 6.9,
  },
  {
    id: 'claude-fable',
    name: 'Claude Fable 5',
    provider: 'Anthropic',
    desc: 'Most capable',
    descZh: '能力最强',
    brand: 'claude',
    priceIn: 14.375,
    priceOut: 57.5,
  },
  {
    id: 'claude-opus',
    name: 'Claude Opus 4.8',
    provider: 'Anthropic',
    desc: 'Coding & agents flagship',
    descZh: '编程与智能体旗舰',
    brand: 'claude',
    priceIn: 7.1875,
    priceOut: 28.75,
  },
  {
    id: 'deepseek-v4-flash',
    name: 'DeepSeek V4.1 Flash',
    provider: 'DeepSeek',
    desc: 'Fast general model',
    descZh: '快速通用模型',
    brand: 'deepseek',
    priceIn: 0.345,
    priceOut: 1.38,
  },
  {
    id: 'deepseek-v4-pro',
    name: 'DeepSeek-V4 Pro',
    provider: 'DeepSeek',
    desc: 'Advanced reasoning',
    descZh: '高级推理',
    brand: 'deepseek',
    priceIn: 1.518,
    priceOut: 4.554,
  },
  {
    id: 'glm-5',
    name: 'GLM-5',
    provider: 'Z.ai',
    desc: 'Deep reasoning flagship',
    descZh: '深度推理旗舰',
    brand: 'zhipu',
    priceIn: 1.15,
    priceOut: 3.68,
  },
]

// Display description for a Claw402 model in the requested language.
// `desc` stays the canonical English text; `descZh` is the Chinese display text.
export function getClaw402ModelDesc(
  model: Claw402Model,
  language?: string
): string {
  return language === 'zh' ? model.descZh : model.desc
}

// AI Provider configuration - default models and API links
export const AI_PROVIDER_CONFIG: Record<string, AIProviderConfig> = {
  claw402: {
    defaultModel: DEFAULT_CLAW402_MODEL,
    apiUrl: 'https://claw402.ai',
    apiName: 'Claw402',
  },
  deepseek: {
    defaultModel: 'deepseek-chat',
    apiUrl: 'https://platform.deepseek.com/api_keys',
    apiName: 'DeepSeek',
  },
  qwen: {
    defaultModel: 'qwen3-max',
    apiUrl: 'https://bailian.console.aliyun.com/',
    apiName: 'Qwen',
  },
  openai: {
    defaultModel: 'gpt-5.4',
    apiUrl: 'https://platform.openai.com/api-keys',
    apiName: 'OpenAI',
  },
  claude: {
    defaultModel: '',
    apiUrl: 'https://console.anthropic.com/settings/keys',
    apiName: 'Claude',
  },
  gemini: {
    defaultModel: '',
    apiUrl: 'https://aistudio.google.com/apikey',
    apiName: 'Gemini',
  },
  grok: {
    defaultModel: '',
    apiUrl: 'https://console.x.ai/',
    apiName: 'Grok',
  },
  kimi: {
    defaultModel: '',
    apiUrl: 'https://platform.moonshot.ai/console/api-keys',
    apiName: 'Kimi',
  },
  minimax: {
    defaultModel: 'MiniMax-M2.7',
    apiUrl: 'https://platform.minimax.io/',
    apiName: 'MiniMax',
  },
}

// Generic OpenAI-compatible endpoint: URL and model name are mandatory.
export const CUSTOM_PROVIDER = 'custom'

// Helper function to get exchange display name from exchange ID (UUID)
export function getExchangeDisplayName(
  exchangeId: string | undefined,
  exchanges: {
    id: string
    exchange_type?: string
    name: string
    account_name?: string
  }[],
  language?: string
): string {
  if (!exchangeId) return language === 'zh' ? '未知' : 'Unknown'
  const exchange = exchanges.find((e) => e.id === exchangeId)
  if (!exchange) return exchangeId.substring(0, 8).toUpperCase() + '...' // Show truncated UUID if not found
  const typeName = exchange.exchange_type?.toUpperCase() || exchange.name
  return exchange.account_name
    ? `${typeName} - ${exchange.account_name}`
    : typeName
}

// Helper function to check if exchange is a perp-dex type (wallet-based)
export function isPerpDexExchange(exchangeType: string | undefined): boolean {
  if (!exchangeType) return false
  const perpDexTypes = ['hyperliquid', 'lighter', 'aster']
  return perpDexTypes.includes(exchangeType.toLowerCase())
}

// Helper function to get wallet address for perp-dex exchanges
export function getWalletAddress(
  exchange:
    | {
        exchange_type?: string
        hyperliquidWalletAddr?: string
        lighterWalletAddr?: string
        asterSigner?: string
      }
    | undefined
): string | undefined {
  if (!exchange) return undefined
  const type = exchange.exchange_type?.toLowerCase()
  switch (type) {
    case 'hyperliquid':
      return exchange.hyperliquidWalletAddr
    case 'lighter':
      return exchange.lighterWalletAddr
    case 'aster':
      return exchange.asterSigner
    default:
      return undefined
  }
}

// Helper function to truncate wallet address for display
export function truncateAddress(
  address: string,
  startLen = 6,
  endLen = 4
): string {
  if (address.length <= startLen + endLen + 3) return address
  return `${address.slice(0, startLen)}...${address.slice(-endLen)}`
}
