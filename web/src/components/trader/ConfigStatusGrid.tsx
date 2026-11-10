import {
  Brain,
  Landmark,
  Eye,
  EyeOff,
  Copy,
  Check,
  Plus,
  ArrowUpRight,
} from 'lucide-react'
import type { AIModel, Exchange, ExchangeAccountState } from '../../types'
import type { Language } from '../../i18n/translations'
import { getModelIcon } from '../common/ModelIcons'
import { getExchangeIcon } from '../common/ExchangeIcons'
import { AI_PROVIDER_CONFIG, truncateAddress } from './model-constants'

interface UsageInfo {
  runningCount: number
  totalCount: number
}
interface ConfigStatusGridProps {
  configuredModels: AIModel[]
  configuredExchanges: Exchange[]
  exchangeAccountStates?: Record<string, ExchangeAccountState>
  isExchangeAccountStatesLoading?: boolean
  accountStateError?: string
  onCheckAccounts?: () => void
  visibleExchangeAddresses: Set<string>
  copiedId: string | null
  language: Language
  getModelUsageInfo: (id: string) => UsageInfo
  getExchangeUsageInfo: (id: string) => UsageInfo
  onModelClick: (id: string) => void
  onExchangeClick: (id: string) => void
  onToggleExchangeAddress: (id: string) => void
  onCopyAddress: (id: string, address: string) => void
  onAddModel?: () => void
  onAddExchange?: () => void
  loading?: boolean
}
const displayFunds = (value: string) => {
  const raw = value.replace(/,/g, '').trim()
  if (!/^\d+(?:\.\d+)?$/.test(raw) || !Number.isFinite(Number(raw)))
    return value
  const amount = Number(raw)
  return amount.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: amount > 0 && amount < 0.01 ? 6 : 2,
  })
}
const accountStatus = (
  state: ExchangeAccountState | undefined,
  language: Language
) => {
  const zh = language === 'zh'
  if (!state) return zh ? '需要验证' : 'Verification needed'
  const labels: Record<string, string> = zh
    ? {
        ok: '已连接',
        disabled: '已禁用',
        missing_credentials: '完成设置',
        invalid_credentials: '请检查凭据',
        permission_denied: '请检查权限',
      }
    : {
        ok: 'Connected',
        disabled: 'Disabled',
        missing_credentials: 'Finish setup',
        invalid_credentials: 'Check credentials',
        permission_denied: 'Check permissions',
      }
  return labels[state.status] || (zh ? '连接不可用' : 'Connection unavailable')
}
const accountBalance = (state: ExchangeAccountState) => {
  const asset = state.asset || ''
  const balance = state.display_balance || String(state.total_equity ?? '')
  const numeric =
    asset && balance.endsWith(' ' + asset)
      ? balance.slice(0, -asset.length - 1)
      : balance
  return `${displayFunds(numeric)}${asset ? ' ' + asset : ''}`
}
const usage = (value: UsageInfo, language: Language) => {
  const zh = language === 'zh'
  return value.runningCount > 0
    ? zh
      ? `${value.runningCount} 个 Autopilot 正在运行`
      : `${value.runningCount} running Autopilot${value.runningCount === 1 ? '' : 's'}`
    : value.totalCount > 0
      ? zh
        ? `${value.totalCount} 个已保存的 Autopilot`
        : `${value.totalCount} saved Autopilot${value.totalCount === 1 ? '' : 's'}`
      : zh
        ? '尚未被 Autopilot 使用'
        : 'Not used by Autopilot'
}

