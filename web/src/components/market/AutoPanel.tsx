import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Bot, Loader2 } from 'lucide-react'
import { api } from '../../lib/api'
import { confirmToast, notify } from '../../lib/notify'
import { configFromForm, type FreeForm } from '../../pages/FreeStrategyPage'
import { buildDashboardPath } from '../../router/paths'
import type { AIModel, Exchange } from '../../types'
import { BN } from './BinanceChart'
import { tr, ui } from './marketUi'
import { accountLabel, isPaper } from './TradePanel'

interface Props {
  symbol: string
  interval: string
  models: AIModel[]
  exchanges: Exchange[]
  language: string
}

// Strategy timeframes the decision engine accepts.
function strategyTimeframe(interval: string): string {
  if (['5m', '15m', '30m', '1h', '4h'].includes(interval)) return interval
  if (interval === '1m' || interval === '3m') return '5m'
  if (interval === '2h') return '1h'
  return '4h'
}

const scanOptions = [5, 15, 30, 60]

// Hands the current chart to Autopilot: a one-coin strategy plus a trader
// that re-runs the AI every scan interval and trades on its own.
export function AutoPanel({
  symbol,
  interval,
  models,
  exchanges,
  language,
}: Props) {
  const navigate = useNavigate()
  const [modelId, setModelId] = useState('')
  const [exchangeId, setExchangeId] = useState('')
  const [leverage, setLeverage] = useState(3)
  const [scan, setScan] = useState(15)
  const [starting, setStarting] = useState(false)
  const model = models.find((m) => m.id === modelId) || models[0]
  const exchange =
    exchanges.find((e) => e.id === exchangeId) ||
    exchanges.find(isPaper) ||
    exchanges[0]
  const timeframe = strategyTimeframe(interval)

  const start = async () => {
    if (!model || !exchange) return
    const live = !isPaper(exchange)
    const ok = await confirmToast(
      live
        ? tr(
            language,
            `AI 会用币安实盘真实资金自动交易 ${symbol}，每 ${scan} 分钟分析一次，自己开仓和平仓。确定启动？`,
            `The AI will trade ${symbol} with REAL funds on Binance, analyzing every ${scan} minutes and opening/closing on its own. Start?`
          )
        : tr(
            language,
            `AI 会在模拟盘上自动交易 ${symbol}，每 ${scan} 分钟分析一次。确定启动？`,
            `The AI will paper-trade ${symbol}, analyzing every ${scan} minutes. Start?`
          ),
      {
        title: live
          ? tr(language, '启动实盘自动交易', 'Start LIVE auto trading')
          : tr(language, '启动模拟自动交易', 'Start paper auto trading'),
        okText: tr(language, '启动', 'Start'),
        cancelText: tr(language, '取消', 'Cancel'),
      }
    )
    if (!ok) return
    setStarting(true)
    try {
      const defaults = await api.getDefaultStrategyConfig().catch(() => null)
      const form: FreeForm = {
        name: `${symbol} ${timeframe} AI`,
        description: '',
        coinsInput: symbol,
        timeframe,
        bars: 30,
        leverage,
        maxPositions: 1,
        marginUsage: 0.5,
        minConfidence: 75,
        minRiskReward: 2,
        customPrompt: '',
        indicators: {
          enable_ema: true,
          enable_macd: true,
          enable_rsi: true,
          enable_atr: true,
          enable_boll: true,
          enable_volume: true,
        },
      }
      const lang = language === 'zh' ? 'zh' : 'en'
      const strategy = await api.createStrategy({
        name: form.name,
        description: tr(
          language,
          '由行情页一键创建',
          'Created from the market page'
        ),
        config: configFromForm(defaults, form, lang, defaults),
      })
      const trader = await api.createTrader({
        name: `${symbol} ${live ? 'LIVE' : 'Paper'} AI`,
        ai_model_id: model.id,
        exchange_id: exchange.id,
        strategy_id: strategy.id,
        scan_interval_minutes: scan,
        is_cross_margin: true,
        btc_eth_leverage: leverage,
        altcoin_leverage: leverage,
      })
      if (!trader.is_running) await api.startTrader(trader.trader_id)
      notify.success(tr(language, '自动交易已启动', 'Auto trading started'))
      if (trader.startup_warning) notify.warning(trader.startup_warning)
      navigate(buildDashboardPath(trader.trader_id))
    } catch (err) {
      notify.error(
        err instanceof Error
          ? err.message
          : tr(language, '启动失败', 'Failed to start')
      )
    } finally {
      setStarting(false)
    }
  }

  return (
    <section
      className={ui.panel}
      aria-label={tr(language, 'AI 自动交易', 'AI auto trading')}
    >
      <div className={ui.title}>
        {tr(language, 'AI 自动交易', 'AI auto trading')}
      </div>
      <p className={ui.muted}>
        {tr(
          language,
          `让 AI 按 ${timeframe} 周期持续盯 ${symbol}，自己判断开仓、止损、止盈和平仓。`,
          `Let the AI watch ${symbol} on ${timeframe} and open, protect and close positions on its own.`
        )}
      </p>
      {models.length === 0 || exchanges.length === 0 ? (
        <p className="mt-2 text-xs" style={{ color: ui.warn }}>
          {tr(
            language,
            '需要先添加一个 AI 和一个币安账户（可以是模拟盘）。',
            'Add an AI and a Binance account (paper is fine) first.'
          )}
        </p>
      ) : (
        <div className="mt-2 space-y-2">
          <select
            aria-label={tr(language, 'AI 模型', 'AI model')}
            className={ui.input}
            value={model?.id}
            onChange={(e) => setModelId(e.target.value)}
          >
            {models.map((m) => (
              <option key={m.id} value={m.id}>
                {m.name}
              </option>
            ))}
          </select>
          <select
            aria-label={tr(language, '账户', 'Account')}
            className={ui.input}
            value={exchange?.id}
            onChange={(e) => setExchangeId(e.target.value)}
          >
            {exchanges.map((e) => (
              <option key={e.id} value={e.id}>
                {accountLabel(e, language)}
              </option>
            ))}
          </select>
          <div className="grid grid-cols-2 gap-2">
            <select
              aria-label={tr(language, '杠杆', 'Leverage')}
              className={ui.input}
              value={leverage}
              onChange={(e) => setLeverage(Number(e.target.value))}
            >
              {[1, 2, 3, 5, 10].map((l) => (
                <option key={l} value={l}>
                  {tr(language, '杠杆', 'Lev')} {l}x
                </option>
              ))}
            </select>
            <select
              aria-label={tr(language, '分析间隔', 'Scan interval')}
              className={ui.input}
              value={scan}
              onChange={(e) => setScan(Number(e.target.value))}
            >
              {scanOptions.map((m) => (
                <option key={m} value={m}>
                  {tr(language, `每 ${m} 分钟`, `Every ${m} min`)}
                </option>
              ))}
            </select>
          </div>
          {!isPaper(exchange) ? (
            <p className="text-xs font-semibold" style={{ color: BN.down }}>
              {tr(language, '⚠ 实盘账户', '⚠ Live account')}
            </p>
          ) : null}
          <button
            type="button"
            className={`${ui.btnGold} inline-flex items-center justify-center gap-2`}
            onClick={start}
            disabled={starting}
          >
            {starting ? (
              <Loader2 className="h-4 w-4 animate-spin" />
            ) : (
              <Bot className="h-4 w-4" />
            )}
            {tr(language, '启动 AI 自动交易', 'Start AI auto trading')}
          </button>
          <p className={ui.muted}>
            {tr(
              language,
              '每次分析都会消耗你 AI 账号的额度。可在「仪表盘」查看和停止。',
              'Each analysis uses your AI quota. View or stop it on the Dashboard.'
            )}
          </p>
        </div>
      )}
    </section>
  )
}