export function ConfigStatusGrid({
  configuredModels,
  configuredExchanges,
  exchangeAccountStates,
  isExchangeAccountStatesLoading,
  accountStateError,
  onCheckAccounts,
  visibleExchangeAddresses,
  copiedId,
  getModelUsageInfo,
  getExchangeUsageInfo,
  onModelClick,
  onExchangeClick,
  onToggleExchangeAddress,
  onCopyAddress,
  onAddModel,
  onAddExchange,
  loading,
  language,
}: ConfigStatusGridProps) {
  const zh = language === 'zh'
  return (
    <div className="config-connections">
      <section className="config-card" aria-labelledby="config-models-title">
        <header>
          <div className="config-section-icon">
            <Brain size={20} />
          </div>
          <div>
            <h2 id="config-models-title">{zh ? 'AI 连接' : 'AI connections'}</h2>
            <p>{zh ? '为你的交易计划提供智能支持。' : 'The intelligence behind your trade plans.'}</p>
          </div>
          <button
            className="config-add"
            onClick={onAddModel}
            disabled={loading}
          >
            <Plus size={14} /> {zh ? '添加 AI' : 'Add AI'}
          </button>
        </header>
        <div className="config-card-body">
          {loading ? (
            <p role="status" className="config-loading">
              {zh ? '正在加载 AI 连接…' : 'Loading AI connections…'}
            </p>
          ) : (
            configuredModels.map((model) => (
              <article className="config-connection" key={model.id}>
                <button
                  className="config-connection-main"
                  onClick={() => onModelClick(model.id)}
                  aria-label={zh ? `编辑 ${model.name}` : `Edit ${model.name}`}
                >
                  <span className="config-provider-icon">
                    {getModelIcon(model.provider || model.id, {
                      width: 23,
                      height: 23,
                    }) || <Brain size={22} />}
                  </span>
                  <span className="config-connection-name">
                    <strong>{model.name}</strong>
                    <span>
                      {model.customModelName ||
                        AI_PROVIDER_CONFIG[model.provider]?.defaultModel ||
                        model.provider}
                    </span>
                  </span>
                  <span
                    className={
                      'config-status ' + (model.enabled ? 'positive' : '')
                    }
                  >
                    {model.enabled
                      ? zh
                        ? '已启用'
                        : 'Enabled'
                      : zh
                        ? '已禁用'
                        : 'Disabled'}
                  </span>
                  <ArrowUpRight size={14} />
                </button>
                <div className="config-connection-foot">
                  <span>{usage(getModelUsageInfo(model.id), language)}</span>
                  <button onClick={() => onModelClick(model.id)}>
                    {zh ? '管理连接' : 'Manage connection'}
                  </button>
                </div>
              </article>
            ))
          )}
          {!loading && !configuredModels.length && (
            <div className="config-empty">
              <Brain size={28} />
              <h3>{zh ? '添加你的第一个 AI 连接' : 'Add your first AI connection'}</h3>
              <p>
                {zh
                  ? '用你自己的 API Key 连接受支持的 AI 服务商。交易资金与之分开。'
                  : 'Connect a supported AI provider with your own API key. Trading funds stay separate.'}
              </p>
              <button onClick={onAddModel}>
                {zh ? '连接 AI' : 'Connect AI'} <ArrowUpRight size={14} />
              </button>
            </div>
          )}
        </div>
        <footer>
          {zh
            ? 'AI 调用费用由你在服务商处的账户承担，与交易余额分开。'
            : 'AI usage is billed by your provider account, separate from your trading balance.'}
        </footer>
      </section>
      <section className="config-card" aria-labelledby="config-accounts-title">
        <header>
          <div className="config-section-icon">
            <Landmark size={20} />
          </div>
          <div>
            <h2 id="config-accounts-title">{zh ? '交易账户' : 'Trading accounts'}</h2>
            <p>{zh ? '选择你批准的订单在哪里下单。' : 'Choose where your approved orders are placed.'}</p>
          </div>
          <button
            className="config-add"
            onClick={onAddExchange}
            disabled={loading}
          >
            <Plus size={14} /> {zh ? '添加账户' : 'Add account'}
          </button>
        </header>
        <div className="config-card-body">
          {accountStateError && (
            <p role="alert" className="config-load-error">
              {accountStateError}
            </p>
          )}
          {loading ? (
            <p role="status" className="config-loading">
              {zh ? '正在加载交易账户…' : 'Loading trading accounts…'}
            </p>
          ) : (
            configuredExchanges.map((exchange) => {
              const state = exchangeAccountStates?.[exchange.id]
              const address =
                exchange.hyperliquidWalletAddr ||
                exchange.asterUser ||
                exchange.lighterWalletAddr
              const visible = visibleExchangeAddresses.has(exchange.id)
              return (
                <article className="config-connection" key={exchange.id}>
                  <button
                    className="config-connection-main"
                    onClick={() => onExchangeClick(exchange.id)}
                    aria-label={zh ? `编辑 ${exchange.name}` : `Edit ${exchange.name}`}
                  >
                    <span className="config-provider-icon">
                      {getExchangeIcon(exchange.exchange_type || exchange.id, {
                        width: 23,
                        height: 23,
                      }) || <Landmark size={22} />}
                    </span>
                    <span className="config-connection-name">
                      <strong>{exchange.account_name || exchange.name}</strong>
                      <span>
                        {exchange.exchange_type || exchange.type}
                        {exchange.testnet
                          ? zh
                            ? ' · 测试网'
                            : ' · Testnet'
                          : zh
                            ? ' · 主网'
                            : ' · Mainnet'}
                      </span>
                    </span>
                    <span
                      className={
                        'config-status ' +
                        (state?.status === 'ok' ? 'positive' : '')
                      }
                    >
                      {isExchangeAccountStatesLoading && !state
                        ? zh
                          ? '检查中…'
                          : 'Checking…'
                        : accountStateError
                          ? zh
                            ? '验证不可用'
                            : 'Verification unavailable'
                          : accountStatus(state, language)}
                    </span>
                    <ArrowUpRight size={14} />
                  </button>
                  <div className="config-account-balance">
                    <span>
                      {exchange.exchange_type === 'binance'
                        ? zh
                          ? 'USDT 合约余额'
                          : 'USDT Futures balance'
                        : zh
                          ? '账户余额'
                          : 'Account balance'}
                    </span>
                    <strong>
                      {state?.status === 'ok' && state.display_balance
                        ? accountBalance(state)
                        : isExchangeAccountStatesLoading
                          ? zh
                            ? '检查中…'
                            : 'Checking…'
                          : zh
                            ? '未验证'
                            : 'Not verified'}
                    </strong>
                  </div>
                  {state?.status === 'ok' &&
                    state.available_balance !== undefined && (
                      <div className="config-account-available">
                        <span>{zh ? '可用于交易' : 'Available to trade'}</span>
                        <strong>
                          {displayFunds(String(state.available_balance))}{' '}
                          {state.asset}
                        </strong>
                      </div>
                    )}
                  {state?.error_message && (
                    <p className="config-account-error">
                      {state.error_message}
                    </p>
                  )}
                  {address && (
                    <div className="config-address">
                      <code>
                        {visible ? address : truncateAddress(address)}
                      </code>
                      <button
                        aria-label={
                          zh
                            ? `${visible ? '隐藏' : '显示'} ${exchange.name} 钱包地址`
                            : `${visible ? 'Hide' : 'Show'} ${exchange.name} wallet address`
                        }
                        onClick={() => onToggleExchangeAddress(exchange.id)}
                      >
                        {visible ? <EyeOff size={13} /> : <Eye size={13} />}
                      </button>
                      <button
                        aria-label={
                          zh
                            ? `复制 ${exchange.name} 钱包地址`
                            : `Copy ${exchange.name} wallet address`
                        }
                        onClick={() =>
                          onCopyAddress(`exchange-${exchange.id}`, address)
                        }
                      >
                        {copiedId === `exchange-${exchange.id}` ? (
                          <Check size={13} />
                        ) : (
                          <Copy size={13} />
                        )}
                      </button>
                    </div>
                  )}
                  <div className="config-connection-foot">
                    <span>
                      {getExchangeUsageInfo(exchange.id).totalCount > 0
                        ? usage(getExchangeUsageInfo(exchange.id), language)
                        : zh
                          ? 'Autopilot 可选 · Frontier 不需要'
                          : 'Autopilot optional · not required for Frontier'}
                    </span>
                    <button
                      disabled={isExchangeAccountStatesLoading}
                      onClick={onCheckAccounts}
                    >
                      {isExchangeAccountStatesLoading
                        ? zh
                          ? '检查中…'
                          : 'Checking…'
                        : zh
                          ? '验证连接'
                          : 'Verify connection'}
                    </button>
                    <button onClick={() => onExchangeClick(exchange.id)}>
                      {zh ? '管理账户' : 'Manage account'}
                    </button>
                  </div>
                </article>
              )
            })
          )}
          {!loading && !configuredExchanges.length && (
            <div className="config-empty">
              <Landmark size={28} />
              <h3>{zh ? '连接交易账户' : 'Connect a trading account'}</h3>
              <p>
                {zh
                  ? '添加 Hyperliquid、Binance 或其他受支持的交易所。连接不会开始交易。'
                  : 'Add Hyperliquid, Binance or another supported exchange. Connecting does not start trading.'}
              </p>
              <button onClick={onAddExchange}>
                {zh ? '添加账户' : 'Add account'} <ArrowUpRight size={14} />
              </button>
            </div>
          )}
        </div>
        <footer>
          {zh
            ? '交易前请检查权限和余额。保存的账户并不等于正在运行的机器人。'
            : 'Review permissions and balances before trading. A saved account is not an active bot.'}
        </footer>
      </section>
    </div>
  )
}
